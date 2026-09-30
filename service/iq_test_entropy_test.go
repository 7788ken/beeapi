package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

type iqPrefProbe struct {
	t          *testing.T
	responses  []string // cycled per preference sample
	echo       string   // value of the "model" field in responses; empty = mirror request
	sawTemp    atomic.Int64
	sawMaxTok  atomic.Int64
	prefCalls  atomic.Int64
	scoreCalls atomic.Int64
}

// newIQEntropyServer answers IQ bank questions correctly and serves the
// preference question from a cycling list, capturing sampling-request shape.
func newIQEntropyServer(p *iqPrefProbe) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model       string   `json:"model"`
			Messages    []struct{ Content string } `json:"messages"`
			MaxTokens   *int     `json:"max_tokens"`
			Temperature *float64 `json:"temperature"`
		}
		require.NoError(p.t, common.DecodeJson(r.Body, &body))
		echo := p.echo
		if echo == "" {
			echo = body.Model
		}
		if body.Messages[0].Content == iqPreferenceQuestion.Prompt {
			p.prefCalls.Add(1)
			if body.Temperature != nil {
				p.sawTemp.Add(1)
			}
			if body.MaxTokens != nil {
				p.sawMaxTok.Add(int64(*body.MaxTokens))
			}
			index := int(p.prefCalls.Load()-1) % len(p.responses)
			fmt.Fprintf(w, `{"model":%q,"choices":[{"message":{"content":%q}}]}`, echo, p.responses[index])
			return
		}
		p.scoreCalls.Add(1)
		for _, question := range iqBank {
			if question.Prompt == body.Messages[0].Content {
				fmt.Fprintf(w, `{"model":%q,"choices":[{"message":{"content":%q}}]}`, echo, question.Expected)
				return
			}
		}
		p.t.Errorf("unknown prompt %q", body.Messages[0].Content)
	}))
}

func iqEntropyDetail(t *testing.T, result *model.IQTestResult) map[string]any {
	t.Helper()
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.Detail), &parsed))
	return parsed
}

func iqPreferenceBlocks(t *testing.T, detail map[string]any) []map[string]any {
	t.Helper()
	raw, ok := detail["preference_samples"].([]any)
	require.True(t, ok, "preference sampling block present: %v", detail["preference_samples"])
	blocks := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		blocks = append(blocks, item.(map[string]any))
	}
	return blocks
}

func TestIQPreferenceSamplingFlagsEntropyCollapse(t *testing.T) {
	p := &iqPrefProbe{t: t, responses: []string{"Brazil", "Brazil", "Brazil", "Brazil", "Brazil", "Brazil"}}
	server := newIQEntropyServer(p)
	defer server.Close()
	result := RunIQTestResultWithConfig(context.Background(), iqProbeChannel(server.URL), iqProbeModel(), "collapse", 8, time.Second)
	require.Equal(t, model.IQTestResultStatusSuccess, result.Status, result.Detail)
	require.EqualValues(t, 6, p.prefCalls.Load(), "six preference samples per round")
	require.EqualValues(t, 0, p.sawTemp.Load(), "preference sampling must not pin temperature")
	detail := iqEntropyDetail(t, result)
	blocks := iqPreferenceBlocks(t, detail)
	require.Len(t, blocks, 1)
	require.Equal(t, "preference-1", blocks[0]["qid"])
	require.Equal(t, "collapse", blocks[0]["verdict"])
	require.Equal(t, 100, *result.Score, "collapse is signal-only; the score stays untouched")
}

func TestIQPreferenceSamplingVariedAnswersStayOk(t *testing.T) {
	p := &iqPrefProbe{t: t, responses: []string{"Brazil", "Japan", "Brazil", "Iceland", "Brazil", "Japan"}}
	server := newIQEntropyServer(p)
	defer server.Close()
	result := RunIQTestResultWithConfig(context.Background(), iqProbeChannel(server.URL), iqProbeModel(), "varied", 8, time.Second)
	blocks := iqPreferenceBlocks(t, iqEntropyDetail(t, result))
	require.Equal(t, "ok", blocks[0]["verdict"])
}

func TestIQPreferenceSamplingSkipsWhenSampleTooSmall(t *testing.T) {
	withNoIQBackoff(t)
	p := &iqPrefProbe{t: t, responses: []string{"Brazil"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		require.NoError(t, common.DecodeJson(r.Body, &body))
		if body.Messages[0].Content == iqPreferenceQuestion.Prompt {
			p.prefCalls.Add(1)
			if p.prefCalls.Load() > 3 { // only 3 of 6 samples answer
				w.WriteHeader(503)
				return
			}
			fmt.Fprint(w, `{"choices":[{"message":{"content":"Brazil"}}]}`)
			return
		}
		for _, question := range iqBank {
			if question.Prompt == body.Messages[0].Content {
				fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, question.Expected)
				return
			}
		}
	}))
	defer server.Close()
	result := RunIQTestResultWithConfig(context.Background(), iqProbeChannel(server.URL), iqProbeModel(), "thin", 8, time.Second)
	blocks := iqPreferenceBlocks(t, iqEntropyDetail(t, result))
	require.Equal(t, "skipped", blocks[0]["verdict"])
}

func TestIQModelEchoMismatchCapsScore(t *testing.T) {
	p := &iqPrefProbe{t: t, responses: []string{"Brazil", "Japan", "Brazil", "Japan", "Brazil", "Japan"}, echo: "deepseek-v3-chat"}
	server := newIQEntropyServer(p)
	defer server.Close()
	ch := iqProbeChannel(server.URL)
	result := RunIQTestResultWithConfig(context.Background(), ch, iqProbeModel(), "capped", 8, time.Second)
	require.Equal(t, model.IQTestResultStatusSuccess, result.Status)
	require.Equal(t, 60, *result.Score, "cross-family echo mismatch caps the score at 60")
	require.Equal(t, -10, *result.Margin, "capped margin feeds the existing demotion pipeline")
	detail := iqEntropyDetail(t, result)
	echo, ok := detail["model_echo"].(map[string]any)
	require.True(t, ok, "echo block present")
	require.Equal(t, "public-alias", echo["requested"])
	require.Equal(t, "deepseek-v3-chat", echo["echoed"])
	require.Equal(t, "mismatch", echo["verdict"])
}

func TestIQModelEchoFamilyVariantDoesNotCap(t *testing.T) {
	for _, echo := range []string{"public-alias", "public-alias-2026-02-13"} {
		p := &iqPrefProbe{t: t, responses: []string{"Brazil", "Japan", "Brazil", "Japan", "Brazil", "Japan"}, echo: echo}
		server := newIQEntropyServer(p)
		result := RunIQTestResultWithConfig(context.Background(), iqProbeChannel(server.URL), iqProbeModel(), "family-"+echo, 8, time.Second)
		require.Equal(t, 100, *result.Score, "echo %q is the requested model or its dated variant and must not cap", echo)
		server.Close()
	}
}

func TestIQPreferenceQuestionStaysOutOfScoringBank(t *testing.T) {
	for _, question := range iqBank {
		require.NotEqual(t, "preference", question.Category, "the preference probe must not join the weighted bank")
	}
	for count := 1; count <= len(iqBank); count++ {
		for _, question := range selectIQQuestions(count, "pref-exclusion") {
			require.NotEqual(t, iqPreferenceQuestion.ID, question.ID)
		}
	}
}
