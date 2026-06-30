package tag_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/tag"
)

// --- mock ---

type createMockStore struct {
	created *tag.Tag
	tags    []*tag.Tag
	err     error
}

func (m *createMockStore) Create(_ context.Context, t *tag.Tag) error {
	if m.err != nil {
		return m.err
	}
	m.created = t
	return nil
}

func (m *createMockStore) Get(_ context.Context, _ uuid.UUID) (*tag.Tag, error) {
	if m.err != nil {
		return nil, m.err
	}
	if len(m.tags) > 0 {
		return m.tags[0], nil
	}
	return nil, tag.ErrNotFound
}

func (m *createMockStore) Update(_ context.Context, _ uuid.UUID, _ tag.UpdateReq) (*tag.Tag, error) {
	if m.err != nil {
		return nil, m.err
	}
	if len(m.tags) > 0 {
		return m.tags[0], nil
	}
	return nil, tag.ErrNotFound
}

func (m *createMockStore) Delete(_ context.Context, _ uuid.UUID) error { return m.err }

func (m *createMockStore) ListByWorkspace(_ context.Context, _ uuid.UUID) ([]*tag.Tag, error) {
	return m.tags, m.err
}

func (m *createMockStore) GetByName(_ context.Context, _ uuid.UUID, _ string) (*tag.Tag, error) {
	if m.err != nil {
		return nil, m.err
	}
	if len(m.tags) > 0 {
		return m.tags[0], nil
	}
	return nil, tag.ErrNotFound
}

// --- tests ---

func TestTagService_Create_OK(t *testing.T) {
	svc := tag.NewService(&createMockStore{})
	wsID := uuid.New()

	got, err := svc.Create(context.Background(), wsID, "user1", tag.CreateReq{Name: "backend"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "backend" {
		t.Errorf("name = %q, want %q", got.Name, "backend")
	}
	if got.Color == "" {
		t.Error("expected default color to be set")
	}
	if got.WorkspaceID != wsID {
		t.Error("workspace_id mismatch")
	}
}

func TestTagService_Create_DefaultColor(t *testing.T) {
	svc := tag.NewService(&createMockStore{})
	got, err := svc.Create(context.Background(), uuid.New(), "u", tag.CreateReq{Name: "ops"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Color != "#1677ff" {
		t.Errorf("default color = %q, want #1677ff", got.Color)
	}
}

func TestTagService_Create_CustomColor(t *testing.T) {
	svc := tag.NewService(&createMockStore{})
	got, err := svc.Create(context.Background(), uuid.New(), "u", tag.CreateReq{Name: "ops", Color: "#ff0000"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Color != "#ff0000" {
		t.Errorf("color = %q, want #ff0000", got.Color)
	}
}

func TestTagService_Create_EmptyNameRejected(t *testing.T) {
	svc := tag.NewService(&createMockStore{})
	_, err := svc.Create(context.Background(), uuid.New(), "u", tag.CreateReq{Name: "  "})
	if err == nil {
		t.Fatal("expected error for blank name")
	}
}

func TestTagService_ListByWorkspace(t *testing.T) {
	existing := []*tag.Tag{
		{ID: uuid.New(), Name: "a"},
		{ID: uuid.New(), Name: "b"},
	}
	svc := tag.NewService(&createMockStore{tags: existing})
	list, err := svc.ListByWorkspace(context.Background(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Errorf("len = %d, want 2", len(list))
	}
}
