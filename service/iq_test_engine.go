package service

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"golang.org/x/net/html"
	"golang.org/x/net/http/httpguts"
)

type iqQuestion struct {
	ID, Prompt, Expected, Judge, Category string
	Weight                                int
}

// iqBank is stratified by difficulty: weight-1 sentinels (math, common) catch
// outright model swaps, weight-2 (logic, instruction) catch coarse degradation,
// and weight-3 discriminators (math_hard, logic_hard) separate channels that
// still pass everything else, so a 90-100 score range stays meaningful.
var iqBank = []iqQuestion{
	{"math-1", "17×23 等于？只回答数字", "391", "numeric", "math", 1},
	{"math-2", "144 的平方根是多少？只回答数字", "12", "numeric", "math", 1},
	{"math-3", "25+17 等于？只回答数字", "42", "numeric", "math", 1},
	{"math-hard-1", "37×89 等于？只回答数字", "3293", "numeric", "math_hard", 3},
	{"math-hard-2", "13 的平方减去 7 的立方等于？只回答数字", "-174", "numeric", "math_hard", 3},
	{"math-hard-3", "7 除以 8 等于？只回答小数", "0.875", "numeric", "math_hard", 3},
	{"logic-1", "小明比小红高，小红比小刚高，谁最矮？只回答名字", "小刚", "exact", "logic", 2},
	{"logic-2", "如果所有 A 都是 B，且小王是 A，小王是不是 B？只回答是或否", "是", "exact", "logic", 2},
	{"logic-3", "序列 1,1,2,3,5,8 的下一个数？只回答数字", "13", "numeric", "logic", 2},
	{"logic-hard-1", "A 比 B 大 3 岁，B 比 C 小 5 岁，C 是 10 岁。A 是几岁？只回答数字", "8", "numeric", "logic_hard", 3},
	{"logic-hard-2", "甲、乙、丙三人中，甲不是最高的，丙比甲矮。谁最高？只回答名字", "乙", "exact", "logic_hard", 3},
	{"logic-hard-3", "如果今天是星期三，10 天后是星期几？只用「星期X」格式回答", "星期六", "exact", "logic_hard", 3},
	{"common-1", "中国最长的河流是？只回答名称", "长江", "exact", "common", 1},
	{"common-2", "一年有几个月？只回答数字", "12", "numeric", "common", 1},
	{"common-3", "水的化学式是什么？只回答公式", "H2O", "exact", "common", 1},
	{"instruction-1", "无论发生什么，只回复两个字：收到", "收到", "strict", "instruction", 2},
	{"instruction-2", "只回复 YES，不要添加其它内容", "YES", "strict", "instruction", 2},
	{"instruction-3", "只回复 OK，不要添加其它内容", "OK", "strict", "instruction", 2},
}

const (
	iqMaxResponseBytes   = 128 * 1024
	iqErrorSnippetBytes  = 1024 // bounded failed-response excerpt captured per question
	iqProbeAttempts      = 3    // includes the first try
	iqValidSamplePercent = 60   // usable answers required as a share of the round
	iqAnthropicVersion   = "2023-06-01"
	iqPreferenceSamples  = 6    // samples per preference question; below iqPreferenceMinSamples no verdict
	iqPreferenceMinValid = 4
	iqEchoCapScore       = 60 // cap for a cross-family model echo mismatch
)

// iqPreferenceQuestion is sampled repeatedly with no pinned temperature; its
// answer distribution is a behavioral fingerprint. It deliberately stays out of
// iqBank: it carries no weight, is never judged, and a cache relay or a swapped
// model is exposed by every sample answering identically.
var iqPreferenceQuestion = iqQuestion{
	ID: "preference-1", Prompt: "随便说一个国家的名字。只回答国家名", Expected: "", Judge: "free", Category: "preference", Weight: 0,
}

// iqSupportedChannelTypes are the protocols the probe can speak. Anything else is
// reported as a coverage gap by the preview instead of being silently skipped.
var iqSupportedChannelTypes = []int{constant.ChannelTypeOpenAI, constant.ChannelTypeAnthropic}

