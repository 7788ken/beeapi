package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCalculateTextQuotaSummaryUnifiedForClaudeSemantic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	usage := &dto.Usage{
		PromptTokens:     1000,
		CompletionTokens: 200,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         100,
			CachedCreationTokens: 50,
		},
		ClaudeCacheCreation5mTokens: 10,
		ClaudeCacheCreation1hTokens: 20,
	}

	priceData := types.PriceData{
		ModelRatio:           1,
		CompletionRatio:      2,
		CacheRatio:           0.1,
		CacheCreationRatio:   1.25,
		CacheCreation5mRatio: 1.25,
		CacheCreation1hRatio: 2,
		GroupRatioInfo: types.GroupRatioInfo{
			GroupRatio: 1,
		},
	}

	chatRelayInfo := &relaycommon.RelayInfo{
		RelayFormat:             types.RelayFormatOpenAI,
		FinalRequestRelayFormat: types.RelayFormatClaude,
		OriginModelName:         "claude-3-7-sonnet",
		PriceData:               priceData,
		StartTime:               time.Now(),
	}
	messageRelayInfo := &relaycommon.RelayInfo{
		RelayFormat:             types.RelayFormatClaude,
		FinalRequestRelayFormat: types.RelayFormatClaude,
		OriginModelName:         "claude-3-7-sonnet",
		PriceData:               priceData,
		StartTime:               time.Now(),
	}

	chatSummary := calculateTextQuotaSummary(ctx, chatRelayInfo, usage)
	messageSummary := calculateTextQuotaSummary(ctx, messageRelayInfo, usage)

	require.Equal(t, messageSummary.Quota, chatSummary.Quota)
	require.Equal(t, messageSummary.CacheCreationTokens5m, chatSummary.CacheCreationTokens5m)
	require.Equal(t, messageSummary.CacheCreationTokens1h, chatSummary.CacheCreationTokens1h)
	require.True(t, chatSummary.IsClaudeUsageSemantic)
	require.Equal(t, 1488, chatSummary.Quota)
}

func TestCalculateTextQuotaSummaryUsesSplitClaudeCacheCreationRatios(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:             types.RelayFormatOpenAI,
		FinalRequestRelayFormat: types.RelayFormatClaude,
		OriginModelName:         "claude-3-7-sonnet",
		PriceData: types.PriceData{
			ModelRatio:           1,
			CompletionRatio:      1,
			CacheRatio:           0,
			CacheCreationRatio:   1,
			CacheCreation5mRatio: 2,
			CacheCreation1hRatio: 3,
			GroupRatioInfo: types.GroupRatioInfo{
				GroupRatio: 1,
			},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 0,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedCreationTokens: 10,
		},
		ClaudeCacheCreation5mTokens: 2,
		ClaudeCacheCreation1hTokens: 3,
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	// 100 + remaining(5)*1 + 2*2 + 3*3 = 118
	require.Equal(t, 118, summary.Quota)
}

func TestCalculateTextQuotaSummaryUsesAnthropicUsageSemanticFromUpstreamUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "claude-3-7-sonnet",
		PriceData: types.PriceData{
			ModelRatio:           1,
			CompletionRatio:      2,
			CacheRatio:           0.1,
			CacheCreationRatio:   1.25,
			CacheCreation5mRatio: 1.25,
			CacheCreation1hRatio: 2,
			GroupRatioInfo: types.GroupRatioInfo{
				GroupRatio: 1,
			},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     1000,
		CompletionTokens: 200,
		UsageSemantic:    "anthropic",
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens:         100,
			CachedCreationTokens: 50,
		},
		ClaudeCacheCreation5mTokens: 10,
		ClaudeCacheCreation1hTokens: 20,
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	require.True(t, summary.IsClaudeUsageSemantic)
	require.Equal(t, "anthropic", summary.UsageSemantic)
	require.Equal(t, 1488, summary.Quota)
}

