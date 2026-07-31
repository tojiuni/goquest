package mention

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgStore struct{ db *pgxpool.Pool }

func NewPGStore(db *pgxpool.Pool) Store { return &pgStore{db: db} }

const mentionSelectFields = `
	id, ticket_id, comment_id, mentioned_actor_id, source,
	created_at, created_by, seen_at`

func (s *pgStore) Create(ctx context.Context, m *Mention) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO ticket_mentions
			(id, ticket_id, comment_id, mentioned_actor_id, source, created_at, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		m.ID, m.TicketID, m.CommentID, m.MentionedActorID, string(m.Source), m.CreatedAt, m.CreatedBy,
	)
	if err != nil {
		return fmt.Errorf("mention: create: %w", err)
	}
	return nil
}

func (s *pgStore) ListByActor(ctx context.Context, actorID string, limit, offset int) ([]*Mention, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+mentionSelectFields+`
		FROM ticket_mentions
		WHERE mentioned_actor_id=$1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`, actorID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("mention: list by actor: %w", err)
	}
	defer rows.Close()

	var out []*Mention
	for rows.Next() {
		m, err := scanMention(rows)
		if err != nil {
			return nil, fmt.Errorf("mention: scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *pgStore) CountUnseenByActor(ctx context.Context, actorID string) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM ticket_mentions
		WHERE mentioned_actor_id=$1 AND seen_at IS NULL`, actorID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("mention: count unseen: %w", err)
	}
	return n, nil
}

func (s *pgStore) MarkSeen(ctx context.Context, id uuid.UUID, actorID string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE ticket_mentions SET seen_at=$1
		WHERE id=$2 AND mentioned_actor_id=$3 AND seen_at IS NULL`,
		time.Now().UTC(), id, actorID,
	)
	if err != nil {
		return fmt.Errorf("mention: mark seen: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanMention(row pgx.Row) (*Mention, error) {
	var m Mention
	var source string
	if err := row.Scan(
		&m.ID, &m.TicketID, &m.CommentID, &m.MentionedActorID, &source,
		&m.CreatedAt, &m.CreatedBy, &m.SeenAt,
	); err != nil {
		return nil, err
	}
	m.Source = Source(source)
	return &m, nil
}
