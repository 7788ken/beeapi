package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func iqProbeChannel(base string) *model.Channel {
	return &model.Channel{Id: 1, Type: constant.ChannelTypeOpenAI, BaseURL: &base, Key: "iq-test-secret", IQRevision: 12}
}

func iqAnthropicProbeChannel(base string) *model.Channel {
	ch := iqProbeChannel(base)
	ch.Type = constant.ChannelTypeAnthropic
	return ch
}

func iqProbeModel() *model.IQTestModel {
	return &model.IQTestModel{ModelName: "public-alias", BaselineScore: 70}
}

func writeIQCorrectResponse(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	var body struct {
		Messages []struct{ Content string } `json:"messages"`
	}
	require.NoError(t, common.DecodeJson(r.Body, &body))
	require.Len(t, body.Messages, 1)
	if body.Messages[0].Content == iqPreferenceQuestion.Prompt {
		encoded, err := common.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": "冰岛"}}}})
		require.NoError(t, err)
		_, err = w.Write(encoded)
		require.NoError(t, err)
		return
	}
	for _, question := range iqBank {
		if question.Prompt == body.Messages[0].Content {
			encoded, err := common.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": question.Expected}}}})
			require.NoError(t, err)
			_, err = w.Write(encoded)
			require.NoError(t, err)
			return
		}
	}
	t.Errorf("unknown prompt")
}

func TestIQJudgeRejectsContradictionsAndEnforcesInstructions(t *testing.T) {
	for _, question := range iqBank {
		t.Run(question.ID, func(t *testing.T) {
			require.True(t, judgeIQAnswer(question, question.Expected))
			require.False(t, judgeIQAnswer(question, "not "+question.Expected))
			require.False(t, judgeIQAnswer(question, question.Expected+" is incorrect; another answer is right"))
			if question.Judge == "strict" {
				require.False(t, judgeIQAnswer(question, question.Expected+"\n"))
				require.False(t, judgeIQAnswer(question, question.Expected+"。"))
			}
		})
	}
	require.False(t, judgeIQAnswer(iqBank[0], "391 is incorrect; the answer is 400"))
	require.False(t, judgeIQAnswer(iqBank[12], "不是长江，是黄河"))
	require.False(t, judgeIQAnswer(iqBank[0], "39,1"))
	require.False(t, judgeIQAnswer(iqBank[0], "391.00000000000000001"))
	require.False(t, judgeIQAnswer(iqBank[16], "yes"))
	for _, answer := range []string{"NaN", "Inf", "1e9999", "1e-1000000000"} {
		require.False(t, judgeIQAnswer(iqQuestion{Expected: "0.3", Judge: "numeric"}, answer))
	}
	require.True(t, judgeIQAnswer(iqQuestion{Expected: "0.3", Judge: "numeric"}, "0.3000001"))
	require.False(t, judgeIQAnswer(iqQuestion{Expected: "0.3", Judge: "numeric"}, "0.30001"))
	require.True(t, judgeIQAnswer(iqQuestion{Expected: "0.0", Judge: "numeric"}, "0.0000001"))
	require.False(t, judgeIQAnswer(iqQuestion{Expected: "0", Judge: "numeric"}, "0.0000001"))
	// Hard discriminators: negative and fractional exact answers must judge cleanly.
	require.True(t, judgeIQAnswer(iqQuestion{Expected: "-174", Judge: "numeric"}, "-174"))
	require.False(t, judgeIQAnswer(iqQuestion{Expected: "-174", Judge: "numeric"}, "174"))
	require.True(t, judgeIQAnswer(iqQuestion{Expected: "0.875", Judge: "numeric"}, "0.875"))
	require.False(t, judgeIQAnswer(iqQuestion{Expected: "0.875", Judge: "numeric"}, "0.87"))
}

func TestIQQuestionsAreStratifiedAndReplayable(t *testing.T) {
	for count := 1; count <= len(iqBank); count++ {
		questions := selectIQQuestions(count, "run-a")
		require.Len(t, questions, count)
		require.Equal(t, questions, selectIQQuestions(count, "run-a"))
		categories, seen := map[string]int{}, map[string]bool{}
		for _, question := range questions {
			require.False(t, seen[question.ID])
			seen[question.ID] = true
			categories[question.Category]++
			require.Len(t, iqQuestionHash(question), 64)
		}
		if count >= 6 {
			require.Len(t, categories, 6)
		}
		if count == 8 {
			// One question per category plus two repeats, so every 8-question
			// round always contains both weight-3 discriminators.
			for _, perCategory := range categories {
				require.GreaterOrEqual(t, perCategory, 1)
			}
		}
	}
	require.NotEqual(t, selectIQQuestions(8, "run-a"), selectIQQuestions(8, "run-b"))
}

