package controller

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/errgroup"
)

const (
	subSiteUserAgent       = "newapi-sub-site-sync/1.0"
	subSiteVerifyTimeout   = 5 * time.Second
	subSiteOverallTimeout  = 12 * time.Second
	subSiteFetchTimeout    = 15 * time.Second
	subSiteGroupsCacheTTL  = 5 * time.Minute
	subSiteMaxResponseSize = 4 << 20 // 4 MiB
	subSiteCreateBatchMax  = 50
)

// ---------- DTO ----------

type subSiteListItem struct {
	Id                    int64  `json:"id"`
	Name                  string `json:"name"`
	BaseURL               string `json:"base_url"`
	UpstreamUserId        int    `json:"upstream_user_id"`
	Enabled               bool   `json:"enabled"`
	TokenSet              bool   `json:"token_set"`
	Note                  string `json:"note"`
	LastVerifiedAt        int64  `json:"last_verified_at"`
	LastVerifiedStatus    string `json:"last_verified_status"`
	LastVerifiedMsg       string `json:"last_verified_msg"`
	LastVerifiedLatencyMS int    `json:"last_verified_latency_ms"`
	LastVerifiedVersion   string `json:"last_verified_version"`
	CreatedTime           int64  `json:"created_time"`
	UpdatedTime           int64  `json:"updated_time"`
}

type subSiteUpsertRequest struct {
	Id             int64  `json:"id"`
	Name           string `json:"name"`
	BaseURL        string `json:"base_url"`
	UpstreamUserId int    `json:"upstream_user_id"`
	Token          string `json:"token"` // 空字符串=保留旧值
	Enabled        *bool  `json:"enabled"`
	Note           string `json:"note"`
}

type subSiteVerifyRequest struct {
	Id             int64  `json:"id"`
	BaseURL        string `json:"base_url"`
	UpstreamUserId int    `json:"upstream_user_id"`
	Token          string `json:"token"`
}

type subSiteVerifyResult struct {
	// Status:
	//   ok                — token 有效，已绑定用户（任意角色，含普通用户）
	//   auth_failed       — token 无效 / 用户 id 不匹配 / 账号被禁
	//   role_insufficient — 仅向后兼容旧 DB 记录与防御性兜底；新版 verify 不再主动产生
	//   network_error     — 连通失败 / 5xx / 超时
	//   unknown           — 200 但响应无法解析
	Status    string `json:"status"`
	Version   string `json:"version,omitempty"`
	// Role: 上游真实角色（1=user / 10=admin / 100=root），verify 成功时写入。
	Role      int    `json:"role,omitempty"`
	LatencyMS int    `json:"latency_ms"`
	Message   string `json:"message,omitempty"`
}

type subSiteGroup struct {
	Group       string             `json:"group"`
	Description string             `json:"description,omitempty"`
	Ratio       float64            `json:"ratio"`
	TierOverrides map[string]float64 `json:"tier_overrides,omitempty"`
	Models      []string           `json:"models"`
}

type subSiteCreateChannelsRequest struct {
	Strategy string `json:"strategy"` // create / overwrite / skip(dry-run)
	Confirm  bool   `json:"confirm"`
	// ProvisionKeys=true 时：每个 group 在上游调 /api/token/ 自动签发一个专属 token，本地 channel.Key 用该 token；
	// false 时：channel.Key 直接复用上游 admin token（旧行为，调试用）。
	ProvisionKeys bool                    `json:"provision_keys"`
	Groups        []subSiteCreateGroupRow `json:"groups"`
}

type subSiteCreateGroupRow struct {
	Group        string            `json:"group"`
	LocalName    string            `json:"local_name"`
	ModelMapping map[string]string `json:"model_mapping,omitempty"`
	Models       []string          `json:"models,omitempty"`
}

type subSiteCreatePlanItem struct {
	Group        string `json:"group"`
	LocalName    string `json:"local_name"`
	Action       string `json:"action"` // will_create / will_overwrite / will_skip
	ChannelId    int    `json:"channel_id,omitempty"`
	Reason       string `json:"reason,omitempty"`
	WillProvKey  bool   `json:"will_provision_key,omitempty"`
}

type subSiteCreateResult struct {
	DryRun  bool                       `json:"dry_run"`
	Plan    []subSiteCreatePlanItem    `json:"plan,omitempty"`
	OK      []subSiteCreateOKItem      `json:"ok,omitempty"`
	Skipped []subSiteCreateSkippedItem `json:"skipped,omitempty"`
	Failed  []subSiteCreateFailedItem  `json:"failed,omitempty"`
}

