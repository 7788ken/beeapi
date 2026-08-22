package service

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/backgroundtask"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/price_notify_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"gorm.io/gorm"
)

// 价格变动发布与通知。设计见 Docs/price-change-notify-design.md 第 4/7/9 节。

var ErrNoPendingPriceChanges = errors.New("没有待发布的价格变动")

// PriceGroupAll 渠道分组通配值，与 controller/pricing.go filterPricingByUsableGroups 的 "all" 语义同源。
const PriceGroupAll = "all"

var (
	pricePublishMu       sync.Mutex   // 串行化发布，防并发重复批次
	priceEmailJobRunning sync.Map     // batchId -> struct{}，防同批次并发发送
	priceChangesCacheMu  sync.RWMutex // 用户侧 30s 缓存
	priceChangesCache    = map[string]priceChangesCacheEntry{}
)

const (
	priceChangesCacheTTL       = 30 * time.Second
	priceEmailPageSize         = 100
	priceEmailProgressEvery    = 10
	priceEmailThrottleWaitSec  = 60
	priceEmailMaxRowsPerLetter = 30
)

type priceChangesCacheEntry struct {
	batches   []PriceChangeBatchView
	expiresAt time.Time
}

// PriceChangeBatchView 用户侧批次视图（已按请求者可见分组过滤）。
type PriceChangeBatchView struct {
	Id             int              `json:"id"`
	PublishedAt    int64            `json:"published_at"`
	Note           string           `json:"note"`
	AffectedGroups []string         `json:"affected_groups"`
	Summary        PriceDiffSummary `json:"summary"`
	Items          []PriceDiffItem  `json:"items"`
}

// PendingPriceChanges 管理端待发布预览。
type PendingPriceChanges struct {
	HasChanges          bool            `json:"has_changes"`
	AffectedGroups      []string        `json:"affected_groups"`
	GroupCount          int             `json:"group_count"`
	ModelCount          int             `json:"model_count"`
	EstimatedEmailUsers int64           `json:"estimated_email_users"`
	Items               []PriceDiffItem `json:"items"`
}

// ---------------------------------------------------------------------------
// 启动挂载
// ---------------------------------------------------------------------------

func init() {
	// 分组可见性配置变更后，用户侧 30s 缓存按 viewerGroup 分片，必须立刻失效。
	setting.OnUserUsableGroupsChanged = InvalidatePriceChangesCache
}

// InitPriceNotify 启动初始化：批次表为空时写 baseline（快照当前值、无明细、不通知），
// 并恢复 email_state=sending 的批次断点续发。main.go 在 DB/option 初始化完成后调用。
func InitPriceNotify() {
	if !common.IsMasterNode {
		return
	}
	total, err := model.CountPricePublishBatches()
	if err != nil {
		common.SysError("price notify init: count batches failed: " + err.Error())
		return
	}
	if total == 0 {
		snapshotJSON, err := marshalPriceSnapshot(CollectCurrentPriceSnapshot())
		if err != nil {
			common.SysError("price notify init: marshal baseline snapshot failed: " + err.Error())
			return
		}
		baseline := &model.PricePublishBatch{
			CreatedAt:      common.GetTimestamp(),
			Note:           "baseline",
			AffectedGroups: "[]",
			Summary:        marshalPriceSummary(PriceDiffSummary{}),
			Snapshot:       snapshotJSON,
			IsBaseline:     true,
			EmailState:     model.PriceEmailStateNone,
		}
		if err := model.InsertPricePublishBatch(baseline, nil); err != nil {
			common.SysError("price notify init: create baseline batch failed: " + err.Error())
			return
		}
		common.SysLog(fmt.Sprintf("price notify: baseline batch #%d created", baseline.Id))
	}
	// 断点续发
	sending, err := model.GetSendingPricePublishBatches()
	if err != nil {
		common.SysError("price notify init: scan sending batches failed: " + err.Error())
		return
	}
	for _, batch := range sending {
		b := batch
		common.SysLog(fmt.Sprintf("price notify: resume email job for batch #%d from cursor %d", b.Id, b.EmailCursor))
		if err := startPriceEmailJob(b); err != nil {
			common.SysError(fmt.Sprintf("price notify: resume batch #%d failed: %v", b.Id, err))
		}
	}
}

