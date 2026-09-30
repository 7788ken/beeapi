package helper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"

	"github.com/gin-gonic/gin"
)

// 内容备份归档靠 ContextKeyClientDisconnected 区分"流式断连"和"正常收尾"，
// 而这个标记的唯一来源就是下面这几个写出函数的拒写分支。零件测过不算数：
// 只要有一个分支忘了置位，真断连就会被记成 complete —— 比误报更危险。
func TestWriteRefusalMarksClientDisconnected(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name  string
		write func(c *gin.Context)
	}{
		{"FlushWriter", func(c *gin.Context) { _ = FlushWriter(c) }},
		{"ClaudeChunkData", func(c *gin.Context) {
			ClaudeChunkData(c, dto.ClaudeResponse{Type: "content_block_delta"}, "{}")
		}},
		{"ResponseChunkData", func(c *gin.Context) {
			_ = ResponseChunkData(c, dto.ResponsesStreamResponse{Type: "response.output_text.delta"}, "{}")
		}},
		{"StringData", func(c *gin.Context) { _ = StringData(c, "{}") }},
		{"PingData", func(c *gin.Context) { _ = PingData(c) }},
		// Done/ObjectData 都收敛到 StringData，一并覆盖出口
		{"Done", func(c *gin.Context) { Done(c) }},
		{"ObjectData", func(c *gin.Context) { _ = ObjectData(c, map[string]string{"a": "b"}) }},
	}

	for _, tc := range cases {
		t.Run(tc.name+"/客户端已断开", func(t *testing.T) {
			c, rec := newDisconnectTestContext(true)
			tc.write(c)
			if !common.GetContextKeyBool(c, constant.ContextKeyClientDisconnected) {
				t.Fatalf("%s 拒写时没有置位断连标记，归档会把真断连记成 complete", tc.name)
			}
			if rec.Body.Len() != 0 {
				t.Fatalf("%s 在客户端已断开时仍写出了 %d 字节", tc.name, rec.Body.Len())
			}
		})

		t.Run(tc.name+"/连接正常", func(t *testing.T) {
			c, _ := newDisconnectTestContext(false)
			tc.write(c)
			if common.GetContextKeyBool(c, constant.ContextKeyClientDisconnected) {
				t.Fatalf("%s 在连接正常时误置断连标记，正常请求会被记成不完整", tc.name)
			}
		})
	}
}

func newDisconnectTestContext(cancelled bool) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if cancelled {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c.Request = c.Request.WithContext(ctx)
	}
	return c, rec
}
