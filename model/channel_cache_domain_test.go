package model

import "testing"

func mkChannelDomain(id int, domain string) *Channel {
	p := int64(100)
	w := uint(100)
	d := domain
	return &Channel{Id: id, Priority: &p, Weight: &w, CacheDomain: &d}
}

// TestGetRandomSatisfiedChannel_SameDomainFilter 覆盖 same_domain 候选过滤：
// 只在同 cacheDomainFilter 内选渠道；域内全排除时不外溢、返回 nil（不回退到其它域）。
func TestGetRandomSatisfiedChannel_SameDomainFilter(t *testing.T) {
	setMemoryCache("g", "m", []*Channel{
		mkChannelDomain(1, "A"),
		mkChannelDomain(2, "A"),
		mkChannelDomain(3, "B"),
	})

	// 过滤域 A、排除 1 → 必返回 2（同域），绝不返回 3（域 B）
	for i := 0; i < 50; i++ {
		ch, err := GetRandomSatisfiedChannel("g", "m", 0, []int{1}, "A")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if ch == nil || ch.Id != 2 {
			t.Fatalf("filter A excl 1: got %v, want channel 2", ch)
		}
	}

	// 过滤域 A、域内 1+2 全排除 → 不外溢到域 B → nil（filter 生效时无 fallback）
	ch, err := GetRandomSatisfiedChannel("g", "m", 0, []int{1, 2}, "A")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ch != nil {
		t.Fatalf("filter A all-A-excluded: expected nil (no escape to domain B), got channel %d", ch.Id)
	}

	// 不过滤（cross_channel/inherit）→ 排除 1+2 后仍可选到 3（确认 filter 是 opt-in）
	got3 := false
	for i := 0; i < 200; i++ {
		c, _ := GetRandomSatisfiedChannel("g", "m", 0, []int{1, 2}, "")
		if c != nil && c.Id == 3 {
			got3 = true
			break
		}
	}
	if !got3 {
		t.Fatalf("no filter: expected channel 3 reachable when domain filter empty")
	}
}

// TestGetRandomSatisfiedChannel_SingleChannel_DomainExhausted 覆盖单渠道分组的耗尽行为：
// same_domain 档（filter 非空）域内唯一候选已被排除 → fail-fast 返回 nil（与多渠道分支一致）；
// 非 same_domain（filter 空）保留旧 fallback：仍返回该渠道再试一次。
func TestGetRandomSatisfiedChannel_SingleChannel_DomainExhausted(t *testing.T) {
	setMemoryCache("g", "m", []*Channel{mkChannelDomain(7, "A")})

	// 域过滤激活 + 唯一候选已排除 → nil，由 relay 层返回上一轮真实上游错误
	ch, err := GetRandomSatisfiedChannel("g", "m", 0, []int{7}, "A")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ch != nil {
		t.Fatalf("single-channel domain exhausted: expected nil (fail fast), got channel %d", ch.Id)
	}

	// 无域过滤 → 保留旧行为：单渠道被排除仍返回它（避免"无可用渠道"瞬间失败）
	ch2, err := GetRandomSatisfiedChannel("g", "m", 0, []int{7}, "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ch2 == nil || ch2.Id != 7 {
		t.Fatalf("single-channel no-filter: expected fallback to channel 7, got %v", ch2)
	}
}

// TestGetRandomSatisfiedChannel_EmptyDomain_DegradesToSelf 覆盖 same_domain 但未设 cache_domain：
// GetCacheDomain 回退 "ch:<id>"，过滤后只匹配自己；自己被排除则无候选（安全降级为不跨渠道）。
func TestGetRandomSatisfiedChannel_EmptyDomain_DegradesToSelf(t *testing.T) {
	// 两个均未设 cache_domain 的渠道（domain 回退 ch:10 / ch:11）
	c10 := mkChannel(10, 100, 100)
	c11 := mkChannel(11, 100, 100)
	setMemoryCache("g", "m", []*Channel{c10, c11})

	// origin=10 的回退域 = "ch:10"；排除 10 → 11 的域是 "ch:11" != "ch:10" → 无候选
	ch, err := GetRandomSatisfiedChannel("g", "m", 0, []int{10}, c10.GetCacheDomain())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ch != nil {
		t.Fatalf("empty-domain same_domain: expected nil (no peer in ch:10 domain), got channel %d", ch.Id)
	}
}
