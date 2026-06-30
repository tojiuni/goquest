package resolve

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/project"
	"github.com/tojiuni/goquest/internal/tag"
)

type fakeProjectGetter struct {
	wsID uuid.UUID
	err  error
}

func (f fakeProjectGetter) Get(ctx context.Context, id uuid.UUID) (*project.Project, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &project.Project{ID: id, WorkspaceID: f.wsID}, nil
}

type fakeTagGetOrCreator struct {
	created map[string]uuid.UUID
}

func (f *fakeTagGetOrCreator) GetOrCreate(ctx context.Context, workspaceID uuid.UUID, creatorID, name string) (*tag.Tag, error) {
	if f.created == nil {
		f.created = map[string]uuid.UUID{}
	}
	id, ok := f.created[name]
	if !ok {
		id = uuid.New()
		f.created[name] = id
	}
	return &tag.Tag{ID: id, WorkspaceID: workspaceID, Name: name}, nil
}

func TestResolveTagIDs_DedupAndResolve(t *testing.T) {
	ws := uuid.New()
	r := NewTagResolver(fakeProjectGetter{wsID: ws}, &fakeTagGetOrCreator{})
	ids, err := r.ResolveTagIDs(context.Background(), uuid.New(), "creator", []string{"infra", "infra", "goquest"})
	if err != nil {
		t.Fatalf("ResolveTagIDs: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 deduped ids, got %d", len(ids))
	}
}

func TestResolveTagIDs_Empty(t *testing.T) {
	r := NewTagResolver(fakeProjectGetter{wsID: uuid.New()}, &fakeTagGetOrCreator{})
	ids, err := r.ResolveTagIDs(context.Background(), uuid.New(), "creator", nil)
	if err != nil {
		t.Fatalf("ResolveTagIDs: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("expected 0 ids, got %d", len(ids))
	}
}
