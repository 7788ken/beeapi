package middleware

import (
	"bufio"
	"io"
	"net"
	"net/http"

	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// ContentBackupCollector bypass-captures the request and the bytes the gateway
// actually writes, without changing a single client visible byte, status code
// or flush. Mount it inside middleware.Distribute on the HTTP relay group only;
// realtime is a WebSocket route and must never get a capture.
func ContentBackupCollector() gin.HandlerFunc {
	return func(c *gin.Context) {
		capture := service.BeginContentBackupCapture(c)
		if capture == nil {
			c.Next()
			return
		}
		original := c.Writer
		c.Writer = ContentBackupWrapWriter(original, capture)
		// Finish before restoring the writer so the freeze still reads the live
		// status and headers, and always run it: a panicking handler must not
		// leak capture budget.
		defer func() {
			service.FinishContentBackupCapture(c)
			c.Writer = original
		}()
		c.Next()
	}
}

// ContentBackupWrapWriter observes a gin.ResponseWriter. It implements the full
// interface explicitly rather than embedding it, so no method can silently
// bypass the capture if gin grows one.
type contentBackupResponseWriter struct {
	writer  gin.ResponseWriter
	capture *service.ContentBackupCapture
}

var (
	_ gin.ResponseWriter = (*contentBackupResponseWriter)(nil)
	_ io.ReaderFrom      = (*contentBackupResponseWriter)(nil)
	_ io.StringWriter    = (*contentBackupResponseWriter)(nil)
)

func ContentBackupWrapWriter(writer gin.ResponseWriter, capture *service.ContentBackupCapture) gin.ResponseWriter {
	if capture == nil {
		return writer
	}
	return &contentBackupResponseWriter{writer: writer, capture: capture}
}

func ContentBackupCaptureWriter(writer gin.ResponseWriter) (*service.ContentBackupCapture, bool) {
	observer, ok := writer.(*contentBackupResponseWriter)
	if !ok {
		return nil, false
	}
	return observer.capture, true
}

func (w *contentBackupResponseWriter) Header() http.Header { return w.writer.Header() }

func (w *contentBackupResponseWriter) WriteHeader(code int) { w.writer.WriteHeader(code) }

func (w *contentBackupResponseWriter) WriteHeaderNow() { w.writer.WriteHeaderNow() }

func (w *contentBackupResponseWriter) Status() int { return w.writer.Status() }

func (w *contentBackupResponseWriter) Size() int { return w.writer.Size() }

func (w *contentBackupResponseWriter) Written() bool { return w.writer.Written() }

func (w *contentBackupResponseWriter) Pusher() http.Pusher { return w.writer.Pusher() }

func (w *contentBackupResponseWriter) CloseNotify() <-chan bool { return w.writer.CloseNotify() }

func (w *contentBackupResponseWriter) Unwrap() http.ResponseWriter { return w.writer }

// Write records only the n bytes the underlying writer accepted; a short write
// means the client never saw the rest, so the archive must not claim it did.
func (w *contentBackupResponseWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.capture.ObserveResponseWrite(p, n, err)
	return n, err
}

func (w *contentBackupResponseWriter) WriteString(s string) (int, error) {
	n, err := w.writer.WriteString(s)
	w.capture.ObserveResponseWriteString(s, n, err)
	return n, err
}

// ReadFrom must exist and must not delegate: io.Copy prefers dst.(io.ReaderFrom)
// over Write, so an observer that only implements Write still records every byte
// (io.Copy falls back to Write), while an observer that forwards to the
// underlying ReadFrom would bypass Write and lose the whole body, and one that
// records in both places would double count it. Routing back through Write keeps
// exactly one recording point and leaves the client bytes unchanged.
func (w *contentBackupResponseWriter) ReadFrom(r io.Reader) (int64, error) {
	return io.Copy(contentBackupWriteOnly{w}, r)
}

type contentBackupWriteOnly struct{ w io.Writer }

func (o contentBackupWriteOnly) Write(p []byte) (int, error) { return o.w.Write(p) }

func (w *contentBackupResponseWriter) Flush() {
	w.writer.Flush()
	w.capture.ObserveFlush()
}

func (w *contentBackupResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, buffered, err := w.writer.Hijack()
	if err == nil {
		w.capture.ObserveHijack()
	}
	return conn, buffered, err
}
