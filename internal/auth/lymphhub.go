package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/tojiuni/lymphhub/authz"
)

// LymphhubVerifier verifies an opaque lymphhub session token (prefixed "ls_")
// and returns the resolved actor.
type LymphhubVerifier interface {
	Verify(ctx context.Context, bearer string) (authz.Actor, error)
}

// LymphhubSessionMiddleware accepts opaque lymphhub CLI SSO sessions
// (Authorization: Bearer ls_...) alongside the existing JWT/API-key
// middleware. It is strictly additive: any bearer that does not start with
// "ls_" (or when v is nil) is left untouched for downstream middleware.
func LymphhubSessionMiddleware(v LymphhubVerifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if RequesterID(r) != "" { // already authenticated upstream
				next.ServeHTTP(w, r)
				return
			}
			tok := bearerToken(r)
			if !strings.HasPrefix(tok, "ls_") || v == nil {
				next.ServeHTTP(w, r) // not a lymphhub session; leave for JWT/customer mw
				return
			}
			actor, err := v.Verify(r.Context(), tok)
			if err != nil {
				if errors.Is(err, authz.ErrUnauthenticated) {
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				http.Error(w, "auth error", http.StatusBadGateway)
				return
			}
			id := actor.Email
			if id == "" {
				id = actor.ID
			}
			next.ServeHTTP(w, r.WithContext(WithRequesterID(r.Context(), id)))
		})
	}
}
