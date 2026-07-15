package project_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/project"
)

// ── mock writer ───────────────────────────────────────────────────

type mockWriter struct {
	pwLinks   []string // "projectID|wsID"
	pjWrites  []string // "projectID|userID|role"
	pjDeletes []string // "projectID|userID"
}

func (m *mockWriter) WriteTicket(_ context.Context, _, _ uuid.UUID, _, _ string) error { return nil }
func (m *mockWriter) WriteWorkspaceMember(_ context.Context, _ uuid.UUID, _, _ string) error {
	return nil
}
func (m *mockWriter) DeleteWorkspaceMember(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}
func (m *mockWriter) WriteProjectMember(_ context.Context, pid uuid.UUID, uid, role string) error {
	m.pjWrites = append(m.pjWrites, pid.String()+"|"+uid+"|"+role)
	return nil
}
func (m *mockWriter) DeleteProjectMember(_ context.Context, pid uuid.UUID, uid string) error {
	m.pjDeletes = append(m.pjDeletes, pid.String()+"|"+uid)
	return nil
}
func (m *mockWriter) WriteProjectWorkspace(_ context.Context, pid, wid uuid.UUID) error {
	m.pwLinks = append(m.pwLinks, pid.String()+"|"+wid.String())
	return nil
}
func (m *mockWriter) AddTicketViewer(_ context.Context, _ uuid.UUID, _ string) error { return nil }
func (m *mockWriter) ReplaceTicketAssignee(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}
func (m *mockWriter) ReplaceTicketProject(_ context.Context, _, _ uuid.UUID) error { return nil }
func (m *mockWriter) WriteWorkspaceOrg(_ context.Context, _ uuid.UUID, _ string) error { return nil }
func (m *mockWriter) DeleteWorkspaceOrg(_ context.Context, _ uuid.UUID) error          { return nil }

// ── mock store ────────────────────────────────────────────────────

type mockStore struct {
	projects map[uuid.UUID]*project.Project
	members  map[string]*project.Member // key: pid+":"+uid
}

func newMockStore() *mockStore {
	return &mockStore{
		projects: make(map[uuid.UUID]*project.Project),
		members:  make(map[string]*project.Member),
	}
}

func memberKey(pid uuid.UUID, uid string) string { return pid.String() + ":" + uid }

func (m *mockStore) Create(_ context.Context, p *project.Project) error {
	m.projects[p.ID] = p
	return nil
}

func (m *mockStore) Get(_ context.Context, id uuid.UUID) (*project.Project, error) {
	p, ok := m.projects[id]
	if !ok {
		return nil, project.ErrNotFound
	}
	return p, nil
}

func (m *mockStore) GetByWorkspaceAndName(_ context.Context, wid uuid.UUID, name string) (*project.Project, error) {
	for _, p := range m.projects {
		if p.WorkspaceID == wid && strings.EqualFold(p.Name, name) {
			return p, nil
		}
	}
	return nil, project.ErrNotFound
}

func (m *mockStore) Update(_ context.Context, id uuid.UUID, req project.UpdateReq) (*project.Project, error) {
	p, ok := m.projects[id]
	if !ok {
		return nil, project.ErrNotFound
	}
	if req.Name != nil {
		p.Name = *req.Name
	}
	return p, nil
}

func (m *mockStore) Delete(_ context.Context, id uuid.UUID) error {
	delete(m.projects, id)
	return nil
}

func (m *mockStore) ListByWorkspace(_ context.Context, wid uuid.UUID) ([]*project.Project, error) {
	var list []*project.Project
	for _, p := range m.projects {
		if p.WorkspaceID == wid {
			list = append(list, p)
		}
	}
	return list, nil
}

func (m *mockStore) AddMember(_ context.Context, mem *project.Member) error {
	m.members[memberKey(mem.ProjectID, mem.UserID)] = mem
	return nil
}

func (m *mockStore) GetMember(_ context.Context, pid uuid.UUID, uid string) (*project.Member, error) {
	mem, ok := m.members[memberKey(pid, uid)]
	if !ok {
		return nil, project.ErrNotFound
	}
	return mem, nil
}

func (m *mockStore) ListMembers(_ context.Context, pid uuid.UUID) ([]*project.Member, error) {
	var list []*project.Member
	for _, mem := range m.members {
		if mem.ProjectID == pid {
			list = append(list, mem)
		}
	}
	return list, nil
}

func (m *mockStore) SetMemberRole(_ context.Context, pid uuid.UUID, uid string, role project.Role) error {
	key := memberKey(pid, uid)
	mem, ok := m.members[key]
	if !ok {
		return project.ErrNotFound
	}
	mem.Role = role
	return nil
}

