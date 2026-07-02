package auth

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/spicedb"
	"github.com/tojiuni/goquest/internal/ticket"
)

// RequireTicketManage gates mutating /tickets/{id} routes (Update/Cancel/Approve)
// on manage access. It is chained after RequireTicketView and reuses the ticket
// that middleware stashed; used standalone it loads the ticket via getter.
//
// Enforcement follows the same shadow/enforce model as RequireTicketView: in
// shadow mode the wired checker is a NoopChecker (grants everything), so no new
// denials occur until SPICEDB_ENFORCE swaps in the real Checker. Legacy tickets
// (no project) skip the fine gate. Fail-closed on checker error.
func RequireTicketManage(getter ticketGetter, checker spicedb.PermissionChecker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t := TicketFrom(r)
			if t == nil {
				id, err := uuid.Parse(chi.URLParam(r, "id"))
				if err != nil {
					http.Error(w, "invalid ticket id", http.StatusBadRequest)
					return
				}
				t, err = getter.Get(r.Context(), id)
				if err != nil {
					if errors.Is(err, ticket.ErrNotFound) {
						http.Error(w, "ticket not found", http.StatusNotFound)
						return
					}
					http.Error(w, "lookup ticket", http.StatusInternalServerError)
					return
				}
			}

			// Legacy ticket (no project) → no SpiceDB gate, matching RequireTicketView.
			if t.ProjectID == nil {
				next.ServeHTTP(w, r)
				return
			}

			ok, err := checker.CanManageTicket(r.Context(), t.ID, RequesterID(r))
			if err != nil {
				http.Error(w, "permission check", http.StatusInternalServerError) // fail-closed
				return
			}
			if !ok {
				http.Error(w, "access denied", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
