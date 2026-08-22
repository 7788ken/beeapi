package doubao

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service"
)

// 素材库控制面：POST {base_url}/?Action={X}&Version=2024-01-01，Bearer 渠道 key。
// 上游状态流转 Processing → Active/Failed，仅 Active 可通过 asset://<Id> 引用。
const (
	assetAPIVersion     = "2024-01-01"
	assetActionCreate   = "CreateAsset"
	assetActionGet      = "GetAsset"
	assetRequestTimeout = 30 * time.Second
	assetRespBodyLimit  = 1 << 20 // 1MiB，控制面响应不应超过该量级
)

// AssetCreateParams 上游 CreateAsset 入参。
type AssetCreateParams struct {
	Model     string `json:"model"`
	URL       string `json:"URL"`
	Name      string `json:"Name"`
	AssetType string `json:"AssetType"`
	GroupId   string `json:"GroupId,omitempty"`
}

// AssetResult 上游素材信息。仅这些白名单字段会露出给客户，
// 上游响应中的其他内部字段一律丢弃（防内部信息/预签名参数泄露）。
type AssetResult struct {
	Id         string  `json:"Id"`
	Status     string  `json:"Status,omitempty"`
	AssetType  string  `json:"AssetType,omitempty"`
	Name       string  `json:"Name,omitempty"`
	URL        string  `json:"URL,omitempty"`
	GroupId    *string `json:"GroupId,omitempty"`
	CreateTime string  `json:"CreateTime,omitempty"`
	UpdateTime string  `json:"UpdateTime,omitempty"`
}

// AssetUpstreamError 上游业务错误（已脱敏，仅错误码与消息）。
type AssetUpstreamError struct {
	HTTPStatus int
	Code       string
	Message    string
}

func (e *AssetUpstreamError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return e.Message
}

type assetAPIResponse struct {
	ResponseMetadata struct {
		Error *struct {
			Code    string `json:"Code"`
			Message string `json:"Message"`
		} `json:"Error,omitempty"`
	} `json:"ResponseMetadata"`
	Result AssetResult `json:"Result"`
}

// CreateAssetForChannel 按渠道类型分发上游素材协议：
// 渠道 58（sd 网关）走 POST /v1/sd/assets；其余（火山方舟）走 Action=CreateAsset 控制面。
func CreateAssetForChannel(ctx context.Context, channelType int, baseURL, key, proxy string, params AssetCreateParams) (*AssetResult, *AssetUpstreamError, error) {
	if channelType == constant.ChannelTypeSdVideo {
		return createAssetSd(ctx, baseURL, key, proxy, params)
	}
	return CreateAsset(ctx, baseURL, key, proxy, params)
}

// GetAssetForChannel 按渠道类型分发上游素材查询协议（同 CreateAssetForChannel）。
func GetAssetForChannel(ctx context.Context, channelType int, baseURL, key, proxy, assetId string) (*AssetResult, *AssetUpstreamError, error) {
	if channelType == constant.ChannelTypeSdVideo {
		return getAssetSd(ctx, baseURL, key, proxy, assetId)
	}
	return GetAsset(ctx, baseURL, key, proxy, assetId)
}

// CreateAsset 代理上游上传素材，返回素材信息（含 Id）。
// 单次 30s 超时，不自动重试（上游创建幂等性未知，交客户端重试）。
func CreateAsset(ctx context.Context, baseURL, key, proxy string, params AssetCreateParams) (*AssetResult, *AssetUpstreamError, error) {
	result, upErr, err := callAssetAPI(ctx, baseURL, key, proxy, assetActionCreate, params)
	if err != nil || upErr != nil {
		return nil, upErr, err
	}
	if result.Id == "" {
		return nil, nil, fmt.Errorf("upstream CreateAsset returned empty asset id")
	}
	return result, nil, nil
}

// GetAsset 代理上游查询素材（只读幂等，可安全轮询）。
func GetAsset(ctx context.Context, baseURL, key, proxy, assetId string) (*AssetResult, *AssetUpstreamError, error) {
	return callAssetAPI(ctx, baseURL, key, proxy, assetActionGet, map[string]string{"Id": assetId})
}

