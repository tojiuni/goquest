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
// proxies to the upstream client and records an audit entry.
func (s *Service) Approve(ctx context.Context, actorID, workspaceID, wfID string, decision bool) (map[string]any, error) {
	if err := s.ensurePermitted(ctx, actorID, workspaceID); err != nil {
		return nil, err
	}
	out, err := s.client.Approve(ctx, wfID, decision)
	if err != nil {
		return nil, err
	}
	s.audit.Record(ctx, actorID, "contract.approve", "contract", wfID, map[string]any{
		"workspace": workspaceID,
		"decision":  decision,
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
