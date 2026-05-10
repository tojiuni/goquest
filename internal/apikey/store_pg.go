package apikey

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when an API key record does not exist or is expired.
var ErrNotFound = errors.New("api key not found")

type pgStore struct{ pool *pgxpool.Pool }

func NewPGStore(pool *pgxpool.Pool) Store { return &pgStore{pool: pool} }

func (s *pgStore) Create(ctx context.Context, k *APIKey, keyHash string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO project_api_keys (id, project_id, name, key_hash, created_by, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		k.ID, k.ProjectID, k.Name, keyHash, k.CreatedBy, k.ExpiresAt, k.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert api key: %w", err)
	}
	return nil
}

func (s *pgStore) List(ctx context.Context, projectID uuid.UUID) ([]*APIKey, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, project_id, name, created_by, expires_at, last_used_at, created_at
		FROM project_api_keys
		WHERE project_id = $1
		ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("query api keys: %w", err)
	}
	defer rows.Close()

	var keys []*APIKey
	for rows.Next() {
		k := &APIKey{}
		if err := rows.Scan(&k.ID, &k.ProjectID, &k.Name, &k.CreatedBy,
			&k.ExpiresAt, &k.LastUsedAt, &k.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan api key: %w", err)
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (s *pgStore) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM project_api_keys WHERE id = $1`, id)
	return err
}

func (s *pgStore) FindByHash(ctx context.Context, keyHash string) (*APIKey, error) {
	k := &APIKey{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, project_id, name, created_by, expires_at, last_used_at, created_at
		FROM project_api_keys
		WHERE key_hash = $1`, keyHash).
		Scan(&k.ID, &k.ProjectID, &k.Name, &k.CreatedBy,
			&k.ExpiresAt, &k.LastUsedAt, &k.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find api key: %w", err)
	}
	// Reject expired keys.
	if k.ExpiresAt != nil && k.ExpiresAt.Before(time.Now()) {
		return nil, ErrNotFound
	}
	return k, nil
}

func (s *pgStore) TouchLastUsed(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE project_api_keys SET last_used_at = NOW() WHERE id = $1`, id)
	return err
}
