package contentbackup

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
)

// TestContentBackupSessionKey 是 T01 任务卡给出的回归起点，逐字保留。
func TestContentBackupSessionKey(t *testing.T) {
	a := SessionKey("user", "same")
	if len(a) != 64 || a != SessionKey("user", "same") {
		t.Fatal("key must be deterministic full SHA256")
	}
	if a == SessionKey("prompt_cache_key", "same") {
		t.Fatal("sources must be isolated")
	}
}

// TestContentBackupSessionKeyIsStructuredHash 固定哈希输入：必须是
// SHA256(common.Marshal([]string{source, value})) 的小写完整 hex，
// 不是 source+value 的直接拼接，且 source/value 边界不可混淆。
func TestContentBackupSessionKeyIsStructuredHash(t *testing.T) {
	payload, err := common.Marshal([]string{"user", "same"})
	if err != nil {
		t.Fatalf("marshal session payload: %v", err)
	}
	want := common.Sha256(payload)
	got := SessionKey("user", "same")
	if got != want {
		t.Fatalf("SessionKey = %q, want SHA256(common.Marshal([source value])) = %q", got, want)
	}
	if got == common.Sha256([]byte("user"+"same")) {
		t.Fatal("session key must not hash the concatenated source and value")
	}
	if got != strings.ToLower(got) {
		t.Fatalf("session key %q must be lowercase hex", got)
	}
	if len(got) != 64 {
		t.Fatalf("session key must be the full SHA256 hex, got %d chars", len(got))
	}
	if SessionKey("a", "bc") == SessionKey("ab", "c") {
		t.Fatal("structured encoding must keep the source/value boundary")
	}
	if SessionKey("user", "u-1") == SessionKey("user", "u-2") {
		t.Fatal("different values must not collide")
	}
}

// TestContentBackupSessionKeyMissing 覆盖缺会话：目录名固定为 nosession。
func TestContentBackupSessionKeyMissing(t *testing.T) {
	if NoSessionKey != "nosession" {
		t.Fatalf("NoSessionKey is the frozen directory name %q, got %q", "nosession", NoSessionKey)
	}
	if got := SessionKey("user", ""); got != NoSessionKey {
		t.Fatalf("empty value must yield %q, got %q", NoSessionKey, got)
	}
	if got := SessionKey("", ""); got != NoSessionKey {
		t.Fatalf("empty source and value must yield %q, got %q", NoSessionKey, got)
	}
}

// TestContentBackupSessionKeyIsolatesEveryFixedSource 保证同一原值在不同固定来源下不会混档。
func TestContentBackupSessionKeyIsolatesEveryFixedSource(t *testing.T) {
	seen := make(map[string]string, 4)
	for _, source := range SessionSources() {
		key := SessionKey(source, "same")
		if len(key) != 64 {
			t.Fatalf("source %q produced a %d char key, want the full SHA256 hex", source, len(key))
		}
		if key == NoSessionKey {
			t.Fatalf("source %q must not collapse into %q", source, NoSessionKey)
		}
		if previous, duplicated := seen[key]; duplicated {
			t.Fatalf("sources %q and %q collide on the same value", previous, source)
		}
		seen[key] = source
	}
	if len(seen) != len(SessionSources()) {
		t.Fatalf("expected one distinct key per source, got %d", len(seen))
	}
}