func TestCacheWriteTokensTotal(t *testing.T) {
	t.Run("split cache creation", func(t *testing.T) {
		summary := textQuotaSummary{
			CacheCreationTokens:   50,
			CacheCreationTokens5m: 10,
			CacheCreationTokens1h: 20,
		}
		require.Equal(t, 50, cacheWriteTokensTotal(summary))
	})

	t.Run("legacy cache creation", func(t *testing.T) {
		summary := textQuotaSummary{CacheCreationTokens: 50}
		require.Equal(t, 50, cacheWriteTokensTotal(summary))
	})

	t.Run("split cache creation without aggregate remainder", func(t *testing.T) {
		summary := textQuotaSummary{
			CacheCreationTokens5m: 10,
			CacheCreationTokens1h: 20,
		}
		require.Equal(t, 30, cacheWriteTokensTotal(summary))
	})
}

func TestCalculateTextQuotaSummaryBillsOpenAICacheWriteTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "gpt-5.6-sol",
		PriceData: types.PriceData{
			ModelRatio:         1,
			CompletionRatio:    2,
			CacheRatio:         0.1,
			CacheCreationRatio: 1.25,
			GroupRatioInfo:     types.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	t.Run("uncached remainder stays positive", func(t *testing.T) {
		usage := &dto.Usage{
			PromptTokens:     1473,
			CompletionTokens: 19,
			PromptTokensDetails: dto.InputTokenDetails{
				CacheWriteTokens: 1470,
			},
		}

		summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

		require.Equal(t, 1470, summary.CacheCreationTokens)
		require.Equal(t, 1879, summary.Quota)
	})

	t.Run("uncached remainder clamps to zero", func(t *testing.T) {
		usage := &dto.Usage{
			PromptTokens:     3619,
			CompletionTokens: 36,
			PromptTokensDetails: dto.InputTokenDetails{
				CachedTokens:     2921,
				CacheWriteTokens: 3616,
			},
		}

		summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

		require.Equal(t, 3616, summary.CacheCreationTokens)
		require.Equal(t, 4884, summary.Quota)
	})
}

func TestCalculateTextQuotaSummaryHandlesLegacyClaudeDerivedOpenAIUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "claude-3-7-sonnet",
		PriceData: types.PriceData{
			ModelRatio:           1,
			CompletionRatio:      5,
			CacheRatio:           0.1,
			CacheCreationRatio:   1.25,
			CacheCreation5mRatio: 1.25,
			CacheCreation1hRatio: 2,
			GroupRatioInfo:       types.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     62,
		CompletionTokens: 95,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 3544,
		},
		ClaudeCacheCreation5mTokens: 586,
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	// 62 + 3544*0.1 + 586*1.25 + 95*5 = 1624.9 => 1624
	require.Equal(t, 1624, summary.Quota)
}

func TestCalculateTextQuotaSummarySeparatesOpenRouterCacheReadFromPromptBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "openai/gpt-4.1",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: constant.ChannelTypeOpenRouter,
		},
		PriceData: types.PriceData{
			ModelRatio:         1,
			CompletionRatio:    1,
			CacheRatio:         0.1,
			CacheCreationRatio: 1.25,
			GroupRatioInfo:     types.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     2604,
		CompletionTokens: 383,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 2432,
		},
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	// OpenRouter OpenAI-format display keeps prompt_tokens as total input,
	// but billing still separates normal input from cache read tokens.
	// quota = (2604 - 2432) + 2432*0.1 + 383 = 798.2 => 798
	require.Equal(t, 2604, summary.PromptTokens)
	require.Equal(t, 798, summary.Quota)
}

func TestCalculateTextQuotaSummarySeparatesOpenRouterCacheCreationFromPromptBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "openai/gpt-4.1",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: constant.ChannelTypeOpenRouter,
		},
		PriceData: types.PriceData{
			ModelRatio:         1,
			CompletionRatio:    1,
			CacheCreationRatio: 1.25,
			GroupRatioInfo:     types.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     2604,
		CompletionTokens: 383,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedCreationTokens: 100,
		},
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	// prompt_tokens is still logged as total input, but cache creation is billed separately.
	// quota = (2604 - 100) + 100*1.25 + 383 = 3012
	require.Equal(t, 2604, summary.PromptTokens)
	require.Equal(t, 3012, summary.Quota)
}

