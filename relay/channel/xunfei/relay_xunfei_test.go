package xunfei

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/dto"
	"github.com/gorilla/websocket"
)

func TestXunfeiMakeRequestClosesWebSocketWhenContextIsCanceled(t *testing.T) {
	requestStarted := make(chan struct{})
	connectionClosed := make(chan error, 1)
	upgrader := websocket.Upgrader{
		CheckOrigin: func(*http.Request) bool { return true },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			connectionClosed <- err
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			connectionClosed <- err
			return
		}
		close(requestStarted)
		_, _, err = conn.ReadMessage()
		connectionClosed <- err
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	authURL := "ws" + strings.TrimPrefix(server.URL, "http")
	if _, _, err := xunfeiMakeRequest(ctx, dto.GeneralOpenAIRequest{}, "test", authURL, "app"); err != nil {
		t.Fatalf("xunfeiMakeRequest() error = %v", err)
	}
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("Xunfei request did not start")
	}

	cancel()
	select {
	case err := <-connectionClosed:
		if err == nil {
			t.Fatal("server WebSocket remained open after context cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("Xunfei WebSocket did not close after context cancellation")
	}
}