func callAssetAPI(parent context.Context, baseURL, key, proxy, action string, body any) (*AssetResult, *AssetUpstreamError, error) {
	payload, err := common.Marshal(body)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal %s request failed: %w", action, err)
	}

	uri := fmt.Sprintf("%s/?Action=%s&Version=%s", strings.TrimSuffix(baseURL, "/"), action, assetAPIVersion)
	ctx, cancel := context.WithTimeout(parent, assetRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uri, bytes.NewReader(payload))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("call upstream %s failed: %w", action, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, assetRespBodyLimit))
	if err != nil {
		return nil, nil, fmt.Errorf("read upstream %s response failed: %w", action, err)
	}

	var parsed assetAPIResponse
	if err := common.Unmarshal(respBody, &parsed); err != nil {
		if resp.StatusCode >= http.StatusBadRequest {
			// 非 JSON 错误响应（网关 5xx 等）：不透传原文，防泄露上游内部信息
			return nil, &AssetUpstreamError{HTTPStatus: resp.StatusCode, Code: "upstream_error",
				Message: fmt.Sprintf("upstream %s returned status %d", action, resp.StatusCode)}, nil
		}
		return nil, nil, fmt.Errorf("unmarshal upstream %s response failed: %w", action, err)
	}

	if parsed.ResponseMetadata.Error != nil {
		return nil, &AssetUpstreamError{
			HTTPStatus: resp.StatusCode,
			Code:       parsed.ResponseMetadata.Error.Code,
			Message:    parsed.ResponseMetadata.Error.Message,
		}, nil
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, &AssetUpstreamError{HTTPStatus: resp.StatusCode, Code: "upstream_error",
			Message: fmt.Sprintf("upstream %s returned status %d", action, resp.StatusCode)}, nil
	}

	return &parsed.Result, nil, nil
}

// ===== sd 网关风格素材协议（渠道类型 58，上游文档见 sd_real_max）=====
// POST {base}/v1/sd/assets 与 GET {base}/v1/sd/assets/{id}，
// 响应形如 {"success":bool,"data":{...素材字段...,"base_resp":{"status_code":0,"status_msg":"success"}}}。

type sdAssetEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		AssetResult
		BaseResp struct {
			StatusCode int    `json:"status_code"`
			StatusMsg  string `json:"status_msg"`
		} `json:"base_resp"`
	} `json:"data"`
}

// createAssetSd 上传素材（sd 网关旧体系，HC 空间）。请求体为 PascalCase {URL,Name,AssetType}。
// 注意：该接口不支持指定模型素材空间（2026-07-17 实测传 model 被上游忽略，素材固定进 HC
// 默认空间）；260128/ep/mini 系模型的素材须走 sd2 素材组体系（asset_sd2.go）。
func createAssetSd(ctx context.Context, baseURL, key, proxy string, params AssetCreateParams) (*AssetResult, *AssetUpstreamError, error) {
	body := map[string]string{
		"URL":       params.URL,
		"Name":      params.Name,
		"AssetType": params.AssetType,
	}
	if params.GroupId != "" {
		body["GroupId"] = params.GroupId
	}
	payload, err := common.Marshal(body)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal sd asset create request failed: %w", err)
	}
	uri := fmt.Sprintf("%s/v1/sd/assets", strings.TrimSuffix(baseURL, "/"))
	result, upErr, err := callSdAssetAPI(ctx, http.MethodPost, uri, key, proxy, payload)
	if err != nil || upErr != nil {
		return nil, upErr, err
	}
	if result.Id == "" {
		return nil, nil, fmt.Errorf("upstream sd asset create returned empty asset id")
	}
	return result, nil, nil
}

// getAssetSd 查询素材（sd 网关协议，只读幂等）。
func getAssetSd(ctx context.Context, baseURL, key, proxy, assetId string) (*AssetResult, *AssetUpstreamError, error) {
	uri := fmt.Sprintf("%s/v1/sd/assets/%s", strings.TrimSuffix(baseURL, "/"), assetId)
	return callSdAssetAPI(ctx, http.MethodGet, uri, key, proxy, nil)
}

func callSdAssetAPI(parent context.Context, method, uri, key, proxy string, payload []byte) (*AssetResult, *AssetUpstreamError, error) {
	ctx, cancel := context.WithTimeout(parent, assetRequestTimeout)
	defer cancel()
	var bodyReader io.Reader
	if payload != nil {
		bodyReader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, uri, bodyReader)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("call upstream sd asset api failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, assetRespBodyLimit))
	if err != nil {
		return nil, nil, fmt.Errorf("read upstream sd asset response failed: %w", err)
	}

	var parsed sdAssetEnvelope
	if err := common.Unmarshal(respBody, &parsed); err != nil {
		if resp.StatusCode >= http.StatusBadRequest {
			// 非 JSON 错误响应：不透传原文，防泄露上游内部信息
			return nil, &AssetUpstreamError{HTTPStatus: resp.StatusCode, Code: "upstream_error",
				Message: fmt.Sprintf("upstream sd asset api returned status %d", resp.StatusCode)}, nil
		}
		return nil, nil, fmt.Errorf("unmarshal upstream sd asset response failed: %w", err)
	}

	if !parsed.Success || parsed.Data.BaseResp.StatusCode != 0 || resp.StatusCode >= http.StatusBadRequest {
		msg := strings.TrimSpace(parsed.Data.BaseResp.StatusMsg)
		if msg == "" || strings.EqualFold(msg, "success") {
			msg = fmt.Sprintf("upstream sd asset api returned status %d", resp.StatusCode)
		}
		return nil, &AssetUpstreamError{
			HTTPStatus: resp.StatusCode,
			Code:       fmt.Sprintf("base_resp_%d", parsed.Data.BaseResp.StatusCode),
			Message:    msg,
		}, nil
	}

	return &parsed.Data.AssetResult, nil, nil
}
