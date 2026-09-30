package doubao

import (
	"math"
	"strings"
	"testing"
)

// TestGetVideoInputRatio_ModelNameNormalization 验证折扣模型名归一化：
// 对外名（无版本号）、带版本号、dreamina 映射名均应命中同一折扣族，
// 修复此前 videoInputRatioMap 只认带版本号键、导致对外名查不到折扣的缺陷。
func TestGetVideoInputRatio_ModelNameNormalization(t *testing.T) {
	const (
		stdRatio  = 4.3 / 7.0
		fastRatio = 2.48 / 4.20
		miniRatio = 0.84 / 1.40
		v25Ratio  = 6.4 / 10.7
	)
	cases := []struct {
		name      string
		model     string
		wantOK    bool
		wantRatio float64
	}{
		// 标准 2.0 —— 各种别名都应命中标准折扣
		{"对外名无版本号", "doubao-seedance-2-0", true, stdRatio},
		{"带版本号", "doubao-seedance-2-0-260128", true, stdRatio},
		{"dreamina 映射名", "dreamina-seedance-2-0", true, stdRatio},
		{"大小写混合", "Doubao-Seedance-2-0", true, stdRatio},
		// fast —— 各种别名都应命中 fast 折扣
		{"fast 对外名", "doubao-seedance-2-0-fast", true, fastRatio},
		{"fast 带版本号", "doubao-seedance-2-0-fast-260128", true, fastRatio},
		{"fast dreamina", "dreamina-seedance-2-0-fast", true, fastRatio},
		// max 线路（-max 后缀）与 hc/df 折扣不变，应命中同一模型族
		{"max 标准", "dreamina-seedance-2-0-260128-max", true, stdRatio},
		{"max fast", "dreamina-seedance-2-0-fast-260128-max", true, fastRatio},
		// mini / 2.5 —— 上游报价单里都有带参考视频折扣
		{"mini 对外名", "dreamina-seedance-2-0-mini", true, miniRatio},
		{"mini max 线路", "dreamina-seedance-2-0-mini-260615-max", true, miniRatio},
		{"2.5 max 线路", "dreamina-seedance-2-5-260628-max", true, v25Ratio},
		{"2.5 对外名", "dreamina-seedance-2-5", true, v25Ratio},
		// 未配置折扣的模型/变体 —— 应返回 false，按基础价计费
		{"1.0 pro 无折扣配置", "doubao-seedance-1-0-pro-250528", false, 0},
		{"filter-off 未配置", "dreamina-seedance-2-0-fast-filter-off", false, 0},
		{"空字符串", "", false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := GetVideoInputRatio(tc.model)
			if ok != tc.wantOK {
				t.Fatalf("model=%q: ok=%v, want %v", tc.model, ok, tc.wantOK)
			}
			if tc.wantOK && math.Abs(got-tc.wantRatio) > 1e-9 {
				t.Fatalf("model=%q: ratio=%v, want %v", tc.model, got, tc.wantRatio)
			}
		})
	}
}

