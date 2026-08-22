package controller

// 对账 tab 上游账单：从 balance 面板拉取上游侧当日实际扣费并绑定到渠道行。
//
// 绑定链路（全自动，零渠道配置）：
//  1. 渠道 base_url 与面板账号 baseUrl 按注册域归并 → 渠道↔上游账号（站点）；
//  2. 对每个命中站点的账号调面板消耗明细接口（window=today|yesterday）拿
//     按令牌(keyname) 扣费（USD，已含充值比例；newapi/donehub 系支持，sub2api/custom 无令牌维度）；
//  3. 令牌名与渠道名做域内包含匹配（不区分大小写，多候选取最长），
//     绑定成功的渠道行显示"该渠道上游令牌当日扣费"，与本站费用并排对账。
// 同一令牌被多渠道共享时各行显示同值并标 ×N；未匹配/不支持显示 —。
//
// 通过运行时配置启用：ReconcileBalancePanelBaseURL + ReconcileBalancePanelToken
//（面板侧只读服务账号令牌，仅 recon.view 权限）；未配置时返回 configured=false，前端整体隐藏。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/errgroup"
)

type UpstreamBillAccount struct {
	Name               string  `json:"name"`
	Platform           string  `json:"platform"`
	Success            bool    `json:"success"`
	Yesterday          float64 `json:"yesterday"`
	Today              float64 `json:"today"`
	YesterdayEstimated bool    `json:"yesterday_estimated"`
	Error              string  `json:"error,omitempty"`
}

// UpstreamChannelBill 渠道绑定的上游令牌当日扣费。
type UpstreamChannelBill struct {
	Keyname string  `json:"keyname"`
	Amount  float64 `json:"amount"`
	Account string  `json:"account"`
	Shared  int     `json:"shared"`        // 共享该令牌的渠道数（>1 时各行同值，勿重复加总）
	Via     string  `json:"via,omitempty"` // key=密钥指纹精确匹配 | name=名称归一化匹配
}

type UpstreamBillResponse struct {
	Configured     bool                        `json:"configured"`
	Day            string                      `json:"day,omitempty"`      // today | yesterday
	DayDate        string                      `json:"day_date,omitempty"` // 面板侧该窗口的 UTC+8 日期
	AsOf           string                      `json:"as_of,omitempty"`
	Timezone       string                      `json:"timezone,omitempty"`
	YesterdayDate  string                      `json:"yesterday_date,omitempty"`
	TodayDate      string                      `json:"today_date,omitempty"`
	Accounts       []UpstreamBillAccount       `json:"accounts,omitempty"`
	ChannelBills   map[int]UpstreamChannelBill `json:"channel_bills,omitempty"`
	DetailFailed   []string                    `json:"detail_failed,omitempty"` // 按令牌明细获取失败的账号
	TotalYesterday float64                     `json:"total_yesterday"`
	TotalToday     float64                     `json:"total_today"`
	GeneratedAt    int64                       `json:"generated_at"`
}

// 面板对账/明细接口会实时查询全部上游账号，短缓存避免反复刷新打满上游
// （面板侧另有 reconciliation 实时 + detail 5min 缓存）。
const upstreamBillCacheTTL = 120 * time.Second
const upstreamBillRequestTimeout = 60 * time.Second
const upstreamBillDetailTimeout = 45 * time.Second

// 明细阶段整体止损：超过后未完成的账号计入 detail_failed，保证接口总耗时 CF 可承受。
const upstreamBillDetailBudget = 80 * time.Second
const upstreamBillDetailConcurrency = 6

// 归一化后令牌名短于该长度不参与绑定，避免 "ai2" 之类过短名误命中大量渠道。
const upstreamBillMinKeynameLen = 5

var (
	upstreamBillMu       sync.Mutex
	upstreamBillCache    = map[string]*UpstreamBillResponse{}
	upstreamBillCachedAt = map[string]time.Time{}
)

