package service

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"

	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

const qualityEventStreamHeadersKey = "event_stream_headers_set"
const qualityHoldContextKey = "response_quality_hold"
const qualitySkipNoteKey = "quality_skip_stream_note"

// QualityHold 在质量闸门打开时先拦住写出。可见正文达到低 token 阈值且道歉前缀干净后放行，
// 之后的 Write 直接打到原 writer。未放行就命中道歉则丢掉缓冲，结束时再返回配置错误。
type QualityHold struct {
	c        *gin.Context
	info     *relaycommon.RelayInfo
	original gin.ResponseWriter
	writer   *qualityHoldWriter
	active   bool
	released bool
	blocked  bool
	finished bool
	blockErr *types.NewAPIError
}

func BeginQualityHold(c *gin.Context, info *relaycommon.RelayInfo) *QualityHold {
	hold := &QualityHold{c: c, info: info}
	if info != nil {
		info.ResetQualityInspect()
	}
	if c != nil {
		c.Set(qualityHoldContextKey, hold)
	}
	if c == nil || c.Writer == nil || !ResponseQualityActive(c, info) {
		return hold
	}
	original := c.Writer
	writer := newQualityHoldWriter(original)
	c.Writer = writer
	hold.original = original
	hold.writer = writer
	hold.active = true
	return hold
}

func (h *QualityHold) Released() bool {
	return h != nil && h.released
}

func (h *QualityHold) BlockedError() *types.NewAPIError {
	if h == nil || !h.blocked {
		return nil
	}
	return h.blockErr
}

func (h *QualityHold) Discard() {
	if h == nil || !h.active || h.finished || h.released {
		return
	}
	h.finished = true
	if h.info != nil {
		h.info.ClearSentResponse()
	}
	if h.c != nil {
		h.c.Writer = h.original
		delete(h.c.Keys, qualityEventStreamHeadersKey)
	}
}

func (h *QualityHold) Flush() error {
	if h == nil || !h.active || h.finished || h.blocked {
		return nil
	}
	h.released = true
	h.finished = true
	if h.c != nil {
		h.c.Writer = h.original
	}
	if h.writer == nil {
		return nil
	}
	return h.writer.flushTo(h.original)
}

type qualityHoldWriter struct {
	underlying gin.ResponseWriter
	header     http.Header
	buf        bytes.Buffer
	status     int
	flushed    bool
	drop       bool
}

func newQualityHoldWriter(underlying gin.ResponseWriter) *qualityHoldWriter {
	header := make(http.Header)
	if underlying != nil {
		for key, values := range underlying.Header() {
			header[key] = append([]string(nil), values...)
		}
	}
	return &qualityHoldWriter{
		underlying: underlying,
		header:     header,
		status:     http.StatusOK,
	}
}

var (
	_ gin.ResponseWriter = (*qualityHoldWriter)(nil)
	_ io.ReaderFrom      = (*qualityHoldWriter)(nil)
	_ io.StringWriter    = (*qualityHoldWriter)(nil)
)

func (w *qualityHoldWriter) Header() http.Header { return w.header }

func (w *qualityHoldWriter) WriteHeader(code int) {
	if w.flushed {
		w.underlying.WriteHeader(code)
		return
	}
	w.status = code
}

func (w *qualityHoldWriter) WriteHeaderNow() {}

func (w *qualityHoldWriter) Status() int {
	if w.flushed {
		return w.underlying.Status()
	}
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *qualityHoldWriter) Size() int {
	if w.flushed {
		return w.underlying.Size()
	}
	return w.buf.Len()
}

func (w *qualityHoldWriter) Written() bool {
	if w.flushed {
		return w.underlying.Written()
	}
	return false
}

func (w *qualityHoldWriter) Pusher() http.Pusher { return w.underlying.Pusher() }

func (w *qualityHoldWriter) CloseNotify() <-chan bool { return w.underlying.CloseNotify() }

func (w *qualityHoldWriter) Unwrap() http.ResponseWriter { return w.underlying }

func (w *qualityHoldWriter) Write(p []byte) (int, error) {
	if w.drop {
		return len(p), nil
	}
	if w.flushed {
		return w.underlying.Write(p)
	}
	return w.buf.Write(p)
}

func (w *qualityHoldWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *qualityHoldWriter) ReadFrom(r io.Reader) (int64, error) {
	return io.Copy(qualityHoldWriteOnly{w}, r)
}

type qualityHoldWriteOnly struct{ w io.Writer }

func (o qualityHoldWriteOnly) Write(p []byte) (int, error) { return o.w.Write(p) }

func (w *qualityHoldWriter) Flush() {
	if w.flushed {
		w.underlying.Flush()
	}
}

func (w *qualityHoldWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.underlying.Hijack()
}

func (w *qualityHoldWriter) flushTo(dst gin.ResponseWriter) error {
	if w.flushed || dst == nil {
		return nil
	}
	w.flushed = true
	for key, values := range w.header {
		dst.Header()[key] = append([]string(nil), values...)
	}
	if w.status != 0 {
		dst.WriteHeader(w.status)
	}
	if w.buf.Len() == 0 {
		return nil
	}
	_, err := dst.Write(w.buf.Bytes())
	if err != nil {
		return err
	}
	dst.Flush()
	return nil
}
