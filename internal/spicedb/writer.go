package spicedb

import (
	"context"
	"fmt"

	v1 "github.com/authzed/authzed-go/proto/authzed/api/v1"
	"github.com/google/uuid"
)

// Writer writes and deletes relationships in SpiceDB.
type Writer struct {
	c *Client
}

// WriteTicket creates the ticket→project relationship and requester/assignee subjects.
func (w *Writer) WriteTicket(ctx context.Context, ticketID, projectID uuid.UUID, requesterID, assigneeID string) error {
	rels := []*v1.RelationshipUpdate{
		touch("ticket", ticketID.String(), "project", "project", projectID.String(), ""),
		touch("ticket", ticketID.String(), "requester", "user", requesterID, ""),
	}
	if assigneeID != "" && assigneeID != requesterID {
		rels = append(rels, touch("ticket", ticketID.String(), "assignee", "user", assigneeID, ""))
	}
	return w.write(ctx, rels...)
}

// WriteWorkspaceMember grants a role relation on the workspace for a user.
func (w *Writer) WriteWorkspaceMember(ctx context.Context, workspaceID uuid.UUID, userID, role string) error {
	return w.write(ctx, touch("workspace", workspaceID.String(), role, "user", userID, ""))
}

// DeleteWorkspaceMember removes all direct role relations for a user in a workspace.
func (w *Writer) DeleteWorkspaceMember(ctx context.Context, workspaceID uuid.UUID, userID string) error {
	for _, role := range []string{"owner", "admin", "member", "viewer"} {
		if err := w.delete(ctx, "workspace", workspaceID.String(), role, "user", userID, ""); err != nil {
			return err
		}
	}
	return nil
}

// WriteProjectMember grants a role relation on the project for a user.
func (w *Writer) WriteProjectMember(ctx context.Context, projectID uuid.UUID, userID, role string) error {
	return w.write(ctx, touch("project", projectID.String(), role, "user", userID, ""))
}

// WriteProjectWorkspace links a project to its parent workspace.
func (w *Writer) WriteProjectWorkspace(ctx context.Context, projectID, workspaceID uuid.UUID) error {
	return w.write(ctx, touch("project", projectID.String(), "workspace", "workspace", workspaceID.String(), ""))
}

// AddTicketViewer grants an explicit viewer on a restricted ticket.
func (w *Writer) AddTicketViewer(ctx context.Context, ticketID uuid.UUID, userID string) error {
	return w.write(ctx, touch("ticket", ticketID.String(), "restricted_viewer", "user", userID, ""))
}

func (w *Writer) write(ctx context.Context, updates ...*v1.RelationshipUpdate) error {
	_, err := w.c.inner.WriteRelationships(ctx, &v1.WriteRelationshipsRequest{Updates: updates})
	if err != nil {
		return fmt.Errorf("spicedb write: %w", err)
	}
	return nil
}

func (w *Writer) delete(ctx context.Context, resType, resID, relation, subType, subID, subRelation string) error {
	filter := &v1.RelationshipFilter{
		ResourceType:       resType,
		OptionalResourceId: resID,
		OptionalRelation:   relation,
		OptionalSubjectFilter: &v1.SubjectFilter{
			SubjectType:       subType,
			OptionalSubjectId: subID,
		},
	}
	_, err := w.c.inner.DeleteRelationships(ctx, &v1.DeleteRelationshipsRequest{RelationshipFilter: filter})
	if err != nil {
		return fmt.Errorf("spicedb delete relation: %w", err)
	}
	return nil
}

func touch(resType, resID, relation, subType, subID, subRelation string) *v1.RelationshipUpdate {
	sub := &v1.SubjectReference{Object: &v1.ObjectReference{ObjectType: subType, ObjectId: subID}}
	if subRelation != "" {
		sub.OptionalRelation = subRelation
	}
	return &v1.RelationshipUpdate{
		Operation: v1.RelationshipUpdate_OPERATION_TOUCH,
		Relationship: &v1.Relationship{
			Resource: &v1.ObjectReference{ObjectType: resType, ObjectId: resID},
			Relation: relation,
			Subject:  sub,
		},
	}
}
