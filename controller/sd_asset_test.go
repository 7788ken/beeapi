package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"

	"github.com/gin-gonic/gin"
)

func newSdAssetMappingCtx(t *testing.T, mappingJSON string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if mappingJSON != "" {
		common.SetContextKey(c, constant.ContextKeyChannelModelMapping, mappingJSON)
	}
	return c
}

func TestApplySdAssetModelMapping(t *testing.T) {
	// 无映射：原样返回
	c := newSdAssetMappingCtx(t, "")
	if got := applySdAssetModelMapping(c, "dreamina-seedance-2-0-ep"); got != "dreamina-seedance-2-0-ep" {
		t.Fatalf("no mapping should passthrough, got %q", got)
	}

	// 单级映射
	c = newSdAssetMappingCtx(t, `{"doubao-seedance-2-0":"dreamina-seedance-2-0-hc"}`)
	if got := applySdAssetModelMapping(c, "doubao-seedance-2-0"); got != "dreamina-seedance-2-0-hc" {
		t.Fatalf("mapping not applied, got %q", got)
	}
	// 不在映射表：原样
	if got := applySdAssetModelMapping(c, "dreamina-seedance-2-0-ep"); got != "dreamina-seedance-2-0-ep" {
		t.Fatalf("unmapped model should passthrough, got %q", got)
	}

	// 链式映射 a→b→c
	c = newSdAssetMappingCtx(t, `{"a":"b","b":"c"}`)
	if got := applySdAssetModelMapping(c, "a"); got != "c" {
		t.Fatalf("chained mapping should reach tail, got %q", got)
	}

	// 环：a→b→a 不死循环，停在环入口
	c = newSdAssetMappingCtx(t, `{"a":"b","b":"a"}`)
	if got := applySdAssetModelMapping(c, "a"); got != "b" {
		t.Fatalf("cycle should stop at visited, got %q", got)
	}

	// 非法 JSON：原样返回
	c = newSdAssetMappingCtx(t, `{bad json`)
	if got := applySdAssetModelMapping(c, "m"); got != "m" {
		t.Fatalf("invalid mapping json should passthrough, got %q", got)
	}
}
