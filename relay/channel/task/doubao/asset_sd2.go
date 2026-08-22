package doubao

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
)

// sd2 素材组体系（上游文档 sd2_real）：260128/ep/mini 系模型的素材库，与 HC 的
// /v1/sd/assets 旧体系不互通。流程：创建素材组 → 上传素材（异步，返回 task_id）→
// 轮询 /v1/assets/get 至 completed。生成/任务接口与 sd 旧体系相同（sd_flavor.go）。
//
// 判定规则见 IsSd2AssetModel：显式指定的模型不含 "-hc" 即走本体系。

// IsSd2AssetModel 判断素材应注册进 sd2 素材组体系还是旧 /v1/sd/assets 体系。
// 上游模型列表（260128/ep/fast-ep/mini-*）均走 sd2；仅 hc 族走旧体系。
func IsSd2AssetModel(modelName string) bool {
	if modelName == "" {
		return false
	}
	return !strings.Contains(strings.ToLower(modelName), "-hc")
}

// Sd2CreateResult sd2 上传返回：素材 id + 处理任务 id（查询时需要）。
type Sd2CreateResult struct {
	AssetID string
	TaskID  string
	GroupID string
	Status  string // 已映射为对外统一状态（Processing 等）
}

// ===== 素材组缓存 =====
// 每渠道维护一个上游素材组（上游会轮转回收组，上传前须校验存在，失效即重建）。
var (
	sd2GroupCache   = map[int]string{} // channelId → groupId
	sd2GroupCacheMu sync.Mutex
)

// EnsureSd2Group 返回可用的素材组 id：显式传入优先；否则用渠道缓存（校验存在），
// 失效/缺失时新建 "beeapi-ch<channelId>" 组并缓存。
func EnsureSd2Group(ctx context.Context, channelId int, baseURL, key, proxy, explicitGroupId string) (string, *AssetUpstreamError, error) {
	if explicitGroupId != "" {
		return explicitGroupId, nil, nil
	}

	sd2GroupCacheMu.Lock()
	cached := sd2GroupCache[channelId]
	sd2GroupCacheMu.Unlock()

	if cached != "" {
		if exists, upErr, err := sd2GroupExists(ctx, baseURL, key, proxy, cached); err != nil {
			return "", nil, err
		} else if upErr != nil {
			return "", upErr, nil
		} else if exists {
			return cached, nil, nil
		}
		// 组被上游轮转回收 → 走重建
	}

	groupId, upErr, err := createSd2Group(ctx, baseURL, key, proxy, fmt.Sprintf("beeapi-ch%d", channelId))
	if err != nil || upErr != nil {
		return "", upErr, err
	}
	sd2GroupCacheMu.Lock()
	sd2GroupCache[channelId] = groupId
	sd2GroupCacheMu.Unlock()
	return groupId, nil, nil
}

func createSd2Group(ctx context.Context, baseURL, key, proxy, name string) (string, *AssetUpstreamError, error) {
	payload, err := common.Marshal(map[string]string{
		"name":        name,
		"description": "managed by beeapi sd asset proxy",
	})
	if err != nil {
		return "", nil, err
	}
	respBody, status, err := callSd2API(ctx, http.MethodPost, fmt.Sprintf("%s/v1/asset-groups", strings.TrimSuffix(baseURL, "/")), key, proxy, payload)
	if err != nil {
		return "", nil, err
	}
	if status >= http.StatusBadRequest {
		return "", sd2UpstreamError(status, respBody, "create asset group"), nil
	}
	var parsed struct {
		ID string `json:"id"`
	}
	if err := common.Unmarshal(respBody, &parsed); err != nil || parsed.ID == "" {
		return "", nil, fmt.Errorf("upstream create asset group returned no id")
	}
	return parsed.ID, nil, nil
}

// sd2GroupExists 校验素材组是否仍存在（上游轮转回收后需重建）。
// 404/组不存在 → (false, nil, nil)；其他上游错误原样透出。
func sd2GroupExists(ctx context.Context, baseURL, key, proxy, groupId string) (bool, *AssetUpstreamError, error) {
	respBody, status, err := callSd2API(ctx, http.MethodGet, fmt.Sprintf("%s/v1/asset-groups/%s", strings.TrimSuffix(baseURL, "/"), groupId), key, proxy, nil)
	if err != nil {
		return false, nil, err
	}
	if status == http.StatusNotFound {
		return false, nil, nil
	}
	if status >= http.StatusBadRequest {
		// 组不存在的错误形状未知，4xx 一律按不存在处理（重建组代价低且幂等安全）
		if status < http.StatusInternalServerError {
			return false, nil, nil
		}
		return false, sd2UpstreamError(status, respBody, "get asset group"), nil
	}
	return true, nil, nil
}

