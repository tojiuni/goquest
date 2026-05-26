package workspace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/spicedb"
)

var slugRe = regexp.MustCompile(`[^a-z0-9-]+`)

type Service struct {
	store  Store
	writer spicedb.RelationshipWriter
}

func NewService(store Store, writer spicedb.RelationshipWriter) *Service {
	return &Service{store: store, writer: writer}
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
	id := uuid.New()
	slug, err := svc.uniqueSlug(ctx, normalizeSlug(req.Name), id)
	if err != nil {
		return nil, err
	}
	wsType := req.Type
	if wsType == "" {
		wsType = TypeShared
	}

	now := time.Now().UTC()
	ws := &Workspace{
		ID:          id,
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

	if err := svc.writer.WriteWorkspaceMember(ctx, ws.ID, ownerID, string(RoleOwner)); err != nil {
		log.Printf("spicedb dual-write workspace owner %s: %v (continuing)", ws.ID, err)
	}

	return ws, nil
}

const maxSlugAttempts = 50

// uniqueSlug picks a free slug: base, then base-2, base-3...; if base is empty
// (degenerate name) or no free suffix found, falls back to a stable id-based handle.
func (svc *Service) uniqueSlug(ctx context.Context, base string, id uuid.UUID) (string, error) {
	if base == "" {
		return "ws-" + id.String()[:8], nil
	}
	candidate := base
	for i := 2; i <= maxSlugAttempts; i++ {
		_, err := svc.store.GetBySlug(ctx, candidate)
		if errors.Is(err, ErrNotFound) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
	// Exhausted base-2..base-N: fall back to a guaranteed-unique id-based handle.
	return "ws-" + id.String()[:8], nil
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
	if err := svc.store.SetMemberRole(ctx, workspaceID, targetUserID, req.Role); err != nil {
		return err
	}
	_ = svc.writer.DeleteWorkspaceMember(ctx, workspaceID, targetUserID)
	if err := svc.writer.WriteWorkspaceMember(ctx, workspaceID, targetUserID, string(req.Role)); err != nil {
		log.Printf("spicedb dual-write ws role %s/%s: %v (continuing)", workspaceID, targetUserID, err)
	}
	return nil
}

func (svc *Service) RemoveMember(ctx context.Context, workspaceID uuid.UUID, targetUserID string, callerRole Role) error {
	if callerRole != RoleAdmin && callerRole != RoleOwner {
		return errors.New("insufficient permission to remove member")
	}
	if err := svc.store.RemoveMember(ctx, workspaceID, targetUserID); err != nil {
		return err
	}
	if err := svc.writer.DeleteWorkspaceMember(ctx, workspaceID, targetUserID); err != nil {
		log.Printf("spicedb dual-delete ws member %s/%s: %v (continuing)", workspaceID, targetUserID, err)
	}
	return nil
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
	if err := svc.writer.WriteWorkspaceMember(ctx, inv.WorkspaceID, userID, string(inv.Role)); err != nil {
		log.Printf("spicedb dual-write ws invite-accept %s/%s: %v (continuing)", inv.WorkspaceID, userID, err)
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

// normalizeSlug returns a URL-safe base slug for name, or "" if name has no
// slug-able characters (e.g. all-Hangul). Collision/empty handling is done by create().
func normalizeSlug(name string) string {
	s := strings.ToLower(name)
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, s)
	s = slugRe.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
