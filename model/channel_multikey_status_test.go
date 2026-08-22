package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
)

func testMultiKeyChannel(status int) *Channel {
	return &Channel{
		Id:     10,
		Key:    "key-0\nkey-1",
		Status: status,
		ChannelInfo: ChannelInfo{
			IsMultiKey:         true,
			MultiKeySize:       2,
			MultiKeyStatusList: make(map[int]int),
		},
	}
}

func TestHandlerMultiKeyUpdateAutoDisablesOnlyAfterAllKeysDisabled(t *testing.T) {
	channel := testMultiKeyChannel(common.ChannelStatusEnabled)

	handlerMultiKeyUpdate(channel, "key-0", common.ChannelStatusAutoDisabled, "key 0 failed")
	if channel.Status != common.ChannelStatusEnabled {
		t.Fatalf("expected channel to remain enabled after one disabled key, got %d", channel.Status)
	}

	handlerMultiKeyUpdate(channel, "key-1", common.ChannelStatusAutoDisabled, "key 1 failed")
	if channel.Status != common.ChannelStatusAutoDisabled {
		t.Fatalf("expected channel auto disabled after all keys disabled, got %d", channel.Status)
	}
	if channel.ChannelInfo.MultiKeyStatusList[0] != common.ChannelStatusAutoDisabled {
		t.Fatalf("expected key 0 disabled status to be retained")
	}
	if channel.ChannelInfo.MultiKeyStatusList[1] != common.ChannelStatusAutoDisabled {
		t.Fatalf("expected key 1 disabled status to be retained")
	}
	if got := channel.GetOtherInfo()["status_reason"]; got != "All keys are disabled" {
		t.Fatalf("expected all-keys-disabled reason, got %v", got)
	}
}

func TestHandlerMultiKeyUpdateRestoresWhenAKeyIsEnabled(t *testing.T) {
	channel := testMultiKeyChannel(common.ChannelStatusAutoDisabled)
	channel.ChannelInfo.MultiKeyStatusList[0] = common.ChannelStatusAutoDisabled
	channel.ChannelInfo.MultiKeyStatusList[1] = common.ChannelStatusAutoDisabled
	channel.ChannelInfo.MultiKeyDisabledReason = map[int]string{0: "key 0 failed", 1: "key 1 failed"}
	channel.ChannelInfo.MultiKeyDisabledTime = map[int]int64{0: 100, 1: 200}

	handlerMultiKeyUpdate(channel, "key-0", common.ChannelStatusEnabled, "")

	if channel.Status != common.ChannelStatusEnabled {
		t.Fatalf("expected channel enabled after one key restored, got %d", channel.Status)
	}
	if _, ok := channel.ChannelInfo.MultiKeyStatusList[0]; ok {
		t.Fatalf("expected key 0 status to be removed")
	}
	if _, ok := channel.ChannelInfo.MultiKeyDisabledReason[0]; ok {
		t.Fatalf("expected key 0 disabled reason to be removed")
	}
	if _, ok := channel.ChannelInfo.MultiKeyDisabledTime[0]; ok {
		t.Fatalf("expected key 0 disabled time to be removed")
	}
	if channel.ChannelInfo.MultiKeyStatusList[1] != common.ChannelStatusAutoDisabled {
		t.Fatalf("expected key 1 disabled status to be retained")
	}
}

func TestHandlerMultiKeyUpdateDoesNotRestoreManuallyDisabledChannel(t *testing.T) {
	channel := testMultiKeyChannel(common.ChannelStatusManuallyDisabled)
	channel.ChannelInfo.MultiKeyStatusList[0] = common.ChannelStatusAutoDisabled
	channel.ChannelInfo.MultiKeyStatusList[1] = common.ChannelStatusAutoDisabled

	handlerMultiKeyUpdate(channel, "key-0", common.ChannelStatusEnabled, "")

	if channel.Status != common.ChannelStatusManuallyDisabled {
		t.Fatalf("expected manually disabled channel to stay disabled, got %d", channel.Status)
	}
}

func TestHandlerMultiKeyUpdateUnknownKeyDoesNotTouchIndexZero(t *testing.T) {
	channel := testMultiKeyChannel(common.ChannelStatusEnabled)
	channel.ChannelInfo.MultiKeyStatusList[0] = common.ChannelStatusAutoDisabled

	handlerMultiKeyUpdate(channel, "missing-key", common.ChannelStatusEnabled, "")

	if channel.ChannelInfo.MultiKeyStatusList[0] != common.ChannelStatusAutoDisabled {
		t.Fatalf("expected unknown key update not to remove key 0 status")
	}
	if channel.Status != common.ChannelStatusEnabled {
		t.Fatalf("expected unknown key update not to change channel status, got %d", channel.Status)
	}
}

func TestHasEnabledMultiKeyIgnoresStaleStatusIndexes(t *testing.T) {
	keys := []string{"key-0", "key-1"}
	statusList := map[int]int{
		0:  common.ChannelStatusAutoDisabled,
		9:  common.ChannelStatusAutoDisabled,
		10: common.ChannelStatusAutoDisabled,
	}

	if !hasEnabledMultiKey(keys, statusList) {
		t.Fatalf("expected key 1 to be treated as enabled despite stale disabled indexes")
	}
}

func TestCacheUpdateChannelStatusEvictsMultiKeyChannelWhenAllKeysDisabled(t *testing.T) {
	setMemoryCache("g", "m", []*Channel{
		{
			Id:     10,
			Key:    "key-0\nkey-1",
			Status: common.ChannelStatusEnabled,
			Models: "m",
			Group:  "g",
			ChannelInfo: ChannelInfo{
				IsMultiKey:         true,
				MultiKeySize:       2,
				MultiKeyStatusList: map[int]int{0: common.ChannelStatusAutoDisabled},
			},
		},
	})
	defer func() {
		common.MemoryCacheEnabled = false
	}()

	channel, err := GetRandomSatisfiedChannel("g", "m", 0, nil, "")
	if err != nil {
		t.Fatalf("select before disable: %v", err)
	}
	if channel == nil || channel.Id != 10 {
		t.Fatalf("expected channel 10 before all keys disabled, got %v", channel)
	}

	cached, err := CacheGetChannel(10)
	if err != nil {
		t.Fatalf("get cached channel: %v", err)
	}
	beforeStatus := cached.Status
	handlerMultiKeyUpdate(cached, "key-1", common.ChannelStatusAutoDisabled, "key 1 failed")
	if beforeStatus != cached.Status {
		CacheUpdateChannelStatus(10, cached.Status)
	}

	channel, err = GetRandomSatisfiedChannel("g", "m", 0, nil, "")
	if err != nil {
		t.Fatalf("select after disable: %v", err)
	}
	if channel != nil {
		t.Fatalf("expected multi-key channel to be evicted after all keys disabled, got %v", channel)
	}

	cached, err = CacheGetChannel(10)
	if err != nil {
		t.Fatalf("get cached channel: %v", err)
	}
	if cached.Status != common.ChannelStatusAutoDisabled {
		t.Fatalf("expected cached channel auto disabled, got %d", cached.Status)
	}
}
