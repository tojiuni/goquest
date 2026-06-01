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
	approveCalled bool
	abortCalled   bool
	err           error
	out           map[string]any
}

func (f *fakeClient) Approve(ctx context.Context, wf string, d bool) (map[string]any, error) {
	f.approveCalled = true
	return f.out, f.err
}
func (f *fakeClient) Abort(ctx context.Context, wf string) (map[string]any, error) {
	f.abortCalled = true
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

func TestService_Approve_DeniesWithoutPermission(t *testing.T) {
	svc := NewService(&fakeClient{}, &fakeChecker{allow: false}, &fakeAudit{})
	_, err := svc.Approve(context.Background(), "user-1", "workspace-a", "wf1", true)
	require.Error(t, err)
	assert.ErrorIs(t, err, spicedb.ErrForbidden)
}

func TestService_Approve_RecordsAuditAndProxies(t *testing.T) {
	cli := &fakeClient{out: map[string]any{"workflow_id": "wf1", "decision": true}}
	aud := &fakeAudit{}
	svc := NewService(cli, &fakeChecker{allow: true}, aud)
	out, err := svc.Approve(context.Background(), "user-1", "workspace-a", "wf1", true)
	require.NoError(t, err)
	assert.True(t, cli.approveCalled)
	require.Len(t, aud.records, 1)
	rec := aud.records[0]
	assert.Equal(t, "user-1", rec["actor"])
	assert.Equal(t, "contract.approve", rec["action"])
	assert.Equal(t, "contract", rec["kind"])
	assert.Equal(t, "wf1", rec["id"])
	assert.Equal(t, "wf1", out["workflow_id"])
}

func TestService_Approve_DoesNotProxyOnDenied(t *testing.T) {
	cli := &fakeClient{}
	svc := NewService(cli, &fakeChecker{allow: false}, &fakeAudit{})
	_, _ = svc.Approve(context.Background(), "user-1", "workspace-a", "wf1", false)
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
	_, err := svc.Approve(context.Background(), "u", "w", "wf1", true)
	require.Error(t, err)
}
