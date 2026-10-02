package common

import (
	"errors"
	"io"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedactURL(t *testing.T) {
	cases := map[string]string{
		"https://aiplatform.googleapis.com/v1/models/gemini:generateContent?key=AIzaSecret&alt=sse": "https://aiplatform.googleapis.com/v1/models/gemini:generateContent",
		"https://user:pass@upstream.example/v1/chat/completions":                                    "https://upstream.example/v1/chat/completions",
		"wss://upstream.example/v1/realtime?model=gpt-realtime&api_key=secret#frag":                 "wss://upstream.example/v1/realtime",
		"https://upstream.example/v1/models":                                                        "https://upstream.example/v1/models",
		"https://upstream.example/v1/x?":                                                            "https://upstream.example/v1/x",
		// 解析失败时按第一个问号截断
		"http://bad host/v1/files/abc:download?alt=media&key=AIzaSecret": "http://bad host/v1/files/abc:download",
		"http://upstream.example/v1/x?key=secret\x7f":                    "http://upstream.example/v1/x",
	}
	for raw, want := range cases {
		require.Equal(t, want, RedactURL(raw), raw)
	}
}

func TestRedactURLError(t *testing.T) {
	plain := errors.New("plain error with key=secret")
	require.Same(t, plain, RedactURLError(plain))

	cause := io.EOF
	raw := &url.Error{Op: "Post", URL: "https://upstream.example/v1/x?key=secret", Err: cause}
	redacted := RedactURLError(raw)

	var urlErr *url.Error
	require.True(t, errors.As(redacted, &urlErr))
	require.Equal(t, "Post", urlErr.Op)
	require.Equal(t, "https://upstream.example/v1/x", urlErr.URL)
	require.ErrorIs(t, redacted, cause)
	require.NotContains(t, redacted.Error(), "secret")
	require.Contains(t, raw.Error(), "secret", "原错误对象不应被修改")
}