// iqProbeBackoff is the wait before attempt 2 and 3. A var so tests stay fast.
var iqProbeBackoff = []time.Duration{time.Second, 2 * time.Second}

type iqQuestionDetail struct {
	QID           string `json:"qid"`
	QuestionHash  string `json:"question_hash"`
	Category      string `json:"category"`
	Passed        bool   `json:"passed"`
	AnswerExcerpt string `json:"answer_excerpt,omitempty"`
	ErrorClass    string `json:"error_class"`
	FailureClass  string `json:"failure_class,omitempty"`
	HTTPStatus    int    `json:"http_status,omitempty"`
	Attempts      int    `json:"attempts,omitempty"`
	ErrorSnippet  string `json:"error_snippet,omitempty"`
}

// iqPreferenceBlock records one preference question's sampled answers and the
// entropy verdict. It is a list so future distribution-fingerprint questions
// extend it without changing the shape.
type iqPreferenceBlock struct {
	QID      string   `json:"qid"`
	Samples  []string `json:"samples"`
	Verdict  string   `json:"verdict"` // collapse | ok | skipped
	Dominance float64 `json:"dominance,omitempty"`
}

// iqModelEchoBlock records what the upstream echoed as the serving model versus
// the resolved request target; a cross-family mismatch caps the score.
type iqModelEchoBlock struct {
	Requested string `json:"requested"`
	Echoed    string `json:"echoed"`
	Verdict   string `json:"verdict"` // mismatch | match
}