func GetChannelReconcileUpstreamBill(c *gin.Context) {
	common.OptionMapRWMutex.RLock()
	baseURL := strings.TrimSuffix(strings.TrimSpace(common.OptionMap["ReconcileBalancePanelBaseURL"]), "/")
	token := strings.TrimSpace(common.OptionMap["ReconcileBalancePanelToken"])
	common.OptionMapRWMutex.RUnlock()

	if baseURL == "" || token == "" {
		common.ApiSuccess(c, &UpstreamBillResponse{Configured: false, GeneratedAt: common.GetTimestamp()})
		return
	}

	day := c.DefaultQuery("day", "today")
	if day != "today" && day != "yesterday" {
		common.ApiErrorMsg(c, "invalid day, require today or yesterday")
		return
	}

	upstreamBillMu.Lock()
	defer upstreamBillMu.Unlock()
	if cached, ok := upstreamBillCache[day]; ok && time.Since(upstreamBillCachedAt[day]) < upstreamBillCacheTTL {
		common.ApiSuccess(c, cached)
		return
	}

	resp, err := fetchUpstreamBill(c.Request.Context(), baseURL, token, day)
	if err != nil {
		common.ApiErrorMsg(c, fmt.Sprintf("balance panel: %s", err.Error()))
		return
	}
	upstreamBillCache[day] = resp
	upstreamBillCachedAt[day] = time.Now()
	common.ApiSuccess(c, resp)
}

type upstreamBillAccountRef struct {
	Id       string
	Name     string
	Platform string
}