func TestCalculateTextQuotaSummaryKeepsPrePRClaudeOpenRouterBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)

	relayInfo := &relaycommon.RelayInfo{
		FinalRequestRelayFormat: types.RelayFormatClaude,
		OriginModelName:         "anthropic/claude-3.7-sonnet",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType: constant.ChannelTypeOpenRouter,
		},
		PriceData: types.PriceData{
			ModelRatio:         1,
			CompletionRatio:    1,
			CacheRatio:         0.1,
			CacheCreationRatio: 1.25,
			GroupRatioInfo:     types.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     2604,
		CompletionTokens: 383,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 2432,
		},
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	// Pre-PR PostClaudeConsumeQuota behavior for OpenRouter:
	// prompt = 2604 - 2432 = 172
	// quota = 172 + 2432*0.1 + 383 = 798.2 => 798
	require.True(t, summary.IsClaudeUsageSemantic)
	require.Equal(t, 172, summary.PromptTokens)
	require.Equal(t, 798, summary.Quota)
}

func TestComposeTieredTextQuotaKeepsToolCallSurcharges(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Set("image_generation_call", true)
	ctx.Set("image_generation_call_quality", "low")
	ctx.Set("image_generation_call_size", "1024x1024")

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "o1",
		PriceData: types.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1},
		},
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolWebSearchPreview: &relaycommon.BuildInToolInfo{
					CallCount: 1,
				},
				dto.BuildInToolFileSearch: &relaycommon.BuildInToolInfo{
					CallCount: 2,
				},
			},
		},
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode:               "tiered_expr",
			GroupRatio:                1,
			EstimatedQuotaBeforeGroup: 1000,
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)
	quota := composeTieredTextQuota(relayInfo, summary, 1000, &billingexpr.TieredResult{
		ActualQuotaBeforeGroup: 1000,
		ActualQuotaAfterGroup:  1000,
	})

	require.Equal(t, int64(13000), summary.ToolCallSurchargeQuota.Round(0).IntPart())
	require.Equal(t, 14000, quota)
}

func TestComposeTieredTextQuotaFallbackKeepsToolCallSurcharges(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Set("claude_web_search_requests", 2)

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "claude-3-7-sonnet",
		PriceData: types.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1.25},
		},
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode:               "tiered_expr",
			GroupRatio:                1.25,
			EstimatedQuotaBeforeGroup: 1000,
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)
	quota := composeTieredTextQuota(relayInfo, summary, 1250, nil)

	require.Equal(t, int64(12500), summary.ToolCallSurchargeQuota.Round(0).IntPart())
	require.Equal(t, 13750, quota)
}

func TestComposeTieredTextQuotaErrorFallbackUsesPreConsumedQuota(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Set("claude_web_search_requests", 2)

	relayInfo := &relaycommon.RelayInfo{
		OriginModelName: "claude-3-7-sonnet",
		PriceData: types.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1.25},
		},
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{
			BillingMode:               "tiered_expr",
			GroupRatio:                1.25,
			EstimatedQuotaBeforeGroup: 1000,
		},
		StartTime: time.Now(),
	}

	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
	}

	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)

	// tieredResult=nil simulates a settlement error where TryTieredSettle
	// falls back to FinalPreConsumedQuota (2000), which differs from
	// EstimatedQuotaBeforeGroup * GroupRatio (1250).
	preConsumedFallback := 2000
	quota := composeTieredTextQuota(relayInfo, summary, preConsumedFallback, nil)

	require.Equal(t, int64(12500), summary.ToolCallSurchargeQuota.Round(0).IntPart())
	require.Equal(t, 14500, quota)
}

