package githubapp

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrNotFound is returned when GitHub reports the path doesn't exist.
var ErrNotFound = errors.New("githubapp: file not found")

// Client fetches file content from private GitHub repos via a GitHub App
// installation access token (cached until shortly before expiry, same
// pattern as internal/directory.Client's lymphhub client_credentials cache).
type Client struct {
	baseURL        string
	appID          string
	installationID string
	privateKey     *rsa.PrivateKey
	http           *http.Client

	mu          sync.Mutex
	token       string
	tokenExpiry time.Time
}

// NewClient builds a githubapp Client against the real GitHub API.
func NewClient(appID, installationID, privateKeyPEM string, httpClient *http.Client) (*Client, error) {
	return newClientWithBaseURL("https://api.github.com", appID, installationID, privateKeyPEM, httpClient)
}

// newClientWithBaseURL is NewClient with an overridable base URL, so tests
// can point at an httptest.Server instead of the real GitHub API.
func newClientWithBaseURL(baseURL, appID, installationID, privateKeyPEM string, httpClient *http.Client) (*Client, error) {
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return nil, errors.New("githubapp: invalid PEM private key")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("githubapp: parse private key: %w", err)
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		baseURL:        strings.TrimRight(baseURL, "/"),
		appID:          appID,
		installationID: installationID,
		privateKey:     key,
		http:           httpClient,
	}, nil
}

// Content fetches a file's raw text content from owner/repo at path, using
// the GitHub Contents API (works for private repos, unlike raw.githubusercontent.com).
func (c *Client) Content(ctx context.Context, owner, repo, path string) (string, error) {
	tok, err := c.installationToken(ctx)
	if err != nil {
		return "", fmt.Errorf("githubapp: installation token: %w", err)
	}

	url := fmt.Sprintf("%s/repos/%s/%s/contents/%s", c.baseURL, owner, repo, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", ErrNotFound
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("githubapp: contents API: status %d", resp.StatusCode)
	}

	var body struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.Encoding != "base64" {
		return "", fmt.Errorf("githubapp: unexpected encoding %q", body.Encoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(body.Content, "\n", ""))
	if err != nil {
		return "", fmt.Errorf("githubapp: decode content: %w", err)
	}
	return string(decoded), nil
}

// installationToken exchanges a self-signed App JWT for an installation
// access token, reusing the cached one until shortly before it expires.
func (c *Client) installationToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" && time.Now().Before(c.tokenExpiry) {
		return c.token, nil
	}

	appJWT, err := c.appJWT()
	if err != nil {
		return "", fmt.Errorf("app JWT: %w", err)
	}

	url := fmt.Sprintf("%s/app/installations/%s/access_tokens", c.baseURL, c.installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("access_tokens endpoint: status %d", resp.StatusCode)
	}

	var tokResp struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokResp); err != nil {
		return "", err
	}
	expiry, err := time.Parse(time.RFC3339, tokResp.ExpiresAt)
	if err != nil {
		return "", fmt.Errorf("parse expires_at: %w", err)
	}

	c.token = tokResp.Token
	// Refresh 60s before actual expiry — same margin as directory.Client.
	c.tokenExpiry = expiry.Add(-60 * time.Second)
	return c.token, nil
}

// appJWT builds a short-lived (9-minute, under GitHub's 10-minute ceiling)
// RS256 JWT identifying this GitHub App, per GitHub App auth requirements.
func (c *Client) appJWT() (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Issuer:    c.appID,
		IssuedAt:  jwt.NewNumericDate(now.Add(-30 * time.Second)), // clock-skew allowance
		ExpiresAt: jwt.NewNumericDate(now.Add(9 * time.Minute)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(c.privateKey)
}
