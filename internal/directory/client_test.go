package directory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLymphhub serves both the OAuth token endpoint and the lookup endpoint,
// recording what it saw so tests can assert on the request shape.
func fakeLymphhub(t *testing.T, lookupResp []Actor) (*httptest.Server, *[]string) {
	t.Helper()
	var seenBearers []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "tok-1",
				"expires_in":   3600,
			})
		case "/v1/actors/lookup":
			seenBearers = append(seenBearers, r.Header.Get("Authorization"))
			var body struct {
				IDs []string `json:"ids"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(lookupResp)
		default:
			w.WriteHeader(404)
		}
	}))
	return ts, &seenBearers
}

func TestClient_Lookup_ReturnsActors(t *testing.T) {
	ts, _ := fakeLymphhub(t, []Actor{{ID: "a@corp.com", DisplayName: "Alice", Email: "a@corp.com"}})
	defer ts.Close()

	c := NewClient(ts.URL, "", "cid", "csecret", ts.Client())
	actors, err := c.Lookup(context.Background(), []string{"a@corp.com"})
	require.NoError(t, err)
	assert.Equal(t, []Actor{{ID: "a@corp.com", DisplayName: "Alice", Email: "a@corp.com"}}, actors)
}

func TestClient_Lookup_ReusesTokenAcrossCalls(t *testing.T) {
	tokenCalls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			tokenCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok-1", "expires_in": 3600})
			return
		}
		_ = json.NewEncoder(w).Encode([]Actor{})
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "", "cid", "csecret", ts.Client())
	_, err := c.Lookup(context.Background(), []string{"a"})
	require.NoError(t, err)
	_, err = c.Lookup(context.Background(), []string{"b"})
	require.NoError(t, err)
	assert.Equal(t, 1, tokenCalls, "token must be cached and reused, not re-fetched per lookup")
}

func TestClient_Lookup_ErrorsOnLymphhubFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok-1", "expires_in": 3600})
			return
		}
		w.WriteHeader(500)
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "", "cid", "csecret", ts.Client())
	_, err := c.Lookup(context.Background(), []string{"a"})
	require.Error(t, err)
}

// TestClient_SetsTenantHost_OnBothRequests covers the production gap found
// during who-lymphhub-actor-lookup's live verification: lymphhub routes by
// subdomain (Host header), so an in-cluster call using the service DNS name
// as baseURL (e.g. http://oidc.iam.svc.cluster.local:8081) would otherwise
// never be recognized as belonging to any tenant. NewClient's tenantHost
// param must override the Host header (not the dial address) on both the
// token request and the lookup request.
func TestClient_SetsTenantHost_OnBothRequests(t *testing.T) {
	var tokenHost, lookupHost string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			tokenHost = r.Host
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok-1", "expires_in": 3600})
		case "/v1/actors/lookup":
			lookupHost = r.Host
			_ = json.NewEncoder(w).Encode([]Actor{})
		}
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "toji.idp.toji.homes", "cid", "csecret", ts.Client())
	_, err := c.Lookup(context.Background(), []string{"a"})
	require.NoError(t, err)

	assert.Equal(t, "toji.idp.toji.homes", tokenHost, "token request must carry the tenant Host header")
	assert.Equal(t, "toji.idp.toji.homes", lookupHost, "lookup request must carry the tenant Host header")
}

func TestClient_EmptyTenantHost_LeavesHostUnset(t *testing.T) {
	// Backward-compat: an empty tenantHost (e.g. local/test servers) must not
	// override anything — the request's natural Host (from the URL) is used.
	var lookupHost string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok-1", "expires_in": 3600})
		case "/v1/actors/lookup":
			lookupHost = r.Host
			_ = json.NewEncoder(w).Encode([]Actor{})
		}
	}))
	defer ts.Close()

	c := NewClient(ts.URL, "", "cid", "csecret", ts.Client())
	_, err := c.Lookup(context.Background(), []string{"a"})
	require.NoError(t, err)

	assert.Contains(t, ts.URL, lookupHost, "with no tenantHost, the request's own URL-derived Host must be used")
}
