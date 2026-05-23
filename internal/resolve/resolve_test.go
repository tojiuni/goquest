package resolve_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/project"
	"github.com/tojiuni/goquest/internal/resolve"
	"github.com/tojiuni/goquest/internal/workspace"
)

type fakeWS struct {
	bySlug map[string]*workspace.Workspace
	byName map[string]*workspace.Workspace
}

func (f fakeWS) GetBySlug(_ context.Context, s string) (*workspace.Workspace, error) {
	if ws, ok := f.bySlug[s]; ok {
		return ws, nil
	}
	return nil, workspace.ErrNotFound
}
func (f fakeWS) GetByName(_ context.Context, n string) (*workspace.Workspace, error) {
	if ws, ok := f.byName[n]; ok {
		return ws, nil
	}
	return nil, workspace.ErrNotFound
}

type fakePJ struct{ byKey map[string]*project.Project } // key: wid|name

func (f fakePJ) GetByWorkspaceAndName(_ context.Context, wid uuid.UUID, name string) (*project.Project, error) {
	if p, ok := f.byKey[wid.String()+"|"+name]; ok {
		return p, nil
	}
	return nil, project.ErrNotFound
}

func newResolver() (*resolve.Resolver, uuid.UUID, uuid.UUID) {
	wid := uuid.New()
	pid := uuid.New()
	ws := &workspace.Workspace{ID: wid, Name: "Team", Slug: "team"}
	pj := &project.Project{ID: pid, WorkspaceID: wid, Name: "Alpha"}
	r := resolve.New(
		fakeWS{bySlug: map[string]*workspace.Workspace{"team": ws}, byName: map[string]*workspace.Workspace{"Team": ws}},
		fakePJ{byKey: map[string]*project.Project{wid.String() + "|Alpha": pj}},
	)
	return r, wid, pid
}

func TestResolve_BySlug(t *testing.T) {
	r, _, pid := newResolver()
	got, err := r.ResolveProjectID(context.Background(), resolve.Ref{WorkspaceSlug: "team", ProjectName: "Alpha"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || *got != pid {
		t.Fatalf("got %v, want %v", got, pid)
	}
}

func TestResolve_ByName(t *testing.T) {
	r, _, pid := newResolver()
	got, err := r.ResolveProjectID(context.Background(), resolve.Ref{WorkspaceName: "Team", ProjectName: "Alpha"})
	if err != nil || got == nil || *got != pid {
		t.Fatalf("got %v err %v, want %v", got, err, pid)
	}
}

func TestResolve_SlugPreferredOverName(t *testing.T) {
	r, _, pid := newResolver()
	got, err := r.ResolveProjectID(context.Background(), resolve.Ref{WorkspaceSlug: "team", WorkspaceName: "Wrong", ProjectName: "Alpha"})
	if err != nil || got == nil || *got != pid {
		t.Fatalf("slug should win: got %v err %v", got, err)
	}
}

func TestResolve_EmptyRefReturnsNilNil(t *testing.T) {
	r, _, _ := newResolver()
	got, err := r.ResolveProjectID(context.Background(), resolve.Ref{})
	if err != nil || got != nil {
		t.Fatalf("empty ref: got %v err %v, want nil,nil", got, err)
	}
}

func TestResolve_IncompleteRef(t *testing.T) {
	r, _, _ := newResolver()
	_, err := r.ResolveProjectID(context.Background(), resolve.Ref{WorkspaceSlug: "team"})
	if !errors.Is(err, resolve.ErrIncompleteRef) {
		t.Fatalf("err = %v, want ErrIncompleteRef", err)
	}
}

func TestResolve_WorkspaceNotFound(t *testing.T) {
	r, _, _ := newResolver()
	_, err := r.ResolveProjectID(context.Background(), resolve.Ref{WorkspaceName: "Nope", ProjectName: "Alpha"})
	if !errors.Is(err, resolve.ErrWorkspaceNotFound) {
		t.Fatalf("err = %v, want ErrWorkspaceNotFound", err)
	}
}

func TestResolve_ProjectNotFound(t *testing.T) {
	r, _, _ := newResolver()
	_, err := r.ResolveProjectID(context.Background(), resolve.Ref{WorkspaceSlug: "team", ProjectName: "Ghost"})
	if !errors.Is(err, resolve.ErrProjectNotFound) {
		t.Fatalf("err = %v, want ErrProjectNotFound", err)
	}
}
