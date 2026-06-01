package contract

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tojiuni/goquest/internal/audit"
	"github.com/tojiuni/goquest/internal/spicedb"
)

type fakeClient struct {
	approveCalled          bool
	abortCalled            bool
	submissionApproveCalled bool
	rejectCalled           bool
	lastApprover           string
	lastStage              string
	lastReason             string
	err                    error
	out                    map[string]any
}

func (f *fakeClient) Approve(ctx context.Context, wf, approver string) (map[string]any, error) {
	f.approveCalled = true
	f.lastApprover = approver
	return f.out, f.err
}
func (f *fakeClient) Abort(ctx context.Context, wf string) (map[string]any, error) {
	f.abortCalled = true
	return f.out, f.err
}
func (f *fakeClient) SubmissionApprove(ctx context.Context, wf, approver string) (map[string]any, error) {
	f.submissionApproveCalled = true
	f.lastApprover = approver
	return f.out, f.err
}
func (f *fakeClient) Reject(ctx context.Context, wf, approver, stage, reason string) (map[string]any, error) {
	f.rejectCalled = true
	f.lastApprover = approver
	f.lastStage = stage
	f.lastReason = reason
	return f.out, f.err
}

type fakeChecker struct{ allow bool }

func (f *fakeChecker) Check(_ context.Context, _, _, _, _ string) (bool, error) { return f.allow, nil }
func (f *fakeChecker) VisibleResourceIDs(context.Context, string, string, string) ([]string, bool, error) {
	return nil, false, nil
}

type fakeAudit struct{ records []map[string]any }

func (f *fakeAudit) Record(_ context.Context, actor, action, kind, id string, detail map[string]any) {
	f.records = append(f.records, map[string]any{
		"actor": actor, "action": action, "kind": kind, "id": id, "detail": detail,
	})
}
func (f *fakeAudit) List(context.Context, audit.ListFilter) ([]*audit.Entry, error) {
	return nil, nil
}

// ── Approve ───────────────────────────────────────────────────────────────────

func TestService_Approve_DeniesWithoutPermission(t *testing.T) {
	svc := NewService(&fakeClient{}, &fakeChecker{allow: false}, &fakeAudit{})
	_, err := svc.Approve(context.Background(), "user-1", "workspace-a", "wf1")
	require.Error(t, err)
	assert.ErrorIs(t, err, spicedb.ErrForbidden)
}

func TestService_Approve_RecordsAuditAndProxies(t *testing.T) {
	cli := &fakeClient{out: map[string]any{"workflow_id": "wf1", "status": "approved"}}
	aud := &fakeAudit{}
	svc := NewService(cli, &fakeChecker{allow: true}, aud)
	out, err := svc.Approve(context.Background(), "user-1", "workspace-a", "wf1")
	require.NoError(t, err)
	assert.True(t, cli.approveCalled)
	assert.Equal(t, "user-1", cli.lastApprover)
	require.Len(t, aud.records, 1)
	rec := aud.records[0]
	assert.Equal(t, "user-1", rec["actor"])
	assert.Equal(t, "contract.approve", rec["action"])
	assert.Equal(t, "contract", rec["kind"])
	assert.Equal(t, "wf1", rec["id"])
	detail := rec["detail"].(map[string]any)
	assert.Equal(t, "user-1", detail["approver"])
	_, hasDecision := detail["decision"]
	assert.False(t, hasDecision, "decision must not appear in audit detail")
	assert.Equal(t, "wf1", out["workflow_id"])
}

func TestService_Approve_DoesNotProxyOnDenied(t *testing.T) {
	cli := &fakeClient{}
	svc := NewService(cli, &fakeChecker{allow: false}, &fakeAudit{})
	_, _ = svc.Approve(context.Background(), "user-1", "workspace-a", "wf1")
	assert.False(t, cli.approveCalled)
}

func TestService_Abort_RecordsAuditAndProxies(t *testing.T) {
	cli := &fakeClient{out: map[string]any{"state": "aborting"}}
	aud := &fakeAudit{}
	svc := NewService(cli, &fakeChecker{allow: true}, aud)
	_, err := svc.Abort(context.Background(), "user-1", "workspace-a", "wf1")
	require.NoError(t, err)
	assert.True(t, cli.abortCalled)
	require.Len(t, aud.records, 1)
	assert.Equal(t, "contract.abort", aud.records[0]["action"])
}