func TestIQSelectionAlwaysIncludesHardDiscriminators(t *testing.T) {
	for _, runID := range []string{"run-1", "run-2", "run-3", "run-4", "run-5"} {
		questions := selectIQQuestions(8, runID)
		weights := map[string]int{}
		total := 0
		for _, question := range questions {
			weights[question.Category] += question.Weight
			total += question.Weight
		}
		require.Positive(t, weights["math_hard"], "an 8-question round must sample math_hard")
		require.Positive(t, weights["logic_hard"], "an 8-question round must sample logic_hard")
		require.Positive(t, weights["instruction"], "strict instruction compliance must always be sampled")
		require.GreaterOrEqual(t, total, 14)
		require.LessOrEqual(t, total, 18)
	}
}

// TestIQWeightedScoringSeparatesDegradedChannels is the point of builtin-v3:
// losing one weight-3 question must cost visibly more than losing one
// weight-1 sentinel, so near-perfect scores stay informative.
func TestIQWeightedScoringSeparatesDegradedChannels(t *testing.T) {
	// Typical 8-question composition: weights 1+1+2+2+3+3 plus two repeats (say 2+2) = 16.
	status, errorClass, score, margin := iqRoundOutcome(8, 8, 13, 16, "", 70)
	require.Equal(t, model.IQTestResultStatusSuccess, status)
	require.Empty(t, errorClass)
	require.Equal(t, 81, *score, "one weight-3 question wrong: (16-3)/16")
	require.Equal(t, 11, *margin)
	status, _, score, _ = iqRoundOutcome(8, 8, 15, 16, "", 70)
	require.Equal(t, 93, *score, "one weight-1 sentinel wrong: (16-1)/16")
	status, _, score, _ = iqRoundOutcome(8, 8, 16, 16, "", 70)
	require.Equal(t, 100, *score)
	// Probe failures shrink the denominator instead of reading as wrong answers.
	status, _, score, _ = iqRoundOutcome(6, 8, 12, 12, "upstream_4xx", 70)
	require.Equal(t, model.IQTestResultStatusSuccess, status)
	require.Equal(t, 100, *score)
}

func TestIQProbeUsesChannelRequestConfiguration(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, "/prefix/v1/chat/completions", r.URL.Path)
		require.Equal(t, "Key active-key", r.Header.Get("Authorization"))
		require.Equal(t, "override-org", r.Header.Get("OpenAI-Organization"))
		require.Equal(t, "workspace", r.Header.Get("X-Workspace"))
		require.Empty(t, r.Header.Get("X-Client-Token"))
		require.Equal(t, "api.example", r.Host)
		var body map[string]any
		require.NoError(t, common.DecodeJson(r.Body, &body))
		require.Equal(t, "actual-upstream", body["model"])
		if body["max_tokens"] == float64(8) {
			// Preference samples carry no temperature and cap max_tokens at 8.
			require.Nil(t, body["temperature"])
		} else {
			require.Equal(t, float64(64), body["max_tokens"])
			require.EqualValues(t, 0, body["temperature"])
		}
		require.Equal(t, false, body["stream"])
		fmt.Fprint(w, `{"model":"actual-upstream","choices":[{"message":{"content":"incorrect"}}]}`)
	}))
	defer server.Close()
	ch := iqProbeChannel(server.URL + "/prefix/")
	ch.Key = "disabled-key\nactive-key"
	ch.ChannelInfo = model.ChannelInfo{IsMultiKey: true, MultiKeyStatusList: map[int]int{0: common.ChannelStatusAutoDisabled}}
	mapping, headers, org := `{"public-alias":"intermediate","intermediate":"actual-upstream","actual-upstream":"actual-upstream"}`, `{"Authorization":"Key {api_key}","X-Workspace":"workspace","OpenAI-Organization":"override-org","Host":"api.example","*":true,"X-Client-Token":"{client_header:Authorization}"}`, "default-org"
	ch.ModelMapping, ch.HeaderOverride, ch.OpenAIOrganization = &mapping, &headers, &org
	result := RunIQTestResult(context.Background(), ch, iqProbeModel(), "request-config", time.Second)
	require.Equal(t, 14, requests, "8 scoring questions plus 6 unpinned preference samples")
	require.Equal(t, model.IQTestResultStatusSuccess, result.Status)
	require.Equal(t, 0, *result.Score)
	require.Equal(t, -70, *result.Margin)
	require.Equal(t, "actual-upstream", result.UpstreamModel)
	require.Equal(t, 70, result.BaselineScoreSnapshot)
	require.EqualValues(t, 12, result.ChannelRevision)
	require.Equal(t, IQBankVersion(), result.BankVersion)
	require.Positive(t, result.FinishedAt)
	require.NotContains(t, result.Detail, "active-key")
}

