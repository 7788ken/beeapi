package common

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ReaderOnly 交出的 body 必须：不暴露 Closer、能重复交出游标独立的完整副本、
// 副本关闭不影响 storage、storage 关闭后不再交出失效 reader。
func TestReaderOnlyHandsOutIndependentReplayReaders(t *testing.T) {
	payload := []byte(`{"model":"test-model","messages":[{"role":"user","content":"hello"}]}`)

	cases := []struct {
		name string
		open func(t *testing.T) BodyStorage
	}{
		{
			name: "memory",
			open: func(t *testing.T) BodyStorage {
				return newMemoryStorage(append([]byte(nil), payload...))
			},
		},
		{
			name: "disk",
			open: func(t *testing.T) BodyStorage {
				storage, err := newDiskStorage(append([]byte(nil), payload...), GetDiskCachePath())
				require.NoError(t, err)
				return storage
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			storage := tc.open(t)
			defer storage.Close()

			body := ReaderOnly(storage)
			require.EqualValues(t, len(payload), body.Size())

			_, exposesCloser := any(body).(io.Closer)
			require.False(t, exposesCloser, "出站 body 不得暴露 storage 的 Closer")

			first, err := body.NewReader()
			require.NoError(t, err)
			second, err := body.NewReader()
			require.NoError(t, err)

			// 交叉读：主 body 与两个副本各自持有游标，互不干扰
			primary := make([]byte, 8)
			_, err = io.ReadFull(body, primary)
			require.NoError(t, err)
			assert.Equal(t, payload[:8], primary)

			head := make([]byte, 4)
			_, err = io.ReadFull(first, head)
			require.NoError(t, err)
			assert.Equal(t, payload[:4], head)

			secondAll, err := io.ReadAll(second)
			require.NoError(t, err)
			assert.Equal(t, payload, secondAll)
			require.NoError(t, second.Close())

			firstRest, err := io.ReadAll(first)
			require.NoError(t, err)
			assert.Equal(t, payload[4:], firstRest)
			require.NoError(t, first.Close())

			// 副本关闭只释放副本自身：仍可再开一个完整副本
			third, err := body.NewReader()
			require.NoError(t, err)
			thirdAll, err := io.ReadAll(third)
			require.NoError(t, err)
			assert.Equal(t, payload, thirdAll)
			require.NoError(t, third.Close())

			// transport 关闭 req.Body 不得连带关掉 storage
			req, err := http.NewRequest(http.MethodPost, "https://example.com", body)
			require.NoError(t, err)
			require.NoError(t, req.Body.Close())
			afterReqClose, err := body.NewReader()
			require.NoError(t, err)
			require.NoError(t, afterReqClose.Close())

			// storage 关闭后必须报错，而不是交出失效 reader
			require.NoError(t, storage.Close())
			_, err = body.NewReader()
			require.ErrorIs(t, err, ErrStorageClosed)
		})
	}
}
