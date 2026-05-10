package xp

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgStore struct{ db *pgxpool.Pool }

func NewPGStore(db *pgxpool.Pool) Store { return &pgStore{db: db} }

func (s *pgStore) GetUserXP(ctx context.Context, workspaceID, userID string) (*UserXP, error) {
	row := s.db.QueryRow(ctx, `
		SELECT workspace_id, user_id, xp, level, title, joined_at
		FROM workspace_members
		WHERE workspace_id=$1 AND user_id=$2`,
		workspaceID, userID,
	)
	u := &UserXP{}
	if err := row.Scan(&u.WorkspaceID, &u.UserID, &u.XP, &u.Level, &u.Title, &u.JoinedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get user xp: %w", err)
	}
	return u, nil
}

// AddXP atomically adds XP and recalculates level in a single transaction.
// It uses the xp_levels table to determine the new level.
func (s *pgStore) AddXP(ctx context.Context, workspaceID, userID string, amount int) (int, int, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	// Increment XP
	var newXP int
	if err := tx.QueryRow(ctx, `
		UPDATE workspace_members
		SET xp = xp + $3
		WHERE workspace_id=$1 AND user_id=$2
		RETURNING xp`,
		workspaceID, userID, amount,
	).Scan(&newXP); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, 0, ErrNotFound
		}
		return 0, 0, fmt.Errorf("add xp: %w", err)
	}

	// Resolve new level
	var newLevel int
	var title string
	if err := tx.QueryRow(ctx, `
		SELECT level, title FROM xp_levels
		WHERE xp_required <= $1
		ORDER BY level DESC LIMIT 1`,
		newXP,
	).Scan(&newLevel, &title); err != nil {
		return 0, 0, fmt.Errorf("resolve level: %w", err)
	}

	// Update level
	if _, err := tx.Exec(ctx, `
		UPDATE workspace_members SET level=$3 WHERE workspace_id=$1 AND user_id=$2`,
		workspaceID, userID, newLevel,
	); err != nil {
		return 0, 0, fmt.Errorf("update level: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("commit: %w", err)
	}
	return newXP, newLevel, nil
}

func (s *pgStore) ListLevels(ctx context.Context) ([]*Level, error) {
	rows, err := s.db.Query(ctx, `SELECT level, xp_required, title FROM xp_levels ORDER BY level ASC`)
	if err != nil {
		return nil, fmt.Errorf("list levels: %w", err)
	}
	defer rows.Close()

	var list []*Level
	for rows.Next() {
		l := &Level{}
		if err := rows.Scan(&l.Level, &l.XPRequired, &l.Title); err != nil {
			return nil, fmt.Errorf("scan level: %w", err)
		}
		list = append(list, l)
	}
	return list, rows.Err()
}

func (s *pgStore) LevelForXP(ctx context.Context, xpVal int) (*Level, error) {
	row := s.db.QueryRow(ctx, `
		SELECT level, xp_required, title FROM xp_levels
		WHERE xp_required <= $1
		ORDER BY level DESC LIMIT 1`,
		xpVal,
	)
	l := &Level{}
	if err := row.Scan(&l.Level, &l.XPRequired, &l.Title); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &Level{Level: 1, XPRequired: 0, Title: "Novice"}, nil
		}
		return nil, fmt.Errorf("level for xp: %w", err)
	}
	return l, nil
}

func (s *pgStore) ListLeaderboard(ctx context.Context, workspaceID string, limit int) ([]*UserXP, error) {
	rows, err := s.db.Query(ctx, `
		SELECT user_id, xp, level, title
		FROM workspace_members
		WHERE workspace_id=$1
		ORDER BY xp DESC LIMIT $2`,
		workspaceID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list leaderboard: %w", err)
	}
	defer rows.Close()

	var list []*UserXP
	for rows.Next() {
		u := &UserXP{WorkspaceID: workspaceID}
		if err := rows.Scan(&u.UserID, &u.XP, &u.Level, &u.Title); err != nil {
			return nil, fmt.Errorf("scan leaderboard: %w", err)
		}
		list = append(list, u)
	}
	return list, rows.Err()
}

func (s *pgStore) ListXPHistory(ctx context.Context, workspaceID, userID string, limit int) ([]*XPEvent, error) {
	rows, err := s.db.Query(ctx, `
		SELECT t.id, t.title, t.reward_xp, t.completed_at
		FROM tickets t
		JOIN projects p ON t.project_id = p.id
		WHERE p.workspace_id=$1 AND t.assignee_id=$2
		  AND t.status='completed' AND t.reward_xp > 0
		ORDER BY t.completed_at DESC NULLS LAST
		LIMIT $3`,
		workspaceID, userID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list xp history: %w", err)
	}
	defer rows.Close()

	var list []*XPEvent
	for rows.Next() {
		e := &XPEvent{}
		if err := rows.Scan(&e.TicketID, &e.TicketTitle, &e.Amount, &e.EarnedAt); err != nil {
			return nil, fmt.Errorf("scan xp history: %w", err)
		}
		list = append(list, e)
	}
	return list, rows.Err()
}

var ErrNotFound = errors.New("xp: user not found")