func TestCanonicalSeedanceModel(t *testing.T) {
	cases := map[string]string{
		"doubao-seedance-2-0":             "seedance-2-0",
		"doubao-seedance-2-0-260128":      "seedance-2-0",
		"doubao-seedance-2-0-fast-260128": "seedance-2-0-fast",
		"dreamina-seedance-2-0":           "seedance-2-0",
		"  Doubao-Seedance-2-0  ":         "seedance-2-0",
		// -max 线路后缀剥离后与 hc/df 同族（上游公告折扣不变）
		"dreamina-seedance-2-0-260128-max":      "seedance-2-0",
		"dreamina-seedance-2-0-fast-260128-max": "seedance-2-0-fast",
		"dreamina-seedance-2-5-260628-max":      "seedance-2-5",
		// 末尾非纯数字 / 不足 6 位不应被当作版本号裁掉
		"dreamina-seedance-2-0-fast-filter-off": "seedance-2-0-fast-filter-off",
		"seedance-2-0-12345":                    "seedance-2-0-12345",
	}
	for in, want := range cases {
		if got := canonicalSeedanceModel(in); got != want {
			t.Errorf("canonicalSeedanceModel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGetSeedanceBillingRatioResolutionAndVideoMatrix(t *testing.T) {
	tests := []struct {
		model      string
		resolution string
		hasVideo   bool
		want       float64
	}{
		{"doubao-seedance-2-0", "720p", false, 1},
		{"doubao-seedance-2-0", "480p", false, 1},
		{"doubao-seedance-2-0", "", false, 1},
		{"doubao-seedance-2-0-260128", "720p", true, 4.3 / 7.0},
		{"dreamina-seedance-2-0", "1080P", false, 7.7 / 7.0},
		{"doubao-seedance-2-0", "1080p", true, 4.7 / 7.0},
		{"doubao-seedance-2-0", "4K", false, 4.0 / 7.0},
		{"doubao-seedance-2-0", "4k", true, 2.4 / 7.0},
		{"doubao-seedance-2-0-fast", "720p", true, 2.48 / 4.20},
		{"dreamina-seedance-2-0-fast-260128-max", "480p", false, 1},
		// mini：只有 480p/720p 两档
		{"dreamina-seedance-2-0-mini-260615-max", "720p", false, 1},
		{"dreamina-seedance-2-0-mini-260615-max", "480p", true, 0.84 / 1.40},
		// 2.5：480p/720p/1080p，带参考视频有折扣
		{"dreamina-seedance-2-5-260628-max", "720p", false, 1},
		{"dreamina-seedance-2-5-260628-max", "720p", true, 6.4 / 10.7},
		{"dreamina-seedance-2-5-260628-max", "1080p", false, 11.7 / 10.7},
		{"dreamina-seedance-2-5-260628-max", "1080p", true, 7.0 / 10.7},
	}
	for _, test := range tests {
		got, ok := GetSeedanceBillingRatio(test.model, test.resolution, test.hasVideo)
		if !ok || math.Abs(got-test.want) > 1e-9 {
			t.Fatalf("model=%s resolution=%s video=%v: got (%v,%v), want %v", test.model, test.resolution, test.hasVideo, got, ok, test.want)
		}
	}
}

// TestIsSeedanceDurationAllowed 验证 30s 只对登记模型放行：
// 标准区间 4~15s 对所有模型不变；2-5-hc 额外接受精确值 30；
// 16~29s 这类中间值即便对 2-5-hc 也必须拒绝（上游不支持，放过去只会换回 5xx + 重试）。
func TestIsSeedanceDurationAllowed(t *testing.T) {
	cases := []struct {
		name  string
		model string
		sec   int
		want  bool
	}{
		// 标准区间：所有模型一致
		{"标准下界", "doubao-seedance-2-0-260128", 4, true},
		{"标准上界", "doubao-seedance-2-0-260128", 15, true},
		{"低于下界", "doubao-seedance-2-0-260128", 3, false},
		{"超出上界", "doubao-seedance-2-0-260128", 16, false},
		// 2-5-hc：额外放行 30
		{"2-5-hc 标准区间照常", "dreamina-seedance-2-5-hc", 10, true},
		{"2-5-hc 放行 30", "dreamina-seedance-2-5-hc", 30, true},
		{"2-5-hc 大小写不敏感", "Dreamina-Seedance-2-5-HC", 30, true},
		{"2-5-hc 带空白", "  dreamina-seedance-2-5-hc  ", 30, true},
		{"2-5-hc 仍拒中间值 20", "dreamina-seedance-2-5-hc", 20, false},
		{"2-5-hc 仍拒中间值 29", "dreamina-seedance-2-5-hc", 29, false},
		{"2-5-hc 仍拒 31", "dreamina-seedance-2-5-hc", 31, false},
		// 未登记模型不得因族名相近被放行
		{"2-5-ep 未开放 30", "dreamina-seedance-2-5-ep", 30, false},
		{"2-0-hc 未开放 30", "dreamina-seedance-2-0-hc", 30, false},
		{"2-0 未开放 30", "doubao-seedance-2-0-260128", 30, false},
		{"空模型名", "", 30, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSeedanceDurationAllowed(tc.model, tc.sec); got != tc.want {
				t.Fatalf("isSeedanceDurationAllowed(%q, %d) = %v, want %v", tc.model, tc.sec, got, tc.want)
			}
		})
	}
}

// TestSeedanceDurationError 错误文案要带上该模型真正可用的取值，
// 否则客户拿到 400 只知道 4~15，不知道 30 可用。
func TestSeedanceDurationError(t *testing.T) {
	withExtra := seedanceDurationError("dreamina-seedance-2-5-hc", 25).Error()
	for _, want := range []string{"4", "15", "30", "dreamina-seedance-2-5-hc", "25"} {
		if !strings.Contains(withExtra, want) {
			t.Fatalf("error %q missing %q", withExtra, want)
		}
	}
	// 未登记额外值的模型保持原有文案，不出现 30 误导客户
	plain := seedanceDurationError("doubao-seedance-2-0-260128", 30).Error()
	if strings.Contains(plain, "one of") {
		t.Fatalf("unexpected extra-duration hint for unlisted model: %q", plain)
	}
}
