package workspace_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/workspace"
)

// ── mock store ────────────────────────────────────────────────────

type mockStore struct {
	workspaces  map[uuid.UUID]*workspace.Workspace
	members     map[string]*workspace.Member // key: wid+":"+uid
	invitations map[string]*workspace.Invitation // key: token
	createErr   error
	listForUser []*workspace.Workspace
}

func newMockStore() *mockStore {
	return &mockStore{
		workspaces:  make(map[uuid.UUID]*workspace.Workspace),
		members:     make(map[string]*workspace.Member),
		invitations: make(map[string]*workspace.Invitation),
	}
}

func memberKey(wid uuid.UUID, uid string) string {
	return wid.String() + ":" + uid
}

func (m *mockStore) Create(_ context.Context, ws *workspace.Workspace) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.workspaces[ws.ID] = ws
	return nil
}

func (m *mockStore) Get(_ context.Context, id uuid.UUID) (*workspace.Workspace, error) {
	ws, ok := m.workspaces[id]
	if !ok {
		return nil, workspace.ErrNotFound
	}
	return ws, nil
}

func (m *mockStore) GetBySlug(_ context.Context, slug string) (*workspace.Workspace, error) {
	for _, ws := range m.workspaces {
		if ws.Slug == slug {
			return ws, nil
		}
	}
	return nil, workspace.ErrNotFound
}

func (m *mockStore) GetByName(_ context.Context, name string) (*workspace.Workspace, error) {
	for _, ws := range m.workspaces {
		if strings.EqualFold(ws.Name, name) {
			return ws, nil
		}
	}
	return nil, workspace.ErrNotFound
}

func (m *mockStore) Update(_ context.Context, id uuid.UUID, req workspace.UpdateReq) (*workspace.Workspace, error) {
	ws, ok := m.workspaces[id]
	if !ok {
		return nil, workspace.ErrNotFound
	}
	if req.Name != nil {
		ws.Name = *req.Name
	}
	return ws, nil
}

func (m *mockStore) Delete(_ context.Context, id uuid.UUID) error {
	delete(m.workspaces, id)
	return nil
}

func (m *mockStore) ListByUser(_ context.Context, _ string) ([]*workspace.Workspace, error) {
	return m.listForUser, nil
}

func (m *mockStore) AddMember(_ context.Context, mem *workspace.Member) error {
	m.members[memberKey(mem.WorkspaceID, mem.UserID)] = mem
	return nil
}

func (m *mockStore) GetMember(_ context.Context, wid uuid.UUID, uid string) (*workspace.Member, error) {
	mem, ok := m.members[memberKey(wid, uid)]
	if !ok {
		return nil, workspace.ErrNotFound
	}
	return mem, nil
}

func (m *mockStore) ListMembers(_ context.Context, wid uuid.UUID) ([]*workspace.Member, error) {
	var list []*workspace.Member
	for _, mem := range m.members {
		if mem.WorkspaceID == wid {
			list = append(list, mem)
		}
	}
	return list, nil
}

func (m *mockStore) SetMemberRole(_ context.Context, wid uuid.UUID, uid string, role workspace.Role) error {
	key := memberKey(wid, uid)
	mem, ok := m.members[key]
	if !ok {
		return workspace.ErrNotFound
	}
	mem.Role = role
	return nil
}

func (m *mockStore) RemoveMember(_ context.Context, wid uuid.UUID, uid string) error {
	delete(m.members, memberKey(wid, uid))
	return nil
}

func (m *mockStore) UpdateMemberXP(_ context.Context, wid uuid.UUID, uid string, xp, level int, title string) error {
	key := memberKey(wid, uid)
	if mem, ok := m.members[key]; ok {
		mem.XP = xp
		mem.Level = level
		mem.Title = title
	}
	return nil
}

func (m *mockStore) CreateInvitation(_ context.Context, inv *workspace.Invitation) error {
	m.invitations[inv.Token] = inv
	return nil
}