func TestIQProbeUsesConfiguredProxy(t *testing.T) {
	requests := 0
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, "upstream.invalid", r.URL.Host)
		writeIQCorrectResponse(t, w, r)
	}))
	defer proxy.Close()
	ch := iqProbeChannel("http://upstream.invalid")
	encoded, err := common.Marshal(map[string]string{"proxy": proxy.URL})
	require.NoError(t, err)
	setting := string(encoded)
	ch.Setting = &setting
	result := RunIQTestResultWithConfig(context.Background(), ch, iqProbeModel(), "proxy", 5, time.Second)
	require.Equal(t, 11, requests, "5 scoring questions plus 6 preference samples")
	require.Equal(t, model.IQTestResultStatusSuccess, result.Status)
	require.Equal(t, 100, *result.Score)
}

func TestIQProbeExternalFailuresKeepNullScores(t *testing.T) {
	withNoIQBackoff(t)
	// A single-question round can never reach the sample floor, so every external
	// failure stays invalid with its real cause instead of being scored as a wrong answer.
	for _, test := range []struct {
		name, body, errorClass string
		code                   int
	}{
		{"unauthorized", `{}`, "auth_401_403", 401},
		{"forbidden", `{}`, "auth_401_403", 403},
		{"limited", `{}`, "upstream_429", 429},
		{"unsupported", `{}`, "upstream_4xx", 400},
		{"unavailable", `{}`, "upstream_5xx", 503},
		{"invalid-json", `broken`, "invalid_response", 200},
		{"empty-answer", `{"choices":[{"message":{"content":"  "}}]}`, "empty_answer", 200},
		{"oversize", strings.Repeat(" ", iqMaxResponseBytes+1), "response_too_large", 200},
		{"trailing-data", `{"choices":[{"message":{"content":"yes"}}]} another`, "invalid_response", 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(test.code); fmt.Fprint(w, test.body) }))
			defer server.Close()
			result := RunIQTestResultWithConfig(context.Background(), iqProbeChannel(server.URL), iqProbeModel(), test.name, 1, time.Second)
			require.Equal(t, model.IQTestResultStatusInvalid, result.Status)
			require.Equal(t, test.errorClass, result.ErrorClass)
			require.Nil(t, result.Score)
			require.Nil(t, result.Margin)
			require.Positive(t, result.FinishedAt)
			require.NotEmpty(t, result.Detail)
		})
	}
}

func TestIQProbePermanentFailuresWithinBudgetStillScore(t *testing.T) {
	withNoIQBackoff(t)
	for _, tc := range []struct {
		failures   int
		wantStatus string
		wantCalls  int
	}{
		{failures: 1, wantStatus: model.IQTestResultStatusSuccess, wantCalls: 13},
		{failures: 2, wantStatus: model.IQTestResultStatusSuccess, wantCalls: 15},
		{failures: 3, wantStatus: model.IQTestResultStatusInvalid, wantCalls: 9},
	} {
		t.Run(fmt.Sprint(tc.failures), func(t *testing.T) {
			selected := selectIQQuestions(5, "threshold")
			broken := map[string]bool{}
			for _, question := range selected[:tc.failures] {
				broken[question.Prompt] = true
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Messages []struct{ Content string } `json:"messages"`
				}
				require.NoError(t, common.DecodeJson(r.Body, &body))
				calls++
				prompt := body.Messages[0].Content
				if broken[prompt] {
					w.WriteHeader(503)
					return
				}
				for _, question := range iqBank {
					if question.Prompt == prompt {
						_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, question.Expected)
						return
					}
				}
			}))
			defer server.Close()
			result := RunIQTestResultWithConfig(context.Background(), iqProbeChannel(server.URL), iqProbeModel(), "threshold", 5, time.Second)
			require.Equal(t, tc.wantStatus, result.Status, result.Detail)
			require.Equal(t, tc.wantCalls, calls)
			if tc.wantStatus == model.IQTestResultStatusSuccess {
				require.NotNil(t, result.Score)
				require.Equal(t, 100, *result.Score)
				return
			}
			require.Nil(t, result.Score)
			require.Nil(t, result.Margin)
		})
	}
}

