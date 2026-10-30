package approval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgStore struct{ db *pgxpool.Pool }

func NewPGStore(db *pgxpool.Pool) Store { return &pgStore{db: db} }

const cols = `id, ticket_id, kind, subject, status, requested_by, requested_by_label,
	requested_at, request_note, evidence, within_direction, decided_by, decided_at,
	decision_note, basis, delegation_rule_id, user_ref, supersedes_id, legacy,
	source_comment_id, created_at`

func scan(row pgx.Row) (*Approval, error) {
	var (
		a            Approval
		subj, evid   []byte
		kind, status string
		basis        *string
	)
	err := row.Scan(&a.ID, &a.TicketID, &kind, &subj, &status, &a.RequestedBy, &a.RequestedByLabel,
		&a.RequestedAt, &a.RequestNote, &evid, &a.WithinDirection, &a.DecidedBy, &a.DecidedAt,
		&a.DecisionNote, &basis, &a.DelegationRuleID, &a.UserRef, &a.SupersedesID, &a.Legacy,
		&a.SourceCommentID, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.Kind, a.Status = Kind(kind), Status(status)
	if basis != nil {
		b := Basis(*basis)
		a.Basis = &b
	}
	if err := json.Unmarshal(subj, &a.Subject); err != nil {
		return nil, fmt.Errorf("decode subject: %w", err)
	}
	if err := json.Unmarshal(evid, &a.Evidence); err != nil {
		return nil, fmt.Errorf("decode evidence: %w", err)
	}
	return &a, nil
}

func (s *pgStore) Create(ctx context.Context, req NewReq) (*Approval, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	subj, _ := json.Marshal(req.Subject)
	ev := req.Evidence
	if ev == nil {
		ev = []EvidenceItem{}
	}
	evid, _ := json.Marshal(ev)

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Serialise requests per ticket so the duplicate check and the head_sha
	// supersede below cannot race with a concurrent request.
	if _, err := tx.Exec(ctx, `SELECT 1 FROM tickets WHERE id=$1 FOR UPDATE`, req.TicketID); err != nil {
		return nil, fmt.Errorf("lock ticket: %w", err)
	}
	var dup bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM approvals
		WHERE ticket_id=$1 AND kind=$2 AND subject=$3::jsonb AND status='pending')`,
		req.TicketID, string(req.Kind), subj).Scan(&dup); err != nil {
		return nil, fmt.Errorf("check duplicate approval: %w", err)
	}
	if dup {
		return nil, ErrDuplicatePending
	}
	supersedes := req.SupersedesID
	if req.Kind == KindMerge && supersedes == nil {
		ids, err := supersedeOpenMergeTx(ctx, tx, req.TicketID, req.Subject.Repo, req.Subject.PR, req.Subject.HeadSHA, req.RequestedBy)
		if err != nil {
			return nil, err
		}
		if len(ids) > 0 {
			supersedes = &ids[0]
		}
	}
	row := tx.QueryRow(ctx, `
		INSERT INTO approvals (ticket_id, kind, subject, requested_by, requested_by_label,
			request_note, evidence, within_direction, supersedes_id, legacy, source_comment_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING `+cols,
		req.TicketID, string(req.Kind), subj, req.RequestedBy, req.RequestedByLabel,
		req.RequestNote, evid, req.WithinDirection, supersedes, req.Legacy, req.SourceCommentID)
	a, err := scan(row)
	if err != nil {
		return nil, fmt.Errorf("create approval: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return a, nil
}

func (s *pgStore) Get(ctx context.Context, id uuid.UUID) (*Approval, error) {
	a, err := scan(s.db.QueryRow(ctx, `SELECT `+cols+` FROM approvals WHERE id=$1`, id))
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, approval_id, from_status, to_status, actor, note, created_at
		FROM approval_events WHERE approval_id=$1 ORDER BY created_at, id`, id)
	if err != nil {
		return nil, fmt.Errorf("list approval events: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e Event
		var from, to string
		if err := rows.Scan(&e.ID, &e.ApprovalID, &from, &to, &e.Actor, &e.Note, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.From, e.To = Status(from), Status(to)
		a.Events = append(a.Events, e)
	}
	return a, rows.Err()
}

func (s *pgStore) List(ctx context.Context, f ListFilter) ([]*Approval, error) {
	var where []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.TicketID != nil {
		add("ticket_id=$%d", *f.TicketID)
	}
	if f.Status != "" {
		add("status=$%d", string(f.Status))
	}
	if f.Kind != "" {
		add("kind=$%d", string(f.Kind))
	}
	if f.Repo != "" {
		add("subject->>'repo'=$%d", f.Repo)
	}
	if f.RequestedBy != "" {
		add("requested_by=$%d", f.RequestedBy)
	}
	q := `SELECT ` + cols + ` FROM approvals`
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, " AND ")
	}
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args = append(args, limit, f.Offset)
	q += fmt.Sprintf(` ORDER BY created_at DESC, id LIMIT $%d OFFSET $%d`, len(args)-1, len(args))
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list approvals: %w", err)
	}
	defer rows.Close()
	var out []*Approval
	for rows.Next() {
		a, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *pgStore) Transition(ctx context.Context, id uuid.UUID, from, to Status, actor, note string, d *Decision) (*Approval, error) {
	if !CanTransition(from, to) {
		return nil, ErrInvalidTransition
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := transitionTx(ctx, tx, id, from, to, actor, note, d); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// transitionTx is the compare-and-set shared by Transition and SupersedeOpenMerge.
func transitionTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, from, to Status, actor, note string, d *Decision) error {
	var tag int64
	if d != nil {
		var user *string
		if d.UserRef != "" {
			user = &d.UserRef
		}
		ct, err := tx.Exec(ctx, `
			UPDATE approvals SET status=$3, decided_by=$4, decided_at=NOW(), decision_note=$5,
				basis=$6, delegation_rule_id=$7, user_ref=$8
			WHERE id=$1 AND status=$2`,
			id, string(from), string(to), d.DecidedBy, d.Note, string(d.Basis), d.DelegationRuleID, user)
		if err != nil {
			return fmt.Errorf("transition approval: %w", err)
		}
		tag = ct.RowsAffected()
	} else {
		ct, err := tx.Exec(ctx, `UPDATE approvals SET status=$3 WHERE id=$1 AND status=$2`,
			id, string(from), string(to))
		if err != nil {
			return fmt.Errorf("transition approval: %w", err)
		}
		tag = ct.RowsAffected()
	}
	if tag == 0 {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM approvals WHERE id=$1)`, id).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
		return ErrStale
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO approval_events (approval_id, from_status, to_status, actor, note)
		VALUES ($1,$2,$3,$4,$5)`, id, string(from), string(to), actor, note)
	if err != nil {
		return fmt.Errorf("record approval event: %w", err)
	}
	return nil
}

