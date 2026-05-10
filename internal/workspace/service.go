package workspace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

var slugRe = regexp.MustCompile(`[^a-z0-9-]+`)

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

// EnsurePersonalWorkspace creates a personal workspace for userID if none exists yet.
// Called on first login.
func (svc *Service) EnsurePersonalWorkspace(ctx context.Context, userID, displayName string) (*Workspace, error) {
	// Check if user already has a personal workspace.
	list, err := svc.store.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, ws := range list {
		if ws.Type == TypePersonal {
			return ws, nil
		}
	}
	return svc.create(ctx, userID, CreateReq{
		Name: displayName + "'s Workspace",
		Type: TypePersonal,
	})
}

func (svc *Service) Create(ctx context.Context, ownerID string, req CreateReq) (*Workspace, error) {
	return svc.create(ctx, ownerID, req)
}

func (svc *Service) create(ctx context.Context, ownerID string, req CreateReq) (*Workspace, error) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, errors.New("workspace name is required")
	}
	slug := toSlug(req.Name)
	wsType := req.Type
	if wsType == "" {
		wsType = TypeShared
	}

	now := time.Now().UTC()
	ws := &Workspace{
		ID:          uuid.New(),
		Name:        req.Name,
		Slug:        slug,
		Description: req.Description,
		Type:        wsType,
		OwnerID:     ownerID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := svc.store.Create(ctx, ws); err != nil {
		return nil, err
	}

	// Register the creator as owner.
	if err := svc.store.AddMember(ctx, &Member{
		WorkspaceID: ws.ID,
		UserID:      ownerID,
		Role:        RoleOwner,
		Level:       1,
		Title:       "Novice",
		JoinedAt:    now,
	}); err != nil {
		return nil, fmt.Errorf("add owner as member: %w", err)
	}

	return ws, nil
}

func (svc *Service) Get(ctx context.Context, id uuid.UUID) (*Workspace, error) {
	return svc.store.Get(ctx, id)
}

func (svc *Service) Update(ctx context.Context, id uuid.UUID, req UpdateReq) (*Workspace, error) {
	return svc.store.Update(ctx, id, req)
}

func (svc *Service) Delete(ctx context.Context, id uuid.UUID) error {
	return svc.store.Delete(ctx, id)
}

func (svc *Service) ListByUser(ctx context.Context, userID string) ([]*Workspace, error) {
	return svc.store.ListByUser(ctx, userID)
}

// ── Member management ──────────────────────────────────────────────

func (svc *Service) GetMember(ctx context.Context, workspaceID uuid.UUID, userID string) (*Member, error) {
	return svc.store.GetMember(ctx, workspaceID, userID)
}

func (svc *Service) ListMembers(ctx context.Context, workspaceID uuid.UUID) ([]*Member, error) {
	return svc.store.ListMembers(ctx, workspaceID)
}

func (svc *Service) SetMemberRole(ctx context.Context, workspaceID uuid.UUID, targetUserID string, req SetRoleReq, callerRole Role) error {
	if callerRole != RoleOwner {
		return errors.New("only workspace owner can change roles")
	}
	return svc.store.SetMemberRole(ctx, workspaceID, targetUserID, req.Role)
}

func (svc *Service) RemoveMember(ctx context.Context, workspaceID uuid.UUID, targetUserID string, callerRole Role) error {
	if callerRole != RoleAdmin && callerRole != RoleOwner {
		return errors.New("insufficient permission to remove member")
	}
	return svc.store.RemoveMember(ctx, workspaceID, targetUserID)
}

// ── Invitation ─────────────────────────────────────────────────────

const invitationTTL = 7 * 24 * time.Hour

func (svc *Service) CreateInvitation(ctx context.Context, workspaceID uuid.UUID, inviterID string, req InviteReq) (*Invitation, error) {
	token, err := randomHex(16)
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}

	now := time.Now().UTC()
	inv := &Invitation{
		ID:          uuid.New(),
		WorkspaceID: workspaceID,
		Email:       req.Email,
		Token:       token,
		Role:        req.Role,
		InvitedBy:   inviterID,
		ExpiresAt:   now.Add(invitationTTL),
		CreatedAt:   now,
	}
	if err := svc.store.CreateInvitation(ctx, inv); err != nil {
		return nil, err
	}
	return inv, nil
}

func (svc *Service) ListInvitations(ctx context.Context, workspaceID uuid.UUID) ([]*Invitation, error) {
	return svc.store.ListInvitations(ctx, workspaceID)
}

func (svc *Service) DeleteInvitation(ctx context.Context, invID uuid.UUID) error {
	return svc.store.DeleteInvitation(ctx, invID)
}

// AcceptInvitation adds the user as a workspace member and marks the invitation used.
func (svc *Service) AcceptInvitation(ctx context.Context, token, userID string) (*Workspace, error) {
	inv, err := svc.store.GetInvitationByToken(ctx, token)
	if err != nil {
		return nil, err
	}
	if inv.UsedAt != nil {
		return nil, errors.New("invitation already used")
	}
	if time.Now().UTC().After(inv.ExpiresAt) {
		return nil, errors.New("invitation expired")
	}

	now := time.Now().UTC()
	if err := svc.store.AddMember(ctx, &Member{
		WorkspaceID: inv.WorkspaceID,
		UserID:      userID,
		Role:        inv.Role,
		Level:       1,
		Title:       "Novice",
		JoinedAt:    now,
	}); err != nil {
		return nil, err
	}
	if err := svc.store.MarkInvitationUsed(ctx, inv.ID); err != nil {
		return nil, err
	}

	return svc.store.Get(ctx, inv.WorkspaceID)
}

// ── Helpers ────────────────────────────────────────────────────────

// HasRole returns true if memberRole satisfies the required minimum.
func HasRole(memberRole, required Role) bool {
	order := map[Role]int{
		RoleViewer: 0,
		RoleMember: 1,
		RoleAdmin:  2,
		RoleOwner:  3,
	}
	return order[memberRole] >= order[required]
}

func toSlug(name string) string {
	s := strings.ToLower(name)
	s = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return '-'
	}, s)
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	// append random suffix to avoid collision
	suffix, _ := randomHex(3)
	return s + "-" + suffix
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
