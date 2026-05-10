package macro_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/macro"
)

type fakeStore struct {
	items map[uuid.UUID]*macro.Macro
}

func newFakeStore() *fakeStore {
	return &fakeStore{items: make(map[uuid.UUID]*macro.Macro)}
}

func (s *fakeStore) Create(_ context.Context, m *macro.Macro) error {
	s.items[m.ID] = m
	return nil
}

func (s *fakeStore) Get(_ context.Context, id uuid.UUID) (*macro.Macro, error) {
	m, ok := s.items[id]
	if !ok {
		return nil, macro.ErrNotFound
	}
	return m, nil
}

func (s *fakeStore) Update(_ context.Context, id uuid.UUID, req macro.UpdateReq) (*macro.Macro, error) {
	m, ok := s.items[id]
	if !ok {
		return nil, macro.ErrNotFound
	}
	if req.Name != nil {
		m.Name = *req.Name
	}
	if req.Body != nil {
		m.Body = *req.Body
	}
	return m, nil
}

func (s *fakeStore) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := s.items[id]; !ok {
		return macro.ErrNotFound
	}
	delete(s.items, id)
	return nil
}

func (s *fakeStore) ListByWorkspace(_ context.Context, workspaceID uuid.UUID, projectID *uuid.UUID) ([]*macro.Macro, error) {
	var out []*macro.Macro
	for _, m := range s.items {
		if m.WorkspaceID != workspaceID {
			continue
		}
		if projectID != nil {
			// match the project OR workspace-wide (nil)
			if m.ProjectID != nil && *m.ProjectID != *projectID {
				continue
			}
		}
		out = append(out, m)
	}
	return out, nil
}

func TestService_Create_RejectsEmpty(t *testing.T) {
	svc := macro.NewService(newFakeStore())
	if _, err := svc.Create(context.Background(), uuid.New(), "u1", macro.CreateReq{Name: "", Body: "x"}); err == nil {
		t.Fatal("expected error for empty name")
	}
	if _, err := svc.Create(context.Background(), uuid.New(), "u1", macro.CreateReq{Name: "x", Body: " "}); err == nil {
		t.Fatal("expected error for empty body")
	}
}

func TestService_ListByWorkspace_FiltersByProject(t *testing.T) {
	svc := macro.NewService(newFakeStore())
	wid := uuid.New()
	pid := uuid.New()
	other := uuid.New()

	wsWide, _ := svc.Create(context.Background(), wid, "u1", macro.CreateReq{Name: "ws-wide", Body: "b"})
	pjA, _ := svc.Create(context.Background(), wid, "u1", macro.CreateReq{Name: "pj-a", Body: "b", ProjectID: &pid})
	_, _ = svc.Create(context.Background(), wid, "u1", macro.CreateReq{Name: "pj-other", Body: "b", ProjectID: &other})

	got, err := svc.ListByWorkspace(context.Background(), wid, &pid)
	if err != nil {
		t.Fatal(err)
	}
	// Should include wsWide (null project) + pjA (matches pid), but NOT pj-other.
	names := map[string]bool{}
	for _, m := range got {
		names[m.Name] = true
	}
	if !names[wsWide.Name] || !names[pjA.Name] {
		t.Fatalf("missing expected macros, got names=%v", names)
	}
	if names["pj-other"] {
		t.Fatal("project filter leaked an unrelated project's macro")
	}
}
