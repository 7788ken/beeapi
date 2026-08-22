package service

import (
	"strings"
	"testing"
)

// TestAcSearchOnSensitivePatterns 验证 vendor AcSearch 与 sensitive_monitor 共享匹配
// 在大小写、子串、多模式、Unicode 上的预期行为。Phase 2 改造的回归基线。
func TestAcSearchOnSensitivePatterns(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		dict     []string
		wantAny  bool
		wantHits []string
	}{
		{
			name:     "case insensitive substring match",
			body:     "Hello World, FOO is here",
			dict:     []string{"foo"},
			wantAny:  true,
			wantHits: []string{"foo"},
		},
		{
			name:     "no match",
			body:     "完全干净的文本",
			dict:     []string{"badword"},
			wantAny:  false,
			wantHits: nil,
		},
		{
			name:     "multi pattern unicode",
			body:     "请勿提及违禁A 和 违禁B 内容",
			dict:     []string{"违禁a", "违禁b"},
			wantAny:  true,
			wantHits: []string{"违禁a", "违禁b"},
		},
		{
			name:     "empty dict",
			body:     "anything",
			dict:     []string{},
			wantAny:  false,
			wantHits: nil,
		},
		{
			name:     "empty body",
			body:     "",
			dict:     []string{"foo"},
			wantAny:  false,
			wantHits: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lower := strings.ToLower(tc.body)
			got, words := AcSearch(lower, tc.dict, false)
			if got != tc.wantAny {
				t.Fatalf("hit flag: got %v, want %v", got, tc.wantAny)
			}
			gotSet := make(map[string]bool)
			for _, w := range words {
				gotSet[w] = true
			}
			for _, want := range tc.wantHits {
				if !gotSet[want] {
					t.Errorf("expected hit %q, got %v", want, words)
				}
			}
		})
	}
}

// TestExtractSensitiveSnippet 验证片段提取边界。
func TestExtractSensitiveSnippet(t *testing.T) {
	body := "0123456789abcdefghij0123456789ABCDEFGHIJ" // 40 字节
	cases := []struct {
		name       string
		start, end int
		wantPrefix string
	}{
		{"middle", 15, 18, "0123456789abcdef"},
		{"start", 0, 3, "012"},
		{"end", 35, 40, "GHIJ"},
		{"out of range start", -5, 3, "012"},
		{"out of range end", 35, 100, "GHIJ"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := ExtractSensitiveSnippet(body, tc.start, tc.end)
			if !strings.Contains(s, tc.wantPrefix) {
				t.Errorf("snippet %q does not contain %q", s, tc.wantPrefix)
			}
		})
	}
}