func (m *mockStore) GetInvitationByToken(_ context.Context, token string) (*workspace.Invitation, error) {
	inv, ok := m.invitations[token]
	if !ok {
		return nil, workspace.ErrNotFound
	}
	return inv, nil
}

func (m *mockStore) ListInvitations(_ context.Context, _ uuid.UUID) ([]*workspace.Invitation, error) {
	var list []*workspace.Invitation
	for _, inv := range m.invitations {
		list = append(list, inv)
	}
	return list, nil
}

func (m *mockStore) MarkInvitationUsed(_ context.Context, id uuid.UUID) error {
	for _, inv := range m.invitations {
		if inv.ID == id {
			now := time.Now().UTC()
			inv.UsedAt = &now
			return nil
		}
	}
	return nil
}

func (m *mockStore) DeleteInvitation(_ context.Context, id uuid.UUID) error {
	for token, inv := range m.invitations {
		if inv.ID == id {
			delete(m.invitations, token)
			return nil
		}
	}
	return nil
}

// ── HasRole tests ─────────────────────────────────────────────────

func TestHasRole_Ordering(t *testing.T) {
	cases := []struct {
		memberRole workspace.Role
		required   workspace.Role
		want       bool
	}{
		{workspace.RoleOwner, workspace.RoleOwner, true},
		{workspace.RoleOwner, workspace.RoleAdmin, true},
		{workspace.RoleOwner, workspace.RoleMember, true},
		{workspace.RoleOwner, workspace.RoleViewer, true},
		{workspace.RoleAdmin, workspace.RoleOwner, false},
		{workspace.RoleAdmin, workspace.RoleAdmin, true},
		{workspace.RoleAdmin, workspace.RoleMember, true},
		{workspace.RoleMember, workspace.RoleAdmin, false},
		{workspace.RoleMember, workspace.RoleMember, true},
		{workspace.RoleMember, workspace.RoleViewer, true},
		{workspace.RoleViewer, workspace.RoleMember, false},
		{workspace.RoleViewer, workspace.RoleViewer, true},
	}
	for _, c := range cases {
		got := workspace.HasRole(c.memberRole, c.required)
		if got != c.want {
			t.Errorf("HasRole(%q, %q) = %v, want %v", c.memberRole, c.required, got, c.want)
		}
	}
}

// ── Service.Create tests ──────────────────────────────────────────