// ---------------------------------------------------------------------------
// 管理端：pending / publish / 批次视图
// ---------------------------------------------------------------------------

// GetPendingPriceChanges 实时计算 当前值 vs 上次发布快照 的 diff 预览（display 含全部受影响分组）。
func GetPendingPriceChanges() (*PendingPriceChanges, error) {
	diff, _, err := computePendingDiff()
	if err != nil {
		return nil, err
	}
	pending := &PendingPriceChanges{
		HasChanges:     len(diff.Items) > 0,
		AffectedGroups: diff.AffectedGroups,
		GroupCount:     len(diff.AffectedGroups),
		ModelCount:     countDistinctModels(diff.Items),
		Items:          diff.Items,
	}
	buildAdminDisplays(pending.Items)
	if len(diff.AffectedGroups) > 0 {
		count, err := model.CountPriceNotifyUsers(diff.AffectedGroups, containsAllGroup(diff.AffectedGroups))
		if err != nil {
			return nil, err
		}
		pending.EstimatedEmailUsers = count
	}
	return pending, nil
}

// PublishPriceChanges 发布：事务写批次+明细、更新快照、失效用户缓存，并按需启动邮件任务。
func PublishPriceChanges(operatorId int, note string, sendEmail bool) (int, error) {
	pricePublishMu.Lock()
	defer pricePublishMu.Unlock()

	diff, currentSnapshot, err := computePendingDiff()
	if err != nil {
		return 0, err
	}
	if len(diff.Items) == 0 {
		return 0, ErrNoPendingPriceChanges
	}
	snapshotJSON, err := marshalPriceSnapshot(currentSnapshot)
	if err != nil {
		return 0, err
	}
	affectedJSON, err := common.Marshal(diff.AffectedGroups)
	if err != nil {
		return 0, err
	}
	batch := &model.PricePublishBatch{
		CreatedAt:      common.GetTimestamp(),
		OperatorId:     operatorId,
		Note:           note,
		AffectedGroups: string(affectedJSON),
		Summary:        marshalPriceSummary(diff.Summary),
		Snapshot:       snapshotJSON,
		IsBaseline:     false,
		EmailState:     model.PriceEmailStateNone,
	}
	if sendEmail && len(diff.AffectedGroups) > 0 {
		total, err := model.CountPriceNotifyUsers(diff.AffectedGroups, containsAllGroup(diff.AffectedGroups))
		if err != nil {
			return 0, err
		}
		if total > 0 {
			batch.EmailState = model.PriceEmailStateSending
			batch.EmailTotal = int(total)
		}
	}
	if err := model.InsertPricePublishBatch(batch, toDBPriceItems(diff.Items)); err != nil {
		return 0, err
	}
	InvalidatePriceChangesCache()
	common.SysLog(fmt.Sprintf("price notify: batch #%d published by user %d, %d items, groups %v, email=%s",
		batch.Id, operatorId, len(diff.Items), diff.AffectedGroups, batch.EmailState))
	if batch.EmailState == model.PriceEmailStateSending {
		if err := startPriceEmailJob(batch); err != nil {
			common.SysError(fmt.Sprintf("price notify: start batch #%d failed: %v", batch.Id, err))
		}
	}
	return batch.Id, nil
}

// GetPricePublishBatchDetail 管理端单批次明细（不过滤分组，display 含全部受影响分组）。
func GetPricePublishBatchDetail(batchId int) (*model.PricePublishBatch, []PriceDiffItem, error) {
	batch, err := model.GetPricePublishBatchById(batchId)
	if err != nil {
		return nil, nil, err
	}
	dbItems, err := model.GetPricePublishItemsByBatchId(batchId)
	if err != nil {
		return nil, nil, err
	}
	items := fromDBPriceItems(dbItems)
	buildAdminDisplays(items)
	return batch, items, nil
}

