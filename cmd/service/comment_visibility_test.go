package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
)

// TestCommentCanSeeInternal_ClosureLogic exercises the actual production
// function `canSeeInternal` (main.go), which is wired directly into
// handler.NewCommentHandler(commentSvc, canSeeInternal) — no duplicated
// expression here, so drift between this test and the real gate is
// impossible.
//
// goq-org-backend Task 6: external-org callers (admin or member) must be
// treated like magic-link customers — neither should see is_internal=true
// comments. Only a plain staff/OIDC request (no customer token, no org claim)
// is allowed to see them.
func TestCommentCanSeeInternal_ClosureLogic(t *testing.T) {
	tests := []struct {
		name string
		ctx  func(r *http.Request) *http.Request
		want bool
	}{
		{
			name: "no context (unauthenticated) sees internal",
			ctx:  func(r *http.Request) *http.Request { return r },
			want: true,
		},
		{
			name: "customer (magic-link) context cannot see internal",
			ctx: func(r *http.Request) *http.Request {
				ctx := auth.WithCustomerCtx(r.Context(), &auth.CustomerCtx{Email: "cust@example.com"})
				return r.WithContext(ctx)
			},
			want: false,
		},
		{
			name: "org admin context cannot see internal",
			ctx: func(r *http.Request) *http.Request {
				ctx := auth.WithOrgCtx(r.Context(), &auth.OrgCtx{ID: uuid.New(), Role: "admin"})
				return r.WithContext(ctx)
			},
			want: false,
		},
		{
			name: "org member context cannot see internal",
			ctx: func(r *http.Request) *http.Request {
				ctx := auth.WithOrgCtx(r.Context(), &auth.OrgCtx{ID: uuid.New(), Role: "member"})
				return r.WithContext(ctx)
			},
			want: false,
		},
		{
			name: "plain staff/OIDC context (no customer, no org) sees internal",
			ctx: func(r *http.Request) *http.Request {
				ctx := auth.WithRequesterID(r.Context(), "staff@corp.com")
				return r.WithContext(ctx)
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r = tt.ctx(r)
			if got := canSeeInternal(r); got != tt.want {
				t.Errorf("canSeeInternal = %v, want %v", got, tt.want)
			}
		})
	}
}