func TestShouldRefundNoOutput(t *testing.T) {
	originalEnabled := operation_setting.GetQuotaSetting().BillingRefundWhenNoOutput
	t.Cleanup(func() {
		operation_setting.GetQuotaSetting().BillingRefundWhenNoOutput = originalEnabled
	})

	startTime := time.Now()
	newStreamStatus := func(reason relaycommon.StreamEndReason) *relaycommon.StreamStatus {
		ss := relaycommon.NewStreamStatus()
		ss.SetEndReason(reason, nil)
		return ss
	}
	streamEOF := newStreamStatus(relaycommon.StreamEndReasonEOF)
	streamClient := newStreamStatus(relaycommon.StreamEndReasonClientGone)
	streamDone := newStreamStatus(relaycommon.StreamEndReasonDone)
	streamTimeout := newStreamStatus(relaycommon.StreamEndReasonTimeout)
	streamShutdown := newStreamStatus(relaycommon.StreamEndReasonShutdown)
	streamScannerErr := newStreamStatus(relaycommon.StreamEndReasonScannerErr)

	buildRelay := func(stream bool, fr time.Time, ss *relaycommon.StreamStatus, format types.RelayFormat) *relaycommon.RelayInfo {
		ri := &relaycommon.RelayInfo{
			IsStream:          stream,
			StartTime:         startTime,
			FirstResponseTime: fr,
			StreamStatus:      ss,
			RelayFormat:       format,
		}
		return ri
	}

	noFRT := startTime.Add(-time.Second)
	hasFRT := startTime.Add(time.Second)
	openai := types.RelayFormatOpenAI

	cases := []struct {
		name    string
		setting bool
		relay   *relaycommon.RelayInfo
		summary textQuotaSummary
		want    bool
	}{
		// ── 基本通路 ─────────────────────────────────────────────────────
		{"happy_path_refund", true, buildRelay(true, noFRT, streamEOF, openai), textQuotaSummary{CompletionTokens: 0}, true},
		{"feature_off", false, buildRelay(true, noFRT, streamEOF, openai), textQuotaSummary{CompletionTokens: 0}, false},
		{"completion_gt0", true, buildRelay(true, noFRT, streamEOF, openai), textQuotaSummary{CompletionTokens: 12}, false},
		{"nil_relay_info", true, nil, textQuotaSummary{CompletionTokens: 0}, false},

		// ── 非流式 ───────────────────────────────────────────────────────
		// non-stream + chat + completion=0：免单。AWS Bedrock 非流式 / 普通 chat 同步返回 output_tokens=0
		{"not_stream_chat_completion_zero", true, buildRelay(false, noFRT, nil, openai), textQuotaSummary{CompletionTokens: 0}, true},
		{"not_stream_chat_completion_gt0", true, buildRelay(false, noFRT, nil, openai), textQuotaSummary{CompletionTokens: 5}, false},

		// ── 流式结束原因覆盖 ────────────────────────────────────────────
		{"stream_eof_empty", true, buildRelay(true, noFRT, streamEOF, openai), textQuotaSummary{CompletionTokens: 0}, true},
		{"stream_done_empty", true, buildRelay(true, noFRT, streamDone, openai), textQuotaSummary{CompletionTokens: 0}, true},
		{"stream_timeout_empty", true, buildRelay(true, noFRT, streamTimeout, openai), textQuotaSummary{CompletionTokens: 0}, true},
		{"stream_shutdown_empty_no_refund", true, buildRelay(true, noFRT, streamShutdown, openai), textQuotaSummary{CompletionTokens: 0, UseTimeSeconds: 120}, false},
		{"stream_scanner_err_empty", true, buildRelay(true, noFRT, streamScannerErr, openai), textQuotaSummary{CompletionTokens: 0}, true},
		// client_gone + completion=0：仅当请求持续 >= 阈值(默认60s) 才免单
		// 秒断（use_time < 60s）：疑似刷 cache 滥用，不免单
		{"stream_client_gone_short_no_refund", true, buildRelay(true, noFRT, streamClient, openai), textQuotaSummary{CompletionTokens: 0, UseTimeSeconds: 3}, false},
		// 久等（use_time >= 60s）：客户真的在等慢上游，按冤案免单
		{"stream_client_gone_long_refund", true, buildRelay(true, noFRT, streamClient, openai), textQuotaSummary{CompletionTokens: 0, UseTimeSeconds: 120}, true},
		// nil StreamStatus（AWS Bedrock 自管 loop 不初始化）：放行
		{"nil_stream_status_chat", true, buildRelay(true, noFRT, nil, openai), textQuotaSummary{CompletionTokens: 0}, true},

		// ── FRT 与首帧 ──────────────────────────────────────────────────
		// 信封帧到达但内容 0：AWS Bedrock claude message_start-then-EOF，必须免单
		{"first_byte_received_but_empty", true, buildRelay(true, hasFRT, streamEOF, openai), textQuotaSummary{CompletionTokens: 0}, true},
		{"first_byte_received_with_completion", true, buildRelay(true, hasFRT, streamEOF, openai), textQuotaSummary{CompletionTokens: 8}, false},

		// ── 白名单内的所有 token-driven 格式都应通过 ──────────────────────
		{"format_claude_eligible", true, buildRelay(true, noFRT, streamEOF, types.RelayFormatClaude), textQuotaSummary{CompletionTokens: 0}, true},
		{"format_gemini_eligible", true, buildRelay(true, noFRT, streamEOF, types.RelayFormatGemini), textQuotaSummary{CompletionTokens: 0}, true},
		{"format_responses_eligible", true, buildRelay(true, noFRT, streamEOF, types.RelayFormatOpenAIResponses), textQuotaSummary{CompletionTokens: 0}, true},
		{"format_responses_compact_eligible", true, buildRelay(true, noFRT, streamEOF, types.RelayFormatOpenAIResponsesCompaction), textQuotaSummary{CompletionTokens: 0}, true},

		// ── 白名单外的格式：completion=0 是常态，绝不免单 ─────────────────
		{"format_audio_skip", true, buildRelay(false, noFRT, nil, types.RelayFormatOpenAIAudio), textQuotaSummary{CompletionTokens: 0}, false},
		{"format_image_skip", true, buildRelay(false, noFRT, nil, types.RelayFormatOpenAIImage), textQuotaSummary{CompletionTokens: 0}, false},
		{"format_realtime_skip", true, buildRelay(true, noFRT, streamEOF, types.RelayFormatOpenAIRealtime), textQuotaSummary{CompletionTokens: 0}, false},
		{"format_rerank_skip", true, buildRelay(false, noFRT, nil, types.RelayFormatRerank), textQuotaSummary{CompletionTokens: 0}, false},
		{"format_embedding_skip", true, buildRelay(false, noFRT, nil, types.RelayFormatEmbedding), textQuotaSummary{CompletionTokens: 0}, false},
		// task / mj_proxy 理论上不会走到 PostTextConsumeQuota，但作为防御性测试也覆盖
		{"format_task_skip", true, buildRelay(false, noFRT, nil, types.RelayFormatTask), textQuotaSummary{CompletionTokens: 0}, false},
		{"format_mj_proxy_skip", true, buildRelay(false, noFRT, nil, types.RelayFormatMjProxy), textQuotaSummary{CompletionTokens: 0}, false},
		// 空 RelayFormat 兜底：未来新增 format 没接入白名单时默认不免单
		{"format_empty_skip", true, buildRelay(false, noFRT, nil, ""), textQuotaSummary{CompletionTokens: 0}, false},

		// ── 混合计费组件：白名单格式但含 image_gen/web_search 等非 completion 计费 ─
		{"chat_with_image_token_skip", true, buildRelay(true, noFRT, streamEOF, openai),
			textQuotaSummary{CompletionTokens: 0, ImageTokens: 100}, false},
		{"chat_with_image_call_price_skip", true, buildRelay(true, noFRT, streamEOF, openai),
			textQuotaSummary{CompletionTokens: 0, ImageGenerationCallPrice: 0.04}, false},
		{"chat_with_web_search_skip", true, buildRelay(true, noFRT, streamEOF, openai),
			textQuotaSummary{CompletionTokens: 0, WebSearchCallCount: 1}, false},
		{"chat_with_claude_web_search_skip", true, buildRelay(true, noFRT, streamEOF, openai),
			textQuotaSummary{CompletionTokens: 0, ClaudeWebSearchCallCount: 1}, false},
		{"chat_with_file_search_skip", true, buildRelay(true, noFRT, streamEOF, openai),
			textQuotaSummary{CompletionTokens: 0, FileSearchCallCount: 1}, false},
		{"chat_with_audio_input_skip", true, buildRelay(true, noFRT, streamEOF, openai),
			textQuotaSummary{CompletionTokens: 0, AudioTokens: 100, AudioInputPrice: 0.5}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			operation_setting.GetQuotaSetting().BillingRefundWhenNoOutput = c.setting
			got, _ := shouldRefundNoOutput(c.relay, c.summary, "")
			require.Equal(t, c.want, got)
		})
	}
}

