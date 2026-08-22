package channel

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 主 body 被写出（消费）后，GetBody 仍须交出完整 body，
// 这正是 HTTP/2 在上游 REFUSED_STREAM / 流重置后透明重放所依赖的契约。
func TestApplyUpstreamGetBodyReplaysFullBodyAfterPrimaryConsumed(t *testing.T) {
	payload := []byte(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	storage, err := common.CreateBodyStorage(append([]byte(nil), payload...))
	require.NoError(t, err)
	defer storage.Close()

	body := common.ReaderOnly(storage)
	req, err := http.NewRequest(http.MethodPost, "https://upstream.example.com/v1/chat/completions", body)
	require.NoError(t, err)
	require.Nil(t, req.GetBody, "net/http 无法为类型擦除的 body 推导 GetBody")

	applyUpstreamGetBody(req, body)
	require.NotNil(t, req.GetBody)

	consumed, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.Equal(t, payload, consumed)
	require.NoError(t, req.Body.Close())

	for i := 0; i < 2; i++ {
		replay, err := req.GetBody()
		require.NoError(t, err)
		replayed, err := io.ReadAll(replay)
		require.NoError(t, err)
		require.NoError(t, replay.Close())
		assert.Equal(t, payload, replayed, "第 %d 次重放必须是完整 body", i+1)
	}
}

// 不可重放的 body 必须让 GetBody 保持 nil：重试直接失败，
// 而不是像旧写法那样重放同一个已消费 reader 静默发出空 body。
func TestApplyUpstreamGetBodyLeavesNonReplayableBodyNil(t *testing.T) {
	body := struct{ io.Reader }{strings.NewReader(`{"prompt":"a"}`)}
	req, err := http.NewRequest(http.MethodPost, "https://upstream.example.com/v1/videos", body)
	require.NoError(t, err)

	applyUpstreamGetBody(req, body)
	assert.Nil(t, req.GetBody)
}

// *bytes.Reader 等 net/http 已自动推导 GetBody 的 body 不受影响。
func TestApplyUpstreamGetBodyKeepsDerivedGetBody(t *testing.T) {
	body := strings.NewReader(`{"prompt":"a"}`)
	req, err := http.NewRequest(http.MethodPost, "https://upstream.example.com/v1/videos", body)
	require.NoError(t, err)
	require.NotNil(t, req.GetBody)

	applyUpstreamGetBody(req, body)
	require.NotNil(t, req.GetBody)

	replay, err := req.GetBody()
	require.NoError(t, err)
	replayed, err := io.ReadAll(replay)
	require.NoError(t, err)
	assert.Equal(t, `{"prompt":"a"}`, string(replayed))
}
