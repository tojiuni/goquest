package spicedb

import (
	"context"
	"errors"
	"fmt"
	"io"

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

// Check returns true if userID holds permission on objType:objID. It is the generic
// single-resource primitive; the user subject is encoded via subjectID() by check().
func (ch *Checker) Check(ctx context.Context, objType, objID, permission, userID string) (bool, error) {
	return ch.check(ctx, objType, objID, permission, TypeUser, userID)
}

// VisibleResourceIDs streams LookupResources and collects the ids on which userID
// holds permission. A live Checker is always restricted (unrestricted=false).
func (ch *Checker) VisibleResourceIDs(ctx context.Context, objType, permission, userID string) ([]string, bool, error) {
	stream, err := ch.c.inner.LookupResources(ctx, &v1.LookupResourcesRequest{
		ResourceObjectType: objType,
		Permission:         permission,
		Subject: &v1.SubjectReference{
			Object: &v1.ObjectReference{ObjectType: TypeUser, ObjectId: subjectID(userID)},
		},
	})
	if err != nil {
		return nil, false, fmt.Errorf("spicedb lookup %s %s: %w", objType, permission, err)
	}
	var ids []string
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, false, fmt.Errorf("spicedb lookup recv %s %s: %w", objType, permission, err)
		}
		ids = append(ids, resp.ResourceObjectId)
	}
	return ids, false, nil
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
