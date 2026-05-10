package xp_test

import (
	"context"
	"testing"
	"time"

	"github.com/tojiuni/goquest/internal/xp"
)

// --- mock store ---

type mockStore struct {
	userXP   *xp.UserXP
	getErr   error
	newXP    int
	newLevel int
	addErr   error
	levels   []*xp.Level
	levelFor *xp.Level
}

func (m *mockStore) GetUserXP(_ context.Context, _, _ string) (*xp.UserXP, error) {
	return m.userXP, m.getErr
}

func (m *mockStore) AddXP(_ context.Context, _, _ string, _ int) (int, int, error) {
	return m.newXP, m.newLevel, m.addErr
}

func (m *mockStore) ListLevels(_ context.Context) ([]*xp.Level, error) {
	return m.levels, nil
}

func (m *mockStore) LevelForXP(_ context.Context, _ int) (*xp.Level, error) {
	return m.levelFor, nil
}

// --- helpers ---

func makeUserXP(xpVal, level int) *xp.UserXP {
	return &xp.UserXP{
		WorkspaceID: "ws1",
		UserID:      "u1",
		XP:          xpVal,
		Level:       level,
		UpdatedAt:   time.Now().UTC(),
	}
}

// --- tests ---

func TestService_Award_ZeroAmountIsNoop(t *testing.T) {
	svc := xp.NewService(&mockStore{userXP: makeUserXP(100, 2)})
	result, err := svc.Award(context.Background(), "ws1", "u1", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for zero xp award")
	}
}

func TestService_Award_NegativeAmountIsNoop(t *testing.T) {
	svc := xp.NewService(&mockStore{userXP: makeUserXP(100, 2)})
	result, err := svc.Award(context.Background(), "ws1", "u1", -10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for negative xp award")
	}
}

func TestService_Award_NoLevelUp(t *testing.T) {
	store := &mockStore{
		userXP:   makeUserXP(50, 1),
		newXP:    100,
		newLevel: 1,
		levelFor: &xp.Level{Level: 1, XPRequired: 0, Title: "Novice"},
	}
	svc := xp.NewService(store)

	result, err := svc.Award(context.Background(), "ws1", "u1", 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PreviousXP != 50 {
		t.Errorf("PreviousXP = %d, want 50", result.PreviousXP)
	}
	if result.NewXP != 100 {
		t.Errorf("NewXP = %d, want 100", result.NewXP)
	}
	if result.LevelledUp {
		t.Error("expected no level-up")
	}
	if result.Title != "Novice" {
		t.Errorf("Title = %q, want Novice", result.Title)
	}
}

func TestService_Award_LevelUp(t *testing.T) {
	store := &mockStore{
		userXP:   makeUserXP(90, 1),
		newXP:    200,
		newLevel: 2,
		levelFor: &xp.Level{Level: 2, XPRequired: 100, Title: "Apprentice"},
	}
	svc := xp.NewService(store)

	result, err := svc.Award(context.Background(), "ws1", "u1", 110)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.LevelledUp {
		t.Error("expected level-up")
	}
	if result.NewLevel != 2 {
		t.Errorf("NewLevel = %d, want 2", result.NewLevel)
	}
	if result.PreviousLevel != 1 {
		t.Errorf("PreviousLevel = %d, want 1", result.PreviousLevel)
	}
}

func TestService_AwardXP_SatisfiesInterface(t *testing.T) {
	store := &mockStore{
		userXP:   makeUserXP(0, 1),
		newXP:    50,
		newLevel: 1,
	}
	svc := xp.NewService(store)

	err := svc.AwardXP(context.Background(), "ws1", "u1", 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestService_ListLevels(t *testing.T) {
	levels := []*xp.Level{
		{Level: 1, XPRequired: 0, Title: "Novice"},
		{Level: 2, XPRequired: 100, Title: "Apprentice"},
	}
	store := &mockStore{levels: levels}
	svc := xp.NewService(store)

	got, err := svc.ListLevels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len(levels) = %d, want 2", len(got))
	}
}

func TestService_GetUserXP(t *testing.T) {
	expected := makeUserXP(250, 3)
	store := &mockStore{userXP: expected}
	svc := xp.NewService(store)

	got, err := svc.GetUserXP(context.Background(), "ws1", "u1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.XP != 250 {
		t.Errorf("XP = %d, want 250", got.XP)
	}
	if got.Level != 3 {
		t.Errorf("Level = %d, want 3", got.Level)
	}
}
