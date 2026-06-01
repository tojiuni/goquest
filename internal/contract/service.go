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

func (s *Service) ensurePermitted(ctx context.Context, actorID, workspaceID string) error {
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
