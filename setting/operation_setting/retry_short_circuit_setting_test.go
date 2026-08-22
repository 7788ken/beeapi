package operation_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
)

func TestRetryShortCircuitConfigDefaults(t *testing.T) {
	cfg := GetRetryShortCircuitConfig()
	if cfg.Enabled {
		t.Error("enabled must default to false")
	}
	if cfg.MinDurationSeconds != 300 {
		t.Errorf("min_duration_seconds default = %d, want 300", cfg.MinDurationSeconds)
	}
	if cfg.TTLMinutes != 15 {
		t.Errorf("ttl_minutes default = %d, want 15", cfg.TTLMinutes)
	}

	// 已注册进全局配置系统（SyncOptions 热加载 / options 表读写走它）
	if config.GlobalConfig.Get("retry_short_circuit_setting") == nil {
		t.Fatal("retry_short_circuit_setting not registered in GlobalConfig")
	}

	// options 表扁平键名必须稳定：retry_short_circuit_setting.<json标签>
	all := config.GlobalConfig.ExportAllConfigs()
	for key, want := range map[string]string{
		"retry_short_circuit_setting.enabled":              "false",
		"retry_short_circuit_setting.min_duration_seconds": "300",
		"retry_short_circuit_setting.ttl_minutes":          "15",
	} {
		got, ok := all[key]
		if !ok {
			t.Errorf("option key %q missing from ExportAllConfigs", key)
			continue
		}
		if got != want {
			t.Errorf("option key %q = %q, want %q", key, got, want)
		}
	}
}

func TestRetryShortCircuitConfigParse(t *testing.T) {
	// 在局部副本上验证解析语义，不动包级全局配置
	cfg := RetryShortCircuitConfig{Enabled: false, MinDurationSeconds: 300, TTLMinutes: 15}
	if err := config.UpdateConfigFromMap(&cfg, map[string]string{
		"enabled":              "true",
		"min_duration_seconds": "120",
		"ttl_minutes":          "30",
	}); err != nil {
		t.Fatalf("UpdateConfigFromMap: %v", err)
	}
	if !cfg.Enabled || cfg.MinDurationSeconds != 120 || cfg.TTLMinutes != 30 {
		t.Errorf("parsed = %+v, want {true 120 30}", cfg)
	}

	// 非法值不覆盖已有值
	if err := config.UpdateConfigFromMap(&cfg, map[string]string{
		"enabled":              "not-a-bool",
		"min_duration_seconds": "abc",
	}); err != nil {
		t.Fatalf("UpdateConfigFromMap: %v", err)
	}
	if !cfg.Enabled || cfg.MinDurationSeconds != 120 {
		t.Errorf("invalid values must keep previous config, got %+v", cfg)
	}

	// float 形式的整数（历史兼容路径）
	if err := config.UpdateConfigFromMap(&cfg, map[string]string{
		"min_duration_seconds": "600.000000",
	}); err != nil {
		t.Fatalf("UpdateConfigFromMap: %v", err)
	}
	if cfg.MinDurationSeconds != 600 {
		t.Errorf("float-format int parsed = %d, want 600", cfg.MinDurationSeconds)
	}
}
