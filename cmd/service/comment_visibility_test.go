package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
	wskg "github.com/tojiuni/goquest/internal/workspace"
)

type fakeStaffChecker struct {
	workspaces []*wskg.Workspace
	err        error
}

func (f *fakeStaffChecker) ListByUser(_ context.Context, _ string) ([]*wskg.Workspace, error) {
	return f.workspaces, f.err
}

// TestCommentCanSeeInternal_ClosureLogic exercises the actual production
// function `canSeeInternal` (main.go), which is wired directly into
// handler.NewCommentHandler(commentSvc, func(r) bool { return
// canSeeInternal(r, wsSvc) }) — no duplicated expression here, so drift
// between this test and the real gate is impossible.
//
// goq-org-backend Task 6: external-org callers (admin or member) must be
// treated like magic-link customers — neither should see is_internal=true
// comments, UNLESS they are also staff (goq-org-claim-staff-content-comment-bypass:
// an org claim only adds access, it never subtracts the access a caller
// already has as staff).
func TestCommentCanSeeInternal_ClosureLogic(t *testing.T) {
	notStaff := &fakeStaffChecker{}
	isStaff := &fakeStaffChecker{workspaces: []*wskg.Workspace{{ID: uuid.New(), Name: "personal"}}}

	tests := []struct {
		name  string
		ctx   func(r *http.Request) *http.Request
		staff auth.StaffChecker
		want  bool
	}{
		{
			name:  "no context (unauthenticated) sees internal",
			ctx:   func(r *http.Request) *http.Request { return r },
			staff: notStaff,
			want:  true,
		},
		{
			name: "customer (magic-link) context cannot see internal",
			ctx: func(r *http.Request) *http.Request {
				ctx := auth.WithCustomerCtx(r.Context(), &auth.CustomerCtx{Email: "cust@example.com"})
				return r.WithContext(ctx)
			},
			staff: isStaff,
			want:  false,
		},
		{
			name: "org admin context, not a workspace member, cannot see internal",
			ctx: func(r *http.Request) *http.Request {
				ctx := auth.WithRequesterID(r.Context(), "outsider@rival.com")
				ctx = auth.WithOrgCtx(ctx, &auth.OrgCtx{ID: uuid.New(), Role: "admin"})
				return r.WithContext(ctx)
			},
			staff: notStaff,
			want:  false,
		},
		{
			name: "org member context, not a workspace member, cannot see internal",
			ctx: func(r *http.Request) *http.Request {
				ctx := auth.WithRequesterID(r.Context(), "outsider@rival.com")
				ctx = auth.WithOrgCtx(ctx, &auth.OrgCtx{ID: uuid.New(), Role: "member"})
				return r.WithContext(ctx)
			},
			staff: notStaff,
			want:  false,
		},
		{
			name: "plain staff/OIDC context (no customer, no org) sees internal",
			ctx: func(r *http.Request) *http.Request {
				ctx := auth.WithRequesterID(r.Context(), "staff@corp.com")
				return r.WithContext(ctx)
			},
			staff: notStaff,
			want:  true,
		},
		{
			name: "staff who also holds an org claim still sees internal",
			ctx: func(r *http.Request) *http.Request {
				ctx := auth.WithRequesterID(r.Context(), "lyckabc@gmail.com")
				ctx = auth.WithOrgCtx(ctx, &auth.OrgCtx{ID: uuid.New(), Role: "admin"})
				return r.WithContext(ctx)
			},
			staff: isStaff,
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r = tt.ctx(r)
			if got := canSeeInternal(r, tt.staff); got != tt.want {
				t.Errorf("canSeeInternal = %v, want %v", got, tt.want)
			}
		})
	}
}
