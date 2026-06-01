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

func TestClient_Approve_PostsApprover(t *testing.T) {
	var seen struct {
		Path string
		Body map[string]any
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&seen.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{"workflow_id": "wf1", "status": "approved"})
	}))
	defer ts.Close()

	c := NewClient(ts.URL, ts.Client())
	out, err := c.Approve(context.Background(), "wf1", "approver@example.com")
	require.NoError(t, err)
	assert.Equal(t, "/contracts/wf1/approve", seen.Path)
	assert.Equal(t, "approver@example.com", seen.Body["approver"])
	// legacy decision field must NOT be present
	_, hasDecision := seen.Body["decision"]
	assert.False(t, hasDecision, "decision field must be absent from Phase A approve body")
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
	_, err := c.Approve(context.Background(), "wf-missing", "approver@example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "contract-plane")
}

func TestClient_SubmissionApprove_PostsToGoalsPath(t *testing.T) {
	var seen struct {
		Path string
		Body map[string]any
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&seen.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{"workflow_id": "wf2", "status": "approved"})
	}))
	defer ts.Close()

	c := NewClient(ts.URL, ts.Client())
	out, err := c.SubmissionApprove(context.Background(), "wf2", "approver@example.com")
	require.NoError(t, err)
	assert.Equal(t, "/goals/wf2/submission-approve", seen.Path)
	assert.Equal(t, "approver@example.com", seen.Body["approver"])
	assert.Equal(t, "wf2", out["workflow_id"])
}

func TestClient_Reject_SubmissionRoutes_ToGoalsPath(t *testing.T) {
	var seen struct {
		Path string
		Body map[string]any
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&seen.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{"workflow_id": "wf3"})
	}))
	defer ts.Close()

	c := NewClient(ts.URL, ts.Client())
	_, err := c.Reject(context.Background(), "wf3", "approver@example.com", "submission", "not ready")
	require.NoError(t, err)
	assert.Equal(t, "/goals/wf3/reject", seen.Path)
	assert.Equal(t, "approver@example.com", seen.Body["approver"])
	assert.Equal(t, "submission", seen.Body["stage"])
	assert.Equal(t, "not ready", seen.Body["reason"])
}

func TestClient_Reject_PlanRoutes_ToContractsPath(t *testing.T) {
	var seen struct {
		Path string
		Body map[string]any
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&seen.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{"workflow_id": "wf4"})
	}))
	defer ts.Close()

	c := NewClient(ts.URL, ts.Client())
	_, err := c.Reject(context.Background(), "wf4", "approver@example.com", "plan", "budget exceeded")
	require.NoError(t, err)
	assert.Equal(t, "/contracts/wf4/reject", seen.Path)
	assert.Equal(t, "approver@example.com", seen.Body["approver"])
	assert.Equal(t, "plan", seen.Body["stage"])
	assert.Equal(t, "budget exceeded", seen.Body["reason"])
}
