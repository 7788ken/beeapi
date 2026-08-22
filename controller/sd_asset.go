package controller

import (
	"fmt"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/task/doubao"

	"github.com/gin-gonic/gin"
)

// sdAssetWaitBackoff wait=true 时的退避轮询序列，累计 57s < 60s 总上限（PRD §3.3(b)）。
var sdAssetWaitBackoff = []time.Duration{
	2 * time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second, 20 * time.Second,
}

func sdAssetAbort(c *gin.Context, statusCode int, message string) {
	c.JSON(statusCode, gin.H{
		"success": false,
		"data": gin.H{
			"base_resp": dto.SdBaseResp{StatusCode: statusCode, StatusMsg: message},
		},
	})
}

func sdAssetUpstreamAbort(c *gin.Context, upErr *doubao.AssetUpstreamError) {
	status := http.StatusBadGateway
	if upErr.HTTPStatus >= http.StatusBadRequest && upErr.HTTPStatus < http.StatusInternalServerError {
		status = upErr.HTTPStatus
	}
	sdAssetAbort(c, status, upErr.Error())
}

// sdAssetData 把白名单 AssetResult 转成响应 data（附成功 base_resp）。
func sdAssetData(result *doubao.AssetResult) (gin.H, error) {
	raw, err := common.Marshal(result)
	if err != nil {
		return nil, err
	}
	data := gin.H{}
	if err := common.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	data["base_resp"] = dto.SdBaseResp{StatusCode: 0, StatusMsg: "success"}
	return data, nil
}

