package helper

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newSystemOneContext(body string) *gin.Context {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

func TestGetAndValidateSystemOneRequestAcceptsWellFormed(t *testing.T) {
	body := `{"model":"jev-latest","state":{"doc":"hi"},"questions":{"q":{"type":"noul","instructions":"x"}}}`
	c := newSystemOneContext(body)
	req, err := GetAndValidateSystemOneRequest(c)
	require.NoError(t, err)
	require.Equal(t, "jev-latest", req.Model)
	require.Len(t, req.Questions, 1)

	// body 仍可复用：透传路径要把客户原文再读一遍
	storage, err := common.GetBodyStorage(c)
	require.NoError(t, err)
	raw, err := io.ReadAll(common.ReaderOnly(storage))
	require.NoError(t, err)
	require.JSONEq(t, body, string(raw))
}

func TestGetAndValidateSystemOneRequestRejectsBadShapes(t *testing.T) {
	cases := map[string]string{
		"missing model":       `{"state":"hi","questions":{"q":{"type":"noul"}}}`,
		"missing state":       `{"model":"jev-latest","questions":{"q":{"type":"noul"}}}`,
		"missing questions":   `{"model":"jev-latest","state":"hi"}`,
		"empty questions":     `{"model":"jev-latest","state":"hi","questions":{}}`,
		"question not object": `{"model":"jev-latest","state":"hi","questions":{"q":"is it a refund?"}}`,
	}
	for name, body := range cases {
		_, err := GetAndValidateSystemOneRequest(newSystemOneContext(body))
		require.Error(t, err, name)
	}
}

// 问题数量上限：整个 state 按每个问题重读计费，不设上限等于不设账单上限。
func TestGetAndValidateSystemOneRequestCapsQuestionCount(t *testing.T) {
	questions := map[string]any{}
	for i := 0; i <= dto.MaxSystemOneQuestions; i++ {
		questions[fmt.Sprintf("q%d", i)] = map[string]any{"type": "noul", "instructions": "x"}
	}
	raw, err := common.Marshal(map[string]any{"model": "jev-latest", "state": "hi", "questions": questions})
	require.NoError(t, err)
	_, err = GetAndValidateSystemOneRequest(newSystemOneContext(string(raw)))
	require.Error(t, err)
	require.Contains(t, err.Error(), "at most")
}
