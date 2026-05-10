package xp

import (
	"context"
	"fmt"
	"log"
)

// Service handles XP award logic and level-up detection.
type Service struct {
	store Store
}

func NewService(store Store) *Service { return &Service{store: store} }

// AwardXP satisfies ticket.XPAwarder — thin wrapper around Award.
func (s *Service) AwardXP(ctx context.Context, workspaceID, userID string, amount int) error {
	_, err := s.Award(ctx, workspaceID, userID, amount)
	return err
}

// Award grants XP to a user and returns a GainResult describing what changed.
// If the user is not found in the workspace, the error is logged and swallowed
// (legacy tickets without a workspace context should not block completion).
func (s *Service) Award(ctx context.Context, workspaceID, userID string, amount int) (*GainResult, error) {
	if amount <= 0 {
		return nil, nil
	}

	current, err := s.store.GetUserXP(ctx, workspaceID, userID)
	if err != nil {
		return nil, fmt.Errorf("get user xp: %w", err)
	}

	newXP, newLevel, err := s.store.AddXP(ctx, workspaceID, userID, amount)
	if err != nil {
		return nil, fmt.Errorf("add xp: %w", err)
	}

	lv, err := s.store.LevelForXP(ctx, newXP)
	if err != nil {
		log.Printf("warn: resolve level title: %v", err)
	}

	title := ""
	if lv != nil {
		title = lv.Title
	}

	return &GainResult{
		PreviousXP:    current.XP,
		NewXP:         newXP,
		PreviousLevel: current.Level,
		NewLevel:      newLevel,
		LevelledUp:    newLevel > current.Level,
		Title:         title,
	}, nil
}

// GetUserXP returns the current XP/level for display.
func (s *Service) GetUserXP(ctx context.Context, workspaceID, userID string) (*UserXP, error) {
	return s.store.GetUserXP(ctx, workspaceID, userID)
}

// ListLevels returns all level thresholds.
func (s *Service) ListLevels(ctx context.Context) ([]*Level, error) {
	return s.store.ListLevels(ctx)
}

// ListLeaderboard returns workspace members ordered by XP descending.
func (s *Service) ListLeaderboard(ctx context.Context, workspaceID string, limit int) ([]*UserXP, error) {
	return s.store.ListLeaderboard(ctx, workspaceID, limit)
}

// GetXPHistory returns the caller's XP gains from completed tickets.
func (s *Service) GetXPHistory(ctx context.Context, workspaceID, userID string, limit int) ([]*XPEvent, error) {
	return s.store.ListXPHistory(ctx, workspaceID, userID, limit)
}