// TestShouldRefundNoOutputUpstreamRefusalAndDeniedReasons 覆盖"上游显式拒绝不免单"
// 开关与 refund_denied_reason 落点：只认显式标记家族，无标记/无关标记不受影响；
// 开关关闭时保持上线前行为（refusal 也免单）。
func TestShouldRefundNoOutputUpstreamRefusalAndDeniedReasons(t *testing.T) {
	qs := operation_setting.GetQuotaSetting()
	originalEnabled := qs.BillingRefundWhenNoOutput
	originalExclude := qs.RefundNoOutputExcludeUpstreamRefusal
	t.Cleanup(func() {
		qs.BillingRefundWhenNoOutput = originalEnabled
		qs.RefundNoOutputExcludeUpstreamRefusal = originalExclude
	})
	qs.BillingRefundWhenNoOutput = true

	startTime := time.Now()
	newStreamStatus := func(reason relaycommon.StreamEndReason) *relaycommon.StreamStatus {
		ss := relaycommon.NewStreamStatus()
		ss.SetEndReason(reason, nil)
		return ss
	}
	buildRelay := func(ss *relaycommon.StreamStatus) *relaycommon.RelayInfo {
		return &relaycommon.RelayInfo{
			IsStream:          true,
			StartTime:         startTime,
			FirstResponseTime: startTime.Add(time.Second),
			StreamStatus:      ss,
			RelayFormat:       types.RelayFormatClaude,
		}
	}
	empty := textQuotaSummary{CompletionTokens: 0}

	cases := []struct {
		name           string
		excludeRefusal bool
		rejectReason   string
		relay          *relaycommon.RelayInfo
		summary        textQuotaSummary
		wantRefund     bool
		wantDenied     string
	}{
		// ── 开关开启：显式拒绝家族全部拒退 ─────────────────────────────
		{"claude_refusal_denied", true, "claude_stop_reason=refusal", buildRelay(newStreamStatus(relaycommon.StreamEndReasonDone)), empty, false, refundDeniedUpstreamRefusal},
		{"openai_content_filter_denied", true, "openai_finish_reason=content_filter", buildRelay(newStreamStatus(relaycommon.StreamEndReasonEOF)), empty, false, refundDeniedUpstreamRefusal},
		{"gemini_block_reason_denied", true, "gemini_block_reason=SAFETY", buildRelay(newStreamStatus(relaycommon.StreamEndReasonEOF)), empty, false, refundDeniedUpstreamRefusal},
		{"gemini_finish_reason_denied", true, "gemini_finish_reason=PROHIBITED_CONTENT", buildRelay(newStreamStatus(relaycommon.StreamEndReasonEOF)), empty, false, refundDeniedUpstreamRefusal},
		{"gemini_image_prohibited_denied", true, "gemini_finish_reason=IMAGE_PROHIBITED_CONTENT", buildRelay(newStreamStatus(relaycommon.StreamEndReasonEOF)), empty, false, refundDeniedUpstreamRefusal},
		// ── 非拒绝家族标记不影响免单 ──────────────────────────────────
		// blockReason/finishReason 值白名单：OTHER/UNSPECIFIED（原因不明桶）与
		// RECITATION（模型侧归因）不算客户触发的拒绝，照常免单
		{"gemini_block_reason_other_still_refund", true, "gemini_block_reason=OTHER", buildRelay(newStreamStatus(relaycommon.StreamEndReasonEOF)), empty, true, ""},
		{"gemini_block_reason_unspecified_still_refund", true, "gemini_block_reason=BLOCK_REASON_UNSPECIFIED", buildRelay(newStreamStatus(relaycommon.StreamEndReasonEOF)), empty, true, ""},
		{"gemini_finish_reason_recitation_still_refund", true, "gemini_finish_reason=RECITATION", buildRelay(newStreamStatus(relaycommon.StreamEndReasonEOF)), empty, true, ""},
		{"gemini_empty_candidates_still_refund", true, "gemini_empty_candidates", buildRelay(newStreamStatus(relaycommon.StreamEndReasonEOF)), empty, true, ""},
		{"unrelated_reject_reason_still_refund", true, "some_admin_note", buildRelay(newStreamStatus(relaycommon.StreamEndReasonEOF)), empty, true, ""},
		{"no_reject_reason_still_refund", true, "", buildRelay(newStreamStatus(relaycommon.StreamEndReasonEOF)), empty, true, ""},
		// ── 门控顺序锁定：白名单外格式/混合计费组件先于 refusal 判定，不产出拒退标记 ──
		{"refusal_with_image_component_no_denied_marker", true, "claude_stop_reason=refusal", buildRelay(newStreamStatus(relaycommon.StreamEndReasonEOF)), textQuotaSummary{CompletionTokens: 0, ImageTokens: 100}, false, ""},
		// ── 开关关闭：refusal 仍免单（上线前行为）─────────────────────
		{"claude_refusal_refund_when_disabled", false, "claude_stop_reason=refusal", buildRelay(newStreamStatus(relaycommon.StreamEndReasonDone)), empty, true, ""},
		// ── completion>0：正常计费路径，无拒退标记 ─────────────────────
		{"refusal_with_completion_normal_billing", true, "claude_stop_reason=refusal", buildRelay(newStreamStatus(relaycommon.StreamEndReasonDone)), textQuotaSummary{CompletionTokens: 9}, false, ""},
		// ── 既有策略性拒退的 denied_reason 落点 ────────────────────────
		{"client_gone_quick_denied_reason", false, "", buildRelay(newStreamStatus(relaycommon.StreamEndReasonClientGone)), textQuotaSummary{CompletionTokens: 0, UseTimeSeconds: 3}, false, refundDeniedClientGoneQuick},
		{"shutdown_denied_reason", false, "", buildRelay(newStreamStatus(relaycommon.StreamEndReasonShutdown)), textQuotaSummary{CompletionTokens: 0, UseTimeSeconds: 120}, false, refundDeniedShutdown},
		// ── refusal 优先于 client_gone 秒断 ────────────────────────────
		{"refusal_takes_precedence_over_client_gone", true, "claude_stop_reason=refusal", buildRelay(newStreamStatus(relaycommon.StreamEndReasonClientGone)), textQuotaSummary{CompletionTokens: 0, UseTimeSeconds: 3}, false, refundDeniedUpstreamRefusal},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			qs.RefundNoOutputExcludeUpstreamRefusal = c.excludeRefusal
			gotRefund, gotDenied := shouldRefundNoOutput(c.relay, c.summary, c.rejectReason)
			require.Equal(t, c.wantRefund, gotRefund)
			require.Equal(t, c.wantDenied, gotDenied)
		})
	}
}

