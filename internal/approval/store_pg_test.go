package approval_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tojiuni/goquest/internal/approval"
	"github.com/tojiuni/goquest/internal/migrate"
)

// PG integration tests: skipped unless GOQUEST_TEST_DATABASE_URL is set (the
// repo's unit suites stay hermetic). They cover what the in-memory store
// cannot: the DB-level append-only guard and the CAS in Transition.
func setup(t *testing.T) (*pgxpool.Pool, approval.Store, uuid.UUID) {
	t.Helper()
	dsn := os.Getenv("GOQUEST_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("GOQUEST_TEST_DATABASE_URL not set; skipping PG integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("ping: %v", err)
	}
	if err := migrate.Run(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// a second run must be a no-op (migrations re-run on every boot)
	if err := migrate.Run(ctx, pool); err != nil {
		t.Fatalf("migrate re-run: %v", err)
	}
	var tid uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO tickets (title, type, status, priority, requester_id)
		VALUES ('approval pg test','task','pending','normal','test') RETURNING id`).Scan(&tid); err != nil {
		t.Skipf("insert ticket (schema drift?): %v", err)
	}
	return pool, approval.NewPGStore(pool), tid
}

func TestPG_AppendOnlyAndCAS(t *testing.T) {
	ctx := context.Background()
	pool, st, tid := setup(t)
	a, err := st.Create(ctx, approval.NewReq{TicketID: tid, Kind: approval.KindMerge, RequestedBy: "desk",
		Subject: approval.Subject{Repo: "o/r", PR: 1, HeadSHA: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	d := &approval.Decision{DecidedBy: "user", Basis: approval.BasisUserDirect}
	got, err := st.Transition(ctx, a.ID, approval.StatusPending, approval.StatusApproved, "user", "", d)
	if err != nil || got.Status != approval.StatusApproved || len(got.Events) != 1 {
		t.Fatalf("transition: %v %+v", err, got)
	}
	if _, err := st.Transition(ctx, a.ID, approval.StatusPending, approval.StatusRejected, "user", "", d); !errors.Is(err, approval.ErrStale) {
		t.Errorf("want ErrStale, got %v", err)
	}
	// Each statement targets a row that exists (a.ID), so a rejection can only
	// come from the guard trigger (raise_exception, P0001), not from 0 rows matched.
	for name, q := range map[string]string{
		"delete approval": `DELETE FROM approvals WHERE id=$1`,
		"rewrite request": `UPDATE approvals SET request_note='x' WHERE id=$1`,
		"rewrite decider": `UPDATE approvals SET decided_by='someone-else' WHERE id=$1`,
		"rewrite basis":   `UPDATE approvals SET basis='desk_delegated' WHERE id=$1`,
	} {
		_, err := pool.Exec(ctx, q, a.ID)
		wantPGCode(t, name, err, "P0001")
	}
	if _, err := pool.Exec(ctx, `UPDATE approval_events SET note='x' WHERE approval_id=$1`, a.ID); err == nil {
		t.Error("approval_events update must be rejected")
	} else {
		wantPGCode(t, "update event", err, "P0001")
	}
	_, err = pool.Exec(ctx, `DELETE FROM approval_events WHERE approval_id=$1`, a.ID)
	wantPGCode(t, "delete event", err, "P0001")

	// the record is untouched by all of the above
	after, err := st.Get(ctx, a.ID)
	if err != nil || after.RequestNote != "" || after.DecidedBy == nil || *after.DecidedBy != "user" || len(after.Events) != 1 {
		t.Fatalf("record changed despite guard: %v %+v", err, after)
	}
}

func TestPG_DelegationRuleCeiling(t *testing.T) {
	ctx := context.Background()
	pool, _, _ := setup(t)
	var wid uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO workspaces (name, slug, owner_id)
		VALUES ('approval pg test', $1, 'test') RETURNING id`, "approval-pg-"+uuid.NewString()).Scan(&wid); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM delegation_rules WHERE workspace_id=$1`, wid)
		pool.Exec(ctx, `DELETE FROM workspaces WHERE id=$1`, wid)
	})
	ins := `INSERT INTO delegation_rules (workspace_id, principal, kinds, created_by) VALUES ($1,'p',$2,'u')`
	for _, kinds := range [][]string{{"infra"}, {"design"}, {"ops"}, {"ticket"}, {"merge", "infra"}, {}} {
		_, err := pool.Exec(ctx, ins, wid, kinds)
		wantPGCode(t, fmt.Sprintf("kinds %v", kinds), err, "23514")
	}
	// a rule may not name a person (migration 028)
	for _, principal := range []string{"lyckabc@gmail.com", " ", ""} {
		_, err := pool.Exec(ctx, `INSERT INTO delegation_rules (workspace_id, principal, kinds, created_by) VALUES ($1,$2,'{merge}','u')`, wid, principal)
		wantPGCode(t, fmt.Sprintf("principal %q", principal), err, "23514")
	}
	for _, kinds := range [][]string{{"merge"}, {"deviation"}, {"merge", "deviation"}} {
		if _, err := pool.Exec(ctx, ins, wid, kinds); err != nil {
			t.Errorf("kinds %v must be allowed: %v", kinds, err)
		}
	}
}

// wantPGCode fails unless err is a Postgres error with the given SQLSTATE.
func wantPGCode(t *testing.T, what string, err error, code string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Errorf("%s: want pg error %s, got %v", what, code, err)
		return
	}
	if pgErr.Code != code {
		t.Errorf("%s: want SQLSTATE %s, got %s (%s)", what, code, pgErr.Code, pgErr.Message)
	}
}

func TestPG_CreateSupersedesAndRejectsDuplicates(t *testing.T) {
	ctx := context.Background()
	_, st, tid := setup(t)
	req := func(sha string) approval.NewReq {
		return approval.NewReq{TicketID: tid, Kind: approval.KindMerge, RequestedBy: "desk",
			Subject: approval.Subject{Repo: "o/r", PR: 9, HeadSHA: sha}}
	}
	old, err := st.Create(ctx, req("old"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Create(ctx, req("old")); !errors.Is(err, approval.ErrDuplicatePending) {
		t.Fatalf("want ErrDuplicatePending, got %v", err)
	}
	fresh, err := st.Create(ctx, req("new"))
	if err != nil {
		t.Fatal(err)
	}
	if fresh.SupersedesID == nil || *fresh.SupersedesID != old.ID {
		t.Fatalf("supersedes_id = %v want %v", fresh.SupersedesID, old.ID)
	}
	a, _ := st.Get(ctx, old.ID)
	if a.Status != approval.StatusSuperseded || len(a.Events) != 1 {
		t.Errorf("old record: %+v", a)
	}
	if ids, err := st.SupersedeOpenMerge(ctx, tid, "o/r", 9, "new", "desk"); err != nil || len(ids) != 0 {
		t.Errorf("idempotent supersede: %v %v", ids, err)
	}
}

func TestPG_DelegatedDecisionEndToEnd(t *testing.T) {
	ctx := context.Background()
	pool, st, tid := setup(t)
	var wid uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO workspaces (name, slug, owner_id)
		VALUES ('approval pg test', $1, 'test') RETURNING id`, "approval-pg-"+uuid.NewString()).Scan(&wid); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM delegation_rules WHERE workspace_id=$1`, wid)
		pool.Exec(ctx, `DELETE FROM workspaces WHERE id=$1`, wid)
	})
	rs := approval.NewPGRuleStore(pool)
	rule := &approval.DelegationRule{WorkspaceID: wid, Principal: "desk", Kinds: []approval.Kind{approval.KindMerge},
		Repos: []string{"o/*"}, Conditions: map[string]any{"require_evidence_types": []any{"ci_run"}}, UserRef: "ticket#1", CreatedBy: "user"}
	if err := rs.Create(ctx, rule); err != nil || rule.ID == uuid.Nil {
		t.Fatalf("create rule: %v", err)
	}
	svc := approval.NewService(st).WithRules(rs)
	a, err := st.Create(ctx, approval.NewReq{TicketID: tid, Kind: approval.KindMerge, RequestedBy: "requester",
		Subject:  approval.Subject{Repo: "o/r", PR: 5, HeadSHA: "a"},
		Evidence: []approval.EvidenceItem{{Type: "ci_run", Ref: "https://ci/1"}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Decide(ctx, approval.DecideInput{TicketID: tid, WorkspaceID: wid, ApprovalID: a.ID,
		Decision: approval.StatusApproved, Note: "ok", UserRef: "chat"}, approval.Actor{ID: "desk", Kind: approval.ActorSession})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if got.Basis == nil || *got.Basis != approval.BasisDeskDelegate || got.DelegationRuleID == nil || *got.DelegationRuleID != rule.ID {
		t.Fatalf("delegation not recorded: %+v", got)
	}
	// revoked rules stop applying
	if err := rs.Revoke(ctx, wid, rule.ID); err != nil {
		t.Fatal(err)
	}
	b, _ := st.Create(ctx, approval.NewReq{TicketID: tid, Kind: approval.KindMerge, RequestedBy: "requester",
		Subject:  approval.Subject{Repo: "o/r", PR: 6, HeadSHA: "a"},
		Evidence: []approval.EvidenceItem{{Type: "ci_run", Ref: "https://ci/2"}}})
	_, err = svc.Decide(ctx, approval.DecideInput{TicketID: tid, WorkspaceID: wid, ApprovalID: b.ID,
		Decision: approval.StatusApproved, UserRef: "chat"}, approval.Actor{ID: "desk", Kind: approval.ActorSession})
	var de *approval.DeniedError
	if !errors.As(err, &de) || de.Reason != approval.ReasonNoMatchingRule {
		t.Errorf("revoked rule: want no_matching_rule, got %v", err)
	}
}

func TestPG_RuleListAndRevoke(t *testing.T) {
	ctx := context.Background()
	pool, _, _ := setup(t)
	var wid uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO workspaces (name, slug, owner_id)
		VALUES ('approval pg test', $1, 'test') RETURNING id`, "approval-pg-"+uuid.NewString()).Scan(&wid); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM delegation_rules WHERE workspace_id=$1`, wid)
		pool.Exec(ctx, `DELETE FROM workspaces WHERE id=$1`, wid)
	})
	rs := approval.NewPGRuleStore(pool)
	r := &approval.DelegationRule{WorkspaceID: wid, Principal: "tokenhub-x", Kinds: []approval.Kind{approval.KindMerge}, UserRef: "u", CreatedBy: "owner"}
	if err := rs.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := rs.Create(ctx, &approval.DelegationRule{WorkspaceID: wid, Principal: "lyckabc@gmail.com", Kinds: []approval.Kind{approval.KindMerge}, CreatedBy: "o"}); !errors.Is(err, approval.ErrInvalidRule) {
		t.Errorf("email principal must be rejected before the DB: %v", err)
	}
	all, err := rs.ListByWorkspace(ctx, wid)
	if err != nil || len(all) != 1 || all[0].ID != r.ID {
		t.Fatalf("list: %v %+v", err, all)
	}
	if err := rs.Revoke(ctx, uuid.New(), r.ID); !errors.Is(err, approval.ErrRuleNotFound) {
		t.Errorf("revoke in another workspace: %v", err)
	}
	if err := rs.Revoke(ctx, wid, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := rs.Revoke(ctx, wid, r.ID); !errors.Is(err, approval.ErrRuleNotFound) {
		t.Errorf("second revoke: %v", err)
	}
}

func TestPG_RecordTicketApproval(t *testing.T) {
	ctx := context.Background()
	_, st, tid := setup(t)
	svc := approval.NewService(st)
	// an unattested caller is stored with a NULL basis (not an empty string, which the CHECK rejects)
	a, err := svc.RecordTicketApproval(ctx, tid, "rejected", "nope", approval.Actor{ID: "bot", Kind: approval.ActorOther}, "boss@x.io")
	if err != nil || a.Basis != nil || !a.Legacy || a.Status != approval.StatusRejected {
		t.Fatalf("unattested: %v %+v", err, a)
	}
	b, err := svc.RecordTicketApproval(ctx, tid, "approved", "", approval.Actor{ID: "u@x.io", Kind: approval.ActorHuman}, "")
	if err != nil || b.Basis == nil || *b.Basis != approval.BasisUserDirect || b.Legacy {
		t.Fatalf("person: %v %+v", err, b)
	}
	// a pending record left behind is reused instead of tripping the duplicate check
	if _, err := st.Create(ctx, approval.NewReq{TicketID: tid, Kind: approval.KindTicket, RequestedBy: "u@x.io", Subject: approval.Subject{Ticket: tid.String()}}); err != nil {
		t.Fatal(err)
	}
	if c, err := svc.RecordTicketApproval(ctx, tid, "revision", "x", approval.Actor{ID: "u@x.io", Kind: approval.ActorHuman}, ""); err != nil || c.Status != approval.StatusRevisionRequested {
		t.Fatalf("reuse pending: %v %+v", err, c)
	}
}

func TestPG_SupersedeStaleMergeAcrossTickets(t *testing.T) {
	ctx := context.Background()
	pool, st, t1 := setup(t)
	var t2 uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO tickets (title, type, status, priority, requester_id)
		VALUES ('approval pg test 2','task','pending','normal','test') RETURNING id`).Scan(&t2); err != nil {
		t.Skipf("insert ticket: %v", err)
	}
	mk := func(tid uuid.UUID, pr int, sha string) *approval.Approval {
		a, err := st.Create(ctx, approval.NewReq{TicketID: tid, Kind: approval.KindMerge, RequestedBy: "r",
			Subject: approval.Subject{Repo: "o/stale", PR: pr, HeadSHA: sha}})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a1, a2 := mk(t1, 1, "old"), mk(t2, 1, "old")
	if _, err := st.Transition(ctx, a2.ID, approval.StatusPending, approval.StatusApproved, "u", "",
		&approval.Decision{DecidedBy: "u", Basis: approval.BasisUserDirect}); err != nil {
		t.Fatal(err)
	}
	keep := mk(t1, 2, "old")  // other PR
	fresh := mk(t1, 1, "new") // Create already retired a1 on this ticket
	ids, err := st.SupersedeStaleMerge(ctx, "o/stale", 1, "new", "vcs-webhook")
	if err != nil || len(ids) != 1 || ids[0] != a2.ID {
		t.Fatalf("only the other ticket's approval should remain to retire: %v %v", ids, err)
	}
	for id, want := range map[uuid.UUID]approval.Status{
		a1.ID: approval.StatusSuperseded, a2.ID: approval.StatusSuperseded,
		keep.ID: approval.StatusPending, fresh.ID: approval.StatusPending,
	} {
		if a, _ := st.Get(ctx, id); a.Status != want {
			t.Errorf("%s: %s want %s", id, a.Status, want)
		}
	}
	if ids, err := st.SupersedeStaleMerge(ctx, "o/stale", 1, "new", "vcs-webhook"); err != nil || len(ids) != 0 {
		t.Errorf("idempotent: %v %v", ids, err)
	}
}
