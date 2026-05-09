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