func TestCalculateTextQuotaSummaryUsesClaudeBillingUsageBeforeTopLevelUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: "claude-3-7-sonnet",
		PriceData: types.PriceData{
			ModelRatio: 1, CompletionRatio: 2, CacheRatio: 0.1,
			CacheCreationRatio: 1.25, CacheCreation5mRatio: 1.25, CacheCreation1hRatio: 2,
			GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}
	usage := &dto.Usage{
		PromptTokens: 999, CompletionTokens: 999, TotalTokens: 1998,
		BillingUsage: dto.NewClaudeMessagesBillingUsage(&dto.ClaudeUsage{
			InputTokens: 70, CacheReadInputTokens: 30, CacheCreationInputTokens: 20, OutputTokens: 7,
			CacheCreation: &dto.ClaudeCacheCreationUsage{Ephemeral5mInputTokens: 12, Ephemeral1hInputTokens: 8},
		}),
	}
	summary := calculateTextQuotaSummary(ctx, relayInfo, effectiveBillingUsage(usage))
	require.True(t, summary.IsClaudeUsageSemantic)
	require.Equal(t, dto.BillingUsageSemanticAnthropic, summary.UsageSemantic)
	require.Equal(t, 70, summary.PromptTokens)
	require.Equal(t, 7, summary.CompletionTokens)
	require.Equal(t, 30, summary.CacheTokens)
	require.Equal(t, 20, summary.CacheCreationTokens)
	require.Equal(t, 12, summary.CacheCreationTokens5m)
	require.Equal(t, 8, summary.CacheCreationTokens1h)
	require.Equal(t, 118, summary.Quota)
}

