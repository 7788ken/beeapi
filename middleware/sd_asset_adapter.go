package middleware

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
)

const (
	sdAssetDefaultModelEnv = "SD_ASSET_DEFAULT_MODEL"
	sdAssetFallbackModel   = "doubao-seedance-2-0"
	// SdAssetExplicitModelKey context 标志：客户是否显式传了 model（决定是否透传上游）
	SdAssetExplicitModelKey = "sd_asset_explicit_model"
)

var validSdAssetTypes = map[string]struct{}{
	"Image": {},
	"Video": {},
	"Audio": {},
}

// abortWithSdAssetMessage 以 sd 素材接口的响应形状（success + base_resp）返回错误，
// 与 /v1/sd/assets 对外协议保持一致（status_code 非 0 表示失败，此处复用 HTTP 状态码）。
func abortWithSdAssetMessage(c *gin.Context, statusCode int, message string) {
	c.JSON(statusCode, gin.H{
		"success": false,
		"data": gin.H{
			"base_resp": dto.SdBaseResp{StatusCode: statusCode, StatusMsg: message},
		},
	})
	c.Abort()
}

// IsSdAssetExplicitModel 返回本次 /v1/sd/assets 请求是否由客户显式指定了 model。
func IsSdAssetExplicitModel(c *gin.Context) bool {
	return c.GetBool(SdAssetExplicitModelKey)
}

// SdAssetRequestConvert 解析并校验 /v1/sd/assets 上传请求（PascalCase 对齐 sd_real_max），
// 注入默认 model 后重写请求体，供后续 Distribute() 按 model 选择 doubao-video 渠道。
func SdAssetRequestConvert() func(c *gin.Context) {
	return func(c *gin.Context) {
		var req dto.SdAssetCreateRequest
		if err := common.UnmarshalBodyReusable(c, &req); err != nil {
			abortWithSdAssetMessage(c, http.StatusBadRequest, "invalid request body: "+common.MaskSensitiveInfo(err.Error()))
			return
		}

		if req.URL == "" || !(strings.HasPrefix(req.URL, "http://") || strings.HasPrefix(req.URL, "https://")) {
			abortWithSdAssetMessage(c, http.StatusBadRequest, "URL is required and must be a publicly downloadable http(s) address")
			return
		}
		if _, ok := validSdAssetTypes[req.AssetType]; !ok {
			abortWithSdAssetMessage(c, http.StatusBadRequest, "AssetType must be one of Image/Video/Audio")
			return
		}
		if req.Model == "" {
			req.Model = common.GetEnvOrDefaultString(sdAssetDefaultModelEnv, sdAssetFallbackModel)
			// 默认 model 仅用于渠道分发；未显式指定时不透传给上游（素材归上游默认空间）。
			// 显式传入的 model 会注册进对应模型的素材空间（跨空间引用会 asset not found）。
			c.Set(SdAssetExplicitModelKey, false)
		} else {
			c.Set(SdAssetExplicitModelKey, true)
		}

		jsonData, err := common.Marshal(req)
		if err != nil {
			abortWithSdAssetMessage(c, http.StatusInternalServerError, "failed to marshal request body")
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewBuffer(jsonData))
		c.Set(common.KeyRequestBody, jsonData)

		c.Next()
	}
}
