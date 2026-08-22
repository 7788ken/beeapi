package dto

// SdAssetCreateRequest /v1/sd/assets 上传素材入参。
// 字段名 PascalCase 对齐上游 CreateAsset 与 sd_real_max 对外文档；
// Go JSON 匹配大小写不敏感，"url"/"name"/"assettype" 亦可被解析。
type SdAssetCreateRequest struct {
	URL       string `json:"URL"`
	Name      string `json:"Name"`
	AssetType string `json:"AssetType"` // Image / Video / Audio
	GroupId   string `json:"GroupId,omitempty"`
	// Model 可选：仅用于渠道分发（Distribute 按 model+分组选渠道），
	// 缺省由 SdAssetRequestConvert 注入 SD_ASSET_DEFAULT_MODEL（默认 doubao-seedance-2-0）。
	Model string `json:"model,omitempty"`
}

// SdBaseResp sd 素材接口响应中的 base_resp，status_code 为 0 表示成功。
type SdBaseResp struct {
	StatusCode int    `json:"status_code"`
	StatusMsg  string `json:"status_msg"`
}
