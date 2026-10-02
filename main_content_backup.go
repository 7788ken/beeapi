package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// contentBackupDefaultSpoolDir is relative to the working directory, which the container
// image sets to /data — the volume every deployment already mounts. Nothing else on the
// host has to exist for the pipeline to work; CONTENT_BACKUP_SPOOL_DIR overrides it.
const contentBackupDefaultSpoolDir = "content-backup"

// contentBackupModuleEnabled 解析部署级模块开关 CONTENT_BACKUP_MODULE：未设置或 on 为开启，
// off 为关闭（不启动采集/上传/告警，不注册 /api/content_backup/*）；
// 其它取值直接启动失败，避免拼错后模块被静默开关。
// 公开后台不再提供内容备份入口；开启时也只有持有 CONTENT_BACKUP_CONSOLE_TOKEN 的管理端能调接口。
func contentBackupModuleEnabled() (bool, error) {
	switch strings.TrimSpace(os.Getenv("CONTENT_BACKUP_MODULE")) {
	case "", "on":
		return true, nil
	case "off":
		return false, nil
	default:
		return false, errors.New("CONTENT_BACKUP_MODULE must be on or off")
	}
}

func contentBackupConsoleToken() string {
	return strings.TrimSpace(os.Getenv("CONTENT_BACKUP_CONSOLE_TOKEN"))
}

// initContentBackupCapture starts the in-process backup runtime and arms the capture hooks.
// A failure to start (unwritable spool) only disables the feature and logs; it must never
// stop the gateway (design doc 4.1/4.2).
func initContentBackupCapture() {
	spoolDir := os.Getenv("CONTENT_BACKUP_SPOOL_DIR")
	if spoolDir == "" {
		spoolDir = contentBackupDefaultSpoolDir
	}
	if abs, err := filepath.Abs(spoolDir); err == nil {
		spoolDir = abs
	}

	var enqueuer service.ContentBackupEnqueuer
	if err := service.StartContentBackupRuntime(context.Background(), spoolDir); err != nil {
		common.SysError("content backup runtime not started (capture disabled): " + err.Error())
	} else {
		enqueuer = service.TryEnqueue
	}

	service.ConfigureContentBackupCapture(service.ContentBackupCaptureHooks{
		Runtime: func() service.ContentBackupRuntime {
			cfg := operation_setting.GetContentBackupConfig()
			return service.ContentBackupRuntime{
				Enabled:       cfg.Enabled,
				SiteID:        cfg.SiteLabel,
				StorageNodeID: service.ContentBackupStorageNodeID(),
				TargetID:      cfg.TargetID,
				ConfigVersion: cfg.Version,
				MaxBodyBytes:  cfg.MaxBodyBytes,
			}
		},
		Enqueuer: enqueuer,
		ChannelEnabled: func(settings any) bool {
			channelSettings, ok := settings.(dto.ChannelSettings)
			if !ok {
				if pointer, isPointer := settings.(*dto.ChannelSettings); isPointer && pointer != nil {
					channelSettings = *pointer
					ok = true
				}
			}
			if !ok {
				return false
			}
			return channelSettings.ContentBackupEnabled
		},
	})
	cfg := operation_setting.GetContentBackupConfig()
	service.SetContentBackupCaptureBudget(int64(cfg.CaptureMemoryMB)<<20, cfg.MaxInflightCaptures)
}
