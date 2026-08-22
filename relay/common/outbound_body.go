package common

import (
	"io"

	"github.com/QuantumNous/new-api/common"
)

// NewOutboundJSONBody wraps the already-marshaled upstream request body in
// BodyStorage so large payloads can spill to disk when disk cache is enabled.
//
// 返回的 body 经 common.ReaderOnly 包装：隐藏 io.Closer（storage 的关闭由调用方
// 通过返回的 closer 负责），同时保留 Size/NewReader，使请求构造侧能补回
// ContentLength 与 GetBody —— 后者让 HTTP/2 在上游重置流后可以透明重放。
func NewOutboundJSONBody(data []byte) (body io.Reader, size int64, closer io.Closer, err error) {
	storage, err := common.CreateBodyStorage(data)
	if err != nil {
		return nil, 0, nil, err
	}
	return common.ReaderOnly(storage), storage.Size(), storage, nil
}