func fetchUpstreamBill(ctx context.Context, baseURL, token, day string) (*UpstreamBillResponse, error) {
	body, err := upstreamBillGet(ctx, baseURL+"/api/balance/reconciliation", token, upstreamBillRequestTimeout)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		Success       bool   `json:"success"`
		Error         string `json:"error"`
		AsOf          string `json:"asOf"`
		Timezone      string `json:"timezone"`
		YesterdayDate string `json:"yesterdayDate"`
		TodayDate     string `json:"todayDate"`
		Accounts      []struct {
			AccountId          string  `json:"accountId"`
			Name               string  `json:"name"`
			Platform           string  `json:"platform"`
			Success            bool    `json:"success"`
			Yesterday          float64 `json:"yesterday"`
			Today              float64 `json:"today"`
			YesterdayEstimated bool    `json:"yesterdayEstimated"`
			Error              string  `json:"error"`
		} `json:"accounts"`
		Totals struct {
			Yesterday float64 `json:"yesterday"`
			Today     float64 `json:"today"`
		} `json:"totals"`
	}
	if err := common.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("invalid response: %s", truncateBillBody(body))
	}
	if !parsed.Success {
		return nil, fmt.Errorf("panel error: %s", truncateBillBody(body))
	}

	// 账号 id → 注册域（面板 /api/accounts 才有 baseUrl，对账响应里没有）。
	accountDomains, err := fetchUpstreamBillAccountDomains(ctx, baseURL, token)
	if err != nil {
		return nil, fmt.Errorf("accounts: %s", err.Error())
	}
	channels, err := model.GetChannelBillSources(ctx)
	if err != nil {
		return nil, err
	}

	out := &UpstreamBillResponse{
		Configured:     true,
		Day:            day,
		AsOf:           parsed.AsOf,
		Timezone:       parsed.Timezone,
		YesterdayDate:  parsed.YesterdayDate,
		TodayDate:      parsed.TodayDate,
		TotalYesterday: parsed.Totals.Yesterday,
		TotalToday:     parsed.Totals.Today,
		GeneratedAt:    common.GetTimestamp(),
	}
	if day == "today" {
		out.DayDate = parsed.TodayDate
	} else {
		out.DayDate = parsed.YesterdayDate
	}

	// 站点（注册域）→ 渠道 / 账号
	chansByDomain := map[string][]model.ChannelBillSource{}
	for _, ch := range channels {
		domain := upstreamBillDomain(ch.BaseUrl)
		if domain != "" {
			chansByDomain[domain] = append(chansByDomain[domain], ch)
		}
	}
	accountsByDomain := map[string][]upstreamBillAccountRef{}
	for _, a := range parsed.Accounts {
		out.Accounts = append(out.Accounts, UpstreamBillAccount{
			Name:               a.Name,
			Platform:           a.Platform,
			Success:            a.Success,
			Yesterday:          a.Yesterday,
			Today:              a.Today,
			YesterdayEstimated: a.YesterdayEstimated,
			Error:              a.Error,
		})
		domain := accountDomains[a.AccountId]
		if domain == "" || len(chansByDomain[domain]) == 0 {
			continue
		}
		// sub2api/custom 无令牌维度，明细必失败，直接跳过不打面板。
		if a.Platform != "newapi" && a.Platform != "donehub" {
			continue
		}
		accountsByDomain[domain] = append(accountsByDomain[domain], upstreamBillAccountRef{
			Id: a.AccountId, Name: a.Name, Platform: a.Platform,
		})
	}

	// 拉各账号按令牌明细（面板 detail 5min 缓存 + 在途去重，冷缓存整体 80s 止损）。
	type keynameCost struct {
		Keyname string
		Amount  float64
		Account string
	}
	detailCtx, cancel := context.WithTimeout(ctx, upstreamBillDetailBudget)
	defer cancel()
	var detailMu sync.Mutex
	keynamesByDomain := map[string]map[string]*keynameCost{} // domain → norm(keyname) → cost
	keyShaByDomain := map[string]map[string]string{}         // domain → sha256(去sk-前缀key) → norm(keyname)
	var eg errgroup.Group
	eg.SetLimit(upstreamBillDetailConcurrency)
	for domain, accs := range accountsByDomain {
		for _, acc := range accs {
			domain, acc := domain, acc
			eg.Go(func() error {
				items, fetchErr := fetchUpstreamBillTokenCosts(detailCtx, baseURL, token, acc.Id, day)
				detailMu.Lock()
				defer detailMu.Unlock()
				if fetchErr != nil {
					out.DetailFailed = append(out.DetailFailed, acc.Name)
					return nil
				}
				m, ok := keynamesByDomain[domain]
				if !ok {
					m = map[string]*keynameCost{}
					keynamesByDomain[domain] = m
				}
				sm, ok := keyShaByDomain[domain]
				if !ok {
					sm = map[string]string{}
					keyShaByDomain[domain] = sm
				}
				for _, it := range items {
					norm := upstreamBillNormalize(it.Name)
					if norm == "" {
						continue
					}
					if cur, exists := m[norm]; exists {
						cur.Amount += it.Amount // 同站多账号同名令牌：合计
					} else {
						m[norm] = &keynameCost{Keyname: it.Name, Amount: it.Amount, Account: acc.Name}
					}
					for _, sha := range it.KeyShas {
						sm[strings.ToLower(sha)] = norm
					}
				}
				return nil
			})
		}
	}
	_ = eg.Wait()
	sort.Strings(out.DetailFailed)

	// 域内绑定：密钥指纹精确匹配优先；否则归一化（小写、去分隔符）名称包含匹配，多候选取最长。
	// 归一化让 "#ai-wave-Claude官转-25" 命中令牌 "Claude-官转-25" 这类分隔差异；
	// 过短令牌名（归一化 <5 字符）不参与，避免 "ai2" 之类误绑。
	out.ChannelBills = map[int]UpstreamChannelBill{}
	sharedCount := map[string]int{} // domain+norm(keyname) → 绑定渠道数
	type binding struct {
		channelId int
		domain    string
		norm      string
		via       string
	}
	var bindings []binding
	for domain, chans := range chansByDomain {
		keynames := keynamesByDomain[domain]
		if len(keynames) == 0 {
			continue
		}
		shaIndex := keyShaByDomain[domain]
		for _, ch := range chans {
			best := ""
			via := ""
			if norm, ok := shaIndex[channelKeySha(ch.Key)]; ok {
				best, via = norm, "key"
			} else {
				nameNorm := upstreamBillNormalize(ch.Name)
				for norm := range keynames {
					if len(norm) >= upstreamBillMinKeynameLen &&
						strings.Contains(nameNorm, norm) && len(norm) > len(best) {
						best = norm
					}
				}
				via = "name"
			}
			if best == "" {
				continue
			}
			bindings = append(bindings, binding{channelId: ch.Id, domain: domain, norm: best, via: via})
			sharedCount[domain+"\x00"+best]++
		}
	}
	for _, b := range bindings {
		kc := keynamesByDomain[b.domain][b.norm]
		out.ChannelBills[b.channelId] = UpstreamChannelBill{
			Keyname: kc.Keyname,
			Amount:  kc.Amount,
			Account: kc.Account,
			Shared:  sharedCount[b.domain+"\x00"+b.norm],
			Via:     b.via,
		}
	}
	return out, nil
}