func computePendingDiff() (*PriceDiffResult, map[string]string, error) {
	lastBatch, err := model.GetLatestPricePublishBatch()
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, errors.New("价格基线未初始化，请重启服务后重试")
		}
		return nil, nil, err
	}
	var lastSnapshot map[string]string
	if err := common.UnmarshalJsonStr(lastBatch.Snapshot, &lastSnapshot); err != nil {
		return nil, nil, fmt.Errorf("解析上次发布快照失败: %w", err)
	}
	currentSnapshot := CollectCurrentPriceSnapshot()
	diff, err := ComputePriceDiff(lastSnapshot, currentSnapshot, buildModelGroupsFromPricing())
	if err != nil {
		return nil, nil, err
	}
	return diff, currentSnapshot, nil
}

// buildModelGroupsFromPricing 生产影响面：模型 → enable_groups（model/pricing.go 1min 缓存数据）。
func buildModelGroupsFromPricing() map[string][]string {
	pricing := model.GetPricing()
	m := make(map[string][]string, len(pricing))
	for _, p := range pricing {
		m[p.ModelName] = p.EnableGroup
	}
	return m
}

// ---------------------------------------------------------------------------
// 用户侧查询（30s 缓存，发布时失效）
// ---------------------------------------------------------------------------

// GetVisiblePriceChanges 返回近 days 天、按请求者可见分组过滤后的批次（倒序，不含 baseline）。
// viewerGroup=="" 表示匿名，可见范围与 pricing 页匿名口径同源（service.GetUserUsableGroups）。
func GetVisiblePriceChanges(viewerGroup string, days int) ([]PriceChangeBatchView, error) {
	cacheKey := viewerGroup + "|" + strconv.Itoa(days)
	priceChangesCacheMu.RLock()
	if entry, ok := priceChangesCache[cacheKey]; ok && time.Now().Before(entry.expiresAt) {
		priceChangesCacheMu.RUnlock()
		return entry.batches, nil
	}
	priceChangesCacheMu.RUnlock()

	batches, err := buildVisiblePriceChanges(viewerGroup, days)
	if err != nil {
		return nil, err
	}
	priceChangesCacheMu.Lock()
	priceChangesCache[cacheKey] = priceChangesCacheEntry{batches: batches, expiresAt: time.Now().Add(priceChangesCacheTTL)}
	priceChangesCacheMu.Unlock()
	return batches, nil
}

func InvalidatePriceChangesCache() {
	priceChangesCacheMu.Lock()
	priceChangesCache = map[string]priceChangesCacheEntry{}
	priceChangesCacheMu.Unlock()
}

func buildVisiblePriceChanges(viewerGroup string, days int) ([]PriceChangeBatchView, error) {
	sinceTs := time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	batches, err := model.GetRecentPricePublishBatches(sinceTs)
	if err != nil {
		return nil, err
	}
	visibleSet := make(map[string]bool)
	for g := range GetUserVisibleGroups(viewerGroup) {
		visibleSet[g] = true
	}
	// 模型可见性以「当前」pricing 的 enable_groups 为准，而非发布时冻结的快照；整轮请求只取一次。
	modelGroups := buildModelGroupsFromPricing()
	views := make([]PriceChangeBatchView, 0, len(batches))
	for _, batch := range batches {
		dbItems, err := model.GetPricePublishItemsByBatchId(batch.Id)
		if err != nil {
			return nil, err
		}
		visibleItems := filterPriceItemsForViewer(fromDBPriceItems(dbItems), viewerGroup, visibleSet, modelGroups)
		if len(visibleItems) == 0 {
			continue // 整批对该请求者无可见内容
		}
		views = append(views, PriceChangeBatchView{
			Id:             batch.Id,
			PublishedAt:    batch.CreatedAt,
			Note:           batch.Note,
			AffectedGroups: unionItemGroups(visibleItems),
			Summary:        summarizePriceItems(visibleItems),
			Items:          visibleItems,
		})
	}
	return views, nil
}

