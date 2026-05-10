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
	Title       string    `json:"title"`
	JoinedAt    time.Time `json:"joined_at"`
}

// XPEvent represents a single XP gain from a completed ticket.
type XPEvent struct {
	TicketID    string    `json:"ticket_id"`
	TicketTitle string    `json:"ticket_title"`
	Amount      int       `json:"amount"`
	EarnedAt    time.Time `json:"earned_at"`
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
