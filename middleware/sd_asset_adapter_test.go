package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
)

func runSdAssetConvert(t *testing.T, body string) (*httptest.ResponseRecorder, *gin.Context, bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, err := http.NewRequest(http.MethodPost, "/v1/sd/assets", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.Request = req

	nextCalled := false
	c.Set("__test_next", &nextCalled)
	handler := SdAssetRequestConvert()
	handler(c)
	return w, c, !c.IsAborted()
}

func TestSdAssetConvertRejectsMissingURL(t *testing.T) {
	w, _, passed := runSdAssetConvert(t, `{"Name":"a","AssetType":"Image"}`)
	if passed || w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 abort, got code=%d passed=%v", w.Code, passed)
	}
	if !strings.Contains(w.Body.String(), `"success":false`) || !strings.Contains(w.Body.String(), "base_resp") {
		t.Fatalf("error should use sd base_resp shape, got: %s", w.Body.String())
	}
}

func TestSdAssetConvertRejectsNonHttpURL(t *testing.T) {
	w, _, passed := runSdAssetConvert(t, `{"URL":"ftp://example.com/a.jpg","Name":"a","AssetType":"Image"}`)
	if passed || w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 abort, got code=%d passed=%v", w.Code, passed)
	}
}

func TestSdAssetConvertRejectsBadAssetType(t *testing.T) {
	w, _, passed := runSdAssetConvert(t, `{"URL":"https://example.com/a.jpg","Name":"a","AssetType":"Document"}`)
	if passed || w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 abort, got code=%d passed=%v", w.Code, passed)
	}
	if !strings.Contains(w.Body.String(), "Image/Video/Audio") {
		t.Fatalf("expected asset type hint, got: %s", w.Body.String())
	}
}

func TestSdAssetConvertInjectsDefaultModel(t *testing.T) {
	_, c, passed := runSdAssetConvert(t, `{"URL":"https://example.com/a.jpg","Name":"a","AssetType":"Image"}`)
	if !passed {
		t.Fatalf("expected request to pass validation")
	}
	rewritten, ok := c.Get(common.KeyRequestBody)
	if !ok {
		t.Fatalf("rewritten body not cached in context")
	}
	body := string(rewritten.([]byte))
	if !strings.Contains(body, `"model":"doubao-seedance-2-0"`) {
		t.Fatalf("default model not injected, got: %s", body)
	}
	if !strings.Contains(body, `"URL":"https://example.com/a.jpg"`) {
		t.Fatalf("original fields lost, got: %s", body)
	}
	if IsSdAssetExplicitModel(c) {
		t.Fatalf("injected default model must NOT be marked explicit")
	}
}

func TestSdAssetConvertKeepsExplicitModel(t *testing.T) {
	_, c, passed := runSdAssetConvert(t, `{"URL":"https://example.com/a.jpg","Name":"a","AssetType":"Video","model":"doubao-seedance-2-0-fast"}`)
	if !passed {
		t.Fatalf("expected request to pass validation")
	}
	rewritten, _ := c.Get(common.KeyRequestBody)
	body := string(rewritten.([]byte))
	if !strings.Contains(body, `"model":"doubao-seedance-2-0-fast"`) {
		t.Fatalf("explicit model overridden, got: %s", body)
	}
	if !IsSdAssetExplicitModel(c) {
		t.Fatalf("explicit model must be marked explicit")
	}
}

func TestSdAssetConvertAcceptsLowercaseFieldNames(t *testing.T) {
	// Go JSON 大小写不敏感匹配："url"/"assettype" 同样可被解析
	_, _, passed := runSdAssetConvert(t, `{"url":"https://example.com/a.mp4","name":"clip","assettype":"Video"}`)
	if !passed {
		t.Fatalf("lowercase field names should be accepted")
	}
}
