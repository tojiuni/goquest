package contract

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type Client interface {
	Approve(ctx context.Context, wfID string, decision bool) (map[string]any, error)
	Abort(ctx context.Context, wfID string) (map[string]any, error)
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

func (c *httpClient) Approve(ctx context.Context, wfID string, decision bool) (map[string]any, error) {
	return c.postJSON(ctx, fmt.Sprintf("/contracts/%s/approve", wfID),
		map[string]any{"decision": decision})
}

func (c *httpClient) Abort(ctx context.Context, wfID string) (map[string]any, error) {
	return c.postJSON(ctx, fmt.Sprintf("/contracts/%s/abort", wfID), nil)
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
