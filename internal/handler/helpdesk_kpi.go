package handler

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// HelpdeskKPIHandler exposes lightweight overview metrics for the admin
// console. All counts are workspace-scoped via a JOIN through projects.
type HelpdeskKPIHandler struct{ pool *pgxpool.Pool }

func NewHelpdeskKPIHandler(pool *pgxpool.Pool) *HelpdeskKPIHandler {
	return &HelpdeskKPIHandler{pool: pool}
}

type kpi struct {
	NewToday        int `json:"new_today"`
	ResolvedToday   int `json:"resolved_today"`
	OpenTotal       int `json:"open_total"`
	OverdueTotal    int `json:"overdue_total"`
	AIAutoResolved  int `json:"ai_auto_resolved"` // tickets where assignee_type='agent' and status='completed' today
}

// GET /workspaces/:wid/helpdesk/kpi
func (h *HelpdeskKPIHandler) Get(w http.ResponseWriter, r *http.Request) {
	wid, err := uuid.Parse(chi.URLParam(r, "wid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	since := time.Now().UTC().Truncate(24 * time.Hour) // start-of-day UTC

	row := h.pool.QueryRow(r.Context(), `
		WITH ws_tickets AS (
			SELECT t.* FROM tickets t
			JOIN projects p ON t.project_id = p.id
			WHERE p.workspace_id = $1
		)
		SELECT
		  (SELECT COUNT(*) FROM ws_tickets WHERE created_at >= $2),
		  (SELECT COUNT(*) FROM ws_tickets WHERE completed_at >= $2),
		  (SELECT COUNT(*) FROM ws_tickets WHERE status NOT IN ('completed','cancelled','failed')),
		  (SELECT COUNT(*) FROM ws_tickets WHERE due_at IS NOT NULL AND due_at < NOW()
		     AND status NOT IN ('completed','cancelled','failed')),
		  (SELECT COUNT(*) FROM ws_tickets WHERE assignee_type='agent' AND status='completed' AND completed_at >= $2)
	`, wid, since)

	out := kpi{}
	if err := row.Scan(&out.NewToday, &out.ResolvedToday, &out.OpenTotal, &out.OverdueTotal, &out.AIAutoResolved); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}