type upstreamBillTokenCost struct {
	Name    string
	Amount  float64
	KeyShas []string
}

// fetchUpstreamBillTokenCosts 面板消耗明细 → 该账号窗口内按令牌扣费（USD）+ key 指纹。
// 跳过残差行（已删除/未匹配令牌）；byToken 不可用视为失败。
func fetchUpstreamBillTokenCosts(ctx context.Context, baseURL, token, accountId, day string) ([]upstreamBillTokenCost, error) {
	u := fmt.Sprintf("%s/api/balance/reconciliation/%s/detail?window=%s", baseURL, url.PathEscape(accountId), day)
	body, err := upstreamBillGet(ctx, u, token, upstreamBillDetailTimeout)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Success  bool `json:"success"`
		Sections struct {
			ByToken struct {
				Available bool   `json:"available"`
				Error     string `json:"error"`
				Items     []struct {
					Name     string   `json:"name"`
					Amount   float64  `json:"amount"`
					Residual bool     `json:"residual"`
					KeyShas  []string `json:"key_shas"`
				} `json:"items"`
			} `json:"byToken"`
		} `json:"sections"`
	}
	if err := common.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("invalid response: %s", truncateBillBody(body))
	}
	if !parsed.Success || !parsed.Sections.ByToken.Available {
		return nil, fmt.Errorf("byToken unavailable: %s", parsed.Sections.ByToken.Error)
	}
	out := make([]upstreamBillTokenCost, 0, len(parsed.Sections.ByToken.Items))
	for _, it := range parsed.Sections.ByToken.Items {
		if it.Residual || it.Name == "" {
			continue
		}
		out = append(out, upstreamBillTokenCost{Name: it.Name, Amount: it.Amount, KeyShas: it.KeyShas})
	}
	return out, nil
}

// fetchUpstreamBillAccountDomains 面板账号列表（apiKey 已由面板脱敏）→ id → 注册域。
func fetchUpstreamBillAccountDomains(ctx context.Context, baseURL, token string) (map[string]string, error) {
	body, err := upstreamBillGet(ctx, baseURL+"/api/accounts", token, upstreamBillRequestTimeout)
	if err != nil {
		return nil, err
	}
	var accounts []struct {
		Id      string `json:"id"`
		BaseUrl string `json:"baseUrl"`
	}
	if err := common.Unmarshal(body, &accounts); err != nil {
		return nil, fmt.Errorf("invalid response: %s", truncateBillBody(body))
	}
	out := make(map[string]string, len(accounts))
	for _, a := range accounts {
		out[a.Id] = upstreamBillDomain(a.BaseUrl)
	}
	return out, nil
}

func upstreamBillGet(ctx context.Context, fullURL, token string, timeout time.Duration) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, truncateBillBody(body))
	}
	return body, nil
}

// upstreamBillDomain URL → 注册域（末两级标签）。IP / 无法解析 → 空（不参与映射）。
func upstreamBillDomain(rawURL string) string {
	u := strings.TrimSpace(rawURL)
	if u == "" {
		return ""
	}
	if !strings.Contains(u, "://") {
		u = "https://" + u
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	host := parsed.Hostname()
	if host == "" || net.ParseIP(host) != nil {
		return ""
	}
	labels := strings.Split(host, ".")
	if len(labels) <= 2 {
		return host
	}
	return strings.Join(labels[len(labels)-2:], ".")
}

// upstreamBillNormalize 匹配用归一化：小写 + 仅保留字母/数字（含中文等 unicode 字母），
// 抹平 "-"、"_"、空格等分隔差异。
func upstreamBillNormalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// channelKeySha 渠道密钥指纹：取首行（多 key 渠道）、去 sk- 前缀后 sha256，
// 与面板侧 tokenKeySha / 上游 key_hash 同口径。
func channelKeySha(key string) string {
	line := key
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "sk-")
	if line == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(line))
	return hex.EncodeToString(sum[:])
}

func truncateBillBody(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
