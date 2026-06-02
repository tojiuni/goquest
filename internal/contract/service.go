package contract

import (
	"context"
	"fmt"

	"github.com/tojiuni/goquest/internal/audit"
	"github.com/tojiuni/goquest/internal/spicedb"
)

// permChecker is the minimal permission interface required by Service.
// spicedb.PermissionChecker satisfies this interface.
type permChecker interface {
	Check(ctx context.Context, objType, objID, permission, userID string) (bool, error)
	VisibleResourceIDs(ctx context.Context, objType, permission, userID string) (ids []string, unrestricted bool, err error)
}

// Service wraps the contract HTTP client with permission checks and audit logging.
type Service struct {
	client  Client
	checker permChecker
	audit   audit.Logger
}

// NewService constructs a Service.
func NewService(c Client, ch permChecker, a audit.Logger) *Service {
	return &Service{client: c, checker: ch, audit: a}
}

// Approve checks that actorID holds contract_approve on workspaceID, then
// proxies plan_approve to the upstream client and records an audit entry.
// The actorID is forwarded as the approver; no legacy decision bool is sent.
func (s *Service) Approve(ctx context.Context, actorID, workspaceID, wfID string) (map[string]any, error) {
	if err := s.ensurePermitted(ctx, actorID, workspaceID); err != nil {
		return nil, err
	}
	out, err := s.client.Approve(ctx, wfID, actorID)
	if err != nil {
		return nil, err
	}
	s.audit.Record(ctx, actorID, "contract.approve", "contract", wfID, map[string]any{
		"workspace": workspaceID,
		"approver":  actorID,
	})
	return out, nil
}

// Abort checks permission then proxies to the upstream client and records an audit entry.
func (s *Service) Abort(ctx context.Context, actorID, workspaceID, wfID string) (map[string]any, error) {
	if err := s.ensurePermitted(ctx, actorID, workspaceID); err != nil {
		return nil, err
	}
	out, err := s.client.Abort(ctx, wfID)
	if err != nil {
		return nil, err
	}
	s.audit.Record(ctx, actorID, "contract.abort", "contract", wfID, map[string]any{
		"workspace": workspaceID,
	})
	return out, nil
}

// SubmissionApprove approves the submission gate for a goal workflow.
// Requires contract_approve on workspaceID (same permission as plan approve).
func (s *Service) SubmissionApprove(ctx context.Context, actorID, workspaceID, wfID string) (map[string]any, error) {
	if err := s.ensurePermitted(ctx, actorID, workspaceID); err != nil {
		return nil, err
	}
	out, err := s.client.SubmissionApprove(ctx, wfID, actorID)
	if err != nil {
		return nil, err
	}
	s.audit.Record(ctx, actorID, "contract.submission_approve", "contract", wfID, map[string]any{
		"workspace": workspaceID,
		"approver":  actorID,
	})
	return out, nil
}

// RejectSubmission rejects the submission gate for a goal workflow.
// Requires contract_approve on workspaceID.
func (s *Service) RejectSubmission(ctx context.Context, actorID, workspaceID, wfID, reason string) (map[string]any, error) {
	if err := s.ensurePermitted(ctx, actorID, workspaceID); err != nil {
		return nil, err
	}
	out, err := s.client.Reject(ctx, wfID, actorID, "submission", reason)
	if err != nil {
		return nil, err
	}
	s.audit.Record(ctx, actorID, "contract.reject", "contract", wfID, map[string]any{
		"workspace": workspaceID,
		"approver":  actorID,
		"stage":     "submission",
		"reason":    reason,
	})
	return out, nil
}

// RejectPlan rejects the plan approval gate for a contract workflow.
// Requires contract_approve on workspaceID.
func (s *Service) RejectPlan(ctx context.Context, actorID, workspaceID, wfID, reason string) (map[string]any, error) {
	if err := s.ensurePermitted(ctx, actorID, workspaceID); err != nil {
		return nil, err
	}
	out, err := s.client.Reject(ctx, wfID, actorID, "plan", reason)
	if err != nil {
		return nil, err
	}
	s.audit.Record(ctx, actorID, "contract.reject", "contract", wfID, map[string]any{
		"workspace": workspaceID,
		"approver":  actorID,
		"stage":     "plan",
		"reason":    reason,
	})
	return out, nil
}

// queueMap translates the iOS-facing queue/state name into the contract-plane
// queue value. Unknown values fall back to "all".
//
// iOS semantics → contract-plane queue:
//   - "gate_pending" → "awaiting_plan"  (workflows blocked on plan approval)
//   - "running"      → "running"
//   - "recent"       → "terminal"       (finished — completed/failed/canceled/etc.)
//   - ""             → "all"
func queueMap(iosQueue string) string {
	switch iosQueue {
	case "gate_pending":
		return "awaiting_plan"
	case "running":
		return "running"
	case "recent":
		return "terminal"
	case "awaiting_submission", "awaiting_plan", "terminal", "all":
		// Allow callers to pass through native contract-plane values directly,
		// useful for debugging / future iOS surfaces.
		return iosQueue
	case "":
		return "all"
	default:
		return "all"
	}
}

// List proxies to contract-plane GET /contracts after permission-checking the
// caller against workspaceID. iosQueue is translated via queueMap.
func (s *Service) List(ctx context.Context, actorID, workspaceID, iosQueue string, limit int) (map[string]any, error) {
	if err := s.ensurePermitted(ctx, actorID, workspaceID); err != nil {
		return nil, err
	}
	return s.client.List(ctx, queueMap(iosQueue), limit)
}

// Get proxies to contract-plane GET /contracts/{wfID} after permission-checking
// the caller against workspaceID. Returns ErrNotFound from the underlying
// client when contract-plane responds 404.
func (s *Service) Get(ctx context.Context, actorID, workspaceID, wfID string) (map[string]any, error) {
	if err := s.ensurePermitted(ctx, actorID, workspaceID); err != nil {
		return nil, err
	}
	return s.client.Get(ctx, wfID)
}

func (s *Service) ensurePermitted(ctx context.Context, actorID, workspaceID string) error {
	// workspace-scoped check only when caller pinned a workspace. List/Get
	// without workspace_id (iOS Contracts tab) trust the authenticated
	// caller identity and let contract-plane return their visible set.
	if workspaceID == "" {
		return nil
	}
	ok, err := s.checker.Check(ctx, spicedb.TypeWorkspace, workspaceID,
		spicedb.PermContractApprove, actorID)
	if err != nil {
		return fmt.Errorf("permission check: %w", err)
	}
	if !ok {
		return spicedb.ErrForbidden
	}
	return nil
}