type subSiteCreateOKItem struct {
	Group           string `json:"group"`
	LocalName       string `json:"local_name"`
	ChannelId       int    `json:"channel_id"`
	Action          string `json:"action"`
	ProvisionedKey  bool   `json:"provisioned_key,omitempty"` // true=该渠道 key 是上游签发的专属 token
	UpstreamTokenId int    `json:"upstream_token_id,omitempty"`
}

type subSiteCreateSkippedItem struct {
	Group     string `json:"group"`
	LocalName string `json:"local_name"`
	Reason    string `json:"reason"`
}

type subSiteCreateFailedItem struct {
	Group     string `json:"group"`
	LocalName string `json:"local_name"`
	Error     string `json:"error"`
}

// ---------- helpers ----------

func toSubSiteListItem(s *model.SubSite) subSiteListItem {
	return subSiteListItem{
		Id:                    s.Id,
		Name:                  s.Name,
		BaseURL:               s.BaseURL,
		UpstreamUserId:        s.UpstreamUserId,
		Enabled:               s.Enabled,
		TokenSet:              s.Token != "",
		Note:                  s.Note,
		LastVerifiedAt:        s.LastVerifiedAt,
		LastVerifiedStatus:    s.LastVerifiedStatus,
		LastVerifiedMsg:       s.LastVerifiedMsg,
		LastVerifiedLatencyMS: s.LastVerifiedLatencyMS,
		LastVerifiedVersion:   s.LastVerifiedVersion,
		CreatedTime:           s.CreatedTime,
		UpdatedTime:           s.UpdatedTime,
	}
}

func newSubSiteHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	tr := &http.Transport{
		MaxIdleConns:          50,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: timeout,
		DialContext:           service.NewSubSiteDialContext(*dialer),
	}
	if common.TLSInsecureSkipVerify {
		tr.TLSClientConfig = common.InsecureTLSConfig
	} else {
		tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return &http.Client{Transport: tr, Timeout: timeout}
}

func subSiteDoJSON(ctx context.Context, client *http.Client, method, url, token string, userId int, body io.Reader) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", subSiteUserAgent)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if userId > 0 {
		req.Header.Set("New-Api-User", strconv.Itoa(userId))
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, subSiteMaxResponseSize)
	data, err := io.ReadAll(limited)
	return resp.StatusCode, data, err
}

func subSiteTrimBaseURL(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), "/")
}

// ---------- T05: List / Upsert / Delete ----------

func ListSubSites(c *gin.Context) {
	list, err := model.GetAllSubSites()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	items := make([]subSiteListItem, 0, len(list))
	for _, s := range list {
		items = append(items, toSubSiteListItem(s))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": items})
}

func UpsertSubSite(c *gin.Context) {
	var req subSiteUpsertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "请求参数格式错误"})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.BaseURL = subSiteTrimBaseURL(req.BaseURL)
	if req.Name == "" || req.BaseURL == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "name 与 base_url 必填"})
		return
	}
	if err := service.ValidateSubSiteBaseURL(req.BaseURL); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	row := &model.SubSite{
		Id:             req.Id,
		Name:           req.Name,
		BaseURL:        req.BaseURL,
		UpstreamUserId: req.UpstreamUserId,
		Enabled:        enabled,
		Note:           strings.TrimSpace(req.Note),
	}
	if strings.TrimSpace(req.Token) != "" {
		ct, err := common.EncryptSecret(strings.TrimSpace(req.Token))
		if err != nil {
			common.ApiError(c, err)
			return
		}
		row.Token = ct
	}
	if err := model.UpsertSubSite(row); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": toSubSiteListItem(row)})
}

func DeleteSubSite(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "id 不合法"})
		return
	}
	if err := model.DeleteSubSite(id); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	subSiteGroupsCacheDel(id)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ---------- T06: Verify ----------