// filterPriceItemsForViewer 请求者可见性过滤 + 请求者视角 display。
// 规则（与 pricing 页分组过滤同源，防跨组价格泄露）：
//   - group_ratio：分组 ∈ 请求者可用分组
//   - group_group_ratio：仅请求者本组的专属倍率可见（匿名不可见），且目标组必须在可见集内
//     （复合 group_name 会把目标组名渲染出来，否则泄露不可见分组名）
//   - model：以「当前」enable_groups（modelGroups）与可用分组求交（"all" 取全部可用分组），
//     交集为空（含模型已下架/移出可见分组）则整条丢弃；affected_groups 一律收敛为该交集。
func filterPriceItemsForViewer(items []PriceDiffItem, viewerGroup string, visibleSet map[string]bool, modelGroups map[string][]string) []PriceDiffItem {
	ratioFor := viewerGroupRatioFunc(viewerGroup)
	out := make([]PriceDiffItem, 0, len(items))
	for _, item := range items {
		switch {
		case item.PriceType == PriceTypeGroupGroupRatio:
			ug, target, ok := item.GroupGroupRatioParts()
			if !ok || viewerGroup == "" || ug != viewerGroup || !visibleSet[target] {
				continue
			}
		case item.Scope == PriceScopeGroup:
			if !visibleSet[item.GroupName] {
				continue
			}
		default: // model 级
			currentGroups, ok := modelGroups[item.ModelName]
			if !ok {
				continue // 模型已不在当前 pricing 中
			}
			var visible []string
			if containsAllGroup(currentGroups) {
				visible = sortedKeys(visibleSet)
			} else {
				visible = intersectGroups(currentGroups, visibleSet)
			}
			if len(visible) == 0 {
				continue
			}
			item.AffectedGroups = visible
			BuildPriceItemDisplay(&item, visible, ratioFor, currentModelRatio)
		}
		out = append(out, item)
	}
	return out
}

// viewerGroupRatioFunc 请求者视角分组有效倍率：本组专属倍率（GroupGroupRatio）优先，与 pricing 页同源。
func viewerGroupRatioFunc(viewerGroup string) func(string) float64 {
	return func(g string) float64 {
		if viewerGroup != "" {
			if ratio, ok := ratio_setting.GetGroupGroupRatio(viewerGroup, g); ok {
				return ratio
			}
		}
		return ratio_setting.GetGroupRatio(g)
	}
}

func currentModelRatio(modelName string) float64 {
	ratio, _, _ := ratio_setting.GetModelRatio(modelName)
	return ratio
}

// buildAdminDisplays 管理端 display：覆盖全部受影响分组，倍率取全局 GroupRatio（无请求者视角）。
func buildAdminDisplays(items []PriceDiffItem) {
	allGroups := ratio_setting.GetGroupRatioCopy()
	for i := range items {
		if items[i].Scope != PriceScopeModel {
			continue
		}
		groups := items[i].AffectedGroups
		if containsAllGroup(groups) {
			groupSet := make(map[string]bool, len(allGroups))
			for g := range allGroups {
				groupSet[g] = true
			}
			groups = sortedKeys(groupSet)
		}
		BuildPriceItemDisplay(&items[i], groups, ratio_setting.GetGroupRatio, currentModelRatio)
	}
}

// ---------------------------------------------------------------------------
// 邮件任务
// ---------------------------------------------------------------------------

func startPriceEmailJob(batch *model.PricePublishBatch) error {
	if batch == nil {
		return errors.New("price notify batch is nil")
	}
	return backgroundtask.Start(fmt.Sprintf("price-email-%d", batch.Id), func(ctx context.Context) {
		runPriceEmailJob(ctx, batch)
	})
}

