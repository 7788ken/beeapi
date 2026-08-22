package httplifecycle

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestManagerDrainRejectsNewRequestsAndWaitsForAcceptedRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := NewManager()
	engine := gin.New()
	engine.Use(manager.Middleware())

	started := make(chan struct{})
	release := make(chan struct{})
	engine.GET("/work", func(c *gin.Context) {
		close(started)
		<-release
		c.Status(http.StatusNoContent)
	})

	firstDone := make(chan int, 1)
	go func() {
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/work", nil))
		firstDone <- recorder.Code
	}()
	<-started

	manager.BeginDrain()
	if manager.IsReady() {
		t.Fatal("manager remained ready after BeginDrain")
	}

	rejected := httptest.NewRecorder()
	engine.ServeHTTP(rejected, httptest.NewRequest(http.MethodGet, "/work", nil))
	if rejected.Code != http.StatusServiceUnavailable {
		t.Fatalf("rejected status = %d, want %d", rejected.Code, http.StatusServiceUnavailable)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := manager.WaitIdle(waitCtx); err == nil {
		t.Fatal("WaitIdle returned before accepted request completed")
	}

	close(release)
	if code := <-firstDone; code != http.StatusNoContent {
		t.Fatalf("accepted request status = %d, want %d", code, http.StatusNoContent)
	}
	idleCtx, idleCancel := context.WithTimeout(context.Background(), time.Second)
	defer idleCancel()
	if err := manager.WaitIdle(idleCtx); err != nil {
		t.Fatalf("WaitIdle() error = %v", err)
	}
}

func TestStreamContextIgnoresClientCancelUntilLongLivedCancellation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := NewManager()
	engine := gin.New()
	engine.Use(manager.Middleware())

	streamReady := make(chan context.Context, 1)
	streamDone := make(chan struct{})
	engine.GET("/stream", func(c *gin.Context) {
		streamCtx := StreamContext(c.Request.Context())
		streamReady <- streamCtx
		<-streamCtx.Done()
		close(streamDone)
	})

	parent, cancelParent := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/stream", nil).WithContext(parent)
	go engine.ServeHTTP(httptest.NewRecorder(), request)
	streamCtx := <-streamReady

	cancelParent()
	select {
	case <-streamCtx.Done():
		t.Fatal("stream context was canceled by client disconnect")
	case <-time.After(20 * time.Millisecond):
	}

	manager.BeginDrain()
	select {
	case <-streamCtx.Done():
		t.Fatal("BeginDrain canceled the stream before its normal grace window")
	case <-time.After(20 * time.Millisecond):
	}

	manager.CancelLongLived()
	select {
	case <-streamDone:
	case <-time.After(time.Second):
		t.Fatal("long-lived cancellation did not stop stream handler")
	}
}

func TestCancelLongLivedClosesRegisteredHijackedConnection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := NewManager()
	engine := gin.New()
	engine.Use(manager.Middleware())

	registered := make(chan struct{})
	closed := make(chan struct{})
	engine.GET("/ws", func(c *gin.Context) {
		unregister := RegisterHijacked(c.Request.Context(), func() {
			select {
			case <-closed:
			default:
				close(closed)
			}
		})
		defer unregister()
		close(registered)
		<-closed
	})

	go engine.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ws", nil))
	<-registered
	manager.BeginDrain()
	manager.CancelLongLived()

	idleCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.WaitIdle(idleCtx); err != nil {
		t.Fatalf("WaitIdle() error = %v", err)
	}
}
