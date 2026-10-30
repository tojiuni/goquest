package auth

import (
	"context"
	"log"
	"net/http"
	"strings"

	keyfunc "github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type contextKey string

const requesterKey contextKey = "requester_id"

type Claims struct {
	jwt.RegisteredClaims
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	OrgID         string `json:"org_id,omitempty"`
	OrgRole       string `json:"org_role,omitempty"`
}

// Middleware validates bearer JWTs against one or more JWKS endpoints. jwksURL
// may be a single URL or a comma-separated list — during an IdP migration both
// the old and new issuers' keys are trusted (email-based identity is
// IdP-neutral, so a user keeps the same platform subject across IdPs).
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

	jwks, err := keyfunc.NewDefault(jwksURLs(jwksURL))
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

			ctx := context.WithValue(r.Context(), requesterKey, requesterFromClaims(claims))
			ctx = context.WithValue(ctx, jwtIdentityKey, &JWTIdentity{
				Audience:      append([]string(nil), claims.Audience...),
				Email:         claims.Email,
				EmailVerified: claims.EmailVerified,
			})
			if o := orgFromClaims(claims); o != nil {
				ctx = context.WithValue(ctx, orgCtxKey, o)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func RequesterID(r *http.Request) string {
	v, _ := r.Context().Value(requesterKey).(string)
	return v
}

// WithRequesterID returns a copy of ctx with the requester id set.
// Primarily for tests that need to bypass JWT/API-key middleware.
func WithRequesterID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requesterKey, id)
}

// requesterFromClaims picks the platform subject: the email claim (the standard)
// first, then sub, then RegisteredClaims.Subject. The fallbacks protect tokens
// without an email claim (machine users, non-OIDC, legacy).
//
// email is only trusted when email_verified is true — an unverified email claim
// must not be usable to impersonate another verified identity. (The ZITADEL
// setEmail action emits email_verified alongside email.)
func requesterFromClaims(c *Claims) string {
	if c.Email != "" && c.EmailVerified {
		return c.Email
	}
	if c.Sub != "" {
		return c.Sub
	}
	return c.Subject
}

// jwksURLs splits a comma-separated JWKS_URL into a trimmed, non-empty list.
func jwksURLs(raw string) []string {
	var out []string
	for _, u := range strings.Split(raw, ",") {
		if u = strings.TrimSpace(u); u != "" {
			out = append(out, u)
		}
	}
	return out
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

const orgCtxKey contextKey = "org_ctx"

// OrgCtx carries the caller's lymphhub organization claim. Present only for
// external-org users; staff tokens have no org claim. Role is "admin" or
// "member" — anything else is downgraded to member (fail-closed).
type OrgCtx struct {
	ID   uuid.UUID
	Role string
}

func (o *OrgCtx) IsAdmin() bool { return o != nil && o.Role == "admin" }

func orgFromClaims(c *Claims) *OrgCtx {
	if c.OrgID == "" {
		return nil
	}
	id, err := uuid.Parse(c.OrgID)
	if err != nil {
		return nil
	}
	if id == uuid.Nil {
		// An all-zero UUID parses "successfully" but is not a real org claim —
		// treat it the same as a parse failure (fail-closed).
		return nil
	}
	role := c.OrgRole
	if role != "admin" {
		role = "member"
	}
	return &OrgCtx{ID: id, Role: role}
}

func WithOrgCtx(ctx context.Context, o *OrgCtx) context.Context {
	return context.WithValue(ctx, orgCtxKey, o)
}

func OrgFrom(r *http.Request) *OrgCtx {
	v, _ := r.Context().Value(orgCtxKey).(*OrgCtx)
	return v
}
