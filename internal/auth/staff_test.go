package auth_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
	wskg "github.com/tojiuni/goquest/internal/workspace"
)

type stubStaffChecker struct {
	workspaces []*wskg.Workspace
	err        error
}

func (s stubStaffChecker) ListByUser(_ context.Context, _ string) ([]*wskg.Workspace, error) {
	return s.workspaces, s.err
}

func TestIsExternalOnly_CustomerToken(t *testing.T) {
	r := httptest.NewRequest("GET", "/x", nil)
	r = r.WithContext(auth.WithCustomerCtx(r.Context(), &auth.CustomerCtx{Email: "c@x.com"}))
	if !auth.IsExternalOnly(r.Context(), r, stubStaffChecker{}) {
		t.Error("customer token: want external, got not-external")
	}
}

func TestIsExternalOnly_NoOrgClaim_NoCustomerToken(t *testing.T) {
	r := httptest.NewRequest("GET", "/x", nil)
	r = r.WithContext(auth.WithRequesterID(r.Context(), "staff@corp.com"))
	if auth.IsExternalOnly(r.Context(), r, stubStaffChecker{}) {
		t.Error("plain staff caller, no org claim: want not-external, got external")
	}
}

func TestIsExternalOnly_OrgClaim_StaffMember(t *testing.T) {
	r := httptest.NewRequest("GET", "/x", nil)
	r = r.WithContext(auth.WithRequesterID(r.Context(), "lyckabc@gmail.com"))
	r = r.WithContext(auth.WithOrgCtx(r.Context(), &auth.OrgCtx{ID: uuid.New(), Role: "admin"}))
	checker := stubStaffChecker{workspaces: []*wskg.Workspace{{ID: uuid.New(), Name: "personal"}}}
	if auth.IsExternalOnly(r.Context(), r, checker) {
		t.Error("staff caller who also holds an org claim: want not-external, got external")
	}
}

func TestIsExternalOnly_OrgClaim_NoWorkspaceMembership(t *testing.T) {
	r := httptest.NewRequest("GET", "/x", nil)
	r = r.WithContext(auth.WithRequesterID(r.Context(), "member@rival.com"))
	r = r.WithContext(auth.WithOrgCtx(r.Context(), &auth.OrgCtx{ID: uuid.New(), Role: "member"}))
	checker := stubStaffChecker{workspaces: nil}
	if !auth.IsExternalOnly(r.Context(), r, checker) {
		t.Error("org member with no workspace membership: want external, got not-external")
	}
}

func TestIsExternalOnly_OrgClaim_StaffCheckerErrors_FailsClosed(t *testing.T) {
	r := httptest.NewRequest("GET", "/x", nil)
	r = r.WithContext(auth.WithRequesterID(r.Context(), "someone@corp.com"))
	r = r.WithContext(auth.WithOrgCtx(r.Context(), &auth.OrgCtx{ID: uuid.New(), Role: "admin"}))
	checker := stubStaffChecker{err: errors.New("db down")}
	if !auth.IsExternalOnly(r.Context(), r, checker) {
		t.Error("staff checker error: want fail-closed (external), got not-external")
	}
}

func TestIsExternalOnly_OrgClaim_NoRequesterID(t *testing.T) {
	r := httptest.NewRequest("GET", "/x", nil)
	r = r.WithContext(auth.WithOrgCtx(r.Context(), &auth.OrgCtx{ID: uuid.New(), Role: "admin"}))
	if !auth.IsExternalOnly(r.Context(), r, stubStaffChecker{}) {
		t.Error("org claim with no requester id: want external, got not-external")
	}
}
