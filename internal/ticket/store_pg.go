package ticket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgStore struct {
	db *pgxpool.Pool
}

func NewPGStore(db *pgxpool.Pool) Store {
	return &pgStore{db: db}
}

func (s *pgStore) Create(ctx context.Context, t *Ticket) error {
	agentCtxJSON, err := marshalNullableJSON(t.AgentContext)
	if err != nil {
		return fmt.Errorf("marshal agent_context: %w", err)
	}
	metaJSON, err := marshalNullableJSON(t.Metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	var domain *string
	if t.Domain != "" {
		d := string(t.Domain)
		domain = &d
	}

	var sourceBot *string
	if t.SourceBot != "" {
		sb := t.SourceBot
		sourceBot = &sb
	}

	query := `
		INSERT INTO tickets (
			id, title, description, type, status, priority,
			assignee_type, assignee_id, requester_id, source_bot,
			domain, agent_context, vikunja_task_id, metadata,
			created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16
		)`

	_, err = s.db.Exec(ctx, query,
		t.ID, t.Title, t.Description, string(t.Type), string(t.Status), string(t.Priority),
		string(t.AssigneeType), t.AssigneeID, t.RequesterID, sourceBot,
		domain, agentCtxJSON, t.VikunjaTaskID, metaJSON,
		t.CreatedAt, t.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert ticket: %w", err)
	}
	return nil
}

func (s *pgStore) Get(ctx context.Context, id uuid.UUID) (*Ticket, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, title, description, type, status, priority,
		       assignee_type, assignee_id, requester_id, source_bot,
		       domain, agent_context, vikunja_task_id, metadata,
		       created_at, updated_at, completed_at
		FROM tickets WHERE id = $1`, id)

	t, err := scanTicket(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get ticket: %w", err)
	}
	return t, nil
}

func (s *pgStore) Update(ctx context.Context, id uuid.UUID, req UpdateTicketReq) (*Ticket, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	row := tx.QueryRow(ctx, `
		SELECT id, title, description, type, status, priority,
		       assignee_type, assignee_id, requester_id, source_bot,
		       domain, agent_context, vikunja_task_id, metadata,
		       created_at, updated_at, completed_at
		FROM tickets WHERE id = $1 FOR UPDATE`, id)

	current, err := scanTicket(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get ticket for update: %w", err)
	}

	if req.Status != nil {
		if err := validateTransition(current.Status, *req.Status); err != nil {
			return nil, err
		}
	}

	newStatus := current.Status
	if req.Status != nil {
		newStatus = *req.Status
	}
	newAssigneeID := current.AssigneeID
	if req.AssigneeID != nil {
		newAssigneeID = *req.AssigneeID
	}
	newAssigneeType := current.AssigneeType
	if req.AssigneeType != nil {
		newAssigneeType = *req.AssigneeType
	}

	var newDomain *string
	if req.Domain != nil {
		d := string(*req.Domain)
		newDomain = &d
	} else if current.Domain != "" {
		d := string(current.Domain)
		newDomain = &d
	}

	newAgentCtx := current.AgentContext
	if req.AgentContext != nil {
		newAgentCtx = req.AgentContext
	}

	newVikunja := current.VikunjaTaskID
	if req.VikunjaTaskID != nil {
		newVikunja = req.VikunjaTaskID
	}

	agentCtxJSON, err := marshalNullableJSON(newAgentCtx)
	if err != nil {
		return nil, fmt.Errorf("marshal agent_context: %w", err)
	}

	now := time.Now().UTC()
	var completedAt *time.Time
	if newStatus == StatusCompleted {
		if current.CompletedAt != nil {
			completedAt = current.CompletedAt
		} else {
			completedAt = &now
		}
	}

	_, err = tx.Exec(ctx, `
		UPDATE tickets SET
			status=$2, assignee_id=$3, assignee_type=$4, domain=$5,
			agent_context=$6, vikunja_task_id=$7, updated_at=$8, completed_at=$9
		WHERE id=$1`,
		id, string(newStatus), newAssigneeID, string(newAssigneeType), newDomain,
		agentCtxJSON, newVikunja, now, completedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("update ticket: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	return s.Get(ctx, id)
}

func (s *pgStore) List(ctx context.Context, f ListTicketsFilter) ([]*Ticket, int, error) {
	args := []any{}
	where := "WHERE 1=1"
	i := 1

	if f.Status != nil {
		where += fmt.Sprintf(" AND status=$%d", i)
		args = append(args, string(*f.Status))
		i++
	}
	if f.Domain != nil {
		where += fmt.Sprintf(" AND domain=$%d", i)
		args = append(args, string(*f.Domain))
		i++
	}
	if f.AssigneeType != nil {
		where += fmt.Sprintf(" AND assignee_type=$%d", i)
		args = append(args, string(*f.AssigneeType))
		i++
	}
	if f.AssigneeID != nil {
		where += fmt.Sprintf(" AND assignee_id=$%d", i)
		args = append(args, *f.AssigneeID)
		i++
	}
	if f.RequesterID != nil {
		where += fmt.Sprintf(" AND requester_id=$%d", i)
		args = append(args, *f.RequesterID)
		i++
	}

	countQuery := "SELECT COUNT(*) FROM tickets " + where
	var total int
	if err := s.db.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count tickets: %w", err)
	}

	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	listArgs := append(args, limit, offset)
	rows, err := s.db.Query(ctx, `
		SELECT id, title, description, type, status, priority,
		       assignee_type, assignee_id, requester_id, source_bot,
		       domain, agent_context, vikunja_task_id, metadata,
		       created_at, updated_at, completed_at
		FROM tickets `+where+
		fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", i, i+1),
		listArgs...,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("list tickets: %w", err)
	}
	defer rows.Close()

	var tickets []*Ticket
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan ticket: %w", err)
		}
		tickets = append(tickets, t)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("rows error: %w", err)
	}

	return tickets, total, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanTicket(s scanner) (*Ticket, error) {
	t := &Ticket{}
	var (
		sourceBot     *string
		domain        *string
		agentCtxBytes []byte
		metaBytes     []byte
	)

	err := s.Scan(
		&t.ID, &t.Title, &t.Description, &t.Type, &t.Status, &t.Priority,
		&t.AssigneeType, &t.AssigneeID, &t.RequesterID, &sourceBot,
		&domain, &agentCtxBytes, &t.VikunjaTaskID, &metaBytes,
		&t.CreatedAt, &t.UpdatedAt, &t.CompletedAt,
	)
	if err != nil {
		return nil, err
	}

	if sourceBot != nil {
		t.SourceBot = *sourceBot
	}
	if domain != nil {
		t.Domain = Domain(*domain)
	}
	if len(agentCtxBytes) > 0 {
		if err := json.Unmarshal(agentCtxBytes, &t.AgentContext); err != nil {
			return nil, fmt.Errorf("unmarshal agent_context: %w", err)
		}
	}
	if len(metaBytes) > 0 {
		if err := json.Unmarshal(metaBytes, &t.Metadata); err != nil {
			return nil, fmt.Errorf("unmarshal metadata: %w", err)
		}
	}

	return t, nil
}

// valid transitions; terminal states have no outgoing edges
var transitions = map[Status][]Status{
	StatusPending:    {StatusAssigned, StatusCancelled},
	StatusAssigned:   {StatusInProgress, StatusPending, StatusCancelled},
	StatusInProgress: {StatusCompleted, StatusFailed, StatusPending},
	StatusCompleted:  {},
	StatusFailed:     {StatusPending},
	StatusCancelled:  {},
}

func validateTransition(from, to Status) error {
	allowed, ok := transitions[from]
	if !ok {
		return fmt.Errorf("unknown status: %s", from)
	}
	for _, s := range allowed {
		if s == to {
			return nil
		}
	}
	return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, from, to)
}

func marshalNullableJSON(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

var (
	ErrNotFound          = errors.New("ticket not found")
	ErrInvalidTransition = errors.New("invalid status transition")
)