// runPriceEmailJob 批次邮件异步任务：按 id 游标分页遍历受影响分组用户，
// 逐个 NotifyUser（尊重用户 email/webhook/bark/gotify 偏好），自限速 + 进度回写 + 断点续发。
func runPriceEmailJob(ctx context.Context, batch *model.PricePublishBatch) {
	if _, loaded := priceEmailJobRunning.LoadOrStore(batch.Id, struct{}{}); loaded {
		return // 同批次已有任务在跑
	}
	defer priceEmailJobRunning.Delete(batch.Id)

	sent, failed, cursor := batch.EmailSent, batch.EmailFailed, batch.EmailCursor
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("price notify: email job for batch #%d panicked: %v", batch.Id, r))
			_ = model.FinishPricePublishBatchEmail(batch.Id, model.PriceEmailStatePartial, sent, failed, cursor)
		}
	}()

	var groups []string
	if err := common.UnmarshalJsonStr(batch.AffectedGroups, &groups); err != nil {
		common.SysError(fmt.Sprintf("price notify: batch #%d bad affected groups: %s", batch.Id, err.Error()))
		_ = model.FinishPricePublishBatchEmail(batch.Id, model.PriceEmailStatePartial, sent, failed, cursor)
		return
	}
	dbItems, err := model.GetPricePublishItemsByBatchId(batch.Id)
	if err != nil {
		common.SysError(fmt.Sprintf("price notify: batch #%d load items failed: %s", batch.Id, err.Error()))
		_ = model.FinishPricePublishBatchEmail(batch.Id, model.PriceEmailStatePartial, sent, failed, cursor)
		return
	}
	items := fromDBPriceItems(dbItems)
	allGroups := containsAllGroup(groups)

	pace := price_notify_setting.GetPriceNotifySetting().GetEmailPacePerMinute()
	ticker := time.NewTicker(time.Minute / time.Duration(pace))
	defer ticker.Stop()

	processedSinceFlush := 0
	pauseForShutdown := func() {
		if err := model.UpdatePricePublishBatchEmailProgress(batch.Id, sent, failed, cursor); err != nil {
			common.SysError(fmt.Sprintf("price notify: batch #%d persist shutdown progress failed: %s", batch.Id, err.Error()))
		}
		common.SysLog(fmt.Sprintf("price notify: batch #%d paused for shutdown at cursor %d", batch.Id, cursor))
	}
	for {
		select {
		case <-ctx.Done():
			pauseForShutdown()
			return
		default:
		}
		users, err := model.GetPriceNotifyUsersBatch(groups, allGroups, cursor, priceEmailPageSize)
		if err != nil {
			common.SysError(fmt.Sprintf("price notify: batch #%d query users failed: %s", batch.Id, err.Error()))
			_ = model.FinishPricePublishBatchEmail(batch.Id, model.PriceEmailStatePartial, sent, failed, cursor)
			return
		}
		if len(users) == 0 {
			break
		}
		for _, user := range users {
			userSetting := user.GetSetting()
			if userSetting.PriceChangeNotifyDisabled {
				cursor = user.Id
				processedSinceFlush++
				continue // 用户已退订价格变动通知
			}
			relevant := filterPriceItemsForUserGroup(items, user.Group)
			if len(relevant) == 0 {
				cursor = user.Id
				processedSinceFlush++
				continue
			}
			title, content, err := buildPriceChangeEmail(user.Group, relevant, batch.Note, batch.CreatedAt)
			if err != nil {
				common.SysError(fmt.Sprintf("price notify: batch #%d render email for user %d failed: %s", batch.Id, user.Id, err.Error()))
				failed++
				cursor = user.Id
				processedSinceFlush++
				continue
			}
			select {
			case <-ctx.Done():
				pauseForShutdown()
				return
			case <-ticker.C: // 自限速
			}
			notification := dto.NewNotify(dto.NotifyTypePriceChange, title, content, nil)
			err = NotifyUser(user.Id, user.Email, userSetting, notification)
			if err != nil && isEmailThrottledError(err) {
				// 撞上全局邮件限速（超限直接丢弃），等窗口滑过后重试该用户一次
				timer := time.NewTimer(priceEmailThrottleWaitSec * time.Second)
				select {
				case <-ctx.Done():
					timer.Stop()
					pauseForShutdown()
					return
				case <-timer.C:
				}
				err = NotifyUser(user.Id, user.Email, userSetting, notification)
			}
			if err != nil {
				common.SysError(fmt.Sprintf("price notify: batch #%d notify user %d failed: %s", batch.Id, user.Id, err.Error()))
				failed++
			} else {
				sent++
			}
			cursor = user.Id
			processedSinceFlush++
			if processedSinceFlush >= priceEmailProgressEvery {
				processedSinceFlush = 0
				if err := model.UpdatePricePublishBatchEmailProgress(batch.Id, sent, failed, cursor); err != nil {
					common.SysError(fmt.Sprintf("price notify: batch #%d flush progress failed: %s", batch.Id, err.Error()))
				}
			}
		}
	}
	finalState := model.PriceEmailStateDone
	if failed > 0 {
		finalState = model.PriceEmailStatePartial
	}
	if err := model.FinishPricePublishBatchEmail(batch.Id, finalState, sent, failed, cursor); err != nil {
		common.SysError(fmt.Sprintf("price notify: batch #%d finalize failed: %s", batch.Id, err.Error()))
	}
	common.SysLog(fmt.Sprintf("price notify: batch #%d email job finished, state=%s sent=%d failed=%d", batch.Id, finalState, sent, failed))
}

