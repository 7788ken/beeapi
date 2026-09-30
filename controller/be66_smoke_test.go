package controller

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestSmokeBE66VolcEngineFetchesAPIVersionedModels(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.URL.Path != "/api/v3/models" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"use /api/v3/models"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"doubao-pro-32k","object":"model"}]}`)
	}))
	t.Cleanup(srv.Close)

	base := srv.URL
	ids, err := fetchChannelUpstreamModelIDs(context.Background(), &model.Channel{
		Type:    constant.ChannelTypeVolcEngine,
		Key:     "sk-smoke-not-a-secret",
		BaseURL: &base,
	})
	require.NoError(t, err)
	require.Equal(t, "/api/v3/models", gotPath)
	require.Equal(t, []string{"doubao-pro-32k"}, ids)
}
