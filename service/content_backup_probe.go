package service

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// contentBackupShouldSkipProbeContext is the cheap, body-free gate used at
// Begin: platform channel tests and tokens that are explicitly named as probes.
func contentBackupShouldSkipProbeContext(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if common.GetContextKeyBool(c, constant.ContextKeyChannelTest) {
		return true
	}
	if c.GetInt("id") == model.ChannelTestUserId && c.GetInt("token_id") == 0 {
		return true
	}
	return contentBackupIsProbeTokenName(c.GetString("token_name"))
}

func contentBackupIsProbeTokenName(name string) bool {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return false
	}
	if trimmed == model.ChannelTestTokenName || trimmed == model.ChannelTestOfflineTokenName {
		return true
	}
	lower := strings.ToLower(trimmed)
	for _, marker := range []string{"测活", "探活", "healthcheck", "health-check", "health_check"} {
		if strings.Contains(lower, marker) || strings.Contains(trimmed, marker) {
			return true
		}
	}
	switch lower {
	case "ping", "health", "probe", "alive", "heartbeat":
		return true
	}
	return false
}

// contentBackupShouldSkipProbeCapture covers body-shaped downstream pings
// after freeze. Image endpoints stay eligible: the operator asked to keep
// generated images for now.
func contentBackupShouldSkipProbeCapture(c *gin.Context, capture *contentbackup.Capture) bool {
	if contentBackupShouldSkipProbeContext(c) {
		return true
	}
	if capture == nil {
		return false
	}
	return contentBackupIsProbeRequest(capture.Meta.Endpoint, capture.RequestChunks)
}

func contentBackupIsProbeRequest(endpoint string, chunks [][]byte) bool {
	switch endpoint {
	case "/v1/images/generations", "/v1/images/edits":
		return false
	}
	if len(chunks) == 0 {
		return false
	}
	body := string(joinContentBackupChunks(chunks))
	if strings.TrimSpace(body) == "" {
		return false
	}
	if !gjson.Valid(body) {
		return false
	}
	root := gjson.Parse(body)
	if contentBackupJSONHasNonProbeSystem(root) {
		return false
	}
	texts := contentBackupJSONUserTexts(root)
	if len(texts) == 0 {
		return false
	}
	for _, text := range texts {
		if !contentbackup.IsProbeUserText(text) {
			return false
		}
	}
	return true
}

func contentBackupJSONHasNonProbeSystem(root gjson.Result) bool {
	for _, msg := range root.Get("messages").Array() {
		role := strings.ToLower(msg.Get("role").String())
		if role != "system" && role != "developer" {
			continue
		}
		if strings.TrimSpace(contentBackupJSONMessageText(msg)) != "" {
			return true
		}
	}
	if strings.TrimSpace(root.Get("instructions").String()) != "" {
		return true
	}
	if strings.TrimSpace(root.Get("system").String()) != "" {
		return true
	}
	return false
}

func contentBackupJSONUserTexts(root gjson.Result) []string {
	var texts []string
	for _, msg := range root.Get("messages").Array() {
		role := strings.ToLower(msg.Get("role").String())
		if role != "" && role != "user" {
			continue
		}
		if role == "" {
			continue
		}
		if text := contentBackupJSONMessageText(msg); text != "" {
			texts = append(texts, text)
		}
	}
	if input := root.Get("input"); input.Exists() {
		if input.Type == gjson.String {
			if text := input.String(); text != "" {
				texts = append(texts, text)
			}
		} else {
			for _, item := range input.Array() {
				role := strings.ToLower(item.Get("role").String())
				if role != "" && role != "user" {
					continue
				}
				if text := contentBackupJSONMessageText(item); text != "" {
					texts = append(texts, text)
				} else if item.Type == gjson.String {
					texts = append(texts, item.String())
				}
			}
		}
	}
	return texts
}

func contentBackupJSONMessageText(msg gjson.Result) string {
	content := msg.Get("content")
	if !content.Exists() {
		return strings.TrimSpace(msg.Get("text").String())
	}
	if content.Type == gjson.String {
		return content.String()
	}
	var parts []string
	for _, part := range content.Array() {
		if part.Type == gjson.String {
			if text := strings.TrimSpace(part.String()); text != "" {
				parts = append(parts, text)
			}
			continue
		}
		if text := strings.TrimSpace(part.Get("text").String()); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func joinContentBackupChunks(chunks [][]byte) []byte {
	if len(chunks) == 0 {
		return nil
	}
	if len(chunks) == 1 {
		return chunks[0]
	}
	n := 0
	for _, chunk := range chunks {
		n += len(chunk)
	}
	out := make([]byte, 0, n)
	for _, chunk := range chunks {
		out = append(out, chunk...)
	}
	return out
}

func contentBackupRedactCapture(capture *contentbackup.Capture) {
	if capture == nil {
		return
	}
	rawName := capture.Meta.ChannelName
	capture.Meta.ChannelName = contentbackup.MaskLabel(rawName)
	if rawName != "" && rawName != capture.Meta.ChannelName {
		capture.RequestChunks = replaceExactChunks(capture.RequestChunks, rawName, capture.Meta.ChannelName)
		capture.ResponseChunks = replaceExactChunks(capture.ResponseChunks, rawName, capture.Meta.ChannelName)
	}
	if capture.Meta.SessionValue != nil {
		masked := contentbackup.RedactText(*capture.Meta.SessionValue)
		capture.Meta.SessionValue = &masked
	}
	capture.RequestChunks = contentbackup.RedactChunks(capture.RequestChunks)
	capture.ResponseChunks = contentbackup.RedactChunks(capture.ResponseChunks)
	refreshRedactedBodyMeta(&capture.Meta.Request, capture.RequestChunks)
	refreshRedactedBodyMeta(&capture.Meta.Response, capture.ResponseChunks)
}

func replaceExactChunks(chunks [][]byte, old, new string) [][]byte {
	if old == "" || old == new || len(chunks) == 0 {
		return chunks
	}
	joined := joinContentBackupChunks(chunks)
	replaced := []byte(strings.ReplaceAll(string(joined), old, new))
	if string(joined) == string(replaced) {
		return chunks
	}
	return [][]byte{replaced}
}

func refreshRedactedBodyMeta(body *contentbackup.BodyMeta, chunks [][]byte) {
	if body == nil {
		return
	}
	n := int64(len(joinContentBackupChunks(chunks)))
	body.CapturedBytes = n
	if body.ObservedBytes < n {
		body.ObservedBytes = n
	}
	if body.Truncated && body.ObservedBytes <= body.CapturedBytes {
		body.Truncated = false
	}
}