func isEmailThrottledError(err error) bool {
	return errors.Is(err, common.ErrEmailThrottled)
}

// filterPriceItemsForUserGroup 邮件视角：仅保留影响该用户所在分组的明细。
func filterPriceItemsForUserGroup(items []PriceDiffItem, userGroup string) []PriceDiffItem {
	out := make([]PriceDiffItem, 0, len(items))
	for _, item := range items {
		switch {
		case item.PriceType == PriceTypeGroupGroupRatio:
			ug, _, ok := item.GroupGroupRatioParts()
			if !ok || ug != userGroup {
				continue
			}
		case item.Scope == PriceScopeGroup:
			if item.GroupName != userGroup {
				continue
			}
		default:
			if !containsAllGroup(item.AffectedGroups) && !containsGroup(item.AffectedGroups, userGroup) {
				continue
			}
		}
		out = append(out, item)
	}
	return out
}

// ---------------------------------------------------------------------------
// 邮件内容
// ---------------------------------------------------------------------------

var priceEmailTemplate = template.Must(template.New("priceChangeEmail").Parse(`
<div style="max-width:640px;margin:0 auto;font-family:-apple-system,'PingFang SC','Microsoft YaHei',sans-serif;color:#333;line-height:1.7;">
  <h2 style="color:#1a73e8;">价格调整通知</h2>
  <p>尊敬的用户，您好：</p>
  <p>{{.SiteName}} 已对您所在分组（<b>{{.GroupName}}</b>）相关的价格进行调整，<b>价格已于 {{.PublishedAt}} 生效</b>。变动内容如下：</p>
  {{if .GroupLines}}<ul>{{range .GroupLines}}<li>{{.}}</li>{{end}}</ul>{{end}}
  {{if .ModelRows}}
  <table border="0" cellpadding="6" cellspacing="0" style="border-collapse:collapse;width:100%;font-size:13px;">
    <tr style="background:#f5f5f5;">
      <th align="left" style="border:1px solid #e0e0e0;">模型</th>
      <th align="left" style="border:1px solid #e0e0e0;">项目</th>
      <th align="right" style="border:1px solid #e0e0e0;">调整前</th>
      <th align="right" style="border:1px solid #e0e0e0;">调整后</th>
      <th align="right" style="border:1px solid #e0e0e0;">幅度</th>
    </tr>
    {{range .ModelRows}}
    <tr>
      <td style="border:1px solid #e0e0e0;">{{.Model}}</td>
      <td style="border:1px solid #e0e0e0;">{{.Label}}</td>
      <td align="right" style="border:1px solid #e0e0e0;">{{.OldPrice}}</td>
      <td align="right" style="border:1px solid #e0e0e0;">{{.NewPrice}}</td>
      <td align="right" style="border:1px solid #e0e0e0;">{{.Delta}}</td>
    </tr>
    {{end}}
  </table>
  {{if gt .MoreCount 0}}<p style="color:#888;font-size:13px;">…等 {{.MoreCount}} 项未逐一列出，完整清单请见价格页。</p>{{end}}
  {{end}}
  {{if .Note}}<p><b>管理员说明：</b>{{.Note}}</p>{{end}}
  <p>您可以随时访问 <a href="{{.PricingURL}}">价格页</a> 查看最新完整价格。</p>
  <p style="color:#888;font-size:12px;border-top:1px solid #eee;padding-top:8px;">
    如不希望接收此类通知，可在 个人设置 → 通知设置 中关闭「价格变动通知」。
  </p>
</div>
`))

