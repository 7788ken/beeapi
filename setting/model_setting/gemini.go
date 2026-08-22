package model_setting

import (
	"strings"

	"github.com/QuantumNous/new-api/setting/config"
)

// GeminiSettings defines Gemini model configuration. 注意bool要以enabled结尾才可以生效编辑
type GeminiSettings struct {
	SafetySettings                        map[string]string `json:"safety_settings"`
	VersionSettings                       map[string]string `json:"version_settings"`
	SupportedImagineModels                []string          `json:"supported_imagine_models"`
	SupportedMimeTypes                    []string          `json:"supported_mime_types"`
	ThinkingAdapterEnabled                bool              `json:"thinking_adapter_enabled"`
	ThinkingAdapterBudgetTokensPercentage float64           `json:"thinking_adapter_budget_tokens_percentage"`
	FunctionCallThoughtSignatureEnabled   bool              `json:"function_call_thought_signature_enabled"`
	RemoveFunctionResponseIdEnabled       bool              `json:"remove_function_response_id_enabled"`
}

// 默认配置
var defaultGeminiSettings = GeminiSettings{
	SafetySettings: map[string]string{
		"default": "OFF",
	},
	VersionSettings: map[string]string{
		"default":        "v1beta",
		"gemini-1.0-pro": "v1",
	},
	SupportedImagineModels: []string{
		"gemini-2.0-flash-exp-image-generation",
		"gemini-2.0-flash-exp",
		"gemini-3-pro-image-preview",
		"gemini-3-pro-image-preview-t",
		"gemini-3-pro-image",
		"gemini-2.5-flash-image",
		"gemini-3.1-flash-image",
		"gemini-3.1-flash-image-preview",
		"nanobanana2",
		"nanobananapro",
	},
	// 允许透传给 Gemini 的媒体 MIME 类型；配置为空数组时不做本地校验，由 Google 侧判定。
	// 音频清单来源: ai.google.dev/gemini-api/docs/audio 与
	// docs.cloud.google.com/vertex-ai/generative-ai/docs/multimodal/audio-understanding
	SupportedMimeTypes: []string{
		"application/pdf",
		"audio/mpeg",
		"audio/mp3",
		"audio/wav",
		"audio/aiff",
		"audio/aac",
		"audio/x-aac",
		"audio/ogg",
		"audio/flac",
		"audio/m4a",
		"audio/mpga",
		"audio/mp4",
		"audio/pcm",
		"audio/webm",
		"image/png",
		"image/jpeg",
		"image/jpg",
		"image/webp",
		"image/heic",
		"image/heif",
		"text/plain",
		"video/mov",
		"video/mpeg",
		"video/mp4",
		"video/mpg",
		"video/avi",
		"video/wmv",
		"video/mpegps",
		"video/flv",
	},
	ThinkingAdapterEnabled:                false,
	ThinkingAdapterBudgetTokensPercentage: 0.6,
	FunctionCallThoughtSignatureEnabled:   true,
	RemoveFunctionResponseIdEnabled:       true,
}

// 全局实例
var geminiSettings = defaultGeminiSettings

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("gemini", &geminiSettings)
}

// GetGeminiSettings 获取Gemini配置
func GetGeminiSettings() *GeminiSettings {
	return &geminiSettings
}

// GetGeminiSafetySetting 获取安全设置
func GetGeminiSafetySetting(key string) string {
	if value, ok := geminiSettings.SafetySettings[key]; ok {
		return value
	}
	return geminiSettings.SafetySettings["default"]
}

// GetGeminiVersionSetting 获取版本设置
func GetGeminiVersionSetting(key string) string {
	if value, ok := geminiSettings.VersionSettings[key]; ok {
		return value
	}
	return geminiSettings.VersionSettings["default"]
}

func IsGeminiModelSupportImagine(model string) bool {
	for _, v := range geminiSettings.SupportedImagineModels {
		if v == model {
			return true
		}
	}
	return false
}

// IsGeminiSupportedMimeType 判断媒体 MIME 是否允许透传给 Gemini；列表为空数组时不做本地校验
func IsGeminiSupportedMimeType(mimeType string) bool {
	if len(geminiSettings.SupportedMimeTypes) == 0 {
		return true
	}
	mimeType = strings.ToLower(mimeType)
	for _, v := range geminiSettings.SupportedMimeTypes {
		if strings.ToLower(v) == mimeType {
			return true
		}
	}
	return false
}

// GetGeminiSupportedMimeTypes 返回当前配置的 MIME 白名单（用于错误提示）
func GetGeminiSupportedMimeTypes() []string {
	return geminiSettings.SupportedMimeTypes
}
