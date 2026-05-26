package project

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/spicedb"
)

type Service struct {
	store  Store
	writer spicedb.RelationshipWriter
}

func NewService(store Store, writer spicedb.RelationshipWriter) *Service {
	return &Service{store: store, writer: writer}
}

func (svc *Service) Create(ctx context.Context, workspaceID uuid.UUID, creatorID string, req CreateReq) (*Project, error) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, errors.New("project name is required")
	}

	now := time.Now().UTC()
	p := &Project{
		ID:          uuid.New(),
		WorkspaceID: workspaceID,
		Name:        req.Name,
		Description: req.Description,
		Status:      StatusActive,
		Difficulty:  req.Difficulty,
		RewardXP:    req.RewardXP,
		CreatedBy:   creatorID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := svc.store.Create(ctx, p); err != nil {
		return nil, err
	}

	// creator becomes project admin automatically
	if err := svc.store.AddMember(ctx, &Member{
		ProjectID:  p.ID,
		UserID:     creatorID,
		Role:       RoleAdmin,
		AssignedAt: now,
	}); err != nil {
		return nil, err
	}

	if err := svc.writer.WriteProjectWorkspace(ctx, p.ID, workspaceID); err != nil {
		log.Printf("spicedb dual-write project->workspace %s: %v (continuing)", p.ID, err)
	}
	if err := svc.writer.WriteProjectMember(ctx, p.ID, creatorID, string(RoleAdmin)); err != nil {
		log.Printf("spicedb dual-write project admin %s: %v (continuing)", p.ID, err)
	}

	return p, nil
}

func (svc *Service) Get(ctx context.Context, id uuid.UUID) (*Project, error) {
	return svc.store.Get(ctx, id)
}

func (svc *Service) Update(ctx context.Context, id uuid.UUID, req UpdateReq) (*Project, error) {
	return svc.store.Update(ctx, id, req)
}

func (svc *Service) Delete(ctx context.Context, id uuid.UUID) error {
	return svc.store.Delete(ctx, id)
}

func (svc *Service) ListByWorkspace(ctx context.Context, workspaceID uuid.UUID) ([]*Project, error) {
	return svc.store.ListByWorkspace(ctx, workspaceID)
}

func (svc *Service) GetMember(ctx context.Context, projectID uuid.UUID, userID string) (*Member, error) {
	return svc.store.GetMember(ctx, projectID, userID)
}

func (svc *Service) ListMembers(ctx context.Context, projectID uuid.UUID) ([]*Member, error) {
	return svc.store.ListMembers(ctx, projectID)
}

func (svc *Service) AddMember(ctx context.Context, projectID uuid.UUID, req SetRoleReq, targetUserID string) error {
	if err := svc.store.AddMember(ctx, &Member{
		ProjectID:  projectID,
		UserID:     targetUserID,
		Role:       req.Role,
		AssignedAt: time.Now().UTC(),
	}); err != nil {
		return err
	}
	if err := svc.writer.WriteProjectMember(ctx, projectID, targetUserID, string(req.Role)); err != nil {
		log.Printf("spicedb dual-write project member %s/%s: %v (continuing)", projectID, targetUserID, err)
	}
	return nil
}

func (svc *Service) SetMemberRole(ctx context.Context, projectID uuid.UUID, targetUserID string, req SetRoleReq) error {
	if err := svc.store.SetMemberRole(ctx, projectID, targetUserID, req.Role); err != nil {
		return err
	}
	if err := svc.writer.DeleteProjectMember(ctx, projectID, targetUserID); err != nil {
		log.Printf("spicedb dual-delete prior project role %s/%s: %v (continuing)", projectID, targetUserID, err)
	}
	if err := svc.writer.WriteProjectMember(ctx, projectID, targetUserID, string(req.Role)); err != nil {
		log.Printf("spicedb dual-write project role %s/%s: %v (continuing)", projectID, targetUserID, err)
	}
	return nil
}

func (svc *Service) RemoveMember(ctx context.Context, projectID uuid.UUID, targetUserID string) error {
	if err := svc.store.RemoveMember(ctx, projectID, targetUserID); err != nil {
		return err
	}
	if err := svc.writer.DeleteProjectMember(ctx, projectID, targetUserID); err != nil {
		log.Printf("spicedb dual-delete project member %s/%s: %v (continuing)", projectID, targetUserID, err)
	}
	return nil
}

// HasRole checks if callerRole satisfies the required minimum role.
func HasRole(callerRole, required Role) bool {
	order := map[Role]int{
		RoleViewer: 0,
		RoleMember: 1,
		RoleAdmin:  2,
	}
	return order[callerRole] >= order[required]
}
