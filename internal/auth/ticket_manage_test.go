package auth_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/spicedb"
	"github.com/tojiuni/goquest/internal/ticket"
)

// manageChecker overrides CanManageTicket; embeds *NoopChecker for the rest
// (nil pointer is safe — Noop methods never dereference the receiver).
type manageChecker struct {
	*spicedb.NoopChecker
	allow bool
	err   error
}

func (m manageChecker) CanManageTicket(_ context.Context, _ uuid.UUID, _ string) (bool, error) {
	return m.allow, m.err
}

func TestRequireTicketManage_OIDC(t *testing.T) {
	id := uuid.New()
	pid := uuid.New()
	tk := &ticket.Ticket{ID: id, ProjectID: &pid, Visibility: ticket.VisibilityProject}

	mw := auth.RequireTicketManage(stubGetter{t: tk}, manageChecker{allow: true})
	if rec := serve(t, mw, reqWithUser(id, "manager")); rec.Code != http.StatusOK {
		t.Errorf("manager: want 200, got %d", rec.Code)
	}
	mw = auth.RequireTicketManage(stubGetter{t: tk}, manageChecker{allow: false})
	if rec := serve(t, mw, reqWithUser(id, "viewer")); rec.Code != http.StatusForbidden {
		t.Errorf("view-only user: want 403, got %d", rec.Code)
	}
	mw = auth.RequireTicketManage(stubGetter{err: ticket.ErrNotFound}, manageChecker{allow: true})
	if rec := serve(t, mw, reqWithUser(id, "manager")); rec.Code != http.StatusNotFound {
		t.Errorf("not found: want 404, got %d", rec.Code)
	}
	mw = auth.RequireTicketManage(stubGetter{t: tk}, manageChecker{err: context.DeadlineExceeded})
	if rec := serve(t, mw, reqWithUser(id, "manager")); rec.Code != http.StatusInternalServerError {
		t.Errorf("checker err: want 500 (fail-closed), got %d", rec.Code)
	}
}

// Shadow mode wires a NoopChecker (grants), so manage stays permissive until
// SPICEDB_ENFORCE swaps in the real checker.
func TestRequireTicketManage_Shadow(t *testing.T) {
	id := uuid.New()
	pid := uuid.New()
	tk := &ticket.Ticket{ID: id, ProjectID: &pid, Visibility: ticket.VisibilityProject}
	mw := auth.RequireTicketManage(stubGetter{t: tk}, &spicedb.NoopChecker{})
	if rec := serve(t, mw, reqWithUser(id, "anyone")); rec.Code != http.StatusOK {
		t.Errorf("shadow: want 200, got %d", rec.Code)
	}
}

func TestRequireTicketManage_LegacyNoProject(t *testing.T) {
	id := uuid.New()
	tk := &ticket.Ticket{ID: id, ProjectID: nil, Visibility: ticket.VisibilityProject}
	mw := auth.RequireTicketManage(stubGetter{t: tk}, manageChecker{allow: false})
	if rec := serve(t, mw, reqWithUser(id, "anyone")); rec.Code != http.StatusOK {
		t.Errorf("legacy: want 200, got %d", rec.Code)
	}
}
