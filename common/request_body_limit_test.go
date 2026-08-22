package common

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
)

func TestGetAnonymousRequestBodyLimitBytes(t *testing.T) {
	original := constant.AnonymousRequestBodyLimitKB
	t.Cleanup(func() {
		constant.AnonymousRequestBodyLimitKB = original
	})

	constant.AnonymousRequestBodyLimitKB = 128
	assert.Equal(t, int64(128<<10), GetAnonymousRequestBodyLimitBytes())

	constant.AnonymousRequestBodyLimitKB = 0
	assert.Zero(t, GetAnonymousRequestBodyLimitBytes())

	constant.AnonymousRequestBodyLimitKB = -1
	assert.Equal(t, int64(512<<10), GetAnonymousRequestBodyLimitBytes())
}
