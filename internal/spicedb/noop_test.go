package spicedb_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/spicedb"
)

func TestNoopChecker_AlwaysPermits(t *testing.T) {
	ch := &spicedb.NoopChecker{}
	ctx := context.Background()
	id := uuid.New()

	ok, err := ch.CanViewTicket(ctx, id, "user1", spicedb.VisibilityProject)
	if err != nil || !ok {
		t.Errorf("CanViewTicket: want (true, nil), got (%v, %v)", ok, err)
	}

	ok, err = ch.CanViewTicket(ctx, id, "user1", spicedb.VisibilityRestricted)
	if err != nil || !ok {
		t.Errorf("CanViewTicket restricted: want (true, nil), got (%v, %v)", ok, err)
	}

	ok, err = ch.CanManageTicket(ctx, id, "user1")
	if err != nil || !ok {
		t.Errorf("CanManageTicket: want (true, nil), got (%v, %v)", ok, err)
	}

	ok, err = ch.CanViewProject(ctx, id, "user1")
	if err != nil || !ok {
		t.Errorf("CanViewProject: want (true, nil), got (%v, %v)", ok, err)
	}
}

func TestNoopWriter_NoError(t *testing.T) {
	w := &spicedb.NoopWriter{}
	ctx := context.Background()
	id1, id2 := uuid.New(), uuid.New()

	if err := w.WriteTicket(ctx, id1, id2, "u1", "u2"); err != nil {
		t.Errorf("WriteTicket: %v", err)
	}
	if err := w.WriteWorkspaceMember(ctx, id1, "u1", "admin"); err != nil {
		t.Errorf("WriteWorkspaceMember: %v", err)
	}
	if err := w.DeleteWorkspaceMember(ctx, id1, "u1"); err != nil {
		t.Errorf("DeleteWorkspaceMember: %v", err)
	}
	if err := w.WriteProjectMember(ctx, id1, "u1", "member"); err != nil {
		t.Errorf("WriteProjectMember: %v", err)
	}
	if err := w.WriteProjectWorkspace(ctx, id1, id2); err != nil {
		t.Errorf("WriteProjectWorkspace: %v", err)
	}
	if err := w.AddTicketViewer(ctx, id1, "u1"); err != nil {
		t.Errorf("AddTicketViewer: %v", err)
	}
}

func TestInterfaceSatisfaction(t *testing.T) {
	var _ spicedb.PermissionChecker = &spicedb.NoopChecker{}
	var _ spicedb.RelationshipWriter = &spicedb.NoopWriter{}
}

func TestVisibilityModeConstants(t *testing.T) {
	if spicedb.VisibilityProject != "project" {
		t.Error("VisibilityProject should be 'project'")
	}
	if spicedb.VisibilityRestricted != "restricted" {
		t.Error("VisibilityRestricted should be 'restricted'")
	}
}
