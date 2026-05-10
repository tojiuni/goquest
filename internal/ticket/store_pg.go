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
	specJSON, err := marshalNullableJSON(t.CompletionSpec)
	if err != nil {
		return fmt.Errorf("marshal completion_spec: %w", err)
	}
	if specJSON == nil {
		specJSON = []byte("{}")
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
	visibility := t.Visibility
	if visibility == "" {
		visibility = VisibilityProject
	}
	mode := t.CompletionMode
	if mode == "" {
		mode = CompletionManual
	}
	var callbackURL *string
	if t.CallbackURL != "" {
		callbackURL = &t.CallbackURL
	}

	_, err = s.db.Exec(ctx, `
		INSERT INTO tickets (
			id, title, description, type, status, priority,
			assignee_type, assignee_id, requester_id, source_bot,
			domain, agent_context, vikunja_task_id, metadata,
			project_id, parent_id, visibility, completion_mode,
			completion_spec, reward_xp, due_at, callback_url,
			created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,
			$15,$16,$17,$18,$19,$20,$21,$22,$23,$24
		)`,
		t.ID, t.Title, t.Description, string(t.Type), string(t.Status), string(t.Priority),
		string(t.AssigneeType), t.AssigneeID, t.RequesterID, sourceBot,
		domain, agentCtxJSON, t.VikunjaTaskID, metaJSON,
		t.ProjectID, t.ParentID, string(visibility), string(mode),
		specJSON, t.RewardXP, t.DueAt, callbackURL,
		t.CreatedAt, t.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert ticket: %w", err)
	}
	return nil
}

func (s *pgStore) Get(ctx context.Context, id uuid.UUID) (*Ticket, error) {
	row := s.db.QueryRow(ctx, ticketSelectSQL+"WHERE t.id = $1", id)
	t, err := scanTicket(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get ticket: %w", err)
	}
	return t, nil
}

// ticketSelectSQL is the base SELECT with a LEFT JOIN for tags.
// Callers append a WHERE clause.
const ticketSelectSQL = `
	SELECT t.id, t.title, t.description, t.type, t.status, t.priority,
	       t.assignee_type, t.assignee_id, t.requester_id, t.source_bot,
	       t.domain, t.agent_context, t.vikunja_task_id, t.metadata,
	       t.project_id, t.parent_id, t.visibility, t.completion_mode,
	       t.completion_spec, t.reward_xp, t.due_at, t.callback_url,
	       t.created_at, t.updated_at, t.completed_at,
	       COALESCE(
	         json_agg(json_build_object('id',tg.id,'name',tg.name,'color',tg.color))
	         FILTER (WHERE tg.id IS NOT NULL),
	         '[]'
	       ) AS tags
	FROM tickets t
	LEFT JOIN ticket_tags tt ON tt.ticket_id = t.id
	LEFT JOIN tags tg        ON tg.id = tt.tag_id
`

