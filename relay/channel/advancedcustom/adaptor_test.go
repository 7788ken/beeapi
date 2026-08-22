package advancedcustom

import (
	"net/url"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func TestBuildModelListRequestUsesExplicitRouteAndAuth(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "https://upstream.example/base",
			ApiKey:         "secret-value",
			ChannelOtherSettings: dto.ChannelOtherSettings{
				AdvancedCustom: &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{
					{
						IncomingPath: "/v1/chat/completions",
						UpstreamPath: "/chat",
						Converter:    "none",
					},
					{
						IncomingPath: "/v1/models",
						UpstreamPath: "/catalog/models?source=channel",
						Converter:    "none",
						Auth: &dto.AdvancedCustomRouteAuth{
							Type:  dto.AdvancedCustomAuthTypeHeader,
							Name:  "X-API-Key",
							Value: "prefix-{api_key}",
						},
					},
				}},
			},
		},
	}

	requestURL, header, err := adaptor.BuildModelListRequest(info)
	if err != nil {
		t.Fatalf("BuildModelListRequest() error = %v", err)
	}
	if requestURL != "https://upstream.example/base/catalog/models?source=channel" {
		t.Fatalf("request URL = %q", requestURL)
	}
	if got := header.Get("X-API-Key"); got != "prefix-secret-value" {
		t.Fatalf("X-API-Key = %q", got)
	}
	if got := header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization should be omitted, got %q", got)
	}
}

func TestGetRequestURLMatchesPathModelAndQueryAuth(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		OriginModelName: "public-model",
		RequestURLPath:  "/v1/chat/completions",
		IsStream:        true,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://unused.example",
			ApiKey:            "query-secret",
			UpstreamModelName: "gemini-2.5-pro",
			ChannelOtherSettings: dto.ChannelOtherSettings{
				AdvancedCustom: &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{
					{
						IncomingPath: "/v1/chat/completions",
						UpstreamPath: "https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent",
						Converter:    "openai_chat_completions_to_gemini_generate_content",
						Models:       []string{"public-model"},
						Auth: &dto.AdvancedCustomRouteAuth{
							Type:  dto.AdvancedCustomAuthTypeQuery,
							Name:  "key",
							Value: "{api_key}",
						},
					},
				}},
			},
		},
	}

	requestURL, err := adaptor.GetRequestURL(info)
	if err != nil {
		t.Fatalf("GetRequestURL() error = %v", err)
	}
	parsed, err := url.Parse(requestURL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	if parsed.Path != "/v1beta/models/gemini-2.5-pro:streamGenerateContent" {
		t.Fatalf("path = %q", parsed.Path)
	}
	if parsed.Query().Get("alt") != "sse" || parsed.Query().Get("key") != "query-secret" {
		t.Fatalf("query = %q", parsed.RawQuery)
	}
}

func TestGetRequestURLFailsWhenRouteDoesNotMatchModel(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		OriginModelName: "claude-3-7-sonnet",
		RequestURLPath:  "/v1/chat/completions",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "https://upstream.example",
			ChannelOtherSettings: dto.ChannelOtherSettings{
				AdvancedCustom: &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{
					{
						IncomingPath: "/v1/chat/completions",
						UpstreamPath: "/chat",
						Converter:    "none",
						Models:       []string{"gpt-4o"},
					},
				}},
			},
		},
	}

	if _, err := adaptor.GetRequestURL(info); err == nil {
		t.Fatal("expected route mismatch to fail")
	}
}