type priceEmailData struct {
	SiteName    string
	GroupName   string
	PublishedAt string
	GroupLines  []string
	ModelRows   []priceEmailModelRow
	MoreCount   int
	Note        string
	PricingURL  string
}

type priceEmailModelRow struct {
	Model    string
	Label    string
	OldPrice string
	NewPrice string
	Delta    string
}

var priceEmailTypeLabels = map[string]string{
	PriceTypeModelRatio:       "输入价（$/1M）",
	PriceTypeCompletionRatio:  "输出价（$/1M）",
	PriceTypeCacheRatio:       "缓存读价（$/1M）",
	PriceTypeCreateCacheRatio: "缓存写价（$/1M）",
	PriceTypeModelPrice:       "按次价（$）",
}

// buildPriceChangeEmail 生成邮件标题与 HTML 正文（价格均为该用户分组的有效美元价）。
func buildPriceChangeEmail(userGroup string, items []PriceDiffItem, note string, publishedAt int64) (string, string, error) {
	title := fmt.Sprintf("【%s】您所在分组（%s）价格调整通知", common.SystemName, userGroup)
	ratioFor := viewerGroupRatioFunc(userGroup)
	data := priceEmailData{
		SiteName:    common.SystemName,
		GroupName:   userGroup,
		PublishedAt: time.Unix(publishedAt, 0).Format("2006-01-02 15:04"),
		Note:        note,
		PricingURL:  strings.TrimSuffix(system_setting.ServerAddress, "/") + "/pricing",
	}
	for _, item := range items {
		if item.Scope != PriceScopeGroup {
			continue
		}
		switch item.PriceType {
		case PriceTypeGroupRatio:
			data.GroupLines = append(data.GroupLines, fmt.Sprintf(
				"分组「%s」倍率由 %s 调整为 %s%s，全部模型价格相应变化。",
				item.GroupName, formatRatio(item.OldValue), formatRatio(item.NewValue), formatPercentSuffix(item.OldValue, item.NewValue)))
		case PriceTypeGroupGroupRatio:
			_, targetGroup, ok := item.GroupGroupRatioParts()
			if !ok {
				continue
			}
			data.GroupLines = append(data.GroupLines, fmt.Sprintf(
				"您所在分组使用「%s」分组的专属倍率由 %s 调整为 %s%s。",
				targetGroup, formatRatio(item.OldValue), formatRatio(item.NewValue), formatPercentSuffix(item.OldValue, item.NewValue)))
		}
	}
	modelItemCount := 0
	for _, item := range items {
		if item.Scope != PriceScopeModel {
			continue
		}
		modelItemCount++
		if len(data.ModelRows) >= priceEmailMaxRowsPerLetter {
			continue
		}
		display := item
		BuildPriceItemDisplay(&display, []string{userGroup}, ratioFor, currentModelRatio)
		entry, ok := display.Display[userGroup]
		if !ok {
			continue
		}
		data.ModelRows = append(data.ModelRows, priceEmailModelRow{
			Model:    item.ModelName,
			Label:    priceEmailTypeLabels[item.PriceType],
			OldPrice: formatEmailUsd(entry.OldUsd, item.Direction == PriceDirectionAdded),
			NewPrice: formatEmailUsd(entry.NewUsd, item.Direction == PriceDirectionRemoved),
			Delta:    formatEmailDelta(item.Direction, entry.OldUsd, entry.NewUsd),
		})
	}
	data.MoreCount = modelItemCount - len(data.ModelRows)

	var sb strings.Builder
	if err := priceEmailTemplate.Execute(&sb, data); err != nil {
		return "", "", err
	}
	return title, sb.String(), nil
}

