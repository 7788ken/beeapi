package service

import (
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// 命中片段在落库时左右各保留多少字符（matched_snippet 摘要字段；正文已迁到 dump 文件）
const sensitiveSnippetContext = 60

// CompiledSensitiveWord 是从 DB 加载并预编译后的规则。供 middleware 旧版冷备 + 异步审计 worker 共用一份缓存。
type CompiledSensitiveWord struct {
	Id           int
	Pattern      string
	LowerPattern string // 普通词命中：预存 lowercase，避免热路径重复 ToLower
	IsRegex      bool
	Action       int
	UpdatedAt    int64
	Regex        *regexp.Regexp
}

// 守护范围：仅扫 LLM 文本类 JSON 路径（与旧版 SensitiveFilter 保持一致）。
var sensitiveGuardedPathPrefixes = []string{
	"/v1/chat/completions",
	"/v1/messages",
	"/v1/responses",
	"/v1/completions",
	"/v1/embeddings",
	"/v1/moderations",
	"/v1/rerank",
	"/v1beta/models/",
}

// IsSensitiveGuardedPath 判断当前请求路径是否在守护范围内。
func IsSensitiveGuardedPath(p string) bool {
	for _, prefix := range sensitiveGuardedPathPrefixes {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

var (
	sensitiveWordsCache   []CompiledSensitiveWord
	sensitiveWordsCacheMu sync.RWMutex
)

// LoadSensitiveWords 启动时与 CRUD 写操作后调用，刷新内存缓存。
func LoadSensitiveWords() {
	words, err := model.AllEnabledSensitiveWords()
	if err != nil {
		common.SysError("加载敏感词失败：" + err.Error())
		return
	}
	compiled := compileSensitiveWords(words)
	sensitiveWordsCacheMu.Lock()
	sensitiveWordsCache = compiled
	sensitiveWordsCacheMu.Unlock()
}

func loadSensitiveWordsFresh() ([]CompiledSensitiveWord, error) {
	words, err := model.AllEnabledSensitiveWords()
	if err != nil {
		return nil, err
	}
	return compileSensitiveWords(words), nil
}

func compileSensitiveWords(words []model.SensitiveWord) []CompiledSensitiveWord {
	compiled := make([]CompiledSensitiveWord, 0, len(words))
	for _, w := range words {
		// fail-safe：未知 action 默认按 Monitor（仅记录、不冻结 token）。
		// 已存在的 BLOCK 规则保留原行为；任何脏数据都退化为最小破坏的 Monitor。
		action := model.SensitiveActionMonitor
		if w.Action == model.SensitiveActionBlock {
			action = model.SensitiveActionBlock
		}
		entry := CompiledSensitiveWord{
			Id:        w.Id,
			Pattern:   w.Pattern,
			IsRegex:   w.IsRegex,
			Action:    action,
			UpdatedAt: w.UpdatedAt,
		}
		if w.IsRegex {
			re, err := regexp.Compile(w.Pattern)
			if err != nil {
				common.SysError("敏感词正则编译失败 id=" + strconv.Itoa(w.Id) + " pattern=" + w.Pattern + " err=" + err.Error())
				continue
			}
			entry.Regex = re
		} else {
			entry.LowerPattern = strings.ToLower(w.Pattern)
		}
		compiled = append(compiled, entry)
	}
	return compiled
}

// SensitiveWordsSnapshot 返回当前缓存的副本（slice 头副本，元素只读）。
func SensitiveWordsSnapshot() []CompiledSensitiveWord {
	sensitiveWordsCacheMu.RLock()
	defer sensitiveWordsCacheMu.RUnlock()
	if len(sensitiveWordsCache) == 0 {
		return nil
	}
	out := make([]CompiledSensitiveWord, len(sensitiveWordsCache))
	copy(out, sensitiveWordsCache)
	return out
}

// ScanSensitive 单条规则扫描，命中时返回 (摘要, true)。
func ScanSensitive(e CompiledSensitiveWord, body, lowerBody string) (string, bool) {
	if e.IsRegex {
		if e.Regex == nil {
			return "", false
		}
		loc := e.Regex.FindStringIndex(body)
		if loc == nil {
			return "", false
		}
		return ExtractSensitiveSnippet(body, loc[0], loc[1]), true
	}
	if e.LowerPattern == "" {
		return "", false
	}
	idx := strings.Index(lowerBody, e.LowerPattern)
	if idx < 0 {
		return "", false
	}
	return ExtractSensitiveSnippet(body, idx, idx+len(e.LowerPattern)), true
}

// ExtractSensitiveSnippet 取命中前后各 sensitiveSnippetContext 字符的摘要，硬限 480 字节（对齐表字段）。
func ExtractSensitiveSnippet(body string, start, end int) string {
	if start < 0 {
		start = 0
	}
	if end > len(body) {
		end = len(body)
	}
	left := start - sensitiveSnippetContext
	if left < 0 {
		left = 0
	}
	right := end + sensitiveSnippetContext
	if right > len(body) {
		right = len(body)
	}
	snippet := body[left:right]
	if len(snippet) > 480 {
		snippet = snippet[:480]
	}
	return snippet
}