func TestIQProbeStopsWhenOwnershipIsLost(t *testing.T) {
	requests, checks := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; writeIQCorrectResponse(t, w, r) }))
	defer server.Close()
	ctx := context.WithValue(context.Background(), iqRequestGuardKey{}, func() error {
		checks++
		if checks > 1 {
			return errors.New("lease lost")
		}
		return nil
	})
	result := RunIQTestResultWithConfig(ctx, iqProbeChannel(server.URL), iqProbeModel(), "ownership", 8, time.Second)
	require.Equal(t, 1, requests)
	require.Equal(t, "error", result.Status)
	require.Equal(t, "cancelled", result.ErrorClass)
	require.Nil(t, result.Score)
}

func TestIQProbeTimeoutCancellationAndRedirect(t *testing.T) {
	withNoIQBackoff(t)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(100 * time.Millisecond):
		}
	}))
	defer slow.Close()
	result := RunIQTestResultWithConfig(context.Background(), iqProbeChannel(slow.URL), iqProbeModel(), "timeout", 1, 20*time.Millisecond)
	require.Equal(t, "invalid", result.Status)
	require.Contains(t, result.Detail, "timeout")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result = RunIQTestResultWithConfig(ctx, iqProbeChannel(slow.URL), iqProbeModel(), "cancel", 1, time.Second)
	require.Equal(t, "cancelled", result.ErrorClass)
	targetRequests := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetRequests++ }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	result = RunIQTestResultWithConfig(context.Background(), iqProbeChannel(redirect.URL), iqProbeModel(), "redirect", 1, time.Second)
	require.Equal(t, "upstream_redirect", result.ErrorClass)
	require.Zero(t, targetRequests)
}

func TestIQExcerptIsBoundedUTF8AndStripsMarkup(t *testing.T) {
	for size := 0; size <= 20; size++ {
		excerpt := safeIQExcerpt("<b>中文</b>\x00\n"+strings.Repeat("好", 20), size)
		require.LessOrEqual(t, len(excerpt), size)
		require.True(t, utf8.ValidString(excerpt))
		require.NotContains(t, excerpt, "<")
		require.NotContains(t, excerpt, "\x00")
		require.NotContains(t, excerpt, "\n")
	}
}