func VerifySubSite(c *gin.Context) {
	var req subSiteVerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "请求参数格式错误"})
		return
	}

	baseURL := subSiteTrimBaseURL(req.BaseURL)
	token := strings.TrimSpace(req.Token)
	upstreamUserId := req.UpstreamUserId
	persistedId := req.Id

	if req.Id != 0 {
		row, err := model.GetSubSiteByID(req.Id)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
			return
		}
		if baseURL == "" {
			baseURL = row.BaseURL
		}
		if upstreamUserId == 0 {
			upstreamUserId = row.UpstreamUserId
		}
		if token == "" {
			plain, err := common.DecryptSecret(row.Token)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{"success": false, "message": "decrypt token failed"})
				return
			}
			token = plain
		}
	}
	if baseURL == "" || token == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "base_url 与 token 必填"})
		return
	}
	if err := service.ValidateSubSiteBaseURL(baseURL); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	result := doSubSiteVerify(c.Request.Context(), baseURL, token, upstreamUserId)
	if persistedId != 0 {
		_ = model.UpdateSubSiteVerifyStatus(persistedId, result.Status, result.Message, result.Version, result.LatencyMS)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

func doSubSiteVerify(ctx context.Context, baseURL, token string, upstreamUserId int) subSiteVerifyResult {
	start := time.Now()
	res := subSiteVerifyResult{Status: "unknown"}

	client := newSubSiteHTTPClient(subSiteVerifyTimeout)
	overallCtx, cancel := context.WithTimeout(ctx, subSiteOverallTimeout)
	defer cancel()

	// Step 1: GET /api/status — 拿 version + 验通连通性。
	statusCode, body, err := subSiteDoJSON(overallCtx, client, http.MethodGet, baseURL+"/api/status", "", 0, nil)
	if err != nil {
		res.Status = "network_error"
		res.Message = err.Error()
		res.LatencyMS = int(time.Since(start) / time.Millisecond)
		return res
	}
	if statusCode != http.StatusOK {
		res.Status = "network_error"
		res.Message = "status " + strconv.Itoa(statusCode)
		res.LatencyMS = int(time.Since(start) / time.Millisecond)
		return res
	}
	var statusResp struct {
		Success bool `json:"success"`
		Data    struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	if err := common.Unmarshal(body, &statusResp); err == nil {
		res.Version = statusResp.Data.Version
	}

	// Step 2: GET /api/user/self -- user-level，仅要求 token 有效 + 绑定到 upstream_user_id。
	// 历史版本曾用 /api/option/（root-only），过度限制——sub_site 的后续动作（拉分组 fallback 到
	// /api/pricing.usable_group / 自动签发 /api/token/）实际都能在 user 级运行，故 verify 也降到 user。
	// newapi 习惯用 HTTP 200 + success=false 表达鉴权失败，所以必须解 JSON 而不能只看状态码。
	statusCode, selfBody, err := subSiteDoJSON(overallCtx, client, http.MethodGet, baseURL+"/api/user/self", token, upstreamUserId, nil)
	if err != nil {
		res.Status = "network_error"
		res.Message = err.Error()
		res.LatencyMS = int(time.Since(start) / time.Millisecond)
		return res
	}
	res.LatencyMS = int(time.Since(start) / time.Millisecond)

	// 优先解出业务 message（401/200+success:false 都可能带），供后续路径透传。
	var selfResp struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			Role int `json:"role"`
		} `json:"data"`
	}
	_ = common.Unmarshal(selfBody, &selfResp)

	switch statusCode {
	case http.StatusUnauthorized:
		res.Status = "auth_failed"
		if selfResp.Message != "" {
			// 透传上游 message：能区分 "token 错" / "user id 不匹配" / "未登录"，便于排查。
			res.Message = selfResp.Message
		} else {
			res.Message = "401 unauthorized"
		}
		return res
	case http.StatusForbidden:
		// access token 路径下 403 = 账号过期；保留 role_insufficient 兼容旧分类，message 透传细节。
		res.Status = "role_insufficient"
		if selfResp.Message != "" {
			res.Message = selfResp.Message
		} else {
			res.Message = "403 forbidden"
		}
		return res
	case http.StatusOK:
		// fallthrough → 业务层判定
	default:
		res.Status = "unknown"
		res.Message = "status " + strconv.Itoa(statusCode)
		return res
	}

	if selfResp.Success {
		res.Status = "ok"
		res.Role = selfResp.Data.Role
		return res
	}
	// 200 + success:false 的防御性兜底：常见为账号被禁 / 用户信息无效。
	// 关键字顺序：先 insufficient/privilege/权限，再 unauthorized——
	// i18n 文案 "Unauthorized, insufficient privileges" 同时含两个关键字，
	// 必须把更具体的"权限不足"放前面，否则被吞成 auth_failed 误导排查。
	msgLower := strings.ToLower(selfResp.Message)
	switch {
	case strings.Contains(msgLower, "insufficient") || strings.Contains(msgLower, "privilege") || strings.Contains(msgLower, "无权") || strings.Contains(msgLower, "权限"):
		res.Status = "role_insufficient"
	case strings.Contains(msgLower, "invalid access token") || strings.Contains(msgLower, "unauthorized"):
		res.Status = "auth_failed"
	default:
		res.Status = "auth_failed"
	}
	res.Message = selfResp.Message
	return res
}

// ---------- T07: GetGroups (3-tier fallback + 5min cache) ----------

type cachedGroups struct {
	exp  time.Time
	data []subSiteGroup
}

var (
	subSiteGroupsCache   = map[int64]cachedGroups{}
	subSiteGroupsCacheMu sync.Mutex
)

