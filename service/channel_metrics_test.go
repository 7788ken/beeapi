package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// computeQualityScore 评分公式纯逻辑测试。
// 不依赖 DB；直接构造 channelAgg + channelSample，断言 score。

func TestComputeQualityScore_NoTrafficReturnsNil(t *testing.T) {
	score := computeQualityScore(channelAgg{}, channelSample{})
	assert.Nil(t, score, "无流量应返回 nil（DB 写 NULL）")
}

func TestComputeQualityScore_StreamingPerfectGetsHundred(t *testing.T) {
	agg := channelAgg{SuccessCnt: 100, ErrorCnt: 0, AvgUseTime: 2.0}
	s := channelSample{
		FrtMsSum: 500 * 100, FrtMsCount: 100, // 平均 500ms
		StreamCount: 100, NonStreamCnt: 0,
	}
	score := computeQualityScore(agg, s)
	require := assert.New(t)
	require.NotNil(score)
	require.Equal(100, *score, "流式 100% 成功 + 优秀 frt + 优秀 use_time + 无错 → 满分")
}

func TestComputeQualityScore_NonStreamingPerfectGetsHundred(t *testing.T) {
	agg := channelAgg{SuccessCnt: 50, ErrorCnt: 0, AvgUseTime: 0.5}
	s := channelSample{StreamCount: 0, NonStreamCnt: 50} // 全非流式，无 frt
	score := computeQualityScore(agg, s)
	require := assert.New(t)
	require.NotNil(score)
	require.Equal(100, *score, "非流式 100% 成功 + use_time 0.5s + 无错 → 满分（修复 R2 偏差）")
}

func TestComputeQualityScore_StreamingSlowFrt(t *testing.T) {
	agg := channelAgg{SuccessCnt: 100, ErrorCnt: 0, AvgUseTime: 4.0}
	s := channelSample{
		FrtMsSum: 4500 * 100, FrtMsCount: 100, // 平均 4.5s
		StreamCount: 100, NonStreamCnt: 0,
	}
	score := computeQualityScore(agg, s)
	require := assert.New(t)
	require.NotNil(score)
	// 40 + 12 (frt 5s档) + 20 (use_time <5s) + 15 = 87
	require.Equal(87, *score)
}

func TestComputeQualityScore_PenalizesUpstreamRateLimits(t *testing.T) {
	agg := channelAgg{SuccessCnt: 80, ErrorCnt: 20, AvgUseTime: 3.0}
	s := channelSample{
		FrtMsSum: 1500 * 80, FrtMsCount: 80,
		StreamCount: 80, NonStreamCnt: 0,
		E503: 20, // 全是 503
	}
	score := computeQualityScore(agg, s)
	// p1=32 (80%) + p2=22+20=42 + p3=15-10=5 → 79
	require := assert.New(t)
	require.NotNil(score)
	require.Equal(79, *score)
}

func TestComputeQualityScore_TotalFailureScoresLow(t *testing.T) {
	agg := channelAgg{SuccessCnt: 0, ErrorCnt: 100, AvgUseTime: 0}
	s := channelSample{E503: 100}
	score := computeQualityScore(agg, s)
	// p1=0 + p2=useTimeScoreStrict(0)=45 + p3=max(0, 15-50)=0 → 45
	require := assert.New(t)
	require.NotNil(score)
	// 全错但用时算"快"，分数也不应高
	assert.LessOrEqual(t, *score, 50, "全错的渠道得分应 ≤50")
}

func TestComputeQualityScore_ClampsTo0_100(t *testing.T) {
	// 极端情形：err 计数巨大
	agg := channelAgg{SuccessCnt: 50, ErrorCnt: 1000, AvgUseTime: 100}
	s := channelSample{E503: 1000, E429: 1000, E500: 1000}
	score := computeQualityScore(agg, s)
	require := assert.New(t)
	require.NotNil(score)
	assert.GreaterOrEqual(t, *score, 0)
	assert.LessOrEqual(t, *score, 100)
}

// ──────────────────────────────────────────────────────────────────────────
// 辅助函数测试
// ──────────────────────────────────────────────────────────────────────────

func TestParseStatusCode(t *testing.T) {
	cases := map[string]int{
		"status_code=503, upstream error: do request failed": 503,
		"status_code=429 rate limited":                       429,
		"plain error message":                                0,
		"":                                                   0,
		"status_code=abc":                                    0,
	}
	for in, want := range cases {
		assert.Equal(t, want, parseStatusCode(in), "input=%q", in)
	}
}

func TestContainsTimeout(t *testing.T) {
	assert.True(t, containsTimeout("context deadline exceeded after 30s"))
	assert.True(t, containsTimeout("upstream timeout"))
	assert.True(t, containsTimeout("Timeout while reading"))
	assert.False(t, containsTimeout("status_code=503 service unavailable"))
}

func TestParseFrtFromOther(t *testing.T) {
	// 合法 JSON 含 frt
	frt, ok := parseFrtFromOther(`{"frt":2526,"group_ratio":1}`)
	assert.True(t, ok)
	assert.Equal(t, 2526.0, frt)

	// 空字符串
	_, ok = parseFrtFromOther("")
	assert.False(t, ok)

	// 非法 JSON
	_, ok = parseFrtFromOther(`{not json`)
	assert.False(t, ok)

	// 无 frt 字段
	_, ok = parseFrtFromOther(`{"other":1}`)
	assert.False(t, ok)

	// frt 是 -1000 sentinel（仍返回值，由调用方过滤 > 0）
	frt, ok = parseFrtFromOther(`{"frt":-1000}`)
	assert.True(t, ok)
	assert.Equal(t, -1000.0, frt)
}

// ──────────────────────────────────────────────────────────────────────────
// 流式比例判定
// ──────────────────────────────────────────────────────────────────────────

func TestComputeQualityScore_MajorityStreamUsesTtft(t *testing.T) {
	// 9 个流式 + 1 个非流式 → 视为流式渠道，走 TTFT 公式
	agg := channelAgg{SuccessCnt: 10, ErrorCnt: 0, AvgUseTime: 2.0}
	s := channelSample{
		FrtMsSum: 800 * 9, FrtMsCount: 9,
		StreamCount: 9, NonStreamCnt: 1,
	}
	score := computeQualityScore(agg, s)
	// 40 + 25(frt<1s) + 20(use<5s) + 15 = 100
	require := assert.New(t)
	require.NotNil(score)
	require.Equal(100, *score)
}

func TestComputeQualityScore_MinorityStreamUsesStrict(t *testing.T) {
	// 1 个流式 + 9 个非流式 → 视为非流式渠道，走严格公式
	agg := channelAgg{SuccessCnt: 10, ErrorCnt: 0, AvgUseTime: 0.5}
	s := channelSample{
		FrtMsSum: 800 * 1, FrtMsCount: 1,
		StreamCount: 1, NonStreamCnt: 9,
	}
	score := computeQualityScore(agg, s)
	// 40 + useTimeScoreStrict(0.5s)=45 + 15 = 100
	require := assert.New(t)
	require.NotNil(score)
	require.Equal(100, *score)
}
