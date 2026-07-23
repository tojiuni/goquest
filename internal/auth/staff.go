package auth

import (
	"context"
	"net/http"

	wskg "github.com/tojiuni/goquest/internal/workspace"
)

// StaffChecker resolves the goquest workspaces a user belongs to. Satisfied by
// *workspace.Service. A non-empty result is the signal that a caller is staff:
// external org-portal members are never added as workspace members, so an
// empty result (with no error) means the caller has no internal identity of
// their own.
type StaffChecker interface {
	ListByUser(ctx context.Context, userID string) ([]*wskg.Workspace, error)
}

// IsExternalOnly reports whether the caller is a genuinely external party —
// a magic-link customer, or an org-context caller with no goquest workspace
// membership of their own.
//
// An org claim must only ever ADD access, never subtract access the caller
// would otherwise have as staff (goq-org-claim-staff-visibility-bypass,
// goq-org-ticket-view-visibility-bypass) — so a caller who holds BOTH an org
// claim and a staff identity (workspace membership) is not external. Staff
// membership is checked via StaffChecker rather than assumed absent just
// because an org claim is present.
func IsExternalOnly(ctx context.Context, r *http.Request, staff StaffChecker) bool {
	if IsCustomerRequest(r) {
		return true
	}
	if OrgFrom(r) == nil {
		return false
	}
	userID := RequesterID(r)
	if userID == "" {
		return true
	}
	workspaces, err := staff.ListByUser(ctx, userID)
	if err != nil {
		// Fail closed: if staff membership can't be confirmed, don't grant
		// internal access on the strength of an unverified claim.
		return true
	}
	return len(workspaces) == 0
}