func TestIQProbeRejectsUnsupportedAndMalformedConfiguration(t *testing.T) {
	for _, test := range []struct {
		name, errorClass string
		change           func(*model.Channel)
	}{
		{"unsupported", "unsupported_channel", func(ch *model.Channel) { ch.Type = constant.ChannelTypeGemini }},
		{"invalid-map", "invalid_model_mapping", func(ch *model.Channel) { raw := `broken`; ch.ModelMapping = &raw }},
		{"cyclic-map", "invalid_model_mapping", func(ch *model.Channel) {
			raw := `{"public-alias":"other","other":"public-alias"}`
			ch.ModelMapping = &raw
		}},
		{"invalid-headers", "invalid_header_override", func(ch *model.Channel) { raw := `{"X-Test":true}`; ch.HeaderOverride = &raw }},
		{"no-key", "no_enabled_key", func(ch *model.Channel) { ch.Key = "" }},
		{"disabled-key", "no_enabled_key", func(ch *model.Channel) {
			ch.ChannelInfo = model.ChannelInfo{IsMultiKey: true, MultiKeyStatusList: map[int]int{0: common.ChannelStatusAutoDisabled}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ch := iqProbeChannel("http://example.invalid")
			test.change(ch)
			result := RunIQTestResultWithConfig(context.Background(), ch, iqProbeModel(), test.name, 1, time.Second)
			require.Equal(t, "error", result.Status)
			require.Equal(t, test.errorClass, result.ErrorClass)
			require.Nil(t, result.Score)
		})
	}
}

func TestIQProbeFailurePersistsAndSensitiveAnswerIsRedacted(t *testing.T) {
	setupIQRuntimeDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer server.Close()
	result := RunIQTestResultWithConfig(context.Background(), iqProbeChannel(server.URL), iqProbeModel(), "persist-error", 1, time.Second)
	require.NoError(t, RecordIQTestResult(result))
	persisted, err := model.GetIQTestResult(result.Id)
	require.NoError(t, err)
	require.Nil(t, persisted.Score)
	require.Nil(t, persisted.Margin)
	require.Equal(t, "auth_401_403", persisted.ErrorClass)
	require.Equal(t, 70, persisted.BaselineScoreSnapshot)
	require.Positive(t, persisted.FinishedAt)
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[{"message":{"content":"<b>iq-test-secret</b>"}}]}`)
	}))
	defer echo.Close()
	result = RunIQTestResultWithConfig(context.Background(), iqProbeChannel(echo.URL), iqProbeModel(), "echo", 1, time.Second)
	require.NotContains(t, result.Detail, "iq-test-secret")
	require.Contains(t, result.Detail, "[redacted]")
}

func iqPointer(v int) *int { return &v }

// withNoIQBackoff removes retry sleep so retry behaviour tests stay fast.
func withNoIQBackoff(t *testing.T) {
	t.Helper()
	original := iqProbeBackoff
	iqProbeBackoff = []time.Duration{0, 0}
	t.Cleanup(func() { iqProbeBackoff = original })
}

func TestIQProbeRetriesTransientFailureAndScoresFullRound(t *testing.T) {
	withNoIQBackoff(t)
	var mu sync.Mutex
	attempts := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		require.NoError(t, common.DecodeJson(r.Body, &body))
		prompt := body.Messages[0].Content
		mu.Lock()
		attempts[prompt]++
		first := attempts[prompt] == 1
		mu.Unlock()
		if first {
			w.WriteHeader(503)
			return
		}
		for _, question := range iqBank {
			if question.Prompt == prompt {
				_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, question.Expected)
				return
			}
		}
	}))
	defer server.Close()
	// Every question's first attempt fails, so the whole round only succeeds if
	// transient failures are retried rather than turning the round into an error.
	result := RunIQTestResultWithConfig(context.Background(), iqProbeChannel(server.URL), iqProbeModel(), "retry-all-good", 8, time.Second)
	require.Equal(t, model.IQTestResultStatusSuccess, result.Status, result.Detail)
	require.NotNil(t, result.Score)
	require.Equal(t, 100, *result.Score)
	require.Equal(t, 9, len(attempts), "8 scoring questions plus the preference probe, all retried past a first 503")
	require.Contains(t, result.Detail, `"attempts":2`)
}

func TestIQProbeDoesNotRetryAuthFailure(t *testing.T) {
	withNoIQBackoff(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(401)
	}))
	defer server.Close()
	// Three attempted questions: retrying auth rejections would cost 9 calls.
	result := RunIQTestResultWithConfig(context.Background(), iqProbeChannel(server.URL), iqProbeModel(), "auth-no-retry", 5, time.Second)
	require.Equal(t, 3, calls, "auth failures must not be retried")
	require.Equal(t, model.IQTestResultStatusInvalid, result.Status)
	require.Equal(t, "auth_401_403", result.ErrorClass, "a round with no usable answer keeps the real failure reason")
	require.Nil(t, result.Score)
}

func TestIQProbeScoresValidSubsetAndExcludesProbeFailures(t *testing.T) {
	withNoIQBackoff(t)
	// Two of the eight selected questions always 400; the remaining six answer correctly.
	selected := selectIQQuestions(8, "subset")
	failing := map[string]bool{selected[0].Prompt: true, selected[1].Prompt: true}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		require.NoError(t, common.DecodeJson(r.Body, &body))
		prompt := body.Messages[0].Content
		if failing[prompt] {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"message":"this channel cannot do that"}}`)
			return
		}
		for _, question := range iqBank {
			if question.Prompt == prompt {
				_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, question.Expected)
				return
			}
		}
	}))
	defer server.Close()
	result := RunIQTestResultWithConfig(context.Background(), iqProbeChannel(server.URL), iqProbeModel(), "subset", 8, time.Second)
	require.Equal(t, model.IQTestResultStatusSuccess, result.Status, result.Detail)
	require.NotNil(t, result.Score, "probe failures must not poison the round")
	require.Equal(t, 100, *result.Score, "score is computed over the six answered questions")
	require.Equal(t, 8, result.TotalQuestions)
	require.Contains(t, result.Detail, "this channel cannot do that")
	require.Contains(t, result.Detail, `"error_class":"probe_failed"`)
}

