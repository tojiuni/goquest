package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

const customerCtxKey contextKey = "customer_token"

// CustomerCtx is what we attach to the request context when a magic-link access
// token (Authorization: Bearer ct_*) is presented. Handlers read it via
// CustomerFrom(r) and use it to gate visibility (e.g. comment.is_internal=false).
type CustomerCtx struct {
	Email    string
	TicketID *uuid.UUID
}

// CustomerTokenParser is satisfied by magic.ParseCustomerAccessToken — kept as
// an interface to avoid an auth↔magic import cycle.
type CustomerTokenParser interface {
	ParseCustomerAccessToken(raw string) (email string, ticketID *uuid.UUID, err error)
}

// CustomerTokenMiddleware extracts and validates a Bearer ct_* token. If the
// header isn't a customer token, the middleware is a no-op and JWT/API-key
// flows continue. On success, it sets a synthetic requester id ("customer:<email>")
// AND attaches CustomerCtx so downstream handlers can downgrade visibility.
func CustomerTokenMiddleware(parser CustomerTokenParser) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip if upstream (API key) already authenticated the request.
			if RequesterID(r) != "" {
				next.ServeHTTP(w, r)
				return
			}
			h := r.Header.Get("Authorization")
			if h == "" {
				next.ServeHTTP(w, r)
				return
			}
			parts := strings.SplitN(h, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
				next.ServeHTTP(w, r)
				return
			}
			raw := parts[1]
			if !strings.HasPrefix(raw, "ct_") {
				// Not a customer token — let JWT middleware try.
				next.ServeHTTP(w, r)
				return
			}
			email, ticketID, err := parser.ParseCustomerAccessToken(raw)
			if err != nil {
				http.Error(w, "invalid customer token", http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), requesterKey, "customer:"+email)
			ctx = context.WithValue(ctx, customerCtxKey, &CustomerCtx{
				Email:    email,
				TicketID: ticketID,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// CustomerFrom returns the customer context if the request was authenticated
// via a magic-link access token; nil otherwise.
func CustomerFrom(r *http.Request) *CustomerCtx {
	v, _ := r.Context().Value(customerCtxKey).(*CustomerCtx)
	return v
}

// IsCustomerRequest is a convenience helper.
func IsCustomerRequest(r *http.Request) bool { return CustomerFrom(r) != nil }
