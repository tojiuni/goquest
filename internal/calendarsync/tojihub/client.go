// Package tojihub implements calendarsync.CalendarClient against tojihub's
// v1 Connect API (a plain JSON POST today, not real Connect-RPC framing —
// see geneso:neunexus/plan/2026-08-09-tojihub-caldav-v1.md Task 12. When
// tojihub adopts real Connect-RPC (infra-tojihub-connect-real-protocol),
// only this file's HTTP calls change — callers of this package are
// unaffected, since they only see the calendarsync.CalendarClient
// interface.
package tojihub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/tojiuni/goquest/internal/calendarsync"
)

const httpTimeout = 10 * time.Second

var httpClient = &http.Client{Timeout: httpTimeout}

// Client implements calendarsync.CalendarClient against a tojihub deployment.
type Client struct {
	baseURL      string
	serviceToken string
}

// New returns a Client. baseURL is tojihub's base address (e.g.
// "http://tojihub.tojihub.svc.cluster.local:8080", no trailing slash).
// serviceToken is the shared secret tojihub's Connect handlers check via
// the X-Tojihub-Service-Token header (Vault
// secret/neunexus/tojihub#connect_service_token).
func New(baseURL, serviceToken string) *Client {
	return &Client{baseURL: baseURL, serviceToken: serviceToken}
}

type upsertEventRequest struct {
	Principal  string `json:"principal"`
	CalendarID string `json:"calendar_id"`
	ObjectID   string `json:"object_id"`
	ICalData   string `json:"ical_data"`
}

// UpsertEvent creates or updates ev's calendar event on tojihub.
// CalendarID is always "default" — tojihub's v1 doesn't support
// per-user multiple calendars yet.
func (c *Client) UpsertEvent(ctx context.Context, ev calendarsync.Event) error {
	body, err := json.Marshal(upsertEventRequest{
		Principal:  ev.Principal,
		CalendarID: "default",
		ObjectID:   ev.ObjectID,
		ICalData:   toICal(ev),
	})
	if err != nil {
		return fmt.Errorf("tojihub: marshal UpsertEvent request: %w", err)
	}
	return c.post(ctx, "/connect/v1/UpsertEvent", body)
}

type deleteEventRequest struct {
	Principal  string `json:"principal"`
	CalendarID string `json:"calendar_id"`
	ObjectID   string `json:"object_id"`
}

// DeleteEvent removes a calendar event on tojihub. tojihub does not
// implement this endpoint yet (infra-tojihub-goquest-delete-event) — until
// it does, this returns a non-2xx error for every call, which callers
// (ticket.Service) already log-and-continue on.
func (c *Client) DeleteEvent(ctx context.Context, principal, objectID string) error {
	body, err := json.Marshal(deleteEventRequest{
		Principal:  principal,
		CalendarID: "default",
		ObjectID:   objectID,
	})
	if err != nil {
		return fmt.Errorf("tojihub: marshal DeleteEvent request: %w", err)
	}
	return c.post(ctx, "/connect/v1/DeleteEvent", body)
}

func (c *Client) post(ctx context.Context, path string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("tojihub: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tojihub-Service-Token", c.serviceToken)

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("tojihub: %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("tojihub: %s: non-2xx status %d", path, resp.StatusCode)
	}
	return nil
}
