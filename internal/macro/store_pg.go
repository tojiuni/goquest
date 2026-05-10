package macro

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgStore struct{ db *pgxpool.Pool }

func NewPGStore(db *pgxpool.Pool) Store { return &pgStore{db: db} }

const macroSelectFields = `id, workspace_id, project_id, name, body, created_by, created_at, updated_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanMacro(s rowScanner) (*Macro, error) {
	m := &Macro{}
	err := s.Scan(&m.ID, &m.WorkspaceID, &m.ProjectID, &m.Name, &m.Body, &m.CreatedBy, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan macro: %w", err)
	}
	return m, nil
}

func (s *pgStore) Create(ctx context.Context, m *Macro) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO ticket_macros
			(id, workspace_id, project_id, name, body, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		m.ID, m.WorkspaceID, m.ProjectID, m.Name, m.Body, m.CreatedBy, m.CreatedAt, m.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create macro: %w", err)
	}
	return nil
}

func (s *pgStore) Get(ctx context.Context, id uuid.UUID) (*Macro, error) {
	row := s.db.QueryRow(ctx, `SELECT `+macroSelectFields+` FROM ticket_macros WHERE id=$1`, id)
	return scanMacro(row)
}

func (s *pgStore) Update(ctx context.Context, id uuid.UUID, req UpdateReq) (*Macro, error) {
	_, err := s.db.Exec(ctx, `
		UPDATE ticket_macros SET
			name = COALESCE($2, name),
			body = COALESCE($3, body),
			updated_at = NOW()
		WHERE id=$1`, id, req.Name, req.Body)
	if err != nil {
		return nil, fmt.Errorf("update macro: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *pgStore) Delete(ctx context.Context, id uuid.UUID) error {
	res, err := s.db.Exec(ctx, `DELETE FROM ticket_macros WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("delete macro: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *pgStore) ListByWorkspace(ctx context.Context, workspaceID uuid.UUID, projectID *uuid.UUID) ([]*Macro, error) {
	q := `SELECT ` + macroSelectFields + ` FROM ticket_macros WHERE workspace_id=$1`
	args := []any{workspaceID}
	if projectID != nil {
		// Match exact project OR workspace-wide (null)
		q += ` AND (project_id = $2 OR project_id IS NULL)`
		args = append(args, *projectID)
	}
	q += ` ORDER BY name ASC`

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list macros: %w", err)
	}
	defer rows.Close()

	var list []*Macro
	for rows.Next() {
		m, err := scanMacro(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, m)
	}
	return list, rows.Err()
}
