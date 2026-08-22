package doubao

import (
	"math"
	"testing"
)

// TestGetVideoInputRatio_ModelNameNormalization 验证折扣模型名归一化：
// 对外名（无版本号）、带版本号、dreamina 映射名均应命中同一折扣族，
// 修复此前 videoInputRatioMap 只认带版本号键、导致对外名查不到折扣的缺陷。
func TestGetVideoInputRatio_ModelNameNormalization(t *testing.T) {
	const (
		stdRatio  = 28.0 / 46.0
		fastRatio = 22.0 / 37.0
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
		// 未配置折扣的模型/变体 —— 应返回 false，按基础价计费
		{"1.0 pro 无折扣配置", "doubao-seedance-1-0-pro-250528", false, 0},
		{"mini 未配置", "dreamina-seedance-2-0-mini", false, 0},
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
		{"doubao-seedance-2-0-260128", "720p", true, 28.0 / 46.0},
		{"dreamina-seedance-2-0", "1080P", false, 51.0 / 46.0},
		{"doubao-seedance-2-0", "1080p", true, 31.0 / 46.0},
		{"doubao-seedance-2-0", "4K", false, 26.0 / 46.0},
		{"doubao-seedance-2-0", "4k", true, 16.0 / 46.0},
		{"doubao-seedance-2-0-fast", "720p", true, 22.0 / 37.0},
	}
	for _, test := range tests {
		got, ok := GetSeedanceBillingRatio(test.model, test.resolution, test.hasVideo)
		if !ok || math.Abs(got-test.want) > 1e-9 {
			t.Fatalf("model=%s resolution=%s video=%v: got (%v,%v), want %v", test.model, test.resolution, test.hasVideo, got, ok, test.want)
		}
	}
}
