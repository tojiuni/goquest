package magic

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

func (s *pgStore) Create(ctx context.Context, t *Token, hash string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO magic_tokens
			(id, token_hash, email, ticket_id, expires_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		t.ID, hash, t.Email, t.TicketID, t.ExpiresAt, t.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create magic token: %w", err)
	}
	return nil
}

func (s *pgStore) FindByHash(ctx context.Context, hash string) (*Token, error) {
	t := &Token{}
	err := s.db.QueryRow(ctx, `
		SELECT id, email, ticket_id, expires_at, used_at, created_at
		FROM magic_tokens WHERE token_hash=$1`, hash,
	).Scan(&t.ID, &t.Email, &t.TicketID, &t.ExpiresAt, &t.UsedAt, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find magic token: %w", err)
	}
	return t, nil
}

func (s *pgStore) MarkUsed(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE magic_tokens SET used_at=NOW() WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("mark magic token used: %w", err)
	}
	return nil
}
