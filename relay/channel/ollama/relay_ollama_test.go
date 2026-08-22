package ollama

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPullOllamaModelStreamStopsWhenContextIsCanceled(t *testing.T) {
	requestStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- PullOllamaModelStream(ctx, server.URL, "", "test-model", nil)
	}()

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("Ollama pull request did not start")
	}
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("PullOllamaModelStream() error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Ollama pull did not stop after context cancellation")
	}
}
