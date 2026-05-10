package apikey_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/apikey"
)

// --- mock store ---

type mockStore struct {
	created   *apikey.APIKey
	createErr error
	keys      []*apikey.APIKey
	found     *apikey.APIKey
	findErr   error
	touched   uuid.UUID
}

func (m *mockStore) Create(_ context.Context, k *apikey.APIKey, _ string) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.created = k
	return nil
}
func (m *mockStore) List(_ context.Context, _ uuid.UUID) ([]*apikey.APIKey, error) {
	return m.keys, nil
}
func (m *mockStore) Delete(_ context.Context, _ uuid.UUID) error { return nil }
func (m *mockStore) FindByHash(_ context.Context, _ string) (*apikey.APIKey, error) {
	return m.found, m.findErr
}
func (m *mockStore) TouchLastUsed(_ context.Context, id uuid.UUID) error {
	m.touched = id
	return nil
}

// --- tests ---

func TestService_Create_GeneratesPlainKey(t *testing.T) {
	store := &mockStore{}
	svc := apikey.NewService(store)

	res, err := svc.Create(context.Background(), uuid.New(), apikey.CreateReq{Name: "ci-bot"}, "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.PlainKey == "" {
		t.Error("expected non-empty plain key")
	}
	if len(res.PlainKey) < 10 {
		t.Errorf("plain key too short: %s", res.PlainKey)
	}
	if res.PlainKey[:4] != "gqk_" {
		t.Errorf("expected gqk_ prefix, got %s", res.PlainKey[:4])
	}
}

func TestService_Create_RequiresName(t *testing.T) {
	svc := apikey.NewService(&mockStore{})
	_, err := svc.Create(context.Background(), uuid.New(), apikey.CreateReq{}, "user1")
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestService_Create_StoresRecord(t *testing.T) {
	store := &mockStore{}
	svc := apikey.NewService(store)
	pid := uuid.New()

	_, err := svc.Create(context.Background(), pid, apikey.CreateReq{Name: "deploy-key"}, "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.created == nil {
		t.Fatal("expected store.Create to be called")
	}
	if store.created.ProjectID != pid {
		t.Errorf("project_id mismatch: %v", store.created.ProjectID)
	}
	if store.created.Name != "deploy-key" {
		t.Errorf("name mismatch: %s", store.created.Name)
	}
}

func TestService_Create_TwoKeysAreDifferent(t *testing.T) {
	svc := apikey.NewService(&mockStore{})
	pid := uuid.New()

	r1, _ := svc.Create(context.Background(), pid, apikey.CreateReq{Name: "k1"}, "u")
	r2, _ := svc.Create(context.Background(), pid, apikey.CreateReq{Name: "k2"}, "u")

	if r1.PlainKey == r2.PlainKey {
		t.Error("expected unique keys per creation")
	}
}

func TestService_Verify_ValidKey(t *testing.T) {
	existing := &apikey.APIKey{
		ID:        uuid.New(),
		ProjectID: uuid.New(),
		Name:      "ci-key",
		CreatedBy: "user1",
		CreatedAt: time.Now().UTC(),
	}
	store := &mockStore{found: existing}
	svc := apikey.NewService(store)

	got, err := svc.Verify(context.Background(), "gqk_anyvalidlookinginput")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != existing.ID {
		t.Error("returned wrong key record")
	}
}

func TestService_Verify_InvalidKey(t *testing.T) {
	store := &mockStore{findErr: apikey.ErrNotFound}
	svc := apikey.NewService(store)

	_, err := svc.Verify(context.Background(), "gqk_bogus")
	if !errors.Is(err, apikey.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestService_List(t *testing.T) {
	keys := []*apikey.APIKey{
		{ID: uuid.New(), Name: "k1"},
		{ID: uuid.New(), Name: "k2"},
	}
	store := &mockStore{keys: keys}
	svc := apikey.NewService(store)

	got, err := svc.List(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 keys, got %d", len(got))
	}
}
