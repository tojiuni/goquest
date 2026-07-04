package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tojiuni/lymphhub/authz"
)

// verifierFunc adapts a plain function to the LymphhubVerifier interface.
type verifierFunc func(ctx context.Context, bearer string) (authz.Actor, error)

func (f verifierFunc) Verify(ctx context.Context, bearer string) (authz.Actor, error) {
	return f(ctx, bearer)
}

func TestLymphhubMiddleware_SetsRequesterFromSession(t *testing.T) {
	fake := verifierFunc(func(_ context.Context, b string) (authz.Actor, error) {
		require.Equal(t, "ls_tok", b)
		return authz.Actor{ID: "a-1", Email: "dev@toji.homes", Kind: "human"}, nil
	})
	var got string
	h := LymphhubSessionMiddleware(fake)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = RequesterID(r)
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer ls_tok")
	h.ServeHTTP(httptest.NewRecorder(), req)
	require.Equal(t, "dev@toji.homes", got)
}

func TestLymphhubMiddleware_NonLsBearer_PassesThrough(t *testing.T) {
	h := LymphhubSessionMiddleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer eyJ.jwt.here")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req) // must not panic on nil verifier
	require.Equal(t, 200, rr.Code)
}

func TestLymphhubMiddleware_LsBearerUnauthenticated_Returns401(t *testing.T) {
	fake := verifierFunc(func(_ context.Context, b string) (authz.Actor, error) {
		return authz.Actor{}, authz.ErrUnauthenticated
	})
	h := LymphhubSessionMiddleware(fake)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer ls_bad")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	require.Equal(t, 401, rr.Code)
}
