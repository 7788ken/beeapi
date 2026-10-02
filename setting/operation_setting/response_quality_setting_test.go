package operation_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
)

func TestEffectiveLowTokenThreshold(t *testing.T) {
	t.Parallel()
	if got := (*ResponseQualitySetting)(nil).EffectiveLowTokenThreshold(); got != DefaultLowTokenThreshold {
		t.Fatalf("nil setting = %d", got)
	}
	if got := (&ResponseQualitySetting{}).EffectiveLowTokenThreshold(); got != DefaultLowTokenThreshold {
		t.Fatalf("zero threshold = %d", got)
	}
	if got := (&ResponseQualitySetting{LowTokenThreshold: 120}).EffectiveLowTokenThreshold(); got != 120 {
		t.Fatalf("custom threshold = %d", got)
	}
}

func TestEffectiveQualityResponse(t *testing.T) {
	t.Parallel()
	if got := (*ResponseQualitySetting)(nil).EffectiveApologyStatusCode(); got != DefaultQualityStatusCode {
		t.Fatalf("nil apology status = %d", got)
	}
	if got := (&ResponseQualitySetting{ApologyStatusCode: 80}).EffectiveApologyStatusCode(); got != DefaultQualityStatusCode {
		t.Fatalf("invalid apology status = %d", got)
	}
	if got := (&ResponseQualitySetting{ApologyStatusCode: 429}).EffectiveApologyStatusCode(); got != 429 {
		t.Fatalf("custom apology status = %d", got)
	}
	if got := (&ResponseQualitySetting{}).EffectiveApologyMessage(); got != DefaultApologyMessage {
		t.Fatalf("empty apology message = %q", got)
	}
	if got := (&ResponseQualitySetting{ApologyMessage: "  custom apology  "}).EffectiveApologyMessage(); got != "  custom apology  " {
		t.Fatalf("custom apology message = %q", got)
	}

	if got := (&ResponseQualitySetting{LowTokenStatusCode: 502}).EffectiveLowTokenStatusCode(); got != 502 {
		t.Fatalf("custom low-token status = %d", got)
	}
	if got := (&ResponseQualitySetting{}).EffectiveLowTokenMessage(12, 300); got != "upstream completion tokens 12 below 300" {
		t.Fatalf("default low-token message = %q", got)
	}
	if got := (&ResponseQualitySetting{LowTokenMessage: "short {tokens}/{threshold}"}).EffectiveLowTokenMessage(12, 300); got != "short 12/300" {
		t.Fatalf("templated low-token message = %q", got)
	}
}

func TestEffectiveApologyKeywords(t *testing.T) {
	t.Parallel()
	if got := (*ResponseQualitySetting)(nil).EffectiveApologyKeywords(); len(got) == 0 {
		t.Fatal("nil setting must fall back to defaults")
	}
	if got := (&ResponseQualitySetting{}).EffectiveApologyKeywords(); len(got) == 0 {
		t.Fatal("empty keywords must fall back to defaults")
	}
	got := (&ResponseQualitySetting{ApologyKeywords: "Notice\nI cannot and will not\nnotice\n"}).EffectiveApologyKeywords()
	if len(got) != 2 || got[0] != "notice" || got[1] != "i cannot and will not" {
		t.Fatalf("parsed keywords = %#v", got)
	}
	if (&ResponseQualitySetting{ApplyAllChannels: true}).ChannelSwitchRequired() {
		t.Fatal("apply-all must not require a channel switch")
	}
	if !(&ResponseQualitySetting{}).ChannelSwitchRequired() {
		t.Fatal("zero setting must require a channel switch")
	}
	if (*ResponseQualitySetting)(nil).RetryAfterBlock() || (&ResponseQualitySetting{}).RetryAfterBlock() {
		t.Fatal("missing retry switch must keep intercepts from retrying")
	}
	if !(&ResponseQualitySetting{RetryOnBlock: true}).RetryAfterBlock() {
		t.Fatal("retry switch on must allow intercept retry")
	}
}

func TestResponseQualitySettingExportKeys(t *testing.T) {
	if config.GlobalConfig.Get("response_quality_setting") == nil {
		t.Fatal("response_quality_setting not registered")
	}
	all := config.GlobalConfig.ExportAllConfigs()
	for _, key := range []string{
		"response_quality_setting.block_apology_enabled",
		"response_quality_setting.apology_status_code",
		"response_quality_setting.apology_message",
		"response_quality_setting.apology_keywords",
		"response_quality_setting.apply_all_channels",
		"response_quality_setting.block_low_token_enabled",
		"response_quality_setting.low_token_threshold",
		"response_quality_setting.low_token_status_code",
		"response_quality_setting.low_token_message",
		"response_quality_setting.retry_on_block",
		"response_quality_setting.new_channel_block_apology",
		"response_quality_setting.new_channel_block_low_token",
	} {
		if _, ok := all[key]; !ok {
			t.Errorf("option key %q missing from ExportAllConfigs", key)
		}
	}
}
