package constant

import "strings"

// GeminiActionFromPath returns the native Gemini RPC after the last ':' in a
// /models/{name}:{action} path. Query strings and a trailing slash are ignored.
func GeminiActionFromPath(path string) string {
	path = strings.TrimSpace(path)
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	path = strings.TrimSuffix(path, "/")
	modelsPrefix := "/models/"
	modelsIndex := strings.Index(path, modelsPrefix)
	if modelsIndex == -1 {
		return ""
	}
	rest := path[modelsIndex+len(modelsPrefix):]
	colon := strings.LastIndex(rest, ":")
	if colon < 0 || colon == len(rest)-1 {
		return ""
	}
	action := rest[colon+1:]
	if i := strings.IndexByte(action, '/'); i >= 0 {
		action = action[:i]
	}
	return action
}

// IsUnsupportedGeminiCountTokensPath reports the native :countTokens RPC.
// BeeAPI does not implement it; the router must 404 before auth so the
// request is not rewritten into :generateContent (#7388 / BE-59).
func IsUnsupportedGeminiCountTokensPath(path string) bool {
	return strings.EqualFold(GeminiActionFromPath(path), "countTokens")
}