// TestContentBackupSessionSources 冻结一期固定来源字面量（第 5.1 节）。
func TestContentBackupSessionSources(t *testing.T) {
	if SessionSourceMetadataUserID != "metadata.user_id" {
		t.Fatalf("claude source must stay %q, got %q", "metadata.user_id", SessionSourceMetadataUserID)
	}
	if SessionSourcePromptCacheKey != "prompt_cache_key" {
		t.Fatalf("responses source must stay %q, got %q", "prompt_cache_key", SessionSourcePromptCacheKey)
	}
	if SessionSourceUser != "user" {
		t.Fatalf("top level source must stay %q, got %q", "user", SessionSourceUser)
	}
	want := []string{SessionSourceMetadataUserID, SessionSourcePromptCacheKey, SessionSourceUser}
	got := SessionSources()
	if len(got) != len(want) {
		t.Fatalf("phase 1 fixes %d session sources, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("SessionSources()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	got[0] = "mutated"
	if SessionSources()[0] != want[0] {
		t.Fatal("SessionSources must return a fresh slice the caller may mutate")
	}
}

// TestContentBackupResolveSession 覆盖空值/非字符串/超长/来源判定（第 5.1 节）。
func TestContentBackupResolveSession(t *testing.T) {
	atLimit := strings.Repeat("a", SessionValueMaxBytes)
	overLimit := strings.Repeat("a", SessionValueMaxBytes+1)
	multiByteAtLimit := strings.Repeat("会", 341)   // 1023 bytes
	multiByteOverLimit := strings.Repeat("会", 342) // 1026 bytes
	tests := []struct {
		name       string
		source     string
		raw        any
		wantSource string
		wantValue  string
		wantReason string
	}{
		{name: "accepted string", source: SessionSourceUser, raw: "u-10086", wantSource: SessionSourceUser, wantValue: "u-10086"},
		{name: "absent field", source: SessionSourceUser, raw: nil, wantReason: SessionMissingReasonAbsent},
		{name: "empty string counts as absent", source: SessionSourceUser, raw: "", wantReason: SessionMissingReasonAbsent},
		{name: "boolean is never coerced", source: SessionSourceUser, raw: true, wantReason: SessionMissingReasonNotString},
		{name: "number is never coerced", source: SessionSourceUser, raw: float64(10086), wantReason: SessionMissingReasonNotString},
		{name: "object is never coerced", source: SessionSourceUser, raw: map[string]any{"id": "u-10086"}, wantReason: SessionMissingReasonNotString},
		{name: "array is never coerced", source: SessionSourceUser, raw: []any{"u-10086"}, wantReason: SessionMissingReasonNotString},
		{name: "1024 bytes accepted", source: SessionSourceUser, raw: atLimit, wantSource: SessionSourceUser, wantValue: atLimit},
		{name: "1025 bytes rejected", source: SessionSourceUser, raw: overLimit, wantReason: SessionMissingReasonTooLong},
		{name: "1023 multibyte bytes accepted", source: SessionSourceMetadataUserID, raw: multiByteAtLimit, wantSource: SessionSourceMetadataUserID, wantValue: multiByteAtLimit},
		{name: "1026 multibyte bytes rejected", source: SessionSourceMetadataUserID, raw: multiByteOverLimit, wantReason: SessionMissingReasonTooLong},
		{name: "341 runes stay under the byte limit", source: SessionSourcePromptCacheKey, raw: strings.Repeat("会", 341), wantSource: SessionSourcePromptCacheKey, wantValue: strings.Repeat("会", 341)},
		{name: "empty source cannot be attributed", source: "", raw: "u-10086", wantReason: SessionMissingReasonAbsent},
		{name: "value is kept verbatim", source: SessionSourcePromptCacheKey, raw: "  Cache-Key/1  ", wantSource: SessionSourcePromptCacheKey, wantValue: "  Cache-Key/1  "},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ref := ResolveSession(test.source, test.raw)
			assertSessionRef(t, ref, test.wantSource, test.wantValue, test.wantReason)
		})
	}
	if SessionValueMaxBytes != 1024 {
		t.Fatalf("the raw session value limit is frozen at 1024 bytes, got %d", SessionValueMaxBytes)
	}
}