func TestService_Create_Success(t *testing.T) {
	store := newMockStore()
	svc := workspace.NewService(store)

	ws, err := svc.Create(context.Background(), "user-1", workspace.CreateReq{
		Name: "My Team",
		Type: workspace.TypeShared,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ws.Name != "My Team" {
		t.Errorf("name = %q, want %q", ws.Name, "My Team")
	}
	if ws.OwnerID != "user-1" {
		t.Errorf("owner = %q, want %q", ws.OwnerID, "user-1")
	}
	// owner should be auto-added as member
	mem, err := store.GetMember(context.Background(), ws.ID, "user-1")
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if mem.Role != workspace.RoleOwner {
		t.Errorf("member role = %q, want owner", mem.Role)
	}
}

func TestService_Create_EmptyName(t *testing.T) {
	svc := workspace.NewService(newMockStore())
	_, err := svc.Create(context.Background(), "user-1", workspace.CreateReq{Name: "  "})
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestService_Create_PersonalType(t *testing.T) {
	store := newMockStore()
	svc := workspace.NewService(store)

	ws, err := svc.Create(context.Background(), "user-1", workspace.CreateReq{
		Name: "Personal",
		Type: workspace.TypePersonal,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ws.Type != workspace.TypePersonal {
		t.Errorf("type = %q, want personal", ws.Type)
	}
}

// ── Service.EnsurePersonal tests ──────────────────────────────────

func TestService_EnsurePersonal_CreatesNew(t *testing.T) {
	store := newMockStore()
	svc := workspace.NewService(store)

	ws, err := svc.EnsurePersonalWorkspace(context.Background(), "user-1", "Alice")
	if err != nil {
		t.Fatalf("EnsurePersonal: %v", err)
	}
	if ws.Type != workspace.TypePersonal {
		t.Errorf("type = %q, want personal", ws.Type)
	}
	if len(store.workspaces) != 1 {
		t.Errorf("expected 1 workspace, got %d", len(store.workspaces))
	}
}

func TestService_EnsurePersonal_ReturnsExisting(t *testing.T) {
	store := newMockStore()
	svc := workspace.NewService(store)

	// pre-seed a personal workspace in listForUser
	existing := &workspace.Workspace{
		ID:    uuid.New(),
		Name:  "Alice's Workspace",
		Type:  workspace.TypePersonal,
		OwnerID: "user-1",
	}
	store.listForUser = []*workspace.Workspace{existing}

	ws, err := svc.EnsurePersonalWorkspace(context.Background(), "user-1", "Alice")
	if err != nil {
		t.Fatalf("EnsurePersonal: %v", err)
	}
	if ws.ID != existing.ID {
		t.Errorf("expected existing workspace ID %s, got %s", existing.ID, ws.ID)
	}
	// should NOT create a new one
	if len(store.workspaces) != 0 {
		t.Errorf("expected no new workspace created, got %d", len(store.workspaces))
	}
}

// ── Service.AcceptInvitation tests ───────────────────────────────

func newInvitation(wid uuid.UUID, role workspace.Role, expired, used bool) *workspace.Invitation {
	exp := time.Now().UTC().Add(7 * 24 * time.Hour)
	if expired {
		exp = time.Now().UTC().Add(-1 * time.Hour)
	}
	inv := &workspace.Invitation{
		ID:          uuid.New(),
		WorkspaceID: wid,
		Token:       "test-token",
		Role:        role,
		InvitedBy:   "admin-user",
		ExpiresAt:   exp,
		CreatedAt:   time.Now().UTC(),
	}
	if used {
		now := time.Now().UTC()
		inv.UsedAt = &now
	}
	return inv
}

func TestService_AcceptInvitation_Success(t *testing.T) {
	store := newMockStore()
	svc := workspace.NewService(store)

	wsID := uuid.New()
	ws := &workspace.Workspace{ID: wsID, Name: "Team", Type: workspace.TypeShared}
	store.workspaces[wsID] = ws
	store.invitations["test-token"] = newInvitation(wsID, workspace.RoleMember, false, false)

	result, err := svc.AcceptInvitation(context.Background(), "test-token", "new-user")
	if err != nil {
		t.Fatalf("AcceptInvitation: %v", err)
	}
	if result.ID != wsID {
		t.Errorf("returned wrong workspace")
	}
	// new-user should be a member
	mem, err := store.GetMember(context.Background(), wsID, "new-user")
	if err != nil {
		t.Fatalf("GetMember: %v", err)
	}
	if mem.Role != workspace.RoleMember {
		t.Errorf("member role = %q, want member", mem.Role)
	}
	// invitation should be marked used
	inv := store.invitations["test-token"]
	if inv.UsedAt == nil {
		t.Error("invitation.UsedAt should be set")
	}
}

func TestService_AcceptInvitation_Expired(t *testing.T) {
	store := newMockStore()
	svc := workspace.NewService(store)

	wsID := uuid.New()
	store.workspaces[wsID] = &workspace.Workspace{ID: wsID}
	store.invitations["test-token"] = newInvitation(wsID, workspace.RoleMember, true, false)

	_, err := svc.AcceptInvitation(context.Background(), "test-token", "new-user")
	if err == nil {
		t.Fatal("expected error for expired invitation")
	}
}

func TestService_AcceptInvitation_AlreadyUsed(t *testing.T) {
	store := newMockStore()
	svc := workspace.NewService(store)

	wsID := uuid.New()
	store.workspaces[wsID] = &workspace.Workspace{ID: wsID}
	store.invitations["test-token"] = newInvitation(wsID, workspace.RoleMember, false, true)

	_, err := svc.AcceptInvitation(context.Background(), "test-token", "new-user")
	if err == nil {
		t.Fatal("expected error for already-used invitation")
	}
}

// ── Service.SetMemberRole tests ───────────────────────────────────

func TestService_SetMemberRole_OnlyOwner(t *testing.T) {
	svc := workspace.NewService(newMockStore())
	wid := uuid.New()

	err := svc.SetMemberRole(context.Background(), wid, "target", workspace.SetRoleReq{Role: workspace.RoleMember}, workspace.RoleAdmin)
	if err == nil {
		t.Fatal("expected error: only owner can change roles")
	}
}

func TestService_SetMemberRole_OwnerCanChange(t *testing.T) {
	store := newMockStore()
	svc := workspace.NewService(store)
	wid := uuid.New()

	store.members[memberKey(wid, "target")] = &workspace.Member{
		WorkspaceID: wid, UserID: "target", Role: workspace.RoleViewer,
	}

	err := svc.SetMemberRole(context.Background(), wid, "target", workspace.SetRoleReq{Role: workspace.RoleMember}, workspace.RoleOwner)
	if err != nil {
		t.Fatalf("SetMemberRole: %v", err)
	}
	mem := store.members[memberKey(wid, "target")]
	if mem.Role != workspace.RoleMember {
		t.Errorf("role = %q, want member", mem.Role)
	}
}

// ── Service.RemoveMember tests ────────────────────────────────────

func TestService_RemoveMember_RequiresAdmin(t *testing.T) {
	svc := workspace.NewService(newMockStore())
	wid := uuid.New()

	err := svc.RemoveMember(context.Background(), wid, "target", workspace.RoleMember)
	if err == nil {
		t.Fatal("expected error: member cannot remove others")
	}
}

func TestService_RemoveMember_AdminCanRemove(t *testing.T) {
	store := newMockStore()
	svc := workspace.NewService(store)
	wid := uuid.New()

	store.members[memberKey(wid, "target")] = &workspace.Member{
		WorkspaceID: wid, UserID: "target", Role: workspace.RoleMember,
	}

	err := svc.RemoveMember(context.Background(), wid, "target", workspace.RoleAdmin)
	if err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if _, ok := store.members[memberKey(wid, "target")]; ok {
		t.Error("member should have been removed")
	}
}

// ensure errors sentinel is exported
var _ = errors.Is(workspace.ErrNotFound, workspace.ErrNotFound)

// ── Slug generation tests ─────────────────────────────────────────

func TestCreate_SlugCleanNoRandomSuffix(t *testing.T) {
	svc := workspace.NewService(newMockStore())
	ws, err := svc.Create(context.Background(), "owner-1", workspace.CreateReq{Name: "My Workspace"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ws.Slug != "my-workspace" {
		t.Errorf("slug = %q, want %q (no random suffix)", ws.Slug, "my-workspace")
	}
}

func TestCreate_SlugCollisionDeterministicSuffix(t *testing.T) {
	store := newMockStore()
	svc := workspace.NewService(store)
	ctx := context.Background()
	a, _ := svc.Create(ctx, "o1", workspace.CreateReq{Name: "Dup"})
	b, _ := svc.Create(ctx, "o2", workspace.CreateReq{Name: "Dup"})
	if a.Slug != "dup" {
		t.Errorf("first slug = %q, want dup", a.Slug)
	}
	if b.Slug != "dup-2" {
		t.Errorf("second slug = %q, want dup-2", b.Slug)
	}
}

func TestCreate_SlugDegenerateFallbackToID(t *testing.T) {
	svc := workspace.NewService(newMockStore())
	ws, err := svc.Create(context.Background(), "o1", workspace.CreateReq{Name: "내 워크스페이스"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := "ws-" + ws.ID.String()[:8]
	if ws.Slug != want {
		t.Errorf("slug = %q, want %q (degenerate name → id fallback)", ws.Slug, want)
	}
}
