package spicedb

import (
	"context"
	"fmt"

	v1 "github.com/authzed/authzed-go/proto/authzed/api/v1"
	"github.com/google/uuid"
)

// VisibilityMode corresponds to ticket.Visibility values.
type VisibilityMode string

const (
	VisibilityProject    VisibilityMode = "project"
	VisibilityRestricted VisibilityMode = "restricted"
)

// Checker runs CheckPermission calls against SpiceDB.
type Checker struct {
	c *Client
}

// CanViewTicket returns true if userID may view the ticket.
// It selects can_view_project or can_view_restricted based on visibility.
func (ch *Checker) CanViewTicket(ctx context.Context, ticketID uuid.UUID, userID string, visibility VisibilityMode) (bool, error) {
	permission := "can_view_project"
	if visibility == VisibilityRestricted {
		permission = "can_view_restricted"
	}
	return ch.check(ctx, TypeTicket, ticketID.String(), permission, TypeUser, userID)
}

// CanManageTicket returns true if userID may update/delete the ticket.
func (ch *Checker) CanManageTicket(ctx context.Context, ticketID uuid.UUID, userID string) (bool, error) {
	return ch.check(ctx, TypeTicket, ticketID.String(), "can_manage", TypeUser, userID)
}

// CanViewProject returns true if userID may view the project.
func (ch *Checker) CanViewProject(ctx context.Context, projectID uuid.UUID, userID string) (bool, error) {
	return ch.check(ctx, TypeProject, projectID.String(), "can_view", TypeUser, userID)
}

func (ch *Checker) check(ctx context.Context, resType, resID, permission, subType, subID string) (bool, error) {
	if subType == TypeUser {
		subID = subjectID(subID)
	}
	resp, err := ch.c.inner.CheckPermission(ctx, &v1.CheckPermissionRequest{
		Resource:   &v1.ObjectReference{ObjectType: resType, ObjectId: resID},
		Permission: permission,
		Subject:    &v1.SubjectReference{Object: &v1.ObjectReference{ObjectType: subType, ObjectId: subID}},
	})
	if err != nil {
		return false, fmt.Errorf("spicedb check %s/%s %s: %w", resType, resID, permission, err)
	}
	return resp.Permissionship == v1.CheckPermissionResponse_PERMISSIONSHIP_HAS_PERMISSION, nil
}