// TestContentBackupResolveSessionFromParsedBody 走项目 JSON 包装解析真实请求体，
// 证明数字/布尔 user 不会被转成会话，而嵌套 metadata.user_id 可以。
func TestContentBackupResolveSessionFromParsedBody(t *testing.T) {
	var openaiBody map[string]any
	if err := common.UnmarshalJsonStr(`{"model":"example-model","user":"u-10086"}`, &openaiBody); err != nil {
		t.Fatalf("unmarshal openai body: %v", err)
	}
	assertSessionRef(t, ResolveSession(SessionSourceUser, openaiBody["user"]), SessionSourceUser, "u-10086", "")

	var numericBody map[string]any
	if err := common.UnmarshalJsonStr(`{"user":10086}`, &numericBody); err != nil {
		t.Fatalf("unmarshal numeric body: %v", err)
	}
	assertSessionRef(t, ResolveSession(SessionSourceUser, numericBody["user"]), "", "", SessionMissingReasonNotString)

	var booleanBody map[string]any
	if err := common.UnmarshalJsonStr(`{"user":true}`, &booleanBody); err != nil {
		t.Fatalf("unmarshal boolean body: %v", err)
	}
	assertSessionRef(t, ResolveSession(SessionSourceUser, booleanBody["user"]), "", "", SessionMissingReasonNotString)

	var nullBody map[string]any
	if err := common.UnmarshalJsonStr(`{"user":null}`, &nullBody); err != nil {
		t.Fatalf("unmarshal null body: %v", err)
	}
	assertSessionRef(t, ResolveSession(SessionSourceUser, nullBody["user"]), "", "", SessionMissingReasonAbsent)

	var claudeBody map[string]any
	if err := common.UnmarshalJsonStr(`{"metadata":{"user_id":"claude-user-1"}}`, &claudeBody); err != nil {
		t.Fatalf("unmarshal claude body: %v", err)
	}
	metadata, _ := claudeBody["metadata"].(map[string]any)
	assertSessionRef(t, ResolveSession(SessionSourceMetadataUserID, metadata["user_id"]),
		SessionSourceMetadataUserID, "claude-user-1", "")
}

// TestContentBackupMissingSession 覆盖“有界前缀不足以解析”等由调用方判定的原因。
func TestContentBackupMissingSession(t *testing.T) {
	if SessionMissingReasonAbsent != "absent" {
		t.Fatalf("the envelope example freezes the literal %q, got %q", "absent", SessionMissingReasonAbsent)
	}
	reasons := []string{
		SessionMissingReasonAbsent,
		SessionMissingReasonNotString,
		SessionMissingReasonTooLong,
		SessionMissingReasonUnparseable,
	}
	for _, reason := range reasons {
		if reason == "" {
			t.Fatal("missing reason constants must not be empty")
		}
		ref := MissingSession(reason)
		if ref.Source != nil || ref.Value != nil {
			t.Fatalf("reason %q must not carry a source or value", reason)
		}
		if common.DerefStringOr(ref.MissingReason, "") != reason {
			t.Fatalf("reason = %q, want %q", common.DerefStringOr(ref.MissingReason, ""), reason)
		}
	}
	seen := make(map[string]bool, len(reasons))
	for _, reason := range reasons {
		if seen[reason] {
			t.Fatalf("missing reason %q is duplicated", reason)
		}
		seen[reason] = true
	}
}

// TestContentBackupMetadataSessionKey 覆盖 Metadata 上的会话键派生。
func TestContentBackupMetadataSessionKey(t *testing.T) {
	meta := validMetadata()
	if got := meta.SessionKey(); got != NoSessionKey {
		t.Fatalf("absent session must yield %q, got %q", NoSessionKey, got)
	}
	source := SessionSourcePromptCacheKey
	value := "cache-key-1"
	meta.SessionSource = &source
	meta.SessionValue = &value
	meta.SessionMissingReason = nil
	if got, want := meta.SessionKey(), SessionKey(source, value); got != want {
		t.Fatalf("SessionKey() = %q, want %q", got, want)
	}
	meta.SessionSource = nil
	if got, want := meta.SessionKey(), SessionKey("", value); got != want {
		t.Fatalf("SessionKey() without a source = %q, want %q", got, want)
	}
	empty := ""
	meta.SessionValue = &empty
	if got := meta.SessionKey(); got != NoSessionKey {
		t.Fatalf("an empty session value must yield %q, got %q", NoSessionKey, got)
	}
}

func assertSessionRef(t *testing.T, ref SessionRef, wantSource, wantValue, wantReason string) {
	t.Helper()
	if got := common.DerefStringOr(ref.Source, ""); got != wantSource {
		t.Errorf("source = %q, want %q", got, wantSource)
	}
	if got := common.DerefStringOr(ref.Value, ""); got != wantValue {
		t.Errorf("value = %q, want %q", got, wantValue)
	}
	if got := common.DerefStringOr(ref.MissingReason, ""); got != wantReason {
		t.Errorf("missing reason = %q, want %q", got, wantReason)
	}
	if wantReason != "" && (ref.Source != nil || ref.Value != nil) {
		t.Errorf("reason %q must not carry a source or value", wantReason)
	}
	if wantReason == "" && ref.MissingReason != nil {
		t.Errorf("an accepted session must not carry a missing reason, got %q", *ref.MissingReason)
	}
}
