package doubao

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// db-sd-max 素材体系（上游文档 doubao-seedance-max，Method B 预上传）：/v2 max 线路
// （模型名以 -max 结尾，如 doubao-seedance-2-0-260128-max）专用的预上传素材库。
// 与另外两套互不相通：
//   - HC 旧体系  POST/GET /v1/sd/assets            （-hc 族，asset.go）
//   - sd2 素材组 POST /v1/asset-groups + /v1/assets（260128/ep/mini 非 max，asset_sd2.go）
//   - db-sd-max  POST /v2/db-sd-max/assets + GET /v2/db-sd-max/assets/{Id}（本文件）
//
// 线协议要点：
//   - 请求体 PascalCase {URL,Name,AssetType,Model?}，与 HC 旧体系一致；Model 可选，
//     指定后缩小素材适用范围，不填则适用于全部可用模型。
//   - 响应 {success,data:{Id,Ref,Status,...}}；Ref 即 asset://{Id}（渲染请求里直接引用）。
//   - Status 直接是对外统一状态 Processing/Active/Failed（无需大小写映射，但仍复用
//     mapSd2Status 做防御性归一，它对 PascalCase 输入幂等）。
//   - 无素材组、无处理 task_id：查询直接 GET /v2/db-sd-max/assets/{Id}。
//   - 端级错误 {success:false,message}；素材级失败 success:true 但 data.Status=Failed、
//     data.Error={Code,Message}（如敏感内容）。素材级失败仍返回 AssetResult{Status:Failed}，
//     与 sd2/HC 现状一致（AssetResult 白名单不含失败原因字段）。
const (
	sdMaxAssetPath       = "/v2/db-sd-max/assets"
	SdAssetProtocolSdMax = "sdmax" // 落库 SdAsset.Protocol，GET 查询按此分发
)

// IsSdMaxAssetModel 判断素材是否应走 db-sd-max 预上传体系（/v2/db-sd-max/assets）。
// /v2 max 线路模型名以 -max 结尾（doubao-/dreamina- 前缀均可）；须在 IsSd2AssetModel
// 之前判定 —— -max 模型同样不含 "-hc"，会被 IsSd2AssetModel 误判进 sd2 素材组体系。
func IsSdMaxAssetModel(modelName string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(modelName)), "-max")
}

// sdMaxAssetEnvelope db-sd-max 素材接口响应（create/get 共用）。
type sdMaxAssetEnvelope struct {
	Success bool   `json:"success"`
	Message string `json:"message"` // 端级错误消息（success=false 时）
	Data    struct {
		Id         string `json:"Id"`
		Ref        string `json:"Ref"`    // = asset://{Id}，渲染请求里可直接引用
		Status     string `json:"Status"` // Processing / Active / Failed
		AssetType  string `json:"AssetType"`
		Name       string `json:"Name"`
		Model      string `json:"Model"`
		URL        string `json:"URL"`
		Error      *struct {
			Code    string `json:"Code"`
			Message string `json:"Message"`
		} `json:"Error"`
		CreateTime string `json:"CreateTime"`
		UpdateTime string `json:"UpdateTime"`
	} `json:"data"`
}

// CreateAssetSdMax 上传素材到 db-sd-max 体系（异步：立即返回 Processing，须轮询至 Active）。
// params.Model 建议传上游映射后的模型名，用于缩小素材适用范围。
func CreateAssetSdMax(ctx context.Context, baseURL, key, proxy string, params AssetCreateParams) (*AssetResult, *AssetUpstreamError, error) {
	body := map[string]string{
		"URL":       params.URL,
		"Name":      params.Name,
		"AssetType": params.AssetType,
	}
	if params.Model != "" {
		body["Model"] = params.Model
	}
	payload, err := common.Marshal(body)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal db-sd-max asset create request failed: %w", err)
	}
	uri := fmt.Sprintf("%s%s", strings.TrimSuffix(baseURL, "/"), sdMaxAssetPath)
	result, upErr, err := callSdMaxAssetAPI(ctx, http.MethodPost, uri, key, proxy, payload)
	if err != nil || upErr != nil {
		return nil, upErr, err
	}
	if result.Id == "" {
		return nil, nil, fmt.Errorf("upstream db-sd-max asset create returned empty asset id")
	}
	return result, nil, nil
}

// GetAssetSdMax 查询 db-sd-max 素材状态（GET /v2/db-sd-max/assets/{Id}，只读幂等可轮询）。
func GetAssetSdMax(ctx context.Context, baseURL, key, proxy, assetId string) (*AssetResult, *AssetUpstreamError, error) {
	uri := fmt.Sprintf("%s%s/%s", strings.TrimSuffix(baseURL, "/"), sdMaxAssetPath, assetId)
	return callSdMaxAssetAPI(ctx, http.MethodGet, uri, key, proxy, nil)
}

// callSdMaxAssetAPI 发起 db-sd-max 素材请求并解析响应，复用 callSd2API 的传输层
// （超时/代理/鉴权头/响应体限长均一致），仅信封与错误形状不同。
func callSdMaxAssetAPI(parent context.Context, method, uri, key, proxy string, payload []byte) (*AssetResult, *AssetUpstreamError, error) {
	respBody, status, err := callSd2API(parent, method, uri, key, proxy, payload)
	if err != nil {
		return nil, nil, err
	}

	var parsed sdMaxAssetEnvelope
	if err := common.Unmarshal(respBody, &parsed); err != nil {
		if status >= http.StatusBadRequest {
			// 非 JSON 错误响应（网关 5xx 等）：不透传原文，防泄露上游内部信息
			return nil, &AssetUpstreamError{HTTPStatus: status, Code: "upstream_error",
				Message: fmt.Sprintf("upstream db-sd-max asset api returned status %d", status)}, nil
		}
		return nil, nil, fmt.Errorf("unmarshal upstream db-sd-max asset response failed: %w", err)
	}

	// 端级失败：HTTP >= 400 或 success=false。优先用 data.Error，其次顶层 message。
	if status >= http.StatusBadRequest || !parsed.Success {
		code, msg := "upstream_error", strings.TrimSpace(parsed.Message)
		if parsed.Data.Error != nil {
			if parsed.Data.Error.Code != "" {
				code = parsed.Data.Error.Code
			}
			if msg == "" {
				msg = strings.TrimSpace(parsed.Data.Error.Message)
			}
		}
		if msg == "" {
			msg = fmt.Sprintf("upstream db-sd-max asset api returned status %d", status)
		}
		return nil, &AssetUpstreamError{HTTPStatus: status, Code: code, Message: msg}, nil
	}

	return &AssetResult{
		Id:         parsed.Data.Id,
		Status:     mapSd2Status(parsed.Data.Status), // 已是 PascalCase，mapSd2Status 幂等归一
		AssetType:  parsed.Data.AssetType,
		Name:       parsed.Data.Name,
		URL:        parsed.Data.URL,
		CreateTime: parsed.Data.CreateTime,
		UpdateTime: parsed.Data.UpdateTime,
	}, nil, nil
}