// RelaySdAssetCreate POST /v1/sd/assets：代理上游 CreateAsset。
// 渠道由 Distribute() 按 model 选定（model 由 SdAssetRequestConvert 注入），
// 素材落库绑定该渠道，供 GET 查询路由。素材操作不计费（quota=0）。
func RelaySdAssetCreate(c *gin.Context) {
	var req dto.SdAssetCreateRequest
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		sdAssetAbort(c, http.StatusBadRequest, "invalid request body: "+common.MaskSensitiveInfo(err.Error()))
		return
	}

	channelId := common.GetContextKeyInt(c, constant.ContextKeyChannelId)
	channelType := common.GetContextKeyInt(c, constant.ContextKeyChannelType)
	baseURL, key, proxy := sdAssetChannelConnFromContext(c)

	// 上游 model：决定素材注册进哪套素材体系。
	// sd 网关（58）：显式传 model（经渠道模型映射）→ 非 hc 族走 sd2 素材组体系（260128/ep/mini，
	// 与 HC 的 /v1/sd/assets 旧体系不互通）；hc 族/未传 → 旧体系（HC 默认空间）。
	// 方舟控制面（54）：CreateAsset 必须带 model，未显式传时用分发默认值（同样过映射）。
	upstreamModel := ""
	if middleware.IsSdAssetExplicitModel(c) || channelType != constant.ChannelTypeSdVideo {
		upstreamModel = applySdAssetModelMapping(c, req.Model)
	}

	if channelType == constant.ChannelTypeSdVideo && middleware.IsSdAssetExplicitModel(c) && doubao.IsSd2AssetModel(upstreamModel) {
		relaySdAssetCreateSd2(c, channelId, baseURL, key, proxy, &req)
		return
	}

	result, upErr, err := doubao.CreateAssetForChannel(c.Request.Context(), channelType, baseURL, key, proxy, doubao.AssetCreateParams{
		Model:     upstreamModel,
		URL:       req.URL,
		Name:      req.Name,
		AssetType: req.AssetType,
		GroupId:   req.GroupId,
	})
	if err != nil {
		sdAssetAbort(c, http.StatusBadGateway, common.MaskSensitiveInfo(err.Error()))
		return
	}
	if upErr != nil {
		sdAssetUpstreamAbort(c, upErr)
		return
	}

	record := &model.SdAsset{
		AssetID:   result.Id,
		UserId:    c.GetInt("id"),
		ChannelId: channelId,
		AssetType: req.AssetType,
		Name:      req.Name,
		Status:    "Processing",
	}
	if err := record.Insert(); err != nil {
		// 上游素材已创建成功，本地登记失败只影响后续 GET 路由，不吞掉素材 ID
		common.SysError(fmt.Sprintf("sd asset insert failed: asset_id=%s, user_id=%d, error=%v", result.Id, record.UserId, err))
	}

	final := result
	if c.Query("wait") == "true" {
		if polled := waitSdAssetActive(c, record, func() (*doubao.AssetResult, *doubao.AssetUpstreamError, error) {
			return doubao.GetAssetForChannel(c.Request.Context(), channelType, baseURL, key, proxy, result.Id)
		}); polled != nil {
			final = polled
		}
	}

	data, err := sdAssetData(final)
	if err != nil {
		sdAssetAbort(c, http.StatusInternalServerError, "marshal response failed")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// relaySdAssetCreateSd2 sd2 素材组体系上传（260128/ep/mini 系模型）：
// 确保素材组（渠道级缓存 + 上游轮转失效重建）→ 异步上传 → 落库（记 protocol/task_id/group_id）。
func relaySdAssetCreateSd2(c *gin.Context, channelId int, baseURL, key, proxy string, req *dto.SdAssetCreateRequest) {
	groupId, upErr, err := doubao.EnsureSd2Group(c.Request.Context(), channelId, baseURL, key, proxy, req.GroupId)
	if err != nil {
		sdAssetAbort(c, http.StatusBadGateway, common.MaskSensitiveInfo(err.Error()))
		return
	}
	if upErr != nil {
		sdAssetUpstreamAbort(c, upErr)
		return
	}

	created, upErr, err := doubao.CreateAssetSd2(c.Request.Context(), baseURL, key, proxy, groupId, doubao.AssetCreateParams{
		URL:       req.URL,
		Name:      req.Name,
		AssetType: req.AssetType,
	})
	if err != nil {
		sdAssetAbort(c, http.StatusBadGateway, common.MaskSensitiveInfo(err.Error()))
		return
	}
	if upErr != nil {
		sdAssetUpstreamAbort(c, upErr)
		return
	}

	status := created.Status
	if status == "" {
		status = "Processing"
	}
	record := &model.SdAsset{
		AssetID:        created.AssetID,
		UserId:         c.GetInt("id"),
		ChannelId:      channelId,
		AssetType:      req.AssetType,
		Name:           req.Name,
		Status:         status,
		Protocol:       "sd2",
		UpstreamTaskID: created.TaskID,
		GroupID:        groupId,
	}
	if err := record.Insert(); err != nil {
		common.SysError(fmt.Sprintf("sd asset insert failed: asset_id=%s, user_id=%d, error=%v", created.AssetID, record.UserId, err))
	}

	var final *doubao.AssetResult
	if c.Query("wait") == "true" {
		final = waitSdAssetActive(c, record, func() (*doubao.AssetResult, *doubao.AssetUpstreamError, error) {
			return doubao.GetAssetSd2(c.Request.Context(), baseURL, key, proxy, created.AssetID, created.TaskID)
		})
	}
	if final == nil {
		final = &doubao.AssetResult{Id: created.AssetID, Status: status}
	}

	data, err := sdAssetData(final)
	if err != nil {
		sdAssetAbort(c, http.StatusInternalServerError, "marshal response failed")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// RelaySdAssetGet GET /v1/sd/assets/:asset_id：按素材落库记录路由到创建时的渠道，
// 实时拉上游 GetAsset 并回写状态快照。仅创建者可查（语义对齐任务查询）。
func RelaySdAssetGet(c *gin.Context) {
	userId := c.GetInt("id")
	assetId := c.Param("asset_id")

	record, exist, err := model.GetSdAssetByAssetId(userId, assetId)
	if err != nil {
		sdAssetAbort(c, http.StatusInternalServerError, "get asset record failed")
		return
	}
	if !exist {
		sdAssetAbort(c, http.StatusNotFound, "asset_not_exist")
		return
	}

	channel, err := model.GetChannelById(record.ChannelId, true)
	if err != nil || channel.Status != common.ChannelStatusEnabled {
		sdAssetAbort(c, http.StatusBadRequest, fmt.Sprintf("invalid_channel_id: channel %d bound to this asset is unavailable", record.ChannelId))
		return
	}
	key, _, apiErr := channel.GetNextEnabledKey()
	if apiErr != nil {
		sdAssetAbort(c, http.StatusBadRequest, fmt.Sprintf("invalid_channel_id: channel %d has no enabled key", record.ChannelId))
		return
	}
	baseURL := channel.GetBaseURL()
	if baseURL == "" && channel.Type > 0 && channel.Type < len(constant.ChannelBaseURLs) {
		baseURL = constant.ChannelBaseURLs[channel.Type]
	}

	var result *doubao.AssetResult
	var upErr *doubao.AssetUpstreamError
	if record.Protocol == "sd2" {
		result, upErr, err = doubao.GetAssetSd2(c.Request.Context(), baseURL, key, channel.GetSetting().Proxy, assetId, record.UpstreamTaskID)
	} else {
		result, upErr, err = doubao.GetAssetForChannel(c.Request.Context(), channel.Type, baseURL, key, channel.GetSetting().Proxy, assetId)
	}
	if err != nil {
		sdAssetAbort(c, http.StatusBadGateway, common.MaskSensitiveInfo(err.Error()))
		return
	}
	if upErr != nil {
		sdAssetUpstreamAbort(c, upErr)
		return
	}

	snapshotSdAsset(record, result)
	data, err := sdAssetData(result)
	if err != nil {
		sdAssetAbort(c, http.StatusInternalServerError, "marshal response failed")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// waitSdAssetActive 退避轮询素材状态直至 Active/Failed 或序列耗尽（fetch 由协议方提供）。
// 轮询出错/超时不报错，返回最后已知状态（nil 表示一次都没查成，调用方回退到创建响应）。
func waitSdAssetActive(c *gin.Context, record *model.SdAsset, fetch func() (*doubao.AssetResult, *doubao.AssetUpstreamError, error)) *doubao.AssetResult {
	var last *doubao.AssetResult
	for _, d := range sdAssetWaitBackoff {
		select {
		case <-c.Request.Context().Done():
			// 客户端断连：停止轮询，快照已知状态
			return snapshotAndReturn(record, last)
		case <-time.After(d):
		}
		result, upErr, err := fetch()
		if err != nil || upErr != nil {
			break
		}
		last = result
		if result.Status == "Active" || result.Status == "Failed" {
			break
		}
	}
	return snapshotAndReturn(record, last)
}

func snapshotAndReturn(record *model.SdAsset, result *doubao.AssetResult) *doubao.AssetResult {
	snapshotSdAsset(record, result)
	return result
}

// snapshotSdAsset 回写素材状态快照（best-effort，失败仅记日志）。
func snapshotSdAsset(record *model.SdAsset, result *doubao.AssetResult) {
	if record == nil || record.ID == 0 || result == nil || result.Status == "" {
		return
	}
	raw, err := common.Marshal(result)
	if err != nil {
		return
	}
	if err := record.UpdateStatusSnapshot(result.Status, raw); err != nil {
		common.SysError(fmt.Sprintf("sd asset snapshot update failed: asset_id=%s, error=%v", record.AssetID, err))
	}
}

// applySdAssetModelMapping 对素材上传的 model 应用渠道模型映射（语义对齐 relay 的
// ModelMappedHelper：支持链式重定向，visited 防环）。无映射或解析失败时原样返回。
func applySdAssetModelMapping(c *gin.Context, modelName string) string {
	mapping := common.GetContextKeyString(c, constant.ContextKeyChannelModelMapping)
	if mapping == "" || mapping == "{}" || modelName == "" {
		return modelName
	}
	modelMap := map[string]string{}
	if err := common.Unmarshal([]byte(mapping), &modelMap); err != nil {
		return modelName
	}
	current := modelName
	visited := map[string]bool{current: true}
	for {
		next, ok := modelMap[current]
		if !ok || next == "" || visited[next] {
			break
		}
		visited[next] = true
		current = next
	}
	return current
}

func sdAssetChannelConnFromContext(c *gin.Context) (baseURL, key, proxy string) {
	key = common.GetContextKeyString(c, constant.ContextKeyChannelKey)
	baseURL = common.GetContextKeyString(c, constant.ContextKeyChannelBaseUrl)
	if baseURL == "" {
		channelType := common.GetContextKeyInt(c, constant.ContextKeyChannelType)
		if channelType > 0 && channelType < len(constant.ChannelBaseURLs) {
			baseURL = constant.ChannelBaseURLs[channelType]
		}
	}
	if v, ok := common.GetContextKey(c, constant.ContextKeyChannelSetting); ok {
		if s, ok := v.(dto.ChannelSettings); ok {
			proxy = s.Proxy
		}
	}
	return
}
