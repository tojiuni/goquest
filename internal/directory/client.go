package directory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Actor is the minimum lymphhub returns for a display-name lookup.
type Actor struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}

// Lookuper resolves a batch of requester/assignee ids to Actors. Implemented
// by both Client (talks to lymphhub) and CachedLookuper (wraps a Lookuper
// with a TTL cache) so the handler can depend on the interface, not the
// concrete client.
type Lookuper interface {
	Lookup(ctx context.Context, ids []string) ([]Actor, error)
}

// Client calls lymphhub's actor lookup endpoint, authenticating itself via
// a cached client_credentials access token (fetched from baseURL+"/oauth/token").
type Client struct {
	baseURL      string
	clientID     string
	clientSecret string
	http         *http.Client

	mu          sync.Mutex
	token       string
	tokenExpiry time.Time
}

func NewClient(baseURL, clientID, clientSecret string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		baseURL:      strings.TrimRight(baseURL, "/"),
		clientID:     clientID,
		clientSecret: clientSecret,
		http:         httpClient,
	}
}

func (c *Client) Lookup(ctx context.Context, ids []string) ([]Actor, error) {
	tok, err := c.accessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("lymphhub access token: %w", err)
	}

	body, err := json.Marshal(map[string]any{"ids": ids})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/actors/lookup", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("lymphhub actors lookup: status %d", resp.StatusCode)
	}

	var actors []Actor
	if err := json.NewDecoder(resp.Body).Decode(&actors); err != nil {
		return nil, err
	}
	return actors, nil
}

// accessToken fetches a client_credentials token once and reuses it until
// shortly before it expires. Not cross-process — each goquest replica keeps
// its own, which is fine at this token-request volume.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" && time.Now().Before(c.tokenExpiry) {
		return c.token, nil
	}

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("lymphhub token endpoint: status %d", resp.StatusCode)
	}

	var tokResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokResp); err != nil {
		return "", err
	}

	c.token = tokResp.AccessToken
	// Refresh 60s before actual expiry so a lookup never races an
	// about-to-expire token.
	c.tokenExpiry = time.Now().Add(time.Duration(tokResp.ExpiresIn)*time.Second - 60*time.Second)
	return c.token, nil
}