func TestService_Approve_ReturnsClientErrorAsIs(t *testing.T) {
	cli := &fakeClient{err: errors.New("boom")}
	svc := NewService(cli, &fakeChecker{allow: true}, &fakeAudit{})
	_, err := svc.Approve(context.Background(), "u", "w", "wf1")
	require.Error(t, err)
}

// ── SubmissionApprove ─────────────────────────────────────────────────────────

func TestService_SubmissionApprove_DeniesWithoutPermission(t *testing.T) {
	svc := NewService(&fakeClient{}, &fakeChecker{allow: false}, &fakeAudit{})
	_, err := svc.SubmissionApprove(context.Background(), "user-1", "workspace-a", "wf1")
	require.Error(t, err)
	assert.ErrorIs(t, err, spicedb.ErrForbidden)
}

func TestService_SubmissionApprove_RecordsAuditAndProxies(t *testing.T) {
	cli := &fakeClient{out: map[string]any{"workflow_id": "wf1", "status": "approved"}}
	aud := &fakeAudit{}
	svc := NewService(cli, &fakeChecker{allow: true}, aud)
	out, err := svc.SubmissionApprove(context.Background(), "user-2", "workspace-b", "wf1")
	require.NoError(t, err)
	assert.True(t, cli.submissionApproveCalled)
	assert.Equal(t, "user-2", cli.lastApprover)
	require.Len(t, aud.records, 1)
	rec := aud.records[0]
	assert.Equal(t, "user-2", rec["actor"])
	assert.Equal(t, "contract.submission_approve", rec["action"])
	assert.Equal(t, "wf1", out["workflow_id"])
}

// ── RejectSubmission ──────────────────────────────────────────────────────────

func TestService_RejectSubmission_DeniesWithoutPermission(t *testing.T) {
	svc := NewService(&fakeClient{}, &fakeChecker{allow: false}, &fakeAudit{})
	_, err := svc.RejectSubmission(context.Background(), "user-1", "workspace-a", "wf1", "not ready")
	require.Error(t, err)
	assert.ErrorIs(t, err, spicedb.ErrForbidden)
}

func TestService_RejectSubmission_RecordsAuditAndProxies(t *testing.T) {
	cli := &fakeClient{out: map[string]any{"workflow_id": "wf1"}}
	aud := &fakeAudit{}
	svc := NewService(cli, &fakeChecker{allow: true}, aud)
	_, err := svc.RejectSubmission(context.Background(), "user-3", "workspace-c", "wf1", "not ready")
	require.NoError(t, err)
	assert.True(t, cli.rejectCalled)
	assert.Equal(t, "user-3", cli.lastApprover)
	assert.Equal(t, "submission", cli.lastStage)
	assert.Equal(t, "not ready", cli.lastReason)
	require.Len(t, aud.records, 1)
	rec := aud.records[0]
	assert.Equal(t, "contract.reject", rec["action"])
	detail := rec["detail"].(map[string]any)
	assert.Equal(t, "submission", detail["stage"])
	assert.Equal(t, "not ready", detail["reason"])
}

// ── RejectPlan ────────────────────────────────────────────────────────────────

func TestService_RejectPlan_DeniesWithoutPermission(t *testing.T) {
	svc := NewService(&fakeClient{}, &fakeChecker{allow: false}, &fakeAudit{})
	_, err := svc.RejectPlan(context.Background(), "user-1", "workspace-a", "wf1", "budget exceeded")
	require.Error(t, err)
	assert.ErrorIs(t, err, spicedb.ErrForbidden)
}

func TestService_RejectPlan_RecordsAuditAndProxies(t *testing.T) {
	cli := &fakeClient{out: map[string]any{"workflow_id": "wf1"}}
	aud := &fakeAudit{}
	svc := NewService(cli, &fakeChecker{allow: true}, aud)
	_, err := svc.RejectPlan(context.Background(), "user-4", "workspace-d", "wf1", "budget exceeded")
	require.NoError(t, err)
	assert.True(t, cli.rejectCalled)
	assert.Equal(t, "user-4", cli.lastApprover)
	assert.Equal(t, "plan", cli.lastStage)
	assert.Equal(t, "budget exceeded", cli.lastReason)
	require.Len(t, aud.records, 1)
	rec := aud.records[0]
	assert.Equal(t, "contract.reject", rec["action"])
	detail := rec["detail"].(map[string]any)
	assert.Equal(t, "plan", detail["stage"])
	assert.Equal(t, "budget exceeded", detail["reason"])
}
