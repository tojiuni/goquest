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

func TestPG_SupersedeOpenMerge(t *testing.T) {
	ctx := context.Background()
	_, st, tid := setup(t)
	old, _ := st.Create(ctx, approval.NewReq{TicketID: tid, Kind: approval.KindMerge, RequestedBy: "desk",
		Subject: approval.Subject{Repo: "o/r", PR: 9, HeadSHA: "old"}})
	ids, err := st.SupersedeOpenMerge(ctx, tid, "o/r", 9, "new", "desk")
	if err != nil || len(ids) != 1 || ids[0] != old.ID {
		t.Fatalf("supersede: %v %v", ids, err)
	}
	a, _ := st.Get(ctx, old.ID)
	if a.Status != approval.StatusSuperseded {
		t.Errorf("status %s", a.Status)
	}
}
