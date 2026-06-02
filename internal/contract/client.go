package contract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// ErrNotFound is returned by Client.Get when contract-plane responds 404.
var ErrNotFound = errors.New("contract: not found")

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

	// List returns the contract-plane list envelope for the given queue.
	// queue is one of contract-plane's accepted values: "all",
	// "awaiting_submission", "awaiting_plan", "running", "terminal".
	// Maps to GET /contracts?queue=<queue>&limit=<limit>. Returns the raw
	// upstream envelope (e.g. {"items": [...], "queue": "...", "count": N}).
	List(ctx context.Context, queue string, limit int) (map[string]any, error)

	// Get fetches a single workflow's detail.
	// Maps to GET /contracts/{wfID}. Returns the raw upstream JSON object.
	Get(ctx context.Context, wfID string) (map[string]any, error)
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

func (c *httpClient) List(ctx context.Context, queue string, limit int) (map[string]any, error) {
	path := "/contracts"
	q := url.Values{}
	if queue != "" {
		q.Set("queue", queue)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return c.getJSON(ctx, path)
}

func (c *httpClient) Get(ctx context.Context, wfID string) (map[string]any, error) {
	return c.getJSON(ctx, fmt.Sprintf("/contracts/%s", wfID))
}

func (c *httpClient) getJSON(ctx context.Context, path string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("contract-plane: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
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