func TestCalculateTextQuotaSummaryUsesGeminiBillingUsageBeforeTopLevelUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	relayInfo := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI, OriginModelName: "gemini-2.5-flash",
		PriceData: types.PriceData{
			ModelRatio: 1, CompletionRatio: 2, CacheRatio: 0.1,
			GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}
	usage := &dto.Usage{
		PromptTokens: 999, CompletionTokens: 999, TotalTokens: 1998,
		BillingUsage: dto.NewGeminiChatBillingUsage(&dto.GeminiUsageMetadata{
			PromptTokenCount: 100, ToolUsePromptTokenCount: 5, CandidatesTokenCount: 20,
			ThoughtsTokenCount: 3, TotalTokenCount: 128, CachedContentTokenCount: 7,
		}),
	}
	summary := calculateTextQuotaSummary(ctx, relayInfo, effectiveBillingUsage(usage))
	require.False(t, summary.IsClaudeUsageSemantic)
	require.Equal(t, dto.BillingUsageSemanticGemini, summary.UsageSemantic)
	require.Equal(t, 105, summary.PromptTokens)
	require.Equal(t, 23, summary.CompletionTokens)
	require.Equal(t, 7, summary.CacheTokens)
	require.Equal(t, 128, summary.TotalTokens)
	require.Equal(t, 145, summary.Quota)
}

