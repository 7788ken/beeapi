package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
)

func TestSensitiveCollectorSubmitsBeforeEnteringHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldEnabled := setting.GetSensitiveAsyncEnabled()
	oldRate := setting.GetSensitiveSampleRate()
	setting.SetSensitiveAsyncEnabled(true)
	setting.SetSensitiveSampleRate(100)
	t.Cleanup(func() {
		setting.SetSensitiveAsyncEnabled(oldEnabled)
		setting.SetSensitiveSampleRate(oldRate)
	})

	submitStarted := make(chan struct{})
	releaseSubmit := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseSubmit) })
	})
	var handlerCalled atomic.Bool
	submitError := errors.New("late admission")
	submit := func(service.SensitiveAuditJob, []byte) error {
		close(submitStarted)
		<-releaseSubmit
		return submitError
	}

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("id", 1)
		c.Next()
	})
	router.Use(sensitiveCollector(submit))
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		handlerCalled.Store(true)
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"messages":[{"content":"test"}]}`),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		router.ServeHTTP(recorder, request)
		close(done)
	}()

	select {
	case <-submitStarted:
	case <-time.After(time.Second):
		t.Fatal("collector did not call Submit")
	}
	if handlerCalled.Load() {
		t.Fatal("handler ran before synchronous Submit returned")
	}
	releaseOnce.Do(func() { close(releaseSubmit) })
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("request did not continue after Submit returned")
	}
	if !handlerCalled.Load() {
		t.Fatal("handler did not run")
	}
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("response status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
}
