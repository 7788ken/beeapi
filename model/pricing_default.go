package model

import (
	"strings"
)

// 简化的供应商映射规则。key 是模型名（小写后）里要匹配的子串。
// 匹配结果与本表的遍历顺序无关，多条规则同时命中时的判定规则见 matchDefaultVendor。
var defaultVendorRules = map[string]string{
	"gpt":      "OpenAI",
	"dall-e":   "OpenAI",
	"whisper":  "OpenAI",
	"o1":       "OpenAI",
	"o3":       "OpenAI",
	"claude":   "Anthropic",
	"gemini":   "Google",
	"moonshot": "Moonshot",
	"kimi":     "Moonshot",
	"chatglm":  "智谱",
	"glm-":     "智谱",
	"qwen":     "阿里巴巴",
	"deepseek": "DeepSeek",
	"abab":     "MiniMax",
	"ernie":    "百度",
	"spark":    "讯飞",
	"hunyuan":  "腾讯",
	"command":  "Cohere",
	"@cf/":     "Cloudflare",
	"360":      "360",
	"yi":       "零一万物",
	"jina":     "Jina",
	"mistral":  "Mistral",
	"grok":     "xAI",
	"llama":    "Meta",
	"doubao":   "字节跳动",
	"kling":    "快手",
	"jimeng":   "即梦",
	"vidu":     "Vidu",
}

// 供应商默认图标映射
var defaultVendorIcons = map[string]string{
	"OpenAI":     "OpenAI",
	"Anthropic":  "Claude.Color",
	"Google":     "Gemini.Color",
	"Moonshot":   "Moonshot",
	"智谱":         "Zhipu.Color",
	"阿里巴巴":       "Qwen.Color",
	"DeepSeek":   "DeepSeek.Color",
	"MiniMax":    "Minimax.Color",
	"百度":         "Wenxin.Color",
	"讯飞":         "Spark.Color",
	"腾讯":         "Hunyuan.Color",
	"Cohere":     "Cohere.Color",
	"Cloudflare": "Cloudflare.Color",
	"360":        "Ai360.Color",
	"零一万物":       "Yi.Color",
	"Jina":       "Jina",
	"Mistral":    "Mistral.Color",
	"xAI":        "XAI",
	"Meta":       "Ollama",
	"字节跳动":       "Doubao.Color",
	"快手":         "Kling.Color",
	"即梦":         "Jimeng.Color",
	"Vidu":       "Vidu",
	"微软":         "AzureAI",
	"Microsoft":  "AzureAI",
	"Azure":      "AzureAI",
}

// initDefaultVendorMapping 简化的默认供应商映射
func initDefaultVendorMapping(metaMap map[string]*Model, vendorMap map[int]*Vendor, enableAbilities []AbilityWithChannel) {
	for _, ability := range enableAbilities {
		modelName := ability.Model
		if _, exists := metaMap[modelName]; exists {
			continue
		}

		// 匹配供应商
		vendorID := 0
		if vendorName := matchDefaultVendor(strings.ToLower(modelName)); vendorName != "" {
			vendorID = getOrCreateVendor(vendorName, vendorMap)
		}

		// 创建模型元数据
		metaMap[modelName] = &Model{
			ModelName: modelName,
			VendorID:  vendorID,
			Status:    1,
			NameRule:  NameRuleExact,
		}
	}
}

// matchDefaultVendor 按模型名匹配默认供应商，未命中任何规则时返回空串。
//
// 一个模型名经常同时命中多条规则，必须由确定的判定规则选出唯一赢家：
// 否则遍历顺序（Go map 遍历顺序随机）决定归属，同一模型在不同进程、不同节点上
// 会被判给不同供应商，且每次重启都可能变。判定规则按序应用：
//
//  1. 命中位置更靠左的规则赢。模型名的约定是 `<厂商/家族>-<版本>-<修饰符>`，
//     厂商标识出现在最前面，靠后的 token 是变体或路由修饰符，不代表归属。
//     例：360gpt-pro 归 360 而不是 OpenAI；yi-spark 归零一万物而不是讯飞；
//     gpt-5.3-codex-spark-openai-compact 归 OpenAI 而不是讯飞。
//  2. 位置相同时 pattern 更长的赢，即更具体的规则赢。位置相同意味着短的那条
//     必然是长的那条的前缀，所以 (位置, 长度) 对任意两条不同规则都不同，赢家唯一。
//     当前规则表里没有互为前缀的 pattern，这条只在后续新增规则时才会生效。
func matchDefaultVendor(modelLower string) string {
	bestVendor := ""
	bestIndex, bestLen := 0, 0
	for pattern, vendorName := range defaultVendorRules {
		index := strings.Index(modelLower, pattern)
		if index < 0 {
			continue
		}
		if bestVendor == "" || index < bestIndex || (index == bestIndex && len(pattern) > bestLen) {
			bestVendor, bestIndex, bestLen = vendorName, index, len(pattern)
		}
	}
	return bestVendor
}

// 查找或创建供应商
func getOrCreateVendor(vendorName string, vendorMap map[int]*Vendor) int {
	// 查找现有供应商
	for id, vendor := range vendorMap {
		if vendor.Name == vendorName {
			return id
		}
	}

	// 创建新供应商
	newVendor := &Vendor{
		Name:   vendorName,
		Status: 1,
		Icon:   getDefaultVendorIcon(vendorName),
	}

	if err := newVendor.Insert(); err != nil {
		return 0
	}

	vendorMap[newVendor.Id] = newVendor
	return newVendor.Id
}

// 获取供应商默认图标
func getDefaultVendorIcon(vendorName string) string {
	if icon, exists := defaultVendorIcons[vendorName]; exists {
		return icon
	}
	return ""
}
