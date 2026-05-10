package comment

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

func (s *pgStore) Create(ctx context.Context, c *Comment) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO ticket_comments
			(id, ticket_id, author_id, author_kind, body, is_internal, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		c.ID, c.TicketID, c.AuthorID, string(c.AuthorKind), c.Body, c.IsInternal, c.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create comment: %w", err)
	}
	return nil
}

func (s *pgStore) Get(ctx context.Context, id uuid.UUID) (*Comment, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, ticket_id, author_id, author_kind, body, is_internal, created_at
		FROM ticket_comments WHERE id=$1`, id)
	return scanComment(row)
}

func (s *pgStore) ListByTicket(ctx context.Context, ticketID uuid.UUID, includeInternal bool) ([]*Comment, error) {
	q := `
		SELECT id, ticket_id, author_id, author_kind, body, is_internal, created_at
		FROM ticket_comments
		WHERE ticket_id=$1`
	if !includeInternal {
		q += " AND is_internal = FALSE"
	}
	q += " ORDER BY created_at ASC"

	rows, err := s.db.Query(ctx, q, ticketID)
	if err != nil {
		return nil, fmt.Errorf("list comments: %w", err)
	}
	defer rows.Close()

	var list []*Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

func (s *pgStore) Update(ctx context.Context, id uuid.UUID, body string) (*Comment, error) {
	_, err := s.db.Exec(ctx, `UPDATE ticket_comments SET body=$2 WHERE id=$1`, id, body)
	if err != nil {
		return nil, fmt.Errorf("update comment: %w", err)
	}
	return s.Get(ctx, id)
}

func (s *pgStore) Delete(ctx context.Context, id uuid.UUID) error {
	res, err := s.db.Exec(ctx, "DELETE FROM ticket_comments WHERE id=$1", id)
	if err != nil {
		return fmt.Errorf("delete comment: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

type rowScanner interface{ Scan(dest ...any) error }

func scanComment(s rowScanner) (*Comment, error) {
	c := &Comment{}
	var kind string
	err := s.Scan(&c.ID, &c.TicketID, &c.AuthorID, &kind, &c.Body, &c.IsInternal, &c.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan comment: %w", err)
	}
	c.AuthorKind = AuthorKind(kind)
	return c, nil
}
