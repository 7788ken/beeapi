package middleware

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type trackedRequestBody struct {
	io.Reader
	closed bool
}

func (b *trackedRequestBody) Close() error {
	b.closed = true
	return nil
}

type failingRequestBody struct{}

func (failingRequestBody) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

func (failingRequestBody) Close() error {
	return nil
}

func withAnonymousRequestBodyLimit(t *testing.T, limitKB int) {
	t.Helper()
	original := constant.AnonymousRequestBodyLimitKB
	constant.AnonymousRequestBodyLimitKB = limitKB
	t.Cleanup(func() {
		constant.AnonymousRequestBodyLimitKB = original
	})
}

func TestAnonymousRequestBodyLimitPreservesRawBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withAnonymousRequestBodyLimit(t, 1)

	rawBody := strings.Repeat("signed-body-", 80)
	originalBody := &trackedRequestBody{Reader: strings.NewReader(rawBody)}
	request := httptest.NewRequest(http.MethodPost, "/webhook", nil)
	request.Body = originalBody
	request.ContentLength = -1

	recorder := httptest.NewRecorder()
	router := gin.New()
	router.POST("/webhook", AnonymousRequestBodyLimit(), func(c *gin.Context) {
		got, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		assert.Equal(t, rawBody, string(got))
		assert.Equal(t, int64(len(rawBody)), c.Request.ContentLength)
		c.Status(http.StatusNoContent)
	})

	router.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusNoContent, recorder.Code)
	assert.True(t, originalBody.closed)
}

func TestAnonymousRequestBodyLimitRejectsOversizeBeforeController(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withAnonymousRequestBodyLimit(t, 1)

	originalBody := &trackedRequestBody{Reader: strings.NewReader(strings.Repeat("x", 1025))}
	request := httptest.NewRequest(http.MethodPost, "/webhook", nil)
	request.Body = originalBody
	request.ContentLength = -1

	controllerCalled := false
	recorder := httptest.NewRecorder()
	router := gin.New()
	router.POST("/webhook", AnonymousRequestBodyLimit(), func(c *gin.Context) {
		controllerCalled = true
		c.Status(http.StatusNoContent)
	})

	router.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
	assert.False(t, controllerCalled)
	assert.True(t, originalBody.closed)
}

func TestAnonymousRequestBodyLimitRejectsUnreadableBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withAnonymousRequestBodyLimit(t, 1)

	request := httptest.NewRequest(http.MethodPost, "/webhook", nil)
	request.Body = failingRequestBody{}

	controllerCalled := false
	recorder := httptest.NewRecorder()
	router := gin.New()
	router.POST("/webhook", AnonymousRequestBodyLimit(), func(c *gin.Context) {
		controllerCalled = true
	})

	router.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.False(t, controllerCalled)
}

func TestAnonymousRequestBodyLimitCanBeDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withAnonymousRequestBodyLimit(t, 0)

	request := httptest.NewRequest(http.MethodPost, "/webhook", nil)
	request.Body = failingRequestBody{}

	controllerCalled := false
	recorder := httptest.NewRecorder()
	router := gin.New()
	router.POST("/webhook", AnonymousRequestBodyLimit(), func(c *gin.Context) {
		controllerCalled = true
		c.Status(http.StatusNoContent)
	})

	router.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusNoContent, recorder.Code)
	assert.True(t, controllerCalled)
}
