package xp

import "context"

// Store abstracts XP and level data access.
type Store interface {
	// GetUserXP returns the current XP/level for a user in a workspace.
	GetUserXP(ctx context.Context, workspaceID, userID string) (*UserXP, error)

	// AddXP atomically increments XP and updates the level.
	// Returns the new XP and level values.
	AddXP(ctx context.Context, workspaceID, userID string, amount int) (newXP, newLevel int, err error)

	// ListLevels returns all XP level thresholds ordered by level ASC.
	ListLevels(ctx context.Context) ([]*Level, error)

	// LevelForXP returns the highest level whose xp_required <= xp.
	LevelForXP(ctx context.Context, xp int) (*Level, error)

	// ListLeaderboard returns workspace members ordered by XP descending.
	ListLeaderboard(ctx context.Context, workspaceID string, limit int) ([]*UserXP, error)

	// ListXPHistory returns the caller's XP gains from completed tickets.
	ListXPHistory(ctx context.Context, workspaceID, userID string, limit int) ([]*XPEvent, error)
}
