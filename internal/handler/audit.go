package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/tojiuni/goquest/internal/audit"
)

type AuditHandler struct{ logger audit.Logger }

func NewAuditHandler(logger audit.Logger) *AuditHandler { return &AuditHandler{logger: logger} }

// GET /workspaces/:wid/audit?actor=&action=&target_kind=&target_id=&since=ISO8601&limit=&offset=
func (h *AuditHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := audit.ListFilter{
		Actor:      q.Get("actor"),
		Action:     q.Get("action"),
		TargetKind: q.Get("target_kind"),
		TargetID:   q.Get("target_id"),
	}
	if s := q.Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			f.Limit = n
		}
	}
	if s := q.Get("offset"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			f.Offset = n
		}
	}
	if s := q.Get("since"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			f.Since = &t
		}
	}

	entries, err := h.logger.List(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}
