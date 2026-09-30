package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRelayRouterRejectsGeminiCountTokensBeforeAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRelayRouter(engine)

	countTokensPaths := []string{
		"/v1beta/models/gemini-2.0-flash:countTokens",
		"/v1beta/models/gemini-2.0-flash:CountTokens",
		"/v1/models/gemini-2.0-flash:countTokens",
		"/v1beta/models/gemini-2.0-flash:countTokens?key=sk-should-not-be-used",
	}
	for _, path := range countTokensPaths {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"contents":[{"parts":[{"text":"hi"}]}]}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("x-goog-api-key", "sk-should-not-reach-auth")
			engine.ServeHTTP(rec, req)

			require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
			var body map[string]any
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			errObj, _ := body["error"].(map[string]any)
			require.NotNil(t, errObj)
			require.Equal(t, "invalid_request_error", errObj["type"])
			msg, _ := errObj["message"].(string)
			require.Contains(t, msg, "Invalid URL")
			require.Contains(t, strings.ToLower(msg), "counttokens")
			require.NotContains(t, rec.Body.String(), "generateContent")
		})
	}
}

func TestRelayRouterKeepsGeminiGenerateRoutesRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetRelayRouter(engine)

	keepPaths := []string{
		"/v1beta/models/gemini-2.0-flash:generateContent",
		"/v1beta/models/gemini-2.0-flash:streamGenerateContent",
		"/v1beta/models/gemini-2.0-flash:embedContent",
	}
	for _, path := range keepPaths {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"contents":[{"parts":[{"text":"hi"}]}]}`))
			req.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(rec, req)

			require.NotEqual(t, http.StatusNotFound, rec.Code, rec.Body.String())
			require.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
			require.NotContains(t, rec.Body.String(), "Invalid URL")
		})
	}
}
