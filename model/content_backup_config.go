package model

import (
	"context"
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ContentBackupSettingOptionKey 是渠道内容备份唯一的配置键：整包 contentbackup.Config
// 的 JSON 存在 options 表的这一行里，业务进程与 daemon 都只读它。节点身份、FTPS 账密、
// spool 目录与 socket 路径不在其中，它们只来自部署环境变量。
const ContentBackupSettingOptionKey = "ContentBackupSetting"

// ErrContentBackupConfigConflict 表示 expected_version 与库里当前发布的版本不一致，
// 本次保存一个字节都没写，调用方应重新拉取配置后再提交。
var ErrContentBackupConfigConflict = errors.New("content backup config version conflict")

// LoadContentBackupConfig 只读这一个配置键，供 daemon 每 config_reload_seconds 拉一次
// 不可变快照。没有存过配置时返回 contentbackup.DefaultConfig()；已存但解析或校验失败
// 时返回错误与零值，让调用方保留上一个有效快照并告警，而不是私自换一个目标。
func LoadContentBackupConfig(ctx context.Context, db *gorm.DB) (contentbackup.Config, error) {
	if db == nil {
		return contentbackup.Config{}, errors.New("content backup config database is not initialized")
	}
	value, found, err := contentBackupConfigReadOption(ctx, db, false)
	if err != nil {
		return contentbackup.Config{}, err
	}
	if !found {
		return contentbackup.DefaultConfig(), nil
	}
	return operation_setting.ParseContentBackupConfig(value)
}

// SaveContentBackupConfig 整包校验后发布一个新版本。版本由服务端固定为
// expectedVersion+1，调用方不能自选，否则 applied_config_version 会失去单调性，
// 节点页也就无法据此判断配置是否已经收敛。
//
// 并发安全不靠"读出来比一下再写回"：整个读-比-写在同一个事务内完成，非 SQLite 方言
// 先对 options 行加 FOR UPDATE 排它锁，提交前再用带上旧值的条件 UPDATE 并检查
// RowsAffected。两道守卫任意一道落空都返回 ErrContentBackupConfigConflict 并回滚。
func SaveContentBackupConfig(ctx context.Context, db *gorm.DB, cfg contentbackup.Config, expectedVersion int64) (contentbackup.Config, error) {
	if db == nil {
		return contentbackup.Config{}, errors.New("content backup config database is not initialized")
	}
	if expectedVersion < 1 {
		return contentbackup.Config{}, fmt.Errorf("content backup expected version must be at least 1, got %d", expectedVersion)
	}
	cfg.Version = expectedVersion + 1
	// 先做一次与库无关的校验，把明显的坏输入挡在事务外；口令为空时的补全与最终校验在
	// 事务内完成，因为"沿用已存口令"必须读的是加了锁的那一行。
	if err := contentbackup.ValidateConfig(contentBackupWithAnyPassword(cfg)); err != nil {
		return contentbackup.Config{}, err
	}
	var value string

	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, found, err := contentBackupConfigReadOption(ctx, tx, true)
		if err != nil {
			return err
		}
		baseline := contentbackup.DefaultConfig().Version
		if found {
			baseline = contentBackupConfigBaselineVersion(current)
		}
		if baseline != expectedVersion {
			return ErrContentBackupConfigConflict
		}
		// GET /config 从不回显口令，表单整包保存时口令字段自然是空的；空表示"不改"，
		// 沿用库里那一份。想清空口令只能同时关闭采集（校验会要求启用态必须有口令）。
		if cfg.RemotePassword == "" && found {
			if stored, parseErr := operation_setting.ParseContentBackupConfig(current); parseErr == nil {
				cfg.RemotePassword = stored.RemotePassword
			}
		}
		if err := contentbackup.ValidateConfig(cfg); err != nil {
			return err
		}
		blob, err := common.Marshal(cfg)
		if err != nil {
			return fmt.Errorf("marshal content backup config: %w", err)
		}
		value = string(blob)
		if !found {
			// 两个节点同时首次发布时，唯一键让后来者 DoNothing，RowsAffected 为 0 即冲突。
			created := tx.Clauses(clause.OnConflict{DoNothing: true}).
				Create(&Option{Key: ContentBackupSettingOptionKey, Value: value})
			if created.Error != nil {
				return created.Error
			}
			if created.RowsAffected == 0 {
				return ErrContentBackupConfigConflict
			}
			return nil
		}
		updated := tx.Model(&Option{}).
			Where(map[string]any{"key": ContentBackupSettingOptionKey, "value": current}).
			Update("value", value)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected == 0 {
			return ErrContentBackupConfigConflict
		}
		return nil
	})
	if err != nil {
		return contentbackup.Config{}, err
	}
	contentBackupConfigPublishToMemory(value)
	return cfg, nil
}

// contentBackupWithAnyPassword 只用于事务外的预校验：口令为空在这里表示"沿用已存"，
// 不能因此提前判成"启用态缺口令"；真正的口令来源与最终校验都在事务内。
func contentBackupWithAnyPassword(cfg contentbackup.Config) contentbackup.Config {
	if cfg.RemotePassword == "" {
		cfg.RemotePassword = "\x00unchanged"
	}
	return cfg
}

// contentBackupConfigBaselineVersion 取库里那一行的版本作为 CAS 基线。通用 /option 写
// 入口能先落库再被本模块拒绝，所以那一行可能是坏 JSON；此时退回业务进程内存里的有效
// 快照版本，让管理员改得回来，而并发覆盖仍由条件 UPDATE 拦住。
func contentBackupConfigBaselineVersion(stored string) int64 {
	if parsed, err := operation_setting.ParseContentBackupConfig(stored); err == nil {
		return parsed.Version
	}
	return operation_setting.GetContentBackupConfig().Version
}

func contentBackupConfigReadOption(ctx context.Context, db *gorm.DB, lock bool) (string, bool, error) {
	var option Option
	query := db.WithContext(ctx).Where(map[string]any{"key": ContentBackupSettingOptionKey})
	if lock && !contentBackupConfigUsingSQLite(db) {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := query.Take(&option).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return option.Value, true, nil
}

// 同 ContentBackupStore：优先看句柄自己的方言。daemon 用的是专用连接，可能没有设置
// 全局 common.Using* 标志，而对 SQLite 发 FOR UPDATE 是硬错误。
func contentBackupConfigUsingSQLite(db *gorm.DB) bool {
	if db != nil && db.Dialector != nil {
		return db.Dialector.Name() == "sqlite"
	}
	return common.UsingSQLite
}

// contentBackupConfigPublishToMemory 复用既有的 option 更新钩子，让通用 /option 入口与
// 本模块的保存路径收敛到同一份解析+校验逻辑，再触发集群广播。
func contentBackupConfigPublishToMemory(value string) {
	common.OptionMapRWMutex.RLock()
	optionMapReady := common.OptionMap != nil
	common.OptionMapRWMutex.RUnlock()
	if optionMapReady {
		if err := updateOptionMap(ContentBackupSettingOptionKey, value); err != nil {
			common.SysError("content backup config was persisted but the in-memory snapshot stayed behind: " + err.Error())
		}
	} else if err := operation_setting.UpdateContentBackupSettingByJsonString(value); err != nil {
		common.SysError("content backup config was persisted but the in-memory snapshot stayed behind: " + err.Error())
	}
	broadcastOptionUpdate()
}
