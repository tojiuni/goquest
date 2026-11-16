package auth

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/spicedb"
	"github.com/tojiuni/goquest/internal/ticket"
)

// RequireTicketWork gates the day-to-day write routes on /tickets/{id} (status,
// assignee, dates, priority, title/description, relations) on the can_work
// permission: project editors and everyone who can manage. It is the same
// shape as RequireTicketManage — chained after RequireTicketView it reuses the
// stashed ticket, a project-less legacy ticket is not gated, and a checker
// error denies (fail-closed).
//
// Which FIELDS an editor may change is a separate rule applied by the handler
// (see ticket.EditorFieldViolations): can_work alone does not let a non-manager
// move, hide or delete a ticket.
func RequireTicketWork(getter ticketGetter, checker spicedb.PermissionChecker) func(http.Handler) http.Handler {
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
			if t.ProjectID == nil {
				next.ServeHTTP(w, r)
				return
			}
			ok, err := spicedb.CanWorkTicket(r.Context(), checker, t.ID, RequesterID(r))
			if err != nil {
				http.Error(w, "permission check", http.StatusInternalServerError)
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