func subSiteGroupsCacheGet(id int64) ([]subSiteGroup, bool) {
	subSiteGroupsCacheMu.Lock()
	defer subSiteGroupsCacheMu.Unlock()
	if v, ok := subSiteGroupsCache[id]; ok && time.Now().Before(v.exp) {
		return v.data, true
	}
	return nil, false
}

func subSiteGroupsCacheSet(id int64, data []subSiteGroup) {
	subSiteGroupsCacheMu.Lock()
	defer subSiteGroupsCacheMu.Unlock()
	subSiteGroupsCache[id] = cachedGroups{exp: time.Now().Add(subSiteGroupsCacheTTL), data: data}
}

func subSiteGroupsCacheDel(id int64) {
	subSiteGroupsCacheMu.Lock()
	defer subSiteGroupsCacheMu.Unlock()
	delete(subSiteGroupsCache, id)
}

func GetSubSiteGroups(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "id 不合法"})
		return
	}
	refresh := c.Query("refresh") == "1"
	if !refresh {
		if data, ok := subSiteGroupsCacheGet(id); ok {
			c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"groups": data, "cached": true}})
			return
		}
	}
	site, err := model.GetSubSiteByID(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := service.ValidateSubSiteBaseURL(site.BaseURL); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	token, err := common.DecryptSecret(site.Token)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "decrypt token failed"})
		return
	}
	groups, source, err := fetchSubSiteGroups(c.Request.Context(), site.BaseURL, token, site.UpstreamUserId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	subSiteGroupsCacheSet(id, groups)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"groups": groups, "source": source}})
}

// fetchSubSiteGroups: ① /api/group/groups (admin 全量) → ② /api/pricing.usable_groups → ③ /api/ratio_config
func fetchSubSiteGroups(ctx context.Context, baseURL, token string, upstreamUserId int) ([]subSiteGroup, string, error) {
	client := newSubSiteHTTPClient(subSiteFetchTimeout)
	ctx, cancel := context.WithTimeout(ctx, subSiteFetchTimeout)
	defer cancel()

	type sourceSpec struct {
		name string
		fn   func() ([]subSiteGroup, error)
	}
	var (
		groupsFromGroups  []subSiteGroup
		groupsFromPricing []subSiteGroup
		groupsFromRatio   []subSiteGroup
	)
	sources := []sourceSpec{
		{"group/groups", func() ([]subSiteGroup, error) {
			g, err := fetchSubSiteFromGroups(ctx, client, baseURL, token, upstreamUserId)
			groupsFromGroups = g
			return g, err
		}},
		{"pricing", func() ([]subSiteGroup, error) {
			g, err := fetchSubSiteFromPricing(ctx, client, baseURL, token, upstreamUserId)
			groupsFromPricing = g
			return g, err
		}},
		{"ratio_config", func() ([]subSiteGroup, error) {
			g, err := fetchSubSiteFromRatioConfig(ctx, client, baseURL, token, upstreamUserId)
			groupsFromRatio = g
			return g, err
		}},
	}

	eg, _ := errgroup.WithContext(ctx)
	eg.SetLimit(4)
	for i := range sources {
		s := sources[i]
		eg.Go(func() error {
			if _, err := s.fn(); err != nil {
				logger.LogWarn(ctx, "sub_site groups source "+s.name+" failed: "+err.Error())
			}
			return nil
		})
	}
	_ = eg.Wait()

	// 合并：优先 group/groups（admin 全量），再用 pricing/ratio_config 补 description/models/ratio。
	// 白名单语义：
	//   - admin 路径（groupsFromGroups 非空）：admin 视角看到全量分组，ratio_config 可以补字段也可以新增（覆盖全集）。
	//   - user 路径（groupsFromGroups 为空）：pricing 的 usable_group 是权威白名单，
	//     ratio_config 是公开无认证端点（含全局所有分组的 ratio），不能用它新增 group，
	//     否则会把用户不可见的分组泄漏到同步候选列表。
	merged := map[string]*subSiteGroup{}
	primary := groupsFromGroups
	if len(primary) == 0 {
		primary = groupsFromPricing
	}
	if len(primary) == 0 {
		primary = groupsFromRatio
	}
	for i := range primary {
		gp := primary[i]
		merged[gp.Group] = &gp
	}
	for _, gp := range groupsFromPricing {
		if cur, ok := merged[gp.Group]; ok {
			if cur.Ratio == 0 && gp.Ratio != 0 {
				cur.Ratio = gp.Ratio
			}
			if cur.Description == "" {
				cur.Description = gp.Description
			}
			if len(cur.Models) == 0 {
				cur.Models = gp.Models
			}
		} else {
			merged[gp.Group] = &subSiteGroup{Group: gp.Group, Description: gp.Description, Ratio: gp.Ratio, Models: gp.Models}
		}
	}
	// user 路径下 ratio_config 严格只补字段，不能新增 group。
	ratioConfigCanAddGroup := len(groupsFromGroups) > 0
	for _, gp := range groupsFromRatio {
		if cur, ok := merged[gp.Group]; ok {
			if cur.Ratio == 0 && gp.Ratio != 0 {
				cur.Ratio = gp.Ratio
			}
			if len(cur.TierOverrides) == 0 {
				cur.TierOverrides = gp.TierOverrides
			}
		} else if ratioConfigCanAddGroup {
			merged[gp.Group] = &subSiteGroup{Group: gp.Group, Ratio: gp.Ratio, TierOverrides: gp.TierOverrides}
		}
		// user 路径下 else 分支跳过：不让公开 ratio_config 把白名单外的分组带进候选。
	}

	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]subSiteGroup, 0, len(keys))
	for _, k := range keys {
		gp := merged[k]
		// nil slice 会序列化成 JSON null，前端拿到后 `.length` 会 crash；统一为空数组。
		if gp.Models == nil {
			gp.Models = []string{}
		}
		sort.Strings(gp.Models)
		out = append(out, *gp)
	}
	if len(out) == 0 {
		return nil, "", errors.New("上游未返回任何分组（请检查 token 权限或上游版本）")
	}
	sourceName := ""
	switch {
	case len(groupsFromGroups) > 0:
		sourceName = "group/groups"
	case len(groupsFromPricing) > 0:
		sourceName = "pricing"
	default:
		sourceName = "ratio_config"
	}
	return out, sourceName, nil
}

