package contract

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_Approve_PostsDecision(t *testing.T) {
	var seen struct {
		Path string
		Body map[string]any
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&seen.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{"workflow_id": "wf1", "decision": true})
	}))
	defer ts.Close()

	c := NewClient(ts.URL, ts.Client())
	out, err := c.Approve(context.Background(), "wf1", true)
	require.NoError(t, err)
	assert.Equal(t, "/contracts/wf1/approve", seen.Path)
	assert.Equal(t, true, seen.Body["decision"])
	assert.Equal(t, "wf1", out["workflow_id"])
}

func TestClient_Abort_PostsToCorrectPath(t *testing.T) {
	var seenPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		_, _ = w.Write([]byte(`{"state":"aborting"}`))
	}))
	defer ts.Close()
	c := NewClient(ts.URL, ts.Client())
	_, err := c.Abort(context.Background(), "wf1")
	require.NoError(t, err)
	assert.Equal(t, "/contracts/wf1/abort", seenPath)
}

func TestClient_Approve_ReturnsErrorOn4xx(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"detail":"not found"}`))
	}))
	defer ts.Close()
	c := NewClient(ts.URL, ts.Client())
	_, err := c.Approve(context.Background(), "wf-missing", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "contract-plane")
}
