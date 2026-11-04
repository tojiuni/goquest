package mention

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/google/uuid"
)

func ids(ms []*ProjectMember) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.UserID)
	}
	sort.Strings(out)
	return out
}

func lister(project, workspace []string) EligibleLister {
	ws := uuid.New()
	return EligibleLister{
		Project:     func(context.Context, uuid.UUID) ([]string, error) { return project, nil },
		WorkspaceOf: func(context.Context, uuid.UUID) (uuid.UUID, error) { return ws, nil },
		Workspace:   func(context.Context, uuid.UUID) ([]string, error) { return workspace, nil },
	}
}

func TestEligibleLister_ProjectPlusWorkspaceMembers(t *testing.T) {
	// the common real shape: nobody is an explicit project member, access is workspace-level
	got, err := lister(nil, []string{"01SESS", "bob@x.io"}).ListMembers(context.Background(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if g := ids(got); len(g) != 2 || g[0] != "01SESS" || g[1] != "bob@x.io" {
		t.Errorf("workspace members must be eligible: %v", g)
	}
	// union without duplicates, blanks dropped
	got, _ = lister([]string{"a", "b"}, []string{"b", "c", ""}).ListMembers(context.Background(), uuid.New())
	if g := ids(got); len(g) != 3 || g[0] != "a" || g[1] != "b" || g[2] != "c" {
		t.Errorf("union: %v", g)
	}
}

func TestEligibleLister_RestrictedTicketsStayProjectOnly(t *testing.T) {
	ctx := WithProjectMembersOnly(context.Background())
	got, _ := lister([]string{"a"}, []string{"b", "c"}).ListMembers(ctx, uuid.New())
	if g := ids(got); len(g) != 1 || g[0] != "a" {
		t.Errorf("a restricted ticket must not reach plain workspace members: %v", g)
	}
}

func TestEligibleLister_FailsTowardFewerMentions(t *testing.T) {
	l := lister([]string{"a"}, []string{"b"})
	l.Workspace = func(context.Context, uuid.UUID) ([]string, error) { return nil, errors.New("db") }
	got, err := l.ListMembers(context.Background(), uuid.New())
	if err != nil || len(ids(got)) != 1 {
		t.Errorf("a workspace read failure must degrade to project members, got %v %v", ids(got), err)
	}
	l.WorkspaceOf = func(context.Context, uuid.UUID) (uuid.UUID, error) { return uuid.Nil, errors.New("gone") }
	if got, err := l.ListMembers(context.Background(), uuid.New()); err != nil || len(ids(got)) != 1 {
		t.Errorf("unresolvable workspace must degrade too: %v %v", ids(got), err)
	}
	// but a project-members failure is a real error: the caller treats it as "nothing recorded"
	l.Project = func(context.Context, uuid.UUID) ([]string, error) { return nil, errors.New("db") }
	if _, err := l.ListMembers(context.Background(), uuid.New()); err == nil {
		t.Error("project member read failure must be returned")
	}
}