func TestIQProbeMarksRoundInvalidWhenValidSampleTooSmall(t *testing.T) {
	withNoIQBackoff(t)
	// Six of eight selected questions always fail, so the usable sample can never
	// clear the 60% floor.
	selected := selectIQQuestions(8, "too-small")
	broken := map[string]bool{}
	for _, question := range selected[:6] {
		broken[question.Prompt] = true
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		require.NoError(t, common.DecodeJson(r.Body, &body))
		if broken[body.Messages[0].Content] {
			w.WriteHeader(400)
			return
		}
		for _, question := range iqBank {
			if question.Prompt == body.Messages[0].Content {
				_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, question.Expected)
				return
			}
		}
	}))
	defer server.Close()
	result := RunIQTestResultWithConfig(context.Background(), iqProbeChannel(server.URL), iqProbeModel(), "too-small", 8, time.Second)
	require.Equal(t, model.IQTestResultStatusInvalid, result.Status)
	require.Nil(t, result.Score)
	require.Nil(t, result.Margin)
}

func TestIQRoundOutcomeRequiresUsableSampleFloor(t *testing.T) {
	cases := []struct {
		name                       string
		valid, total               int
		correctWeight, validWeight int
		firstFailureClass          string
		wantStatus, wantErrorClass string
		wantScore                  *int
	}{
		{name: "full round passes", valid: 8, total: 8, correctWeight: 16, validWeight: 16, wantStatus: model.IQTestResultStatusSuccess, wantScore: iqPointer(100)},
		{name: "subset scores on valid answers", valid: 5, total: 8, correctWeight: 8, validWeight: 10, firstFailureClass: "upstream_4xx", wantStatus: model.IQTestResultStatusSuccess, wantScore: iqPointer(80)},
		{name: "one short of the floor", valid: 4, total: 8, correctWeight: 8, validWeight: 8, firstFailureClass: "upstream_4xx", wantStatus: model.IQTestResultStatusInvalid, wantErrorClass: "insufficient_sample"},
		{name: "three question round must be flawless", valid: 2, total: 3, correctWeight: 4, validWeight: 4, firstFailureClass: "upstream_4xx", wantStatus: model.IQTestResultStatusInvalid, wantErrorClass: "insufficient_sample"},
		{name: "flawless three question round scores", valid: 3, total: 3, correctWeight: 6, validWeight: 6, wantStatus: model.IQTestResultStatusSuccess, wantScore: iqPointer(100)},
		{name: "two question round must be flawless but can score", valid: 2, total: 2, correctWeight: 4, validWeight: 4, wantStatus: model.IQTestResultStatusSuccess, wantScore: iqPointer(100)},
		{name: "no usable answer keeps the real reason", valid: 0, total: 8, correctWeight: 0, validWeight: 0, firstFailureClass: "auth_401_403", wantStatus: model.IQTestResultStatusInvalid, wantErrorClass: "auth_401_403"},
		{name: "single question round fails", valid: 0, total: 1, correctWeight: 0, validWeight: 0, firstFailureClass: "timeout", wantStatus: model.IQTestResultStatusInvalid, wantErrorClass: "timeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, errorClass, score, margin := iqRoundOutcome(tc.valid, tc.total, tc.correctWeight, tc.validWeight, tc.firstFailureClass, 70)
			require.Equal(t, tc.wantStatus, status)
			require.Equal(t, tc.wantErrorClass, errorClass)
			if tc.wantScore == nil {
				require.Nil(t, score)
				require.Nil(t, margin)
				return
			}
			require.NotNil(t, score)
			require.Equal(t, *tc.wantScore, *score)
		})
	}
	require.Equal(t, 5, iqMinValidQuestions(8), "60% floor of an 8 question round")
	require.Equal(t, 3, iqMinValidQuestions(4), "at least three usable answers")
	require.Equal(t, 3, iqMinValidQuestions(3), "a 3 question round must be flawless")
	require.Equal(t, 2, iqMinValidQuestions(2), "smaller rounds cap at their own size")
	require.Equal(t, 1, iqMinValidQuestions(1))
}

