package handler

import (
	"net/http"
	"strings"

	"github.com/tojiuni/goquest/internal/directory"
)

// UsersHandler serves GET /users/lookup, resolving requester/assignee ids to
// lymphhub display names. Sits inside goquest's existing authenticated route
// group — any caller who can already list tickets can call this.
type UsersHandler struct {
	lookup directory.Lookuper
}

func NewUsersHandler(l directory.Lookuper) *UsersHandler {
	return &UsersHandler{lookup: l}
}

// Lookup never fails the request on a lymphhub error: this endpoint is a
// display-name nicety, not load-bearing, so a downstream failure degrades to
// an empty result (the UI falls back to showing the raw id) rather than a
// 5xx that would make ticket lists error out whenever lymphhub hiccups.
func (h *UsersHandler) Lookup(w http.ResponseWriter, r *http.Request) {
	idsParam := r.URL.Query().Get("ids")
	if idsParam == "" {
		writeJSON(w, http.StatusOK, []directory.Actor{})
		return
	}
	ids := strings.Split(idsParam, ",")

	actors, err := h.lookup.Lookup(r.Context(), ids)
	if err != nil || actors == nil {
		// A nil slice (no error, e.g. the unconfigured noopLookuper fallback)
		// must not serialize as JSON `null` — callers expect an array.
		actors = []directory.Actor{}
	}
	writeJSON(w, http.StatusOK, actors)
}
