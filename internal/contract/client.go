package contract

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Client is the Phase A contract-plane proxy interface.
type Client interface {
	// Approve signals plan_approve on an existing contract workflow.
	// Maps to POST /contracts/{wfID}/approve with body {"approver": approver}.
	Approve(ctx context.Context, wfID, approver string) (map[string]any, error)

	// Abort cancels the workflow unconditionally.
	// Maps to POST /contracts/{wfID}/abort (no body).
	Abort(ctx context.Context, wfID string) (map[string]any, error)

	// SubmissionApprove approves the goal submission gate.
	// Maps to POST /goals/{wfID}/submission-approve with body {"approver": approver}.
	SubmissionApprove(ctx context.Context, wfID, approver string) (map[string]any, error)

	// Reject rejects at either the "submission" or "plan" stage.
	// stage=="submission" → POST /goals/{wfID}/reject
	// stage=="plan"       → POST /contracts/{wfID}/reject
	// Body: {"approver": approver, "stage": stage, "reason": reason}.
	Reject(ctx context.Context, wfID, approver, stage, reason string) (map[string]any, error)
}

type httpClient struct {
	base string
	hc   *http.Client
}

func NewClient(baseURL string, hc *http.Client) Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &httpClient{base: baseURL, hc: hc}
}

func (c *httpClient) Approve(ctx context.Context, wfID, approver string) (map[string]any, error) {
	return c.postJSON(ctx, fmt.Sprintf("/contracts/%s/approve", wfID),
		map[string]any{"approver": approver})
}

func (c *httpClient) Abort(ctx context.Context, wfID string) (map[string]any, error) {
	return c.postJSON(ctx, fmt.Sprintf("/contracts/%s/abort", wfID), nil)
}

func (c *httpClient) SubmissionApprove(ctx context.Context, wfID, approver string) (map[string]any, error) {
	return c.postJSON(ctx, fmt.Sprintf("/goals/%s/submission-approve", wfID),
		map[string]any{"approver": approver})
}

func (c *httpClient) Reject(ctx context.Context, wfID, approver, stage, reason string) (map[string]any, error) {
	var path string
	if stage == "submission" {
		path = fmt.Sprintf("/goals/%s/reject", wfID)
	} else {
		path = fmt.Sprintf("/contracts/%s/reject", wfID)
	}
	return c.postJSON(ctx, path, map[string]any{
		"approver": approver,
		"stage":    stage,
		"reason":   reason,
	})
}

func (c *httpClient) postJSON(ctx context.Context, path string, body any) (map[string]any, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("contract-plane: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("contract-plane: %d %s", resp.StatusCode, string(raw))
	}
	var out map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
	}
	return out, nil
}
