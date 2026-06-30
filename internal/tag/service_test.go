package tag

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// mockStore implements Store for unit tests.
type mockStore struct {
	tags        map[uuid.UUID]*Tag
	createCalls int
	// failCreateOnce: simulate a concurrent insert losing the race (unique
	// violation) the first time Create is called, then succeed-by-existing.
	failCreateOnce bool
}

func newMockStore() *mockStore { return &mockStore{tags: map[uuid.UUID]*Tag{}} }

func (m *mockStore) Create(ctx context.Context, t *Tag) error {
	m.createCalls++
	if m.failCreateOnce {
		m.failCreateOnce = false
		// Simulate the row already existing (racer committed first).
		existing := &Tag{ID: uuid.New(), WorkspaceID: t.WorkspaceID, Name: t.Name}
		m.tags[existing.ID] = existing
		return errors.New("ERROR: duplicate key value violates unique constraint \"tags_workspace_lower_name_key\" (SQLSTATE 23505)")
	}
	m.tags[t.ID] = t
	return nil
}
func (m *mockStore) Get(ctx context.Context, id uuid.UUID) (*Tag, error) {
	if t, ok := m.tags[id]; ok {
		return t, nil
	}
	return nil, ErrNotFound
}
func (m *mockStore) Update(ctx context.Context, id uuid.UUID, req UpdateReq) (*Tag, error) {
	return nil, errors.New("not used")
}
func (m *mockStore) Delete(ctx context.Context, id uuid.UUID) error { return nil }
func (m *mockStore) ListByWorkspace(ctx context.Context, workspaceID uuid.UUID) ([]*Tag, error) {
	var out []*Tag
	for _, t := range m.tags {
		if t.WorkspaceID == workspaceID {
			out = append(out, t)
		}
	}
	return out, nil
}
func (m *mockStore) GetByName(ctx context.Context, workspaceID uuid.UUID, name string) (*Tag, error) {
	for _, t := range m.tags {
		if t.WorkspaceID == workspaceID && equalFold(t.Name, name) {
			return t, nil
		}
	}
	return nil, ErrNotFound
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

func TestGetOrCreate_CreatesWhenMissing(t *testing.T) {
	st := newMockStore()
	svc := NewService(st)
	ws := uuid.New()

	got, err := svc.GetOrCreate(context.Background(), ws, "creator", "infra")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if got.Name != "infra" || got.WorkspaceID != ws {
		t.Fatalf("unexpected tag: %+v", got)
	}
	if st.createCalls != 1 {
		t.Fatalf("expected 1 create call, got %d", st.createCalls)
	}
}

func TestGetOrCreate_ReturnsExistingCaseInsensitive(t *testing.T) {
	st := newMockStore()
	svc := NewService(st)
	ws := uuid.New()
	if _, err := svc.GetOrCreate(context.Background(), ws, "creator", "Infra"); err != nil {
		t.Fatal(err)
	}
	st.createCalls = 0

	got, err := svc.GetOrCreate(context.Background(), ws, "creator", "infra")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if got.Name != "Infra" {
		t.Fatalf("expected existing tag 'Infra', got %q", got.Name)
	}
	if st.createCalls != 0 {
		t.Fatalf("expected no create call, got %d", st.createCalls)
	}
}

func TestGetOrCreate_AbsorbsRace(t *testing.T) {
	st := newMockStore()
	st.failCreateOnce = true
	svc := NewService(st)
	ws := uuid.New()

	got, err := svc.GetOrCreate(context.Background(), ws, "creator", "data")
	if err != nil {
		t.Fatalf("expected race to be absorbed, got error: %v", err)
	}
	if got.Name != "data" {
		t.Fatalf("expected re-read tag 'data', got %q", got.Name)
	}
}