func TestCalculateTextQuotaSummaryUsesOpenAIBillingUsageBeforeTopLevelUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	relayInfo := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatClaude, OriginModelName: "gpt-4o",
		PriceData: types.PriceData{
			ModelRatio: 1, CompletionRatio: 2,
			GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
		},
		StartTime: time.Now(),
	}
	usage := &dto.Usage{
		PromptTokens: 999, CompletionTokens: 999, TotalTokens: 1998,
		BillingUsage: dto.NewOpenAIChatBillingUsage(&dto.Usage{
			PromptTokens: 80, CompletionTokens: 9, TotalTokens: 89,
		}),
	}
	summary := calculateTextQuotaSummary(ctx, relayInfo, effectiveBillingUsage(usage))
	require.False(t, summary.IsClaudeUsageSemantic)
	require.Equal(t, dto.BillingUsageSemanticOpenAI, summary.UsageSemantic)
	require.Equal(t, 80, summary.PromptTokens)
	require.Equal(t, 9, summary.CompletionTokens)
	require.Equal(t, 89, summary.TotalTokens)
	require.Equal(t, 98, summary.Quota)
}

func TestUsageBillingPathForLog(t *testing.T) {
	require.Equal(t, usageBillingPathLocal, usageBillingPathForLog(true, &dto.Usage{
		BillingUsage: dto.NewClaudeMessagesBillingUsage(&dto.ClaudeUsage{InputTokens: 1}),
	}))
	require.Equal(t, usageBillingPathUpstream, usageBillingPathForLog(false, &dto.Usage{}))
	require.Equal(t, usageBillingPathOpenAI, usageBillingPathForLog(false, &dto.Usage{
		BillingUsage: dto.NewOpenAIChatBillingUsage(&dto.Usage{PromptTokens: 1}),
	}))
	require.Equal(t, usageBillingPathAnthropic, usageBillingPathForLog(false, &dto.Usage{
		BillingUsage: dto.NewClaudeMessagesBillingUsage(&dto.ClaudeUsage{InputTokens: 1}),
	}))
	require.Equal(t, usageBillingPathGemini, usageBillingPathForLog(false, &dto.Usage{
		BillingUsage: dto.NewGeminiChatBillingUsage(&dto.GeminiUsageMetadata{PromptTokenCount: 1}),
	}))
	require.Equal(t, usageBillingPathGeminiEstimated, usageBillingPathForLog(false, &dto.Usage{
		BillingUsage: dto.NewEstimatedGeminiChatBillingUsage(&dto.Usage{PromptTokens: 1}),
	}))
}

func TestAppendUsageBillingPathForLogWritesAdminInfo(t *testing.T) {
	other := map[string]interface{}{"admin_info": map[string]interface{}{}}
	appendUsageBillingPathForLog(other, false, &dto.Usage{
		BillingUsage: dto.NewClaudeMessagesBillingUsage(&dto.ClaudeUsage{InputTokens: 1}),
	})
	adminInfo, ok := other["admin_info"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, usageBillingPathAnthropic, adminInfo["usage_billing_path"])

	other = map[string]interface{}{}
	appendUsageBillingPathForLog(other, true, nil)
	adminInfo, ok = other["admin_info"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, usageBillingPathLocal, adminInfo["usage_billing_path"])
}
