package auth

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const apiKeyMetaCtxKey contextKey = "api_key_meta"

// APIKeyMeta is the rich context attached to a request authenticated via X-API-Key.
// It is set by APIKeyMiddleware (when a RichKeyVerifier is supplied) and consumed
// by RequireKeyCapability / EnforceKeyOrigin / EnforceKeyRateLimit.
type APIKeyMeta struct {
	ID                  uuid.UUID
	ProjectID           uuid.UUID
	Scope               string
	AllowedCapabilities []string
	AllowedOrigins      []string
	RateLimitPerMin     int
}

// RichKeyVerifier extends KeyVerifier to also return APIKeyMeta.
// apikey.Service implements both methods.
type RichKeyVerifier interface {
	KeyVerifier
	VerifyAPIKeyMeta(ctx context.Context, plainKey string) (requesterID string, meta *APIKeyMeta, err error)
}

// keyMetaFrom returns the API key meta if the current request was authenticated
// via X-API-Key. Returns nil for JWT-authenticated requests.
func KeyMeta(r *http.Request) *APIKeyMeta {
	v, _ := r.Context().Value(apiKeyMetaCtxKey).(*APIKeyMeta)
	return v
}

// APIKeyMiddlewareRich is a richer variant of APIKeyMiddleware that stores
// APIKeyMeta in the request context. JWT requests are unaffected.
func APIKeyMiddlewareRich(verifier RichKeyVerifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("X-API-Key")
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}

			requesterID, meta, err := verifier.VerifyAPIKeyMeta(r.Context(), key)
			if err != nil {
				http.Error(w, "invalid api key", http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), requesterKey, requesterID)
			ctx = context.WithValue(ctx, apiKeyMetaCtxKey, meta)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// hasCapability checks the meta.AllowedCapabilities slice; '*' is wildcard.
func hasCapability(meta *APIKeyMeta, cap string) bool {
	if meta == nil {
		return true // JWT path; not gated by API key capabilities
	}
	for _, c := range meta.AllowedCapabilities {
		if c == "*" || c == cap {
			return true
		}
	}
	return false
}

// RequireKeyCapability gates a handler so that requests authenticated via X-API-Key
// must have the given capability. JWT requests are passed through unmodified.
func RequireKeyCapability(cap string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			meta := KeyMeta(r)
			if meta != nil && !hasCapability(meta, cap) {
				http.Error(w, "api key lacks capability: "+cap, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireAPIKeyCapability rejects any request not authenticated via
// X-API-Key (auth.KeyMeta is nil for JWT/session requests) AND requires the
// key to hold the given capability. Unlike RequireKeyCapability, which
// deliberately passes JWT/session requests through unmodified (meta == nil
// short-circuits to allow), this is for service-to-service-only endpoints —
// e.g. the bulk ticket-relations dump the brain importer consumes — where
// no JWT/session caller should ever be allowed through, and the capability
// check lets a specific key be scoped down without granting it to every key.
func RequireAPIKeyCapability(cap string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			meta := KeyMeta(r)
			if meta == nil {
				http.Error(w, "this endpoint requires API-key authentication", http.StatusForbidden)
				return
			}
			if !hasCapability(meta, cap) {
				http.Error(w, "api key lacks capability: "+cap, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// EnforceKeyOrigin checks the Origin header against allowed_origins for public-scope
// keys. Private keys / JWT requests are not gated.
func EnforceKeyOrigin() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			meta := KeyMeta(r)
			if meta == nil || meta.Scope != "public" {
				next.ServeHTTP(w, r)
				return
			}
			origin := r.Header.Get("Origin")
			for _, o := range meta.AllowedOrigins {
				if o == origin {
					next.ServeHTTP(w, r)
					return
				}
			}
			http.Error(w, "origin not allowed", http.StatusForbidden)
		})
	}
}

// EnforceKeyRateLimit applies a per-minute rate limit (per API key) using Redis
// INCR + EXPIRE. JWT requests are not gated. If rdb is nil, the middleware is a no-op.
func EnforceKeyRateLimit(rdb *redis.Client) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			meta := KeyMeta(r)
			if meta == nil || rdb == nil || meta.RateLimitPerMin <= 0 {
				next.ServeHTTP(w, r)
				return
			}
			now := time.Now().UTC()
			window := now.Format("200601021504") // minute bucket
			redisKey := "rl:apikey:" + meta.ID.String() + ":" + window

			ctx := r.Context()
			n, err := rdb.Incr(ctx, redisKey).Result()
			if err != nil {
				// Fail open on Redis errors — better than blocking traffic.
				next.ServeHTTP(w, r)
				return
			}
			if n == 1 {
				_ = rdb.Expire(ctx, redisKey, 65*time.Second).Err()
			}
			if int(n) > meta.RateLimitPerMin {
				w.Header().Set("Retry-After", "60")
				w.Header().Set("X-RateLimit-Limit", strconv.Itoa(meta.RateLimitPerMin))
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
