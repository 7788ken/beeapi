package setting

import (
	"strings"
	"sync/atomic"
)

var CheckSensitiveEnabled = true
var CheckSensitiveOnPromptEnabled = true

// 不良监控异步管道相关开关 —— 替换旧版 SensitiveFilter 同步阻断
var (
	// SensitiveAsyncEnabled 总开关：true 时挂 SensitiveCollector 走异步抽查；
	// false 时不挂任何审查中间件（旧版 SensitiveFilter 不再挂载，仅保留代码作冷备）
	sensitiveAsyncEnabled atomic.Bool
	// SensitiveSampleRate 采样百分比 0-100，命中采样后才落 dump 文件 + 入异步队列
	sensitiveSampleRate atomic.Int32
	// SensitiveDumpToFile 是否把 raw body 写到本地 dump 文件；关闭时丢弃本次审计（无内存 fallback）
	sensitiveDumpToFile atomic.Bool
	// SensitiveDumpRetentionDays dump 文件保留天数（cleaner cron 删除阈值）
	sensitiveDumpRetentionDays atomic.Int32
	// SensitiveDumpDiskGuardPercent 磁盘可用空间百分比阈值，低于此值自动关闭 SensitiveDumpToFile
	sensitiveDumpDiskGuardPercent atomic.Int32
)

func init() {
	sensitiveSampleRate.Store(20)
	sensitiveDumpToFile.Store(true)
	sensitiveDumpRetentionDays.Store(30)
	sensitiveDumpDiskGuardPercent.Store(20)
}

func GetSensitiveAsyncEnabled() bool  { return sensitiveAsyncEnabled.Load() }
func SetSensitiveAsyncEnabled(v bool) { sensitiveAsyncEnabled.Store(v) }
func GetSensitiveSampleRate() int     { return int(sensitiveSampleRate.Load()) }
func SetSensitiveSampleRate(v int) {
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	sensitiveSampleRate.Store(int32(v))
}
func GetSensitiveDumpToFile() bool       { return sensitiveDumpToFile.Load() }
func SetSensitiveDumpToFile(v bool)      { sensitiveDumpToFile.Store(v) }
func GetSensitiveDumpRetentionDays() int { return int(sensitiveDumpRetentionDays.Load()) }
func SetSensitiveDumpRetentionDays(v int) {
	if v < 0 {
		v = 0
	}
	sensitiveDumpRetentionDays.Store(int32(v))
}
func GetSensitiveDumpDiskGuardPercent() int { return int(sensitiveDumpDiskGuardPercent.Load()) }
func SetSensitiveDumpDiskGuardPercent(v int) {
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	sensitiveDumpDiskGuardPercent.Store(int32(v))
}

//var CheckSensitiveOnCompletionEnabled = true

// StopOnSensitiveEnabled 如果检测到敏感词，是否立刻停止生成，否则替换敏感词
var StopOnSensitiveEnabled = true

// StreamCacheQueueLength 流模式缓存队列长度，0表示无缓存
var StreamCacheQueueLength = 0

// SensitiveWords 敏感词
// var SensitiveWords []string
var SensitiveWords = []string{
	"test_sensitive",
}

func SensitiveWordsToString() string {
	return strings.Join(SensitiveWords, "\n")
}

func SensitiveWordsFromString(s string) {
	SensitiveWords = []string{}
	sw := strings.Split(s, "\n")
	for _, w := range sw {
		w = strings.TrimSpace(w)
		if w != "" {
			SensitiveWords = append(SensitiveWords, w)
		}
	}
}

func ShouldCheckPromptSensitive() bool {
	return CheckSensitiveEnabled && CheckSensitiveOnPromptEnabled
}

//func ShouldCheckCompletionSensitive() bool {
//	return CheckSensitiveEnabled && CheckSensitiveOnCompletionEnabled
//}
