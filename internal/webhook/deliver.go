package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

const (
	maxAttempts    = 3
	initialBackoff = 500 * time.Millisecond
	httpTimeout    = 10 * time.Second
)

var httpClient = &http.Client{Timeout: httpTimeout}

// Deliverer sends webhook POST requests.
type Deliverer struct{}

func New() *Deliverer { return &Deliverer{} }

// Send POSTs payload to url as JSON. Retries up to maxAttempts with exponential backoff.
// Intended to be called in a goroutine — ctx should be a background context.
func (d *Deliverer) Send(ctx context.Context, url string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	backoff := initialBackoff
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err = d.post(ctx, url, b)
		if err == nil {
			return nil
		}
		if attempt < maxAttempts {
			log.Printf("webhook attempt %d/%d failed (%v); retrying in %s", attempt, maxAttempts, err, backoff)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}
	}
	return fmt.Errorf("webhook delivery failed after %d attempts: %w", maxAttempts, err)
}

func (d *Deliverer) post(ctx context.Context, url string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "goquest-webhook/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("non-2xx status: %d", resp.StatusCode)
	}
	return nil
}
