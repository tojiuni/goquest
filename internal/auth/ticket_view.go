package auth

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/spicedb"
	"github.com/tojiuni/goquest/internal/ticket"
)

// ticketGetter is the minimal ticket lookup the middleware needs (satisfied by
// *ticket.Service).
type ticketGetter interface {
	Get(ctx context.Context, id uuid.UUID) (*ticket.Ticket, error)
}

type ticketCtxKeyT string

const ticketCtxKey ticketCtxKeyT = "viewed_ticket"

// TicketFrom returns the ticket stashed by RequireTicketView, or nil.
func TicketFrom(r *http.Request) *ticket.Ticket {
	t, _ := r.Context().Value(ticketCtxKey).(*ticket.Ticket)
	return t
}

// RequireTicketView gates every /tickets/{id}/* route on view access and stashes
// the ticket in context. Principal handling:
//   - customer-token (magic-link): allowed iff the token is bound to this ticket id.
//   - OIDC user: SpiceDB CanViewTicket (visibility-aware). Legacy tickets with no
//     project skip the SpiceDB gate. fail-closed on checker error.
func RequireTicketView(getter ticketGetter, checker spicedb.PermissionChecker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := uuid.Parse(chi.URLParam(r, "id"))
			if err != nil {
				http.Error(w, "invalid ticket id", http.StatusBadRequest)
				return
			}

			t, err := getter.Get(r.Context(), id)
			if err != nil {
				if errors.Is(err, ticket.ErrNotFound) {
					http.Error(w, "ticket not found", http.StatusNotFound)
					return
				}
				http.Error(w, "lookup ticket", http.StatusInternalServerError)
				return
			}

			if cust := CustomerFrom(r); cust != nil {
				// magic-link: the token is the capability. It must be bound to this ticket.
				if cust.TicketID == nil || *cust.TicketID != id {
					http.Error(w, "forbidden", http.StatusForbidden)
					return
				}
			} else if o := OrgFrom(r); o != nil {
				// External-org caller: claim-based rule, SpiceDB not consulted.
				// admin → any ticket on the org's requester/assignee axis;
				// member → only tickets they personally requested / are assigned.
				caller := RequesterID(r)
				sameOrg := func(x *uuid.UUID) bool { return x != nil && *x == o.ID }
				allowed := false
				if o.IsAdmin() {
					allowed = sameOrg(t.RequesterOrgID) || sameOrg(t.AssigneeOrgID)
				} else {
					allowed = (sameOrg(t.RequesterOrgID) && t.RequesterID == caller) ||
						(sameOrg(t.AssigneeOrgID) && t.AssigneeID == caller)
				}
				if !allowed {
					http.Error(w, "forbidden", http.StatusForbidden)
					return
				}
			} else if t.ProjectID != nil {
				// OIDC user, project-scoped ticket → SpiceDB gate. RequesterID is "" for
				// requests carrying neither JWT nor API key nor customer token; SpiceDB
				// denies them (no user:"" grant exists), so they fail closed here.
				ok, cErr := checker.CanViewTicket(r.Context(), t.ID, RequesterID(r), spicedb.VisibilityMode(t.Visibility))
				if cErr != nil {
					http.Error(w, "visibility check", http.StatusInternalServerError) // fail-closed
					return
				}
				if !ok {
					http.Error(w, "access denied", http.StatusForbidden)
					return
				}
			}
			// legacy ticket (ProjectID==nil) for OIDC: no fine gate.

			ctx := context.WithValue(r.Context(), ticketCtxKey, t)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
