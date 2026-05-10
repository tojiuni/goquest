package xp

import "time"

// Level represents a rank threshold.
type Level struct {
	Level      int    `json:"level"`
	XPRequired int    `json:"xp_required"`
	Title      string `json:"title"`
}

// UserXP is the denormalised XP/level row stored in workspace_members.
type UserXP struct {
	WorkspaceID string    `json:"workspace_id"`
	UserID      string    `json:"user_id"`
	XP          int       `json:"xp"`
	Level       int       `json:"level"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// GainResult is returned after awarding XP; includes whether the user levelled up.
type GainResult struct {
	PreviousXP    int    `json:"previous_xp"`
	NewXP         int    `json:"new_xp"`
	PreviousLevel int    `json:"previous_level"`
	NewLevel      int    `json:"new_level"`
	LevelledUp    bool   `json:"levelled_up"`
	Title         string `json:"title"`
}
