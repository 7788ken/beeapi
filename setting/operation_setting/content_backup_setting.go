package operation_setting

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
)

// ErrContentBackupConfigInvalid 标记一份完全不可用的整包配置。调用方必须保留上一个
// 有效快照，而不是自行降级到某个猜测值。
var ErrContentBackupConfigInvalid = errors.New("content backup config is not usable")

// contentbackup.Config 是纯值类型，存进 atomic.Value 的每一份都只会被读取，
// 所以 GetContentBackupConfig 返回的拷贝天然就是不可变快照。
var contentBackupConfigSnapshot atomic.Value

// 坏配置会在每次轮询与每次集群广播时被重复拒绝，日志必须限速。
var contentBackupConfigRejectLogInterval = time.Minute

var (
	contentBackupConfigRejectMu       sync.Mutex
	contentBackupConfigRejectLast     time.Time
	contentBackupConfigRejectSinceLog int64
	contentBackupConfigRejectTotal    int64
)

func init() {
	contentBackupConfigSnapshot.Store(contentbackup.DefaultConfig())
}

// GetContentBackupConfig 返回当前生效配置的独立拷贝。业务进程与 daemon 各自按
// config_reload_seconds 拉取即可，不需要外部加锁。
func GetContentBackupConfig() contentbackup.Config {
	if cfg, ok := contentBackupConfigSnapshot.Load().(contentbackup.Config); ok {
		return cfg
	}
	return contentbackup.DefaultConfig()
}

// ContentBackupConfigJSONString 供 InitOptionMap 注入初始值。
func ContentBackupConfigJSONString() string {
	blob, err := common.Marshal(GetContentBackupConfig())
	if err != nil {
		return ""
	}
	return string(blob)
}

// ParseContentBackupConfig 解析整包配置。未知键（例如被塞进来的 ftps_username /
// ftps_password / site_id）在反序列化时就被丢弃：凭据与节点身份只来自部署环境变量，
// 绝不进 options 表，也绝不能由网页改名。
func ParseContentBackupConfig(value string) (contentbackup.Config, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return contentbackup.Config{}, fmt.Errorf("%w: stored value is empty", ErrContentBackupConfigInvalid)
	}
	// 从默认值起解码后，JSON 的 null 对结构体是空操作，会让一份"什么都没有"的行冒充默认配置；
	// 整包必须是一个对象。
	if !strings.HasPrefix(trimmed, "{") {
		return contentbackup.Config{}, fmt.Errorf("%w: stored value is not a JSON object", ErrContentBackupConfigInvalid)
	}
	// 整包必须带齐首版就有的全部键：缺任何一个就是半份配置（典型是 Root 从通用 /option
	// 入口手写了一段），整体拒绝。后续版本新增的键允许缺失、取默认——库里存的是某个历史
	// 版本写下的整包，按零值解码会让 sftp_port=0 撞上范围校验，把一份合法的旧配置整体拒掉
	// （2026-09-18 ai 线上实发：每分钟 "config rejected"，采集开关静默回落默认）。
	var present map[string]any
	if err := common.UnmarshalJsonStr(trimmed, &present); err != nil {
		return contentbackup.Config{}, fmt.Errorf("%w: %w", ErrContentBackupConfigInvalid, err)
	}
	for _, key := range contentbackup.OriginalConfigKeys {
		if _, ok := present[key]; !ok {
			return contentbackup.Config{}, fmt.Errorf("%w: stored value is missing %q (partial write)", ErrContentBackupConfigInvalid, key)
		}
	}
	cfg := contentbackup.DefaultConfig()
	if err := common.UnmarshalJsonStr(value, &cfg); err != nil {
		return contentbackup.Config{}, fmt.Errorf("%w: %w", ErrContentBackupConfigInvalid, err)
	}
	if err := contentbackup.ValidateConfig(cfg); err != nil {
		// 只有"开关为开但启用所需字段缺失"这一类失败可以降级：把开关当作关，其余保留。
		// 典型来源是旧版本保存的整包——当时没有 site_label / 凭据这些启用前置。整包拒掉会让
		// 进程回落默认快照，页面上运维保存过的目标配置全部"消失"，还每分钟记一条拒绝日志；
		// 而一个开着却缺前置的开关本来就采不到任何东西，如实降级比整体拒绝更接近真相。
		if cfg.Enabled {
			degraded := cfg
			degraded.Enabled = false
			if contentbackup.ValidateConfig(degraded) == nil {
				contentBackupConfigDegraded(err.Error())
				return degraded, nil
			}
		}
		return contentbackup.Config{}, fmt.Errorf("%w: %w", ErrContentBackupConfigInvalid, err)
	}
	return cfg, nil
}

// SetContentBackupConfig 一次校验后整包发布。校验失败时保留上一个有效快照，
// 否则半个配置会让采集预算变成未定义值。
func SetContentBackupConfig(cfg contentbackup.Config) error {
	if err := contentbackup.ValidateConfig(cfg); err != nil {
		rejected := fmt.Errorf("%w: %w", ErrContentBackupConfigInvalid, err)
		contentBackupConfigRejected(rejected.Error())
		return rejected
	}
	contentBackupConfigSnapshot.Store(cfg)
	return nil
}

// UpdateContentBackupSettingByJsonString 是 options 表的更新钩子入口。
func UpdateContentBackupSettingByJsonString(value string) error {
	cfg, err := ParseContentBackupConfig(value)
	if err != nil {
		contentBackupConfigRejected(err.Error())
		return err
	}
	contentBackupConfigSnapshot.Store(cfg)
	return nil
}

// ContentBackupConfigRejectCount 返回累计拒绝次数，供告警与测试判断坏配置是否仍在被重放。
func ContentBackupConfigRejectCount() int64 {
	contentBackupConfigRejectMu.Lock()
	defer contentBackupConfigRejectMu.Unlock()
	return contentBackupConfigRejectTotal
}

var (
	contentBackupConfigDegradedMu   sync.Mutex
	contentBackupConfigDegradedLast string
)

// contentBackupConfigDegraded 记录"开关被忽略"这一事实。配置每分钟都会被重新加载，同一个原因
// 只在第一次和原因变化时各记一条，否则运维在填完站点标签之前会看到每分钟一行同样的话。
func contentBackupConfigDegraded(reason string) {
	contentBackupConfigDegradedMu.Lock()
	defer contentBackupConfigDegradedMu.Unlock()
	if reason == contentBackupConfigDegradedLast {
		return
	}
	contentBackupConfigDegradedLast = reason
	common.SysLog("content backup: capture switch ignored until the enable-time requirements are met (set them in the backup settings page): " + reason)
}

func contentBackupConfigRejected(reason string) {
	contentBackupConfigRejectMu.Lock()
	defer contentBackupConfigRejectMu.Unlock()
	contentBackupConfigRejectTotal++
	contentBackupConfigRejectSinceLog++
	now := time.Now()
	if !contentBackupConfigRejectLast.IsZero() && now.Sub(contentBackupConfigRejectLast) < contentBackupConfigRejectLogInterval {
		return
	}
	contentBackupConfigRejectLast = now
	suppressed := contentBackupConfigRejectSinceLog - 1
	contentBackupConfigRejectSinceLog = 0
	message := "content backup config rejected, keeping the previous valid snapshot: " + reason
	if suppressed > 0 {
		message += fmt.Sprintf(" (%d more rejections since the last log)", suppressed)
	}
	common.SysError(message)
}