func (m *mockStore) RemoveMember(_ context.Context, pid uuid.UUID, uid string) error {
	delete(m.members, memberKey(pid, uid))
	return nil
}

// ── HasRole tests ─────────────────────────────────────────────────

func TestHasRole_Ordering(t *testing.T) {
	cases := []struct {
		caller   project.Role
		required project.Role
		want     bool
	}{
		{project.RoleAdmin, project.RoleAdmin, true},
		{project.RoleAdmin, project.RoleMember, true},
		{project.RoleAdmin, project.RoleViewer, true},
		{project.RoleMember, project.RoleAdmin, false},
		{project.RoleMember, project.RoleMember, true},
		{project.RoleMember, project.RoleViewer, true},
		{project.RoleViewer, project.RoleMember, false},
		{project.RoleViewer, project.RoleViewer, true},
	}
	for _, c := range cases {
		got := project.HasRole(c.caller, c.required)
		if got != c.want {
			t.Errorf("HasRole(%q, %q) = %v, want %v", c.caller, c.required, got, c.want)
		}
	}
}

// ── Service.Create tests ──────────────────────────────────────────

func TestService_Create_Success(t *testing.T) {
	store := newMockStore()
	svc := project.NewService(store, &mockWriter{})

	wid := uuid.New()
	p, err := svc.Create(context.Background(), wid, "creator-1", project.CreateReq{
		Name:        "Sprint 1",
		Description: "First sprint",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.Name != "Sprint 1" {
		t.Errorf("name = %q", p.Name)
	}
	if p.WorkspaceID != wid {
		t.Errorf("workspace_id mismatch")
	}
	if p.Status != project.StatusActive {
		t.Errorf("status = %q, want active", p.Status)
	}

	// creator should be project admin
	mem, err := store.GetMember(context.Background(), p.ID, "creator-1")
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if mem.Role != project.RoleAdmin {
		t.Errorf("creator role = %q, want admin", mem.Role)
	}
}

func TestService_Create_EmptyName(t *testing.T) {
	svc := project.NewService(newMockStore(), &mockWriter{})
	wid := uuid.New()
	_, err := svc.Create(context.Background(), wid, "creator-1", project.CreateReq{Name: ""})
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestService_Create_WithDifficulty(t *testing.T) {
	store := newMockStore()
	svc := project.NewService(store, &mockWriter{})
	wid := uuid.New()

	p, err := svc.Create(context.Background(), wid, "creator-1", project.CreateReq{
		Name:       "Legendary Dungeon",
		Difficulty: project.DifficultyLegendary,
		RewardXP:   500,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.Difficulty != project.DifficultyLegendary {
		t.Errorf("difficulty = %q, want legendary", p.Difficulty)
	}
	if p.RewardXP != 500 {
		t.Errorf("reward_xp = %d, want 500", p.RewardXP)
	}
}

func TestService_AddMember(t *testing.T) {
	store := newMockStore()
	svc := project.NewService(store, &mockWriter{})
	wid := uuid.New()

	p, _ := svc.Create(context.Background(), wid, "creator-1", project.CreateReq{Name: "proj"})

	err := svc.AddMember(context.Background(), p.ID, project.SetRoleReq{Role: project.RoleMember}, "user-2")
	if err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	mem, err := store.GetMember(context.Background(), p.ID, "user-2")
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if mem.Role != project.RoleMember {
		t.Errorf("role = %q, want member", mem.Role)
	}
	_ = time.Now() // satisfy import
}

func TestCreate_DualWritesProjectWorkspaceAndCreator(t *testing.T) {
	mw := &mockWriter{}
	svc := project.NewService(newMockStore(), mw)
	wid := uuid.New()
	p, err := svc.Create(context.Background(), wid, "creator-1", project.CreateReq{Name: "P"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(mw.pwLinks) != 1 || mw.pwLinks[0] != p.ID.String()+"|"+wid.String() {
		t.Errorf("project->workspace = %v", mw.pwLinks)
	}
	if len(mw.pjWrites) != 1 || mw.pjWrites[0] != p.ID.String()+"|creator-1|admin" {
		t.Errorf("creator member = %v", mw.pjWrites)
	}
}

func TestRemoveMember_DualDeletesProjectMember(t *testing.T) {
	mw := &mockWriter{}
	svc := project.NewService(newMockStore(), mw)
	pid := uuid.New()
	if err := svc.RemoveMember(context.Background(), pid, "u2"); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if len(mw.pjDeletes) != 1 || mw.pjDeletes[0] != pid.String()+"|u2" {
		t.Errorf("pjDeletes = %v", mw.pjDeletes)
	}
}