func (s *pgStore) Update(ctx context.Context, id uuid.UUID, req UpdateTicketReq) (*Ticket, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	row := tx.QueryRow(ctx,
		ticketSelectSQL+"WHERE t.id = $1 GROUP BY t.id FOR UPDATE OF t", id)

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

	newVisibility := current.Visibility
	if req.Visibility != nil {
		newVisibility = *req.Visibility
	}
	newMode := current.CompletionMode
	if req.CompletionMode != nil {
		newMode = *req.CompletionMode
	}
	newSpec := current.CompletionSpec
	if req.CompletionSpec != nil {
		newSpec = req.CompletionSpec
	}
	newRewardXP := current.RewardXP
	if req.RewardXP != nil {
		newRewardXP = *req.RewardXP
	}
	newDueAt := current.DueAt
	if req.DueAt != nil {
		newDueAt = req.DueAt
	}
	newCallbackURL := current.CallbackURL
	if req.CallbackURL != nil {
		newCallbackURL = *req.CallbackURL
	}

	agentCtxJSON, err := marshalNullableJSON(newAgentCtx)
	if err != nil {
		return nil, fmt.Errorf("marshal agent_context: %w", err)
	}
	specJSON, err := marshalNullableJSON(newSpec)
	if err != nil {
		return nil, fmt.Errorf("marshal completion_spec: %w", err)
	}
	if specJSON == nil {
		specJSON = []byte("{}")
	}

	var callbackURL *string
	if newCallbackURL != "" {
		callbackURL = &newCallbackURL
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
			agent_context=$6, vikunja_task_id=$7,
			visibility=$8, completion_mode=$9, completion_spec=$10,
			reward_xp=$11, due_at=$12, callback_url=$13,
			updated_at=$14, completed_at=$15
		WHERE id=$1`,
		id,
		string(newStatus), newAssigneeID, string(newAssigneeType), newDomain,
		agentCtxJSON, newVikunja,
		string(newVisibility), string(newMode), specJSON,
		newRewardXP, newDueAt, callbackURL,
		now, completedAt,
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
		where += fmt.Sprintf(" AND t.status=$%d", i)
		args = append(args, string(*f.Status))
		i++
	}
	if f.Domain != nil {
		where += fmt.Sprintf(" AND t.domain=$%d", i)
		args = append(args, string(*f.Domain))
		i++
	}
	if f.AssigneeType != nil {
		where += fmt.Sprintf(" AND t.assignee_type=$%d", i)
		args = append(args, string(*f.AssigneeType))
		i++
	}
	if f.AssigneeID != nil {
		where += fmt.Sprintf(" AND t.assignee_id=$%d", i)
		args = append(args, *f.AssigneeID)
		i++
	}
	if f.RequesterID != nil {
		where += fmt.Sprintf(" AND t.requester_id=$%d", i)
		args = append(args, *f.RequesterID)
		i++
	}
	if f.ProjectID != nil {
		where += fmt.Sprintf(" AND t.project_id=$%d", i)
		args = append(args, *f.ProjectID)
		i++
	}
	if f.ParentID != nil {
		where += fmt.Sprintf(" AND t.parent_id=$%d", i)
		args = append(args, *f.ParentID)
		i++
	}

	countQuery := "SELECT COUNT(*) FROM tickets t " + where
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
	rows, err := s.db.Query(ctx,
		ticketSelectSQL+where+
			fmt.Sprintf(" GROUP BY t.id ORDER BY t.created_at DESC LIMIT $%d OFFSET $%d", i, i+1),
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
		specBytes     []byte
		callbackURL   *string
		tagsBytes     []byte
	)

	err := s.Scan(
		&t.ID, &t.Title, &t.Description, &t.Type, &t.Status, &t.Priority,
		&t.AssigneeType, &t.AssigneeID, &t.RequesterID, &sourceBot,
		&domain, &agentCtxBytes, &t.VikunjaTaskID, &metaBytes,
		&t.ProjectID, &t.ParentID, &t.Visibility, &t.CompletionMode,
		&specBytes, &t.RewardXP, &t.DueAt, &callbackURL,
		&t.CreatedAt, &t.UpdatedAt, &t.CompletedAt,
		&tagsBytes,
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
	if callbackURL != nil {
		t.CallbackURL = *callbackURL
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
	if len(specBytes) > 0 {
		if err := json.Unmarshal(specBytes, &t.CompletionSpec); err != nil {
			return nil, fmt.Errorf("unmarshal completion_spec: %w", err)
		}
	}
	if len(tagsBytes) > 0 && string(tagsBytes) != "[]" {
		if err := json.Unmarshal(tagsBytes, &t.Tags); err != nil {
			return nil, fmt.Errorf("unmarshal tags: %w", err)
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

func (s *pgStore) ListSubtasks(ctx context.Context, parentID uuid.UUID) ([]*Ticket, error) {
	q := `
		WITH RECURSIVE subtree AS (
			SELECT t.id FROM tickets t WHERE t.parent_id = $1
			UNION ALL
			SELECT t.id FROM tickets t JOIN subtree st ON t.parent_id = st.id
		)
		` + ticketSelectSQL + `
		JOIN subtree st ON t.id = st.id
		GROUP BY t.id
		ORDER BY t.created_at ASC`

	rows, err := s.db.Query(ctx, q, parentID)
	if err != nil {
		return nil, fmt.Errorf("list subtasks: %w", err)
	}
	defer rows.Close()

	var list []*Ticket
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

func (s *pgStore) AllSubtasksTerminal(ctx context.Context, parentID uuid.UUID) (bool, error) {
	var count int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM tickets
		WHERE parent_id = $1 AND status NOT IN ('completed','cancelled','failed')`,
		parentID,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("all subtasks terminal: %w", err)
	}
	return count == 0, nil
}

func (s *pgStore) SetTicketTags(ctx context.Context, ticketID uuid.UUID, tagIDs []uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "DELETE FROM ticket_tags WHERE ticket_id=$1", ticketID); err != nil {
		return fmt.Errorf("delete ticket_tags: %w", err)
	}
	for _, tagID := range tagIDs {
		if _, err := tx.Exec(ctx,
			"INSERT INTO ticket_tags (ticket_id, tag_id) VALUES ($1,$2) ON CONFLICT DO NOTHING",
			ticketID, tagID,
		); err != nil {
			return fmt.Errorf("insert ticket_tag: %w", err)
		}
	}
	return tx.Commit(ctx)
}

func (s *pgStore) CreateAttempt(ctx context.Context, a *Attempt) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO ticket_attempts (id, ticket_id, user_id, answer, is_correct, ai_feedback, attempted_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		a.ID, a.TicketID, a.UserID, a.Answer, a.IsCorrect, a.AIFeedback, a.AttemptedAt,
	)
	if err != nil {
		return fmt.Errorf("create attempt: %w", err)
	}
	return nil
}

func (s *pgStore) ListAttempts(ctx context.Context, ticketID uuid.UUID) ([]*Attempt, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, ticket_id, user_id, answer, is_correct, ai_feedback, attempted_at
		FROM ticket_attempts WHERE ticket_id=$1 ORDER BY attempted_at ASC`,
		ticketID,
	)
	if err != nil {
		return nil, fmt.Errorf("list attempts: %w", err)
	}
	defer rows.Close()

	var list []*Attempt
	for rows.Next() {
		a := &Attempt{}
		if err := rows.Scan(&a.ID, &a.TicketID, &a.UserID, &a.Answer, &a.IsCorrect, &a.AIFeedback, &a.AttemptedAt); err != nil {
			return nil, fmt.Errorf("scan attempt: %w", err)
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

func (s *pgStore) CountAttempts(ctx context.Context, ticketID uuid.UUID, userID string) (int, error) {
	var count int
	err := s.db.QueryRow(ctx,
		"SELECT COUNT(*) FROM ticket_attempts WHERE ticket_id=$1 AND user_id=$2",
		ticketID, userID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count attempts: %w", err)
	}
	return count, nil
}