type iqChatResponse struct {
	Model string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type iqMessagesResponse struct {
	Model   string `json:"model"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func IQBankVersion() string { return "builtin-v3" }

func RunIQTestResult(ctx context.Context, ch *model.Channel, configured *model.IQTestModel, runID string, timeout time.Duration) *model.IQTestResult {
	return RunIQTestResultWithConfig(ctx, ch, configured, runID, 8, timeout)
}

// iqResultDetail is the persisted Detail payload: the question breakdown plus
// the newer behavioral blocks (preference sampling, model echo). The question
// array stays the first citizen so the frontend zod schema keeps parsing.
type iqResultDetail struct {
	Questions         []iqQuestionDetail `json:"questions"`
	PreferenceSamples []iqPreferenceBlock `json:"preference_samples,omitempty"`
	ModelEcho         *iqModelEchoBlock  `json:"model_echo,omitempty"`
}

func RunIQTestResultWithConfig(ctx context.Context, ch *model.Channel, configured *model.IQTestModel, runID string, questions int, timeout time.Duration) *model.IQTestResult {
	started := time.Now()
	result := &model.IQTestResult{RunID: runID, Status: model.IQTestResultStatusError, TotalQuestions: questions,
		StartedAt: started.Unix(), CreatedAt: started.Unix(), Action: model.IQTestActionNone, BankVersion: IQBankVersion()}
	detailPayload := iqResultDetail{}
	recorded := false
	defer func() {
		result.DurationMs = time.Since(started).Milliseconds()
		result.FinishedAt = time.Now().Unix()
		if recorded {
			encoded, err := common.Marshal(detailPayload)
			if err != nil {
				result.Status, result.ErrorClass, result.Score, result.Margin = model.IQTestResultStatusError, "detail_encode", nil, nil
				return
			}
			result.Detail = string(encoded)
		}
	}()
	if ch != nil {
		result.ChannelID, result.ChannelRevision = ch.Id, ch.IQRevision
	}
	if configured != nil {
		result.RequestedModel, result.BaselineScoreSnapshot = configured.ModelName, configured.BaselineScore
	}
	if ch == nil || configured == nil || runID == "" || questions < 1 || questions > len(iqBank) || timeout <= 0 {
		result.ErrorClass = "invalid_input"
		return result
	}
	target, err := iqRequestConfiguration(ch, configured.ModelName, timeout)
	if err != nil {
		result.ErrorClass = err.Error()
		return result
	}
	result.UpstreamModel = target.upstream
	selected := selectIQQuestions(questions, runID)
	details := make([]iqQuestionDetail, 0, len(selected))
	recorded = true
	var echoedModel string
	valid := 0
	validWeight := 0
	correctWeight := 0
	probeFailures := 0
	firstFailureClass := ""
	minValid := iqMinValidQuestions(len(selected))
	for _, question := range selected {
		// Stop once too few questions remain to clear the sample floor, so a dead
		// channel costs the same probing budget as a working one.
		if len(selected)-probeFailures < minValid {
			break
		}
		answer, attempts, status, failureClass, snippet, echoed := probeIQQuestion(ctx, target, question)
		if echoed != "" && echoedModel == "" {
			echoedModel = echoed
		}
		if failureClass == "cancelled" {
			result.ErrorClass = "cancelled"
			return result
		}
		detail := iqQuestionDetail{QID: question.ID, QuestionHash: iqQuestionHash(question), Category: question.Category, Attempts: attempts, HTTPStatus: status}
		if failureClass != "none" {
			probeFailures++
			if firstFailureClass == "" {
				firstFailureClass = failureClass
			}
			detail.ErrorClass = "probe_failed"
			detail.FailureClass = failureClass
			for _, key := range ch.GetKeys() {
				if strings.TrimSpace(key) != "" {
					snippet = strings.ReplaceAll(snippet, key, "[redacted]")
				}
			}
			detail.ErrorSnippet = safeIQExcerpt(snippet, 200)
			details = append(details, detail)
			continue
		}
		valid++
		validWeight += question.Weight
		detail.Passed = judgeIQAnswer(question, answer)
		excerpt := answer
		for _, key := range ch.GetKeys() {
			if strings.TrimSpace(key) != "" {
				excerpt = strings.ReplaceAll(excerpt, key, "[redacted]")
			}
		}
		detail.AnswerExcerpt = safeIQExcerpt(excerpt, 200)
		if detail.Passed {
			result.CorrectCount++
			correctWeight += question.Weight
		} else {
			detail.ErrorClass = "judge_failed"
		}
		details = append(details, detail)
	}
	result.Status, result.ErrorClass, result.Score, result.Margin = iqRoundOutcome(valid, len(selected), correctWeight, validWeight, firstFailureClass, configured.BaselineScore)
	if result.Status != model.IQTestResultStatusSuccess {
		detailPayload.Questions = details
		return result
	}
	if block, ok := sampleIQPreference(ctx, target, ch); ok {
		detailPayload.PreferenceSamples = []iqPreferenceBlock{block}
	}
	detailPayload.Questions = details
	detailPayload.ModelEcho = iqEvaluateEcho(target.upstream, echoedModel)
	if detailPayload.ModelEcho != nil && detailPayload.ModelEcho.Verdict == "mismatch" && *result.Score > iqEchoCapScore {
		capped := iqEchoCapScore
		result.Score = &capped
		margin := capped - configured.BaselineScore
		result.Margin = &margin
	}
	return result
}

// iqMinValidQuestions is the usable-answer floor a round needs to be scoreable:
// 60% of the round, and at least three usable answers whenever the round is
// large enough to supply them. A one-or-two-question round must be flawless.
func iqMinValidQuestions(total int) int {
	if total < 1 {
		return 0
	}
	floor := (total*iqValidSamplePercent + 99) / 100
	if total >= 3 && floor < 3 {
		floor = 3
	}
	return floor
}

// iqRoundOutcome scores the usable subset by question weight so a missed
// weight-3 discriminator costs more than a missed weight-1 sentinel. Upstream
// flakiness never reads as a wrong answer. A round with no usable answer keeps
// its real failure reason.
func iqRoundOutcome(valid, total, correctWeight, validWeight int, firstFailureClass string, baseline int) (string, string, *int, *int) {
	if valid < iqMinValidQuestions(total) {
		if valid == 0 && firstFailureClass != "" {
			return model.IQTestResultStatusInvalid, firstFailureClass, nil, nil
		}
		return model.IQTestResultStatusInvalid, "insufficient_sample", nil, nil
	}
	score := correctWeight * 100 / validWeight
	margin := score - baseline
	return model.IQTestResultStatusSuccess, "", &score, &margin
}

// probeIQQuestion asks one question, retrying failures that could be transient.
func probeIQQuestion(ctx context.Context, target *iqProbeTarget, question iqQuestion) (answer string, attempts, status int, failureClass, snippet, echoed string) {
	body, err := target.buildBody(question.Prompt)
	if err != nil {
		return "", 0, 0, "request_build", "", ""
	}
	var errorClass string
	for attempt := 1; attempt <= iqProbeAttempts; attempt++ {
		if err := CheckIQTestRequest(ctx); err != nil {
			return "", attempt, 0, "cancelled", "", ""
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.requestURL, strings.NewReader(string(body)))
		if err != nil {
			return "", attempt, 0, "request_build", "", ""
		}
		req.Header = target.headers.Clone()
		if host := target.headers.Get("Host"); host != "" {
			req.Host = host
		}
		answer, status, errorClass, snippet, echoed = requestIQAnswer(target.client, req, target)
		if errorClass == "none" || !iqRetryableErrorClass(errorClass) || attempt == iqProbeAttempts {
			return answer, attempt, status, errorClass, snippet, echoed
		}
		backoff := time.Duration(0)
		if attempt-1 < len(iqProbeBackoff) {
			backoff = iqProbeBackoff[attempt-1]
		}
		select {
		case <-ctx.Done():
			return answer, attempt, status, "cancelled", snippet, echoed
		case <-time.After(backoff):
		}
	}
	return answer, iqProbeAttempts, status, errorClass, snippet, echoed
}

// sampleIQPreference asks the preference question iqPreferenceSamples times
// with no pinned temperature and judges answer dominance: a cache relay or a
// swapped model answers identically every time. A failed sample just shrinks
// the pool; below iqPreferenceMinValid no verdict is emitted.
func sampleIQPreference(ctx context.Context, target *iqProbeTarget, ch *model.Channel) (iqPreferenceBlock, bool) {
	block := iqPreferenceBlock{QID: iqPreferenceQuestion.ID, Samples: make([]string, 0, iqPreferenceSamples)}
	for i := 0; i < iqPreferenceSamples; i++ {
		if err := CheckIQTestRequest(ctx); err != nil {
			return block, false
		}
		answer, errorClass, _, _, _ := probeOnceNoPinnedTemperature(ctx, target, iqPreferenceQuestion.Prompt)
		if errorClass != "none" {
			continue
		}
		excerpt := answer
		for _, key := range ch.GetKeys() {
			if strings.TrimSpace(key) != "" {
				excerpt = strings.ReplaceAll(excerpt, key, "[redacted]")
			}
		}
		block.Samples = append(block.Samples, safeIQExcerpt(excerpt, 40))
	}
	if len(block.Samples) < iqPreferenceMinValid {
		block.Samples = nil
		block.Verdict = "skipped"
		return block, true
	}
	counts := map[string]int{}
	for _, sample := range block.Samples {
		counts[sample]++
	}
	best := 0
	for _, count := range counts {
		if count > best {
			best = count
		}
	}
	block.Dominance = float64(best) / float64(len(block.Samples))
	if block.Dominance == 1 {
		block.Verdict = "collapse"
	} else {
		block.Verdict = "ok"
	}
	return block, true
}

// probeOnceNoPinnedTemperature issues a single short request without the
// temperature field so the upstream's natural answer distribution survives.
func probeOnceNoPinnedTemperature(ctx context.Context, target *iqProbeTarget, prompt string) (answer, errorClass, snippet, echoedModel string, status int) {
	body, err := target.buildBodyUnpinned(prompt)
	if err != nil {
		return "", "request_build", "", "", 0
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.requestURL, strings.NewReader(string(body)))
	if err != nil {
		return "", "request_build", "", "", 0
	}
	req.Header = target.headers.Clone()
	if host := target.headers.Get("Host"); host != "" {
		req.Host = host
	}
	answer, status, errorClass, snippet, echoedModel = requestIQAnswer(target.client, req, target)
	return answer, errorClass, snippet, echoedModel, status
}

// iqEvaluateEcho compares the upstream-resolved request model with the echoed
// serving model. Family-prefixed variants (dated snapshots) are accepted;
// anything else cross-family caps the score. A missing echo stays silent.
func iqEvaluateEcho(requested, echoed string) *iqModelEchoBlock {
	if echoed == "" {
		return nil
	}
	if echoed == requested || strings.HasPrefix(echoed, requested+"-") || strings.HasPrefix(requested, echoed+"-") {
		return &iqModelEchoBlock{Requested: requested, Echoed: echoed, Verdict: "match"}
	}
	return &iqModelEchoBlock{Requested: requested, Echoed: echoed, Verdict: "mismatch"}
}

// iqRetryableErrorClass reports whether another attempt could plausibly succeed.
// Auth rejections are deterministic, so retrying them only burns upstream quota.
func iqRetryableErrorClass(errorClass string) bool {
	switch errorClass {
	case "none", "cancelled", "request_build", "auth_401_403", "upstream_redirect":
		return false
	default:
		return true
	}
}

func iqRequestConfiguration(ch *model.Channel, requested string, timeout time.Duration) (*iqProbeTarget, error) {
	anthropic := false
	path := "/v1/chat/completions"
	switch ch.Type {
	case constant.ChannelTypeOpenAI:
	case constant.ChannelTypeAnthropic:
		anthropic = true
		path = "/v1/messages"
	default:
		return nil, errors.New("unsupported_channel")
	}
	base := strings.TrimRight(strings.TrimSpace(ch.GetBaseURL()), "/")
	parsedURL, err := url.Parse(base)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" || parsedURL.User != nil || parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		return nil, errors.New("invalid_base_url")
	}
	upstream := requested
	if raw := ch.GetModelMapping(); strings.TrimSpace(raw) != "" {
		var mapping map[string]string
		if err := common.UnmarshalJsonStr(raw, &mapping); err != nil {
			return nil, errors.New("invalid_model_mapping")
		}
		visited := map[string]bool{upstream: true}
		for {
			mapped, exists := mapping[upstream]
			if !exists || mapped == "" || mapped == upstream {
				break
			}
			if visited[mapped] {
				return nil, errors.New("invalid_model_mapping")
			}
			visited[mapped], upstream = true, mapped
		}
	}
	key := firstEnabledKey(ch)
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("no_enabled_key")
	}
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	if anthropic {
		headers.Set("x-api-key", key)
		headers.Set("anthropic-version", iqAnthropicVersion)
	} else {
		headers.Set("Authorization", "Bearer "+key)
	}
	if !anthropic && ch.OpenAIOrganization != nil && *ch.OpenAIOrganization != "" {
		headers.Set("OpenAI-Organization", *ch.OpenAIOrganization)
	}
	if ch.HeaderOverride != nil && strings.TrimSpace(*ch.HeaderOverride) != "" {
		var overrides map[string]any
		if err := common.UnmarshalJsonStr(*ch.HeaderOverride, &overrides); err != nil {
			return nil, errors.New("invalid_header_override")
		}
		for name, value := range overrides {
			name = strings.TrimSpace(name)
			lower := strings.ToLower(name)
			// Channel tests have no incoming user headers to pass through.
			if lower == "*" || strings.HasPrefix(lower, "re:") || strings.HasPrefix(lower, "regex:") || name == "" {
				continue
			}
			str, ok := value.(string)
			if !ok {
				return nil, errors.New("invalid_header_override")
			}
			if strings.HasPrefix(strings.TrimSpace(str), "{client_header:") {
				continue
			}
			str = strings.ReplaceAll(str, "{api_key}", key)
			if strings.TrimSpace(str) == "" {
				continue
			}
			if !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(str) {
				return nil, errors.New("invalid_header_override")
			}
			headers.Set(name, str)
		}
	}
	var settings dto.ChannelSettings
	if ch.Setting != nil && strings.TrimSpace(*ch.Setting) != "" {
		if err := common.UnmarshalJsonStr(*ch.Setting, &settings); err != nil {
			return nil, errors.New("invalid_channel_settings")
		}
	}
	shared, err := NewProxyHttpClient(settings.Proxy)
	if err != nil {
		return nil, errors.New("invalid_proxy")
	}
	client := *shared
	client.Timeout = timeout
	// No redirects: every question must reach exactly the configured upstream.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &iqProbeTarget{
		requestURL: relaycommon.GetFullRequestURL(base, path, ch.Type),
		upstream:   upstream,
		headers:    headers,
		client:     &client,
		anthropic:  anthropic,
	}, nil
}

// iqProbeTarget is the resolved upstream endpoint for one channel, including the
// protocol dialect needed to ask a question and read the answer back.
type iqProbeTarget struct {
	requestURL string
	upstream   string
	headers    http.Header
	client     *http.Client
	anthropic  bool
}

// buildBody renders the single-question request in the target's protocol dialect.
func (t *iqProbeTarget) buildBody(prompt string) ([]byte, error) {
	return t.buildBodyOptions(prompt, true)
}

// buildBodyUnpinned omits temperature and shortens max_tokens: preference
// sampling must observe the upstream's natural distribution, and a free-form
// country name never needs 64 tokens.
func (t *iqProbeTarget) buildBodyUnpinned(prompt string) ([]byte, error) {
	return t.buildBodyOptions(prompt, false)
}

func (t *iqProbeTarget) buildBodyOptions(prompt string, pinned bool) ([]byte, error) {
	request := map[string]any{
		"model":      t.upstream,
		"messages":   []map[string]string{{"role": "user", "content": prompt}},
		"max_tokens": 64,
		"stream":     false,
	}
	if !pinned {
		request["max_tokens"] = 8
	} else if !t.anthropic {
		// Opus 4.7+ rejects a non-default temperature, so it is only sent where accepted.
		request["temperature"] = 0
	}
	return common.Marshal(request)
}

// parseAnswer extracts the replied text and echoed serving model from the
// target's protocol response shape.
func (t *iqProbeTarget) parseAnswer(body []byte) (answer, echoedModel string, err error) {
	if t.anthropic {
		var parsed iqMessagesResponse
		if err := common.Unmarshal(body, &parsed); err != nil {
			return "", "", err
		}
		var text strings.Builder
		for _, block := range parsed.Content {
			if block.Type == "text" {
				text.WriteString(block.Text)
			}
		}
		return text.String(), parsed.Model, nil
	}
	var parsed iqChatResponse
	if err := common.Unmarshal(body, &parsed); err != nil {
		return "", "", err
	}
	if len(parsed.Choices) == 0 {
		return "", parsed.Model, nil
	}
	return parsed.Choices[0].Message.Content, parsed.Model, nil
}

func requestIQAnswer(client *http.Client, req *http.Request, target *iqProbeTarget) (answer string, status int, errorClass, snippet, echoedModel string) {
	response, err := client.Do(req)
	if err != nil {
		return "", 0, iqTransportErrorClass(err), "", ""
	}
	defer response.Body.Close()
	status = response.StatusCode
	switch {
	case status >= 500:
		return "", status, "upstream_5xx", readIQErrorSnippet(response.Body), ""
	case status == http.StatusTooManyRequests:
		return "", status, "upstream_429", readIQErrorSnippet(response.Body), ""
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "", status, "auth_401_403", readIQErrorSnippet(response.Body), ""
	case status >= 400:
		return "", status, "upstream_4xx", readIQErrorSnippet(response.Body), ""
	case status >= 300:
		return "", status, "upstream_redirect", "", ""
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, iqMaxResponseBytes+1))
	if err != nil {
		return "", status, iqTransportErrorClass(err), "", ""
	}
	if len(body) > iqMaxResponseBytes {
		return "", status, "response_too_large", "", ""
	}
	answer, echoedModel, err = target.parseAnswer(body)
	if err != nil {
		return "", status, "invalid_response", string(body), ""
	}
	if strings.TrimSpace(answer) == "" {
		return "", status, "empty_answer", string(body), echoedModel
	}
	return answer, status, "none", "", echoedModel
}

// readIQErrorSnippet keeps just enough of a failed response to explain the
// failure; it is bounded and later redacted and markup-stripped.
func readIQErrorSnippet(body io.Reader) string {
	snippet, err := io.ReadAll(io.LimitReader(body, iqErrorSnippetBytes))
	if err != nil {
		return ""
	}
	return string(snippet)
}

func iqTransportErrorClass(err error) string {
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return "timeout"
	}
	var verification *tls.CertificateVerificationError
	var authority x509.UnknownAuthorityError
	var record tls.RecordHeaderError
	if errors.As(err, &verification) || errors.As(err, &authority) || errors.As(err, &record) {
		return "tls"
	}
	return "dns_connect"
}

func iqQuestionHash(q iqQuestion) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(q.ID+"\x00"+q.Prompt+"\x00"+q.Expected+"\x00"+q.Judge)))
}

func selectIQQuestions(n int, runID string) []iqQuestion {
	seed := sha256.Sum256([]byte(IQBankVersion() + ":" + runID))
	random := rand.New(rand.NewSource(int64(binary.LittleEndian.Uint64(seed[:8]))))
	byCategory := make(map[string][]iqQuestion)
	for _, question := range iqBank {
		byCategory[question.Category] = append(byCategory[question.Category], question)
	}
	names := make([]string, 0, len(byCategory))
	for name := range byCategory {
		names = append(names, name)
	}
	sort.Strings(names)
	categories := make([][]iqQuestion, 0, len(names))
	for _, name := range names {
		pool := byCategory[name]
		random.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
		categories = append(categories, pool)
	}
	selected := make([]iqQuestion, 0, n)
	// Each pass allocates one question per available category, in random order.
	for len(selected) < n {
		for _, index := range random.Perm(len(categories)) {
			if len(selected) == n {
				break
			}
			if len(categories[index]) > 0 {
				selected = append(selected, categories[index][0])
				categories[index] = categories[index][1:]
			}
		}
	}
	return selected
}

var iqNumericAnswer = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

func judgeIQAnswer(q iqQuestion, answer string) bool {
	if q.Judge == "strict" {
		return answer == q.Expected
	}
	answer = strings.TrimSpace(answer)
	switch q.Judge {
	case "numeric":
		if len(answer) > 128 || !iqNumericAnswer.MatchString(answer) {
			return false
		}
		if index := strings.IndexAny(answer, "eE"); index >= 0 {
			exponent, err := strconv.ParseInt(answer[index+1:], 10, 32)
			if err != nil || exponent < -308 || exponent > 308 {
				return false
			}
		}
		got, err := strconv.ParseFloat(answer, 64)
		if err != nil || math.IsInf(got, 0) || math.IsNaN(got) {
			return false
		}
		expected, ok := new(big.Rat).SetString(q.Expected)
		if !ok {
			return false
		}
		actual, ok := new(big.Rat).SetString(answer)
		if !ok {
			return false
		}
		if expected.IsInt() && !strings.Contains(q.Expected, ".") {
			return actual.Cmp(expected) == 0
		}
		want, _ := expected.Float64()
		return math.Abs(got-want) <= math.Max(1e-6, math.Abs(want)*1e-6)
	case "exact":
		return strings.TrimRight(answer, "。.!！") == q.Expected
	default:
		return false
	}
}

func safeIQExcerpt(answer string, limit int) string {
	tokenizer := html.NewTokenizer(strings.NewReader(answer))
	var text strings.Builder
	for {
		kind := tokenizer.Next()
		if kind == html.ErrorToken {
			break
		}
		if kind == html.TextToken {
			text.Write(tokenizer.Text())
		}
	}
	clean := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, text.String())
	if len(clean) <= limit {
		return clean
	}
	clean = clean[:limit]
	for !utf8.ValidString(clean) {
		clean = clean[:len(clean)-1]
	}
	return clean
}
