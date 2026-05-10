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
}