func fetchSubSiteFromGroups(ctx context.Context, client *http.Client, baseURL, token string, upstreamUserId int) ([]subSiteGroup, error) {
	// admin 全量分组端点：GET /api/group/  返回 {success,data:[{name,ratio,desc?}]}
	status, body, err := subSiteDoJSON(ctx, client, http.MethodGet, baseURL+"/api/group/", token, upstreamUserId, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("status %d", status)
	}
	// 兼容两种 data 形态：字符串列表 / 对象列表
	var asStrings struct {
		Success bool     `json:"success"`
		Data    []string `json:"data"`
	}
	if err := common.Unmarshal(body, &asStrings); err == nil && asStrings.Success && len(asStrings.Data) > 0 {
		out := make([]subSiteGroup, 0, len(asStrings.Data))
		for _, g := range asStrings.Data {
			out = append(out, subSiteGroup{Group: g})
		}
		return out, nil
	}
	var asObjects struct {
		Success bool `json:"success"`
		Data    []struct {
			Name        string  `json:"name"`
			Description string  `json:"description"`
			Desc        string  `json:"desc"`
			Ratio       float64 `json:"ratio"`
		} `json:"data"`
	}
	if err := common.Unmarshal(body, &asObjects); err == nil && asObjects.Success {
		out := make([]subSiteGroup, 0, len(asObjects.Data))
		for _, g := range asObjects.Data {
			desc := g.Description
			if desc == "" {
				desc = g.Desc
			}
			out = append(out, subSiteGroup{Group: g.Name, Description: desc, Ratio: g.Ratio})
		}
		return out, nil
	}
	return nil, errors.New("unrecognized /api/group/ response")
}

func fetchSubSiteFromPricing(ctx context.Context, client *http.Client, baseURL, token string, upstreamUserId int) ([]subSiteGroup, error) {
	status, body, err := subSiteDoJSON(ctx, client, http.MethodGet, baseURL+"/api/pricing", token, upstreamUserId, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("status %d", status)
	}
	// 上游 /api/pricing.usable_group 实际是 map[string]string（group -> 描述）。
	// 部分 fork 多了 group_ratio map[string]float64 字段，做兼容尝试。
	var resp struct {
		Success bool `json:"success"`
		Data    []struct {
			ModelName    string   `json:"model_name"`
			EnableGroups []string `json:"enable_groups"`
		} `json:"data"`
		UsableGroup map[string]string  `json:"usable_group"`
		GroupRatio  map[string]float64 `json:"group_ratio"`
	}
	if err := common.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, errors.New("upstream /api/pricing returned success=false")
	}
	// 反查 group->models
	groupModels := map[string]map[string]struct{}{}
	for _, m := range resp.Data {
		for _, g := range m.EnableGroups {
			if groupModels[g] == nil {
				groupModels[g] = map[string]struct{}{}
			}
			groupModels[g][m.ModelName] = struct{}{}
		}
	}
	// keys 来源：usable_group 是上游已过滤的"用户可用"权威白名单，作为唯一可信源。
	// data[].enable_groups 字段未被上游过滤（model 的 group 白名单仍含 user 不可见的 group 名），
	// 若用它撑大 keys，会把用户没权限的分组泄漏进同步候选列表。
	// 仅当 usable_group 缺失（个别 fork 行为）才退化用 groupModels 全集。
	keys := map[string]struct{}{}
	for g := range resp.UsableGroup {
		keys[g] = struct{}{}
	}
	if len(keys) == 0 {
		for g := range groupModels {
			keys[g] = struct{}{}
		}
	}
	out := make([]subSiteGroup, 0, len(keys))
	for g := range keys {
		gp := subSiteGroup{Group: g}
		if desc, ok := resp.UsableGroup[g]; ok {
			gp.Description = desc
		}
		if r, ok := resp.GroupRatio[g]; ok {
			gp.Ratio = r
		}
		if ms, ok := groupModels[g]; ok {
			arr := make([]string, 0, len(ms))
			for m := range ms {
				arr = append(arr, m)
			}
			sort.Strings(arr)
			gp.Models = arr
		}
		out = append(out, gp)
	}
	return out, nil
}

