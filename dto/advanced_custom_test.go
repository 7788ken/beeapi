package dto

import (
	"strings"
	"testing"
)

func TestAdvancedCustomConfigModelAwareRouting(t *testing.T) {
	config := &AdvancedCustomConfig{Routes: []AdvancedCustomRoute{
		{
			IncomingPath: "/v1/chat/completions",
			UpstreamPath: "/openai/chat",
			Converter:    "none",
			Models:       []string{"gpt-4o", "re:^o[13]-"},
		},
		{
			IncomingPath: "/v1/chat/completions",
			UpstreamPath: "/fallback/chat",
			Converter:    "none",
		},
		{
			IncomingPath: "/v1/models",
			UpstreamPath: "/catalog/models",
			Converter:    "none",
		},
	}}

	if err := config.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	for _, test := range []struct {
		model string
		path  string
	}{
		{model: "gpt-4o", path: "/openai/chat"},
		{model: "o3-mini", path: "/openai/chat"},
		{model: "claude-3-7-sonnet", path: "/fallback/chat"},
	} {
		route, ok := config.MatchPathForModel("/v1/chat/completions", test.model)
		if !ok || route.UpstreamPath != test.path {
			t.Fatalf("model %q route = %#v, %v; want path %q", test.model, route, ok, test.path)
		}
	}
	if config.SupportsPathForModel("/v1/responses", "gpt-4o") {
		t.Fatal("unexpected support for an unconfigured request path")
	}
}

func TestAdvancedCustomConfigRejectsAmbiguousOrUnsafeRoutes(t *testing.T) {
	tests := []struct {
		name   string
		routes []AdvancedCustomRoute
		want   string
	}{
		{
			name: "catch-all must be last",
			routes: []AdvancedCustomRoute{
				{IncomingPath: "/v1/chat/completions", UpstreamPath: "/a", Converter: "none"},
				{IncomingPath: "/v1/chat/completions", UpstreamPath: "/b", Converter: "none", Models: []string{"gpt-4o"}},
			},
			want: "catch-all route must be last",
		},
		{
			name: "invalid regex",
			routes: []AdvancedCustomRoute{
				{IncomingPath: "/v1/chat/completions", UpstreamPath: "/a", Converter: "none", Models: []string{"re:["}},
			},
			want: "regex is invalid",
		},
		{
			name: "unsupported scheme",
			routes: []AdvancedCustomRoute{
				{IncomingPath: "/v1/chat/completions", UpstreamPath: "ftp://upstream.example/chat", Converter: "none"},
			},
			want: "must use http or https",
		},
		{
			name: "models route cannot convert",
			routes: []AdvancedCustomRoute{
				{IncomingPath: "/v1/models", UpstreamPath: "/v1/models", Converter: "openai_chat_completions_to_openai_responses"},
			},
			want: "converter must be none",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := (&AdvancedCustomConfig{Routes: test.routes}).Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v; want containing %q", err, test.want)
			}
		})
	}
}
