package middleware

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func zstdCompress(t *testing.T, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer, err := zstd.NewWriter(&buf)
	require.NoError(t, err)
	_, err = writer.Write(payload)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return buf.Bytes()
}

func gzipCompress(t *testing.T, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	_, err := writer.Write(payload)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return buf.Bytes()
}

// settleGoroutines gives any in-flight goroutines a chance to finish before the
// count is sampled.
func settleGoroutines() {
	for i := 0; i < 5; i++ {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
	}
}

func TestDecompressRequestMiddlewareDecompressesZstd(t *testing.T) {
	gin.SetMode(gin.TestMode)

	payload := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hello zstd"}]}`)
	originalBody := &trackedRequestBody{Reader: bytes.NewReader(zstdCompress(t, payload))}

	request := httptest.NewRequest(http.MethodPost, "/relay", nil)
	request.Body = originalBody
	request.Header.Set("Content-Encoding", "zstd")

	recorder := httptest.NewRecorder()
	router := gin.New()
	router.POST("/relay", DecompressRequestMiddleware(), func(c *gin.Context) {
		got, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		assert.Equal(t, payload, got)
		// Downstream handlers must not see a stale Content-Encoding for a body
		// that is already plaintext.
		assert.Empty(t, c.GetHeader("Content-Encoding"))
		require.NoError(t, c.Request.Body.Close())
		c.Status(http.StatusNoContent)
	})

	router.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusNoContent, recorder.Code)
	assert.True(t, originalBody.closed, "closing the wrapped body must close the original body")
}

func TestDecompressRequestMiddlewareDecompressesGzip(t *testing.T) {
	gin.SetMode(gin.TestMode)

	payload := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hello gzip"}]}`)
	originalBody := &trackedRequestBody{Reader: bytes.NewReader(gzipCompress(t, payload))}

	request := httptest.NewRequest(http.MethodPost, "/relay", nil)
	request.Body = originalBody
	request.Header.Set("Content-Encoding", "gzip")

	recorder := httptest.NewRecorder()
	router := gin.New()
	router.POST("/relay", DecompressRequestMiddleware(), func(c *gin.Context) {
		got, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		assert.Equal(t, payload, got)
		assert.Empty(t, c.GetHeader("Content-Encoding"))
		require.NoError(t, c.Request.Body.Close())
		c.Status(http.StatusNoContent)
	})

	router.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusNoContent, recorder.Code)
	assert.True(t, originalBody.closed)
}

func TestDecompressRequestMiddlewareLeavesPlainBodyUntouched(t *testing.T) {
	gin.SetMode(gin.TestMode)

	payload := []byte(`{"model":"gpt-4"}`)

	request := httptest.NewRequest(http.MethodPost, "/relay", bytes.NewReader(payload))

	recorder := httptest.NewRecorder()
	router := gin.New()
	router.POST("/relay", DecompressRequestMiddleware(), func(c *gin.Context) {
		got, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		assert.Equal(t, payload, got)
		c.Status(http.StatusNoContent)
	})

	router.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusNoContent, recorder.Code)
}

func TestDecompressRequestMiddlewareSurfacesCorruptZstdAsReadError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	request := httptest.NewRequest(http.MethodPost, "/relay", bytes.NewReader([]byte("this is not a zstd frame")))
	request.Header.Set("Content-Encoding", "zstd")

	recorder := httptest.NewRecorder()
	router := gin.New()
	router.POST("/relay", DecompressRequestMiddleware(), func(c *gin.Context) {
		// zstd.NewReader only validates decoder options, so a corrupt payload is
		// reported when the body is read rather than at construction time.
		_, err := io.ReadAll(c.Request.Body)
		assert.Error(t, err)
		c.Status(http.StatusNoContent)
	})

	router.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusNoContent, recorder.Code)
}

func TestDecompressRequestMiddlewareZstdDoesNotLeakGoroutinesOnAbort(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// The payload must exceed the decoder's output-channel buffer, otherwise an
	// async decoder drains the whole stream and its goroutines exit on their own.
	// Measured threshold is between 64KB and 1MB decompressed; 1MB of repeated
	// text compresses to a couple of hundred bytes, which is precisely what makes
	// this cheap to abuse.
	compressed := zstdCompress(t, bytes.Repeat([]byte("leak-check-abcdef"), (1<<20)/17))

	router := gin.New()
	// Mirrors the production ordering in router.SetRelayRouter: decompression is
	// registered before TokenAuth, so a rejected request is aborted long before
	// common.GetRequestBody — the only thing that closes c.Request.Body — runs.
	router.POST("/relay", DecompressRequestMiddleware(), func(c *gin.Context) {
		c.AbortWithStatus(http.StatusUnauthorized)
	})

	settleGoroutines()
	baseline := runtime.NumGoroutine()

	const requests = 20
	for i := 0; i < requests; i++ {
		request := httptest.NewRequest(http.MethodPost, "/relay", bytes.NewReader(compressed))
		request.Header.Set("Content-Encoding", "zstd")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusUnauthorized, recorder.Code)
	}

	settleGoroutines()
	leaked := runtime.NumGoroutine() - baseline

	// Measured: a default-concurrency zstd.Decoder strands exactly three
	// goroutines per never-closed reader (60 here). They block forever writing to
	// an output channel nobody drains, so only Close() releases them.
	assert.Less(t, leaked, 10,
		"zstd decoding leaked %d goroutines across %d aborted requests", leaked, requests)
}