// CreateAssetSd2 上传素材到 sd2 素材组体系（异步：返回 task_id，须轮询至 completed）。
func CreateAssetSd2(ctx context.Context, baseURL, key, proxy, groupId string, params AssetCreateParams) (*Sd2CreateResult, *AssetUpstreamError, error) {
	payload, err := common.Marshal(map[string]string{
		"group_id":   groupId,
		"url":        params.URL,
		"asset_type": params.AssetType,
		"name":       params.Name,
	})
	if err != nil {
		return nil, nil, err
	}
	respBody, status, err := callSd2API(ctx, http.MethodPost, fmt.Sprintf("%s/v1/assets", strings.TrimSuffix(baseURL, "/")), key, proxy, payload)
	if err != nil {
		return nil, nil, err
	}
	if status >= http.StatusBadRequest {
		return nil, sd2UpstreamError(status, respBody, "create asset"), nil
	}
	var parsed struct {
		ID     string `json:"id"`
		TaskID string `json:"task_id"`
		Status string `json:"status"`
	}
	if err := common.Unmarshal(respBody, &parsed); err != nil {
		return nil, nil, fmt.Errorf("unmarshal sd2 create asset response failed: %w", err)
	}
	if parsed.ID == "" {
		return nil, nil, fmt.Errorf("upstream sd2 create asset returned empty asset id")
	}
	return &Sd2CreateResult{
		AssetID: parsed.ID,
		TaskID:  parsed.TaskID,
		GroupID: groupId,
		Status:  mapSd2Status(parsed.Status),
	}, nil, nil
}

// GetAssetSd2 查询 sd2 素材处理结果（POST /v1/assets/get，只读幂等可轮询）。
// taskId 可为空（上游接受空串）。返回对外统一形状 AssetResult。
func GetAssetSd2(ctx context.Context, baseURL, key, proxy, assetId, taskId string) (*AssetResult, *AssetUpstreamError, error) {
	payload, err := common.Marshal(map[string]string{
		"asset_id": assetId,
		"task_id":  taskId,
	})
	if err != nil {
		return nil, nil, err
	}
	respBody, status, err := callSd2API(ctx, http.MethodPost, fmt.Sprintf("%s/v1/assets/get", strings.TrimSuffix(baseURL, "/")), key, proxy, payload)
	if err != nil {
		return nil, nil, err
	}
	if status >= http.StatusBadRequest {
		return nil, sd2UpstreamError(status, respBody, "get asset"), nil
	}
	var parsed struct {
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		URL       string          `json:"url"`
		AssetType string          `json:"asset_type"`
		GroupID   string          `json:"group_id"`
		Status    string          `json:"status"`
		Error     json.RawMessage `json:"error"`
		CreatedAt string          `json:"created_at"`
		UpdatedAt string          `json:"updated_at"`
	}
	if err := common.Unmarshal(respBody, &parsed); err != nil {
		return nil, nil, fmt.Errorf("unmarshal sd2 get asset response failed: %w", err)
	}
	groupId := parsed.GroupID
	var groupPtr *string
	if groupId != "" {
		groupPtr = &groupId
	}
	return &AssetResult{
		Id:         parsed.ID,
		Status:     mapSd2Status(parsed.Status),
		AssetType:  parsed.AssetType,
		Name:       parsed.Name,
		URL:        parsed.URL,
		GroupId:    groupPtr,
		CreateTime: parsed.CreatedAt,
		UpdateTime: parsed.UpdatedAt,
	}, nil, nil
}

// mapSd2Status 把 sd2 的小写状态映射为对外统一状态（与旧体系一致）：
// processing→Processing / completed→Active / failed→Failed，未知原样返回。
func mapSd2Status(s string) string {
	switch strings.ToLower(s) {
	case "processing", "pending":
		return "Processing"
	case "completed", "active":
		return "Active"
	case "failed":
		return "Failed"
	case "":
		return ""
	default:
		return s
	}
}

// callSd2API 发起 sd2 素材请求，返回（响应体, HTTP 状态码）。传输层错误走 error。
func callSd2API(parent context.Context, method, uri, key, proxy string, payload []byte) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(parent, assetRequestTimeout)
	defer cancel()
	var bodyReader io.Reader
	if payload != nil {
		bodyReader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, uri, bodyReader)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, 0, fmt.Errorf("new proxy http client failed: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("call upstream sd2 asset api failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, assetRespBodyLimit))
	if err != nil {
		return nil, 0, fmt.Errorf("read upstream sd2 asset response failed: %w", err)
	}
	return respBody, resp.StatusCode, nil
}

// sd2UpstreamError 把 sd2 非 2xx 响应转成脱敏后的上游错误：
// 尝试解析 {error:{code,message}} 或 {message}，非 JSON 一律不透传原文。
func sd2UpstreamError(status int, respBody []byte, action string) *AssetUpstreamError {
	var withError struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := common.Unmarshal(respBody, &withError); err == nil {
		if withError.Error != nil && (withError.Error.Code != "" || withError.Error.Message != "") {
			return &AssetUpstreamError{HTTPStatus: status, Code: withError.Error.Code, Message: withError.Error.Message}
		}
		if withError.Message != "" {
			return &AssetUpstreamError{HTTPStatus: status, Code: "upstream_error", Message: withError.Message}
		}
	}
	return &AssetUpstreamError{HTTPStatus: status, Code: "upstream_error",
		Message: fmt.Sprintf("upstream %s returned status %d", action, status)}
}
