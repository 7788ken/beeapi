package constant

import "testing"

func TestGeminiActionFromPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		want string
	}{
		{"/v1beta/models/gemini-2.0-flash:countTokens", "countTokens"},
		{"/v1beta/models/gemini-2.0-flash:generateContent", "generateContent"},
		{"/v1beta/models/gemini-2.0-flash:streamGenerateContent", "streamGenerateContent"},
		{"/v1beta/models/gemini-2.0-flash:embedContent", "embedContent"},
		{"/v1/models/gemini-2.0-flash:countTokens", "countTokens"},
		{"/v1beta/models/gemini-2.0-flash:countTokens?key=sk-test", "countTokens"},
		{"/v1beta/models/gemini-2.0-flash:countTokens/", "countTokens"},
		{"/v1beta/models/gemini-2.0-flash", ""},
		{"/v1/chat/completions", ""},
		{"/v1/models", ""},
	}
	for _, tc := range cases {
		if got := GeminiActionFromPath(tc.path); got != tc.want {
			t.Fatalf("GeminiActionFromPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestIsUnsupportedGeminiCountTokensPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		want bool
	}{
		{"/v1beta/models/gemini-2.0-flash:countTokens", true},
		{"/v1beta/models/gemini-2.0-flash:CountTokens", true},
		{"/v1/models/gemini-2.0-flash:countTokens", true},
		{"/v1beta/models/gemini-2.0-flash:generateContent", false},
		{"/v1beta/models/gemini-2.0-flash:streamGenerateContent", false},
		{"/v1beta/models/gemini-2.0-flash:embedContent", false},
		{"/v1/chat/completions", false},
	}
	for _, tc := range cases {
		if got := IsUnsupportedGeminiCountTokensPath(tc.path); got != tc.want {
			t.Fatalf("IsUnsupportedGeminiCountTokensPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
