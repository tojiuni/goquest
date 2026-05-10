package tag

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgStore struct{ db *pgxpool.Pool }

func NewPGStore(db *pgxpool.Pool) Store { return &pgStore{db: db} }

func (s *pgStore) Create(ctx context.Context, t *Tag) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO tags (id, workspace_id, name, color, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		t.ID, t.WorkspaceID, t.Name, t.Color, t.CreatedBy, t.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create tag: %w", err)
	}
	return nil
}

func (s *pgStore) Get(ctx context.Context, id uuid.UUID) (*Tag, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, workspace_id, name, color, created_by, created_at
		FROM tags WHERE id=$1`, id)
	return scanTag(row)
}

func (s *pgStore) Update(ctx context.Context, id uuid.UUID, req UpdateReq) (*Tag, error) {
	now := time.Now().UTC()
	_ = now
	_, err := s.db.Exec(ctx, `
		UPDATE tags SET
			name  = COALESCE($2, name),
			color = COALESCE($3, color)
		WHERE id=$1`,
		id, req.Name, req.Color,
	)
	if err != nil {
		return nil, fmt.Errorf("update tag: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *pgStore) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, "DELETE FROM tags WHERE id=$1", id)
	return err
}

func (s *pgStore) ListByWorkspace(ctx context.Context, workspaceID uuid.UUID) ([]*Tag, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, workspace_id, name, color, created_by, created_at
		FROM tags WHERE workspace_id=$1 ORDER BY name ASC`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	defer rows.Close()

	var list []*Tag
	for rows.Next() {
		t, err := scanTag(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

type rowScanner interface{ Scan(dest ...any) error }

func scanTag(s rowScanner) (*Tag, error) {
	t := &Tag{}
	err := s.Scan(&t.ID, &t.WorkspaceID, &t.Name, &t.Color, &t.CreatedBy, &t.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan tag: %w", err)
	}
	return t, nil
}

var ErrNotFound = errors.New("tag: not found")
