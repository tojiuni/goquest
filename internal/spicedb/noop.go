package spicedb

import (
	"context"

	"github.com/google/uuid"
)

// NoopChecker always grants permission. Used when SpiceDB is disabled.
type NoopChecker struct{}

func (n *NoopChecker) CanViewTicket(_ context.Context, _ uuid.UUID, _ string, _ VisibilityMode) (bool, error) {
	return true, nil
}
func (n *NoopChecker) CanManageTicket(_ context.Context, _ uuid.UUID, _ string) (bool, error) {
	return true, nil
}
func (n *NoopChecker) CanViewProject(_ context.Context, _ uuid.UUID, _ string) (bool, error) {
	return true, nil
}

// NoopWriter silently discards all writes. Used when SpiceDB is disabled.
type NoopWriter struct{}

func (n *NoopWriter) WriteTicket(_ context.Context, _, _ uuid.UUID, _, _ string) error { return nil }
func (n *NoopWriter) WriteWorkspaceMember(_ context.Context, _ uuid.UUID, _, _ string) error {
	return nil
}
func (n *NoopWriter) DeleteWorkspaceMember(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}
func (n *NoopWriter) WriteProjectMember(_ context.Context, _ uuid.UUID, _, _ string) error {
	return nil
}
func (n *NoopWriter) DeleteProjectMember(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}
func (n *NoopWriter) WriteProjectWorkspace(_ context.Context, _, _ uuid.UUID) error { return nil }
func (n *NoopWriter) AddTicketViewer(_ context.Context, _ uuid.UUID, _ string) error { return nil }
func (n *NoopWriter) ReplaceTicketAssignee(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}
