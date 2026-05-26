package spicedb

import (
	"testing"

	"github.com/google/uuid"
)

func TestObjectTypesArePrefixed(t *testing.T) {
	if TypeWorkspace != "goquest/workspace" || TypeProject != "goquest/project" || TypeTicket != "goquest/ticket" {
		t.Fatalf("types not prefixed: %s %s %s", TypeWorkspace, TypeProject, TypeTicket)
	}
	if TypeUser != "user" {
		t.Fatalf("user type must stay unprefixed, got %s", TypeUser)
	}
}

func TestTouchUsesPrefixedTypes(t *testing.T) {
	u := touch(TypeWorkspace, uuid.New().String(), "owner", TypeUser, "u1", "")
	if got := u.Relationship.Resource.ObjectType; got != "goquest/workspace" {
		t.Errorf("resource type = %s, want goquest/workspace", got)
	}
	if got := u.Relationship.Subject.Object.ObjectType; got != "user" {
		t.Errorf("subject type = %s, want user", got)
	}
}

func TestReplaceTicketAssignee_Compiles(t *testing.T) {
	var _ RelationshipWriter = (*NoopWriter)(nil)
	var _ RelationshipWriter = (*Writer)(nil)
	u := touch(TypeTicket, "tid", "assignee", TypeUser, "lyckabc@toji.homes", "")
	if u.Relationship.Subject.Object.ObjectId == "lyckabc@toji.homes" {
		t.Error("assignee user subject must be encoded")
	}
}