func TestIQProbeStopsEarlyOnceSixtyPercentIsUnreachable(t *testing.T) {
	withNoIQBackoff(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(400)
	}))
	defer server.Close()
	result := RunIQTestResultWithConfig(context.Background(), iqProbeChannel(server.URL), iqProbeModel(), "dead-channel", 8, time.Second)
	require.Equal(t, model.IQTestResultStatusInvalid, result.Status)
	require.Equal(t, "upstream_4xx", result.ErrorClass)
	// Four exhausted questions at 3 attempts each is the most this can cost; the
	// rest are skipped because 60% of the round is already unreachable.
	require.Equal(t, 12, calls)
}

func TestIQProbeRedactsKeysInCapturedErrorSnippet(t *testing.T) {
	withNoIQBackoff(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		fmt.Fprint(w, `{"error":{"message":"bad key iq-test-secret"}}`)
	}))
	defer server.Close()
	result := RunIQTestResultWithConfig(context.Background(), iqProbeChannel(server.URL), iqProbeModel(), "snippet-redact", 1, time.Second)
	require.NotContains(t, result.Detail, "iq-test-secret")
	require.Contains(t, result.Detail, "[redacted]")
}

func TestIQProbeAnthropicChannelUsesNativeMessagesAPI(t *testing.T) {
	withNoIQBackoff(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/messages", r.URL.Path)
		require.Equal(t, "iq-test-secret", r.Header.Get("x-api-key"))
		require.Empty(t, r.Header.Get("Authorization"))
		require.Equal(t, "2023-06-01", r.Header.Get("anthropic-version"))
		var body struct {
			Model       string   `json:"model"`
			MaxTokens   *int     `json:"max_tokens"`
			Temperature *float64 `json:"temperature"`
			Messages    []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		require.NoError(t, common.DecodeJson(r.Body, &body))
		require.Equal(t, "actual-upstream", body.Model)
		require.NotNil(t, body.MaxTokens)
		require.Nil(t, body.Temperature, "Claude models that reject non-default temperature must stay scoreable")
		require.Len(t, body.Messages, 1)
		require.Equal(t, "user", body.Messages[0].Role)
		for _, question := range iqBank {
			if question.Prompt == body.Messages[0].Content {
				_, _ = fmt.Fprintf(w, `{"content":[{"type":"text","text":%q}]}`, question.Expected)
				return
			}
		}
		if body.Messages[0].Content == iqPreferenceQuestion.Prompt {
			_, _ = fmt.Fprint(w, `{"content":[{"type":"text","text":"巴西"}]}`)
			return
		}
		t.Errorf("unknown prompt %q", body.Messages[0].Content)
	}))
	defer server.Close()
	mapping := `{"public-alias":"actual-upstream"}`
	ch := iqAnthropicProbeChannel(server.URL)
	ch.ModelMapping = &mapping
	result := RunIQTestResultWithConfig(context.Background(), ch, iqProbeModel(), "anthropic", 8, time.Second)
	require.Equal(t, model.IQTestResultStatusSuccess, result.Status, result.Detail)
	require.NotNil(t, result.Score)
	require.Equal(t, 100, *result.Score)
	require.Equal(t, "actual-upstream", result.UpstreamModel)
}

func TestIQProbeAnthropicJoinsTextBlocksAndKeepsErrorReason(t *testing.T) {
	withNoIQBackoff(t)
	t.Run("joined text blocks", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"39"},{"type":"text","text":"1"}]}`))
		}))
		defer server.Close()
		result := RunIQTestResultWithConfig(context.Background(), iqAnthropicProbeChannel(server.URL), iqProbeModel(), "join", 1, time.Second)
		require.Contains(t, result.Detail, `"answer_excerpt":"391"`)
	})
	t.Run("native error shape is captured", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: 64 > available budget"}}`)
		}))
		defer server.Close()
		result := RunIQTestResultWithConfig(context.Background(), iqAnthropicProbeChannel(server.URL), iqProbeModel(), "errshape", 1, time.Second)
		require.Equal(t, model.IQTestResultStatusInvalid, result.Status)
		require.Contains(t, result.Detail, "invalid_request_error")
		require.Contains(t, result.Detail, "available budget")
	})
}