func formatRatio(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func formatPercentSuffix(oldVal, newVal float64) string {
	if oldVal <= 0 {
		return ""
	}
	return fmt.Sprintf("（约 %+.1f%%）", (newVal-oldVal)/oldVal*100)
}

func formatEmailUsd(v float64, absent bool) string {
	if absent {
		return "—"
	}
	return "$" + strconv.FormatFloat(v, 'f', -1, 64)
}

func formatEmailDelta(direction string, oldUsd, newUsd float64) string {
	switch direction {
	case PriceDirectionAdded:
		return "新增"
	case PriceDirectionRemoved:
		return "移除"
	}
	if oldUsd <= 0 {
		return "—"
	}
	return fmt.Sprintf("%+.1f%%", (newUsd-oldUsd)/oldUsd*100)
}

// ---------------------------------------------------------------------------
// 工具函数 / DB 转换
// ---------------------------------------------------------------------------

func marshalPriceSnapshot(snapshot map[string]string) (string, error) {
	b, err := common.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func marshalPriceSummary(summary PriceDiffSummary) string {
	b, err := common.Marshal(summary)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func toDBPriceItems(items []PriceDiffItem) []model.PricePublishItem {
	out := make([]model.PricePublishItem, 0, len(items))
	for _, item := range items {
		groupsJSON, err := common.Marshal(item.AffectedGroups)
		if err != nil {
			groupsJSON = []byte("[]")
		}
		out = append(out, model.PricePublishItem{
			Scope:          item.Scope,
			GroupName:      item.GroupName,
			ModelName:      item.ModelName,
			PriceType:      item.PriceType,
			OldValue:       item.OldValue,
			NewValue:       item.NewValue,
			Direction:      item.Direction,
			AffectedGroups: string(groupsJSON),
		})
	}
	return out
}

func fromDBPriceItems(dbItems []model.PricePublishItem) []PriceDiffItem {
	out := make([]PriceDiffItem, 0, len(dbItems))
	for _, dbItem := range dbItems {
		var groups []string
		if dbItem.AffectedGroups != "" {
			_ = common.UnmarshalJsonStr(dbItem.AffectedGroups, &groups)
		}
		if groups == nil {
			groups = []string{}
		}
		out = append(out, PriceDiffItem{
			Scope:          dbItem.Scope,
			GroupName:      dbItem.GroupName,
			ModelName:      dbItem.ModelName,
			PriceType:      dbItem.PriceType,
			OldValue:       dbItem.OldValue,
			NewValue:       dbItem.NewValue,
			Direction:      dbItem.Direction,
			AffectedGroups: groups,
			Display:        map[string]PriceDisplayEntry{},
		})
	}
	return out
}

func summarizePriceItems(items []PriceDiffItem) PriceDiffSummary {
	var s PriceDiffSummary
	for _, item := range items {
		switch item.Direction {
		case PriceDirectionUp:
			s.Up++
		case PriceDirectionDown:
			s.Down++
		case PriceDirectionAdded:
			s.Added++
		case PriceDirectionRemoved:
			s.Removed++
		}
	}
	return s
}

func unionItemGroups(items []PriceDiffItem) []string {
	set := make(map[string]bool)
	for _, item := range items {
		for _, g := range item.AffectedGroups {
			set[g] = true
		}
	}
	return sortedKeys(set)
}

func countDistinctModels(items []PriceDiffItem) int {
	set := make(map[string]bool)
	for _, item := range items {
		if item.Scope == PriceScopeModel {
			set[item.ModelName] = true
		}
	}
	return len(set)
}

func containsAllGroup(groups []string) bool {
	return containsGroup(groups, PriceGroupAll)
}

func containsGroup(groups []string, target string) bool {
	for _, g := range groups {
		if g == target {
			return true
		}
	}
	return false
}

func intersectGroups(groups []string, visibleSet map[string]bool) []string {
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		if visibleSet[g] {
			out = append(out, g)
		}
	}
	return out
}