func fetchSubSiteFromRatioConfig(ctx context.Context, client *http.Client, baseURL, token string, upstreamUserId int) ([]subSiteGroup, error) {
	status, body, err := subSiteDoJSON(ctx, client, http.MethodGet, baseURL+"/api/ratio_config", token, upstreamUserId, nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("status %d", status)
	}
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			GroupRatio      map[string]float64            `json:"group_ratio"`
			GroupGroupRatio map[string]map[string]float64 `json:"group_group_ratio"`
		} `json:"data"`
	}
	if err := common.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, errors.New("upstream /api/ratio_config returned success=false")
	}
	out := make([]subSiteGroup, 0, len(resp.Data.GroupRatio))
	for g, r := range resp.Data.GroupRatio {
		gp := subSiteGroup{Group: g, Ratio: r}
		if tiers, ok := resp.Data.GroupGroupRatio[g]; ok && len(tiers) > 0 {
			gp.TierOverrides = tiers
		}
		out = append(out, gp)
	}
	return out, nil
}

// ---------- T08: CreateChannels ----------

func CreateSubSiteChannels(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "id 不合法"})
		return
	}
	var req subSiteCreateChannelsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "请求参数格式错误"})
		return
	}
	if len(req.Groups) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "至少选择一个分组"})
		return
	}
	if len(req.Groups) > subSiteCreateBatchMax {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": fmt.Sprintf("单次最多 %d 条", subSiteCreateBatchMax)})
		return
	}
	strategy := strings.ToLower(strings.TrimSpace(req.Strategy))
	switch strategy {
	case "create", "overwrite", "skip":
	default:
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "strategy 仅支持 create/overwrite/skip"})
		return
	}
	if strategy == "overwrite" && !req.Confirm {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "overwrite 需 confirm=true"})
		return
	}

	site, err := model.GetSubSiteByID(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	token, err := common.DecryptSecret(site.Token)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "decrypt token failed"})
		return
	}

	result := subSiteCreateResult{DryRun: strategy == "skip"}

	provisionClient := newSubSiteHTTPClient(subSiteFetchTimeout)

	for _, g := range req.Groups {
		g.Group = strings.TrimSpace(g.Group)
		g.LocalName = strings.TrimSpace(g.LocalName)
		if g.Group == "" || g.LocalName == "" {
			result.Failed = append(result.Failed, subSiteCreateFailedItem{Group: g.Group, LocalName: g.LocalName, Error: "group 与 local_name 必填"})
			continue
		}

		existingId := findExistingChannelId(site.BaseURL, g.Group, g.LocalName)

		switch strategy {
		case "skip":
			action := "will_create"
			if existingId > 0 {
				action = "will_overwrite"
			}
			result.Plan = append(result.Plan, subSiteCreatePlanItem{
				Group: g.Group, LocalName: g.LocalName, Action: action, ChannelId: existingId,
				WillProvKey: req.ProvisionKeys,
			})
		case "create":
			if existingId > 0 {
				result.Skipped = append(result.Skipped, subSiteCreateSkippedItem{Group: g.Group, LocalName: g.LocalName, Reason: "本地已有同 (base_url, group) 渠道"})
				continue
			}
			channelKey, tokenId, err := resolveSubSiteChannelKey(c.Request.Context(), provisionClient, site, token, g, req.ProvisionKeys)
			if err != nil {
				result.Failed = append(result.Failed, subSiteCreateFailedItem{Group: g.Group, LocalName: g.LocalName, Error: err.Error()})
				continue
			}
			ch, err := buildSubSiteChannel(site, channelKey, g, 0)
			if err != nil {
				result.Failed = append(result.Failed, subSiteCreateFailedItem{Group: g.Group, LocalName: g.LocalName, Error: err.Error()})
				continue
			}
			// 单条 Create 让 GORM 回填 id 到 ch.Id。
			if err := model.DB.Create(ch).Error; err != nil {
				result.Failed = append(result.Failed, subSiteCreateFailedItem{Group: g.Group, LocalName: g.LocalName, Error: err.Error()})
				continue
			}
			result.OK = append(result.OK, subSiteCreateOKItem{
				Group: g.Group, LocalName: g.LocalName, ChannelId: ch.Id, Action: "create",
				ProvisionedKey: req.ProvisionKeys, UpstreamTokenId: tokenId,
			})
		case "overwrite":
			channelKey, tokenId, err := resolveSubSiteChannelKey(c.Request.Context(), provisionClient, site, token, g, req.ProvisionKeys)
			if err != nil {
				result.Failed = append(result.Failed, subSiteCreateFailedItem{Group: g.Group, LocalName: g.LocalName, Error: err.Error()})
				continue
			}
			ch, err := buildSubSiteChannel(site, channelKey, g, existingId)
			if err != nil {
				result.Failed = append(result.Failed, subSiteCreateFailedItem{Group: g.Group, LocalName: g.LocalName, Error: err.Error()})
				continue
			}
			if existingId > 0 {
				ch.Id = existingId
				if err := model.DB.Save(ch).Error; err != nil {
					result.Failed = append(result.Failed, subSiteCreateFailedItem{Group: g.Group, LocalName: g.LocalName, Error: err.Error()})
					continue
				}
				result.OK = append(result.OK, subSiteCreateOKItem{
					Group: g.Group, LocalName: g.LocalName, ChannelId: ch.Id, Action: "overwrite",
					ProvisionedKey: req.ProvisionKeys, UpstreamTokenId: tokenId,
				})
			} else {
				if err := model.DB.Create(ch).Error; err != nil {
					result.Failed = append(result.Failed, subSiteCreateFailedItem{Group: g.Group, LocalName: g.LocalName, Error: err.Error()})
					continue
				}
				result.OK = append(result.OK, subSiteCreateOKItem{
					Group: g.Group, LocalName: g.LocalName, ChannelId: ch.Id, Action: "create",
					ProvisionedKey: req.ProvisionKeys, UpstreamTokenId: tokenId,
				})
			}
		}
	}

	if !result.DryRun && len(result.OK) > 0 {
		service.ResetProxyClientCache()
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// resolveSubSiteChannelKey 根据 ProvisionKeys 开关决定 channel.Key 的来源：
//   - 关：直接返回 admin token；
//   - 开：在上游签发一个专属 token，返回 "sk-" + 明文 key + 上游 token id。
func resolveSubSiteChannelKey(ctx context.Context, client *http.Client, site *model.SubSite, adminToken string, g subSiteCreateGroupRow, provision bool) (string, int, error) {
	if !provision {
		return adminToken, 0, nil
	}
	rawKey, tokenId, err := provisionUpstreamToken(ctx, client, site.BaseURL, adminToken, site.UpstreamUserId, g.Group, g.LocalName)
	if err != nil {
		return "", 0, err
	}
	return "sk-" + rawKey, tokenId, nil
}

// provisionUpstreamToken 在上游 newapi 站点签发一个专属于 (group, localName) 的 token。
//
// newapi 的 token 创建是三步走：
//  1. POST /api/token/ — 返回 {success:true}，但 **不带 id 与 key**；
//  2. GET  /api/token/ — 按 id desc 列表，按 name 精确匹配拿 id；
//  3. POST /api/token/:id/key — 拿明文 key（普通 GET 列表返回的是 masked）。
//
// token name 加 8 字节随机后缀 ⇒ 避免并发竞争（同时刻多客户端签发）。
func provisionUpstreamToken(ctx context.Context, client *http.Client, baseURL, adminToken string, upstreamUserId int, group, localName string) (string, int, error) {
	suffix := common.GetRandomString(8)
	tokenName := fmt.Sprintf("sub2sync-%s-%s", localName, suffix)
	if len(tokenName) > 50 {
		// upstream Name 列上限 50；超长 truncate
		tokenName = tokenName[:50]
	}

	// step 1: POST /api/token/ 创建
	createBody, err := newJSONReader(map[string]any{
		"name":                 tokenName,
		"group":                group,
		"unlimited_quota":      true,
		"expired_time":         -1,
		"remain_quota":         0,
		"model_limits_enabled": false,
	})
	if err != nil {
		return "", 0, err
	}
	status, respBody, err := subSiteDoJSON(ctx, client, http.MethodPost, baseURL+"/api/token/", adminToken, upstreamUserId, createBody)
	if err != nil {
		return "", 0, fmt.Errorf("create upstream token: %w", err)
	}
	if status != http.StatusOK {
		return "", 0, fmt.Errorf("create upstream token: HTTP %d", status)
	}
	var createResp struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := common.Unmarshal(respBody, &createResp); err != nil {
		return "", 0, fmt.Errorf("create upstream token: parse: %w", err)
	}
	if !createResp.Success {
		return "", 0, errors.New("create upstream token: " + createResp.Message)
	}

	// step 2: GET /api/token/?p=0&size=20 ，按 name 找新建的 id
	status, respBody, err = subSiteDoJSON(ctx, client, http.MethodGet, baseURL+"/api/token/?p=0&size=20", adminToken, upstreamUserId, nil)
	if err != nil {
		return "", 0, fmt.Errorf("list upstream tokens: %w", err)
	}
	if status != http.StatusOK {
		return "", 0, fmt.Errorf("list upstream tokens: HTTP %d", status)
	}
	var listResp struct {
		Success bool `json:"success"`
		Data    struct {
			Items []struct {
				Id   int    `json:"id"`
				Name string `json:"name"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := common.Unmarshal(respBody, &listResp); err != nil {
		return "", 0, fmt.Errorf("list upstream tokens: parse: %w", err)
	}
	if !listResp.Success {
		return "", 0, errors.New("list upstream tokens: success=false")
	}
	tokenId := 0
	for _, t := range listResp.Data.Items {
		if t.Name == tokenName {
			tokenId = t.Id
			break
		}
	}
	if tokenId == 0 {
		return "", 0, fmt.Errorf("provisioned token %q not found in upstream list (size=20)", tokenName)
	}

	// step 3: POST /api/token/:id/key 拿明文
	keyURL := fmt.Sprintf("%s/api/token/%d/key", baseURL, tokenId)
	status, respBody, err = subSiteDoJSON(ctx, client, http.MethodPost, keyURL, adminToken, upstreamUserId, nil)
	if err != nil {
		return "", 0, fmt.Errorf("get upstream token key: %w", err)
	}
	if status != http.StatusOK {
		return "", 0, fmt.Errorf("get upstream token key: HTTP %d", status)
	}
	var keyResp struct {
		Success bool `json:"success"`
		Data    struct {
			Key string `json:"key"`
		} `json:"data"`
	}
	if err := common.Unmarshal(respBody, &keyResp); err != nil {
		return "", 0, fmt.Errorf("get upstream token key: parse: %w", err)
	}
	if !keyResp.Success || keyResp.Data.Key == "" {
		return "", 0, errors.New("get upstream token key: empty")
	}
	return keyResp.Data.Key, tokenId, nil
}

// findExistingChannelId 按 (base_url, group) 联合判定本地是否已有渠道。
// group 列在 PG 是保留字，必须走 model.GroupColumnRef() 取正确的列引用。
func findExistingChannelId(baseURL, group, _ string) int {
	if baseURL == "" || group == "" {
		return 0
	}
	var id int
	err := model.DB.Model(&model.Channel{}).
		Where("base_url = ? AND "+model.GroupColumnRef()+" = ?", baseURL, group).
		Limit(1).
		Select("id").Scan(&id).Error
	if err != nil {
		return 0
	}
	return id
}

func buildSubSiteChannel(site *model.SubSite, token string, g subSiteCreateGroupRow, existingId int) (*model.Channel, error) {
	if len(g.Models) == 0 {
		return nil, errors.New("models is required")
	}
	statusDisabled := common.ChannelStatusManuallyDisabled
	wZero := uint(0)
	pZero := int64(0)
	baseURL := site.BaseURL
	ch := &model.Channel{
		Type:        1, // OpenAI compatible，MVP 阶段仅支持
		Name:        g.LocalName,
		Key:         token,
		Status:      statusDisabled,
		Weight:      &wZero,
		Priority:    &pZero,
		Group:       g.Group,
		BaseURL:     &baseURL,
		Models:      strings.Join(g.Models, ","),
		CreatedTime: common.GetTimestamp(),
	}
	if existingId > 0 {
		ch.Id = existingId
	}
	if len(g.ModelMapping) > 0 {
		b, err := common.Marshal(g.ModelMapping)
		if err != nil {
			return nil, err
		}
		s := string(b)
		ch.ModelMapping = &s
	}
	remark := fmt.Sprintf("synced from sub_site #%d (%s) at %s", site.Id, site.Name, time.Now().Format(time.RFC3339))
	ch.Remark = &remark
	return ch, nil
}

// 通过 io.Reader 拼 JSON body。
func newJSONReader(v any) (io.Reader, error) {
	b, err := common.Marshal(v)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(b), nil
}