func (s *pgStore) SupersedeOpenMerge(ctx context.Context, ticketID uuid.UUID, repo string, pr int, newHeadSHA, actor string) ([]uuid.UUID, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT 1 FROM tickets WHERE id=$1 FOR UPDATE`, ticketID); err != nil {
		return nil, fmt.Errorf("lock ticket: %w", err)
	}
	ids, err := supersedeOpenMergeTx(ctx, tx, ticketID, repo, pr, newHeadSHA, actor)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return ids, nil
}

func supersedeOpenMergeTx(ctx context.Context, tx pgx.Tx, ticketID uuid.UUID, repo string, pr int, newHeadSHA, actor string) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, status FROM approvals
		WHERE ticket_id=$1 AND kind='merge' AND subject->>'repo'=$2
		  AND (subject->>'pr')::int=$3 AND subject->>'head_sha' <> $4
		  AND status IN ('pending','approved')
		ORDER BY created_at, id`, ticketID, repo, pr, newHeadSHA)
	if err != nil {
		return nil, fmt.Errorf("find open merge approvals: %w", err)
	}
	type row struct {
		id     uuid.UUID
		status string
	}
	var found []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.status); err != nil {
			rows.Close()
			return nil, err
		}
		found = append(found, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(found))
	for _, r := range found {
		if err := transitionTx(ctx, tx, r.id, Status(r.status), StatusSuperseded, actor, "head_sha changed", nil); err != nil {
			return nil, err
		}
		ids = append(ids, r.id)
	}
	return ids, nil
}
