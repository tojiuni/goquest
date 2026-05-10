package template

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tojiuni/goquest/internal/ticket"
)

type pgStore struct{ pool *pgxpool.Pool }

func NewPGStore(pool *pgxpool.Pool) Store { return &pgStore{pool: pool} }

func (s *pgStore) Create(ctx context.Context, t *Template) error {
	specJSON, err := json.Marshal(t.CompletionSpec)
	if err != nil {
		return fmt.Errorf("marshal spec: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO ticket_templates
		  (id, project_id, name, title, description, type, priority,
		   completion_mode, completion_spec, reward_xp, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		t.ID, t.ProjectID, t.Name, t.Title, t.Description,
		t.Type, t.Priority, t.CompletionMode, specJSON, t.RewardXP,
		t.CreatedBy, t.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert template: %w", err)
	}
	return nil
}

func (s *pgStore) List(ctx context.Context, projectID uuid.UUID) ([]*Template, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, project_id, name, title, description, type, priority,
		       completion_mode, completion_spec, reward_xp, created_by, created_at
		FROM ticket_templates
		WHERE project_id = $1
		ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("query templates: %w", err)
	}
	defer rows.Close()

	var templates []*Template
	for rows.Next() {
		t, err := scanTemplate(rows.Scan)
		if err != nil {
			return nil, err
		}
		templates = append(templates, t)
	}
	return templates, rows.Err()
}

func (s *pgStore) Get(ctx context.Context, id uuid.UUID) (*Template, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, project_id, name, title, description, type, priority,
		       completion_mode, completion_spec, reward_xp, created_by, created_at
		FROM ticket_templates WHERE id = $1`, id)

	t, err := scanTemplate(row.Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

func (s *pgStore) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM ticket_templates WHERE id = $1`, id)
	return err
}

func scanTemplate(scan func(...any) error) (*Template, error) {
	t := &Template{}
	var specRaw []byte
	var typ, priority *string

	err := scan(
		&t.ID, &t.ProjectID, &t.Name, &t.Title, &t.Description,
		&typ, &priority, &t.CompletionMode, &specRaw, &t.RewardXP,
		&t.CreatedBy, &t.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan template: %w", err)
	}

	if typ != nil {
		t.Type = ticket.TicketType(*typ)
	}
	if priority != nil {
		t.Priority = ticket.Priority(*priority)
	}
	if len(specRaw) > 0 {
		if err := json.Unmarshal(specRaw, &t.CompletionSpec); err != nil {
			return nil, fmt.Errorf("unmarshal spec: %w", err)
		}
	}
	return t, nil
}
