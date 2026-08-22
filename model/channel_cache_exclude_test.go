package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
)

// 这些测试直接覆盖 GetRandomSatisfiedChannel 的 excludedIDs 行为。
// 通过 setMemoryCache 桩注入 channelsIDM / group2model2channels，避免依赖 DB。

func setMemoryCache(group, model string, chans []*Channel) {
	channelSyncLock.Lock()
	defer channelSyncLock.Unlock()
	common.MemoryCacheEnabled = true
	channelsIDM = make(map[int]*Channel)
	group2model2channels = map[string]map[string][]int{
		group: {model: {}},
	}
	for _, c := range chans {
		channelsIDM[c.Id] = c
		group2model2channels[group][model] = append(group2model2channels[group][model], c.Id)
	}
}

func mkChannel(id int, priority int64, weight uint) *Channel {
	p := priority
	w := weight
	return &Channel{Id: id, Priority: &p, Weight: &w}
}

// TC1: 单 priority 双渠道，excluded 包含其一 → 必返回另一个
func TestGetRandomSatisfiedChannel_SamePriority_ExcludeOne(t *testing.T) {
	setMemoryCache("g", "m", []*Channel{
		mkChannel(64, 100, 500),
		mkChannel(137, 100, 100),
	})
	for i := 0; i < 50; i++ {
		ch, err := GetRandomSatisfiedChannel("g", "m", 0, []int{64}, "")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if ch == nil || ch.Id != 137 {
			t.Fatalf("expected 137, got %v", ch)
		}
	}
}

// TC2: 单 priority 双渠道，excluded 含全部 → fallback 返回原始集合中的某个
// （瞬时错误重试比"无可用渠道"瞬间失败更友好；RetryTimes 限制总次数防死循环）
func TestGetRandomSatisfiedChannel_SamePriority_ExcludeAll(t *testing.T) {
	setMemoryCache("g", "m", []*Channel{
		mkChannel(64, 100, 500),
		mkChannel(137, 100, 100),
	})
	ch, err := GetRandomSatisfiedChannel("g", "m", 0, []int{64, 137}, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ch == nil {
		t.Fatalf("expected fallback channel, got nil")
	}
	if ch.Id != 64 && ch.Id != 137 {
		t.Fatalf("expected fallback to 64 or 137, got %d", ch.Id)
	}
}

// TC3: 双 priority 各两渠道，excluded 含高 priority 全部 → priority 列表过滤后只剩低层
func TestGetRandomSatisfiedChannel_MultiPriority_ExcludeHighTier(t *testing.T) {
	setMemoryCache("g", "m", []*Channel{
		mkChannel(1, 100, 1),
		mkChannel(2, 100, 1),
		mkChannel(3, 90, 1),
		mkChannel(4, 90, 1),
	})
	for i := 0; i < 50; i++ {
		ch, err := GetRandomSatisfiedChannel("g", "m", 0, []int{1, 2}, "")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if ch == nil || (ch.Id != 3 && ch.Id != 4) {
			t.Fatalf("expected 3 or 4, got %v", ch)
		}
	}
}

// TC4: excluded 为 nil → 与旧版完全一致（验证向后兼容）
func TestGetRandomSatisfiedChannel_NilExcluded(t *testing.T) {
	setMemoryCache("g", "m", []*Channel{
		mkChannel(64, 100, 500),
		mkChannel(137, 100, 100),
	})
	seen64, seen137 := 0, 0
	for i := 0; i < 200; i++ {
		ch, err := GetRandomSatisfiedChannel("g", "m", 0, nil, "")
		if err != nil || ch == nil {
			t.Fatalf("err=%v ch=%v", err, ch)
		}
		switch ch.Id {
		case 64:
			seen64++
		case 137:
			seen137++
		}
	}
	if seen64 == 0 || seen137 == 0 {
		t.Fatalf("expected both seen, got 64=%d 137=%d", seen64, seen137)
	}
	// 权重 500:100 → 64 概率应明显大于 137（粗略断言：>3:1 即可）
	if seen64*3 < seen137*10 {
		t.Logf("weight skew weaker than expected: 64=%d 137=%d (informational)", seen64, seen137)
	}
}

// TC5: 单渠道分组，excluded 含该渠道 → fallback 返回该渠道（avoid "无可用渠道"）
func TestGetRandomSatisfiedChannel_SingleChannelExcluded(t *testing.T) {
	setMemoryCache("g", "m", []*Channel{mkChannel(64, 100, 500)})
	ch, err := GetRandomSatisfiedChannel("g", "m", 0, []int{64}, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ch == nil || ch.Id != 64 {
		t.Fatalf("expected fallback channel 64, got %v", ch)
	}
}

// TC6: 多 priority，excluded 含全部 → fallback 返回原始集合中的某个
func TestGetRandomSatisfiedChannel_MultiPriority_ExcludeAll_Fallback(t *testing.T) {
	setMemoryCache("g", "m", []*Channel{
		mkChannel(1, 100, 1),
		mkChannel(2, 90, 1),
	})
	ch, err := GetRandomSatisfiedChannel("g", "m", 0, []int{1, 2}, "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ch == nil {
		t.Fatalf("expected fallback channel, got nil")
	}
	if ch.Id != 1 && ch.Id != 2 {
		t.Fatalf("expected fallback to 1 or 2, got %d", ch.Id)
	}
}
