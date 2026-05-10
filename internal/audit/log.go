// Package audit provides a simple append-only log of admin/security actions.
// Insertion is best-effort: log errors locally rather than failing the parent
// operation — audit must never block the user-facing request path.
package audit

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Entry struct {
	ID         int64                  `json:"id"`
	OccurredAt time.Time              `json:"occurred_at"`
	Actor      string                 `json:"actor"`
	Action     string                 `json:"action"`
	TargetKind string                 `json:"target_kind"`
	TargetID   string                 `json:"target_id"`
	Detail     map[string]interface{} `json:"detail"`
}

// ListFilter narrows audit_log queries.
type ListFilter struct {
	Actor      string
	Action     string
	TargetKind string
	TargetID   string
	Since      *time.Time
	Limit      int
	Offset     int
}

type Logger interface {
	Record(ctx context.Context, actor, action, targetKind, targetID string, detail map[string]any)
	List(ctx context.Context, f ListFilter) ([]*Entry, error)
}

type pgLogger struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) Logger { return &pgLogger{pool: pool} }

// Record inserts an audit entry. Errors are logged but not returned —
// audit must not break the calling request.
func (l *pgLogger) Record(ctx context.Context, actor, action, targetKind, targetID string, detail map[string]any) {
	js, err := json.Marshal(detail)
	if err != nil {
		js = []byte("{}")
	}
	_, err = l.pool.Exec(ctx, `
		INSERT INTO audit_log (actor, action, target_kind, target_id, detail)
		VALUES ($1,$2,$3,$4,$5::jsonb)`,
		actor, action, targetKind, targetID, string(js))
	if err != nil {
		log.Printf("warn: audit_log insert failed actor=%s action=%s: %v", actor, action, err)
	}
}

func (l *pgLogger) List(ctx context.Context, f ListFilter) ([]*Entry, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, occurred_at, actor, action, target_kind, target_id, detail
	      FROM audit_log WHERE 1=1`
	args := []any{}
	i := 1
	if f.Actor != "" {
		q += " AND actor=$" + itoa(i)
		args = append(args, f.Actor)
		i++
	}
	if f.Action != "" {
		q += " AND action=$" + itoa(i)
		args = append(args, f.Action)
		i++
	}
	if f.TargetKind != "" {
		q += " AND target_kind=$" + itoa(i)
		args = append(args, f.TargetKind)
		i++
	}
	if f.TargetID != "" {
		q += " AND target_id=$" + itoa(i)
		args = append(args, f.TargetID)
		i++
	}
	if f.Since != nil {
		q += " AND occurred_at >= $" + itoa(i)
		args = append(args, *f.Since)
		i++
	}
	q += " ORDER BY occurred_at DESC LIMIT $" + itoa(i) + " OFFSET $" + itoa(i+1)
	args = append(args, limit, f.Offset)

	rows, err := l.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Entry
	for rows.Next() {
		e := &Entry{}
		var detailBytes []byte
		if err := rows.Scan(&e.ID, &e.OccurredAt, &e.Actor, &e.Action, &e.TargetKind, &e.TargetID, &detailBytes); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(detailBytes, &e.Detail)
		out = append(out, e)
	}
	return out, rows.Err()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
