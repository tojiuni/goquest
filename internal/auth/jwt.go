package auth

import (
	"context"
	"log"
	"net/http"
	"strings"

	keyfunc "github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

const requesterKey contextKey = "requester_id"

type Claims struct {
	jwt.RegisteredClaims
	Sub string `json:"sub"`
}

func Middleware(jwksURL string, skip bool) func(http.Handler) http.Handler {
	if skip {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// inject a dev placeholder so RequesterID never returns empty
				ctx := context.WithValue(r.Context(), requesterKey, "dev-user")
				next.ServeHTTP(w, r.WithContext(ctx))
			})
		}
	}

	jwks, err := keyfunc.NewDefault([]string{jwksURL})
	if err != nil {
		log.Printf("warn: could not initialise JWKS from %s: %v", jwksURL, err)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Already authenticated upstream (e.g. by APIKeyMiddleware).
			if RequesterID(r) != "" {
				next.ServeHTTP(w, r)
				return
			}

			raw := bearerToken(r)
			if raw == "" {
				http.Error(w, "missing authorization header", http.StatusUnauthorized)
				return
			}

			claims := &Claims{}
			_, err := jwt.ParseWithClaims(raw, claims, jwks.Keyfunc)
			if err != nil {
				http.Error(w, "invalid token: "+err.Error(), http.StatusUnauthorized)
				return
			}

			sub := claims.Sub
			if sub == "" {
				sub = claims.Subject // fallback to RegisteredClaims.Subject
			}

			ctx := context.WithValue(r.Context(), requesterKey, sub)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func RequesterID(r *http.Request) string {
	v, _ := r.Context().Value(requesterKey).(string)
	return v
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return ""
	}
	return parts[1]
}

// KeyVerifier validates a plain-text API key and returns the caller identity.
type KeyVerifier interface {
	VerifyAPIKey(ctx context.Context, plainKey string) (requesterID string, err error)
}

// APIKeyMiddleware returns a middleware that accepts X-API-Key as an alternative
// to Bearer JWT. If the header is present, it validates the key and sets the
// requester context; otherwise it falls through to the next handler (typically
// the JWT middleware).
func APIKeyMiddleware(verifier KeyVerifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("X-API-Key")
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}

			requesterID, err := verifier.VerifyAPIKey(r.Context(), key)
			if err != nil {
				http.Error(w, "invalid api key", http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), requesterKey, requesterID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
