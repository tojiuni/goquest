# Ticket on_hold Status (goquest backend) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a new `on_hold` ticket status to goquest's state machine, so intentionally-paused work can be represented distinctly from `pending`, without the SLA auto-fail worker or the parent auto-complete gate misbehaving around it.

**Architecture:** A new `Status` constant backed by a Postgres enum value (added via `ALTER TYPE ... ADD VALUE`, the same pattern used for `pending_approval`/`pending_revision`), three new entries in the in-memory `transitions` map (`pending`/`assigned`/`in_progress` → `on_hold`, `on_hold` → `pending` only), and one SQL WHERE-clause change so the SLA worker's `ListOverdue` scan skips `on_hold` tickets (it currently auto-transitions everything it returns to `failed`). `AllSubtasksTerminal` needs no code change — `on_hold` is absent from its terminal-status list by construction — but gets a regression test locking that in.

**Tech Stack:** Go, PostgreSQL (enum type + migration), existing `internal/ticket` package conventions.

## Global Constraints

- New status value: `on_hold` (Go constant `StatusOnHold`).
- Transitions: `pending`→`on_hold`, `assigned`→`on_hold`, `in_progress`→`on_hold` all allowed; `on_hold`→`pending` is the ONLY allowed outgoing transition (mirrors the existing `failed`→`pending` retry pattern — resuming always restarts from `pending`).
- `on_hold` is non-terminal (not in `terminals`, not in any `NOT IN (...)` "settled" list except `ListOverdue`'s exclusion, which is about SLA auto-fail, not terminality).
- `ListOverdue` (`internal/ticket/store_pg.go`) currently drives `internal/sla/worker.go`'s `Worker.ExpireOnce`, which auto-transitions every ticket it returns to `failed` on a timer. `on_hold` must be added to its exclusion list alongside `completed`, `cancelled`, `failed`, `pending_approval`, `pending_revision` — otherwise a paused ticket past `due_at` gets auto-failed by the background worker.
- Migration file numbering: the latest existing migration is `022_org.sql` — the new one is `internal/migrate/sql/023_on_hold_status.sql`, NOT `011` (010 is not the latest; do not renumber or collide with 011-022 which already exist).
- DB integration tests in this package (`store_pg_test.go`, `refs_store_test.go`) self-skip via `t.Skip("GOQUEST_TEST_DATABASE_URL not set; skipping PG integration test")` when that env var is unset — this is expected in a sandboxed dev pod; do not treat a skip as a failure, and do not attempt to stand up a local Postgres to force it to run. CI provides the real database.

---

### Task 1: Add the `on_hold` status — constant, migration, transition table, quality-gate coverage

**Files:**
- Modify: `internal/ticket/model.go` (add `StatusOnHold` constant)
- Create: `internal/migrate/sql/023_on_hold_status.sql`
- Modify: `internal/ticket/store_pg.go` (the `transitions` map, ~line 727-734)
- Modify: `internal/ticket/statemachine_quality_test.go` (add `StatusOnHold` to `allStatuses`)
- Modify: `internal/ticket/store_transitions_test.go` (add new transition cases)

**Interfaces:**
- Produces: `ticket.StatusOnHold` (type `ticket.Status`, value `"on_hold"`) — used by Task 2 and by the goquest-ui/goquest-mcp plans in later sessions.
- Consumes: nothing new; extends the existing `ticket.Status` type and `transitions` map already defined in this package.

- [ ] **Step 1: Write the failing test — new transition cases**

Add these cases to the `cases` slice in `internal/ticket/store_transitions_test.go` (insert after the existing `{StatusFailed, StatusPending, false},` line, before the `// terminal → anything: error` comment):

```go
		// on_hold transitions (goq-ticket-on-hold-status)
		{StatusPending, StatusOnHold, false},
		{StatusAssigned, StatusOnHold, false},
		{StatusInProgress, StatusOnHold, false},
		{StatusOnHold, StatusPending, false},
		// on_hold must not skip straight back into active work
		{StatusOnHold, StatusAssigned, true},
		{StatusOnHold, StatusInProgress, true},
		{StatusOnHold, StatusCompleted, true},
		// on_hold is not itself a valid source for pending_approval/pending_revision moves
		{StatusOnHold, StatusPendingApproval, true},
```

Also add `StatusOnHold` to `allStatuses` in `internal/ticket/statemachine_quality_test.go`:

```go
var allStatuses = []Status{
	StatusPending, StatusAssigned, StatusInProgress, StatusCompleted,
	StatusFailed, StatusCancelled, StatusPendingApproval, StatusPendingRevision,
	StatusOnHold,
}
```

(`terminals` in that same file stays unchanged — `on_hold` is not terminal.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go build ./... 2>&1 | head -20` (from `/workspace/repos/goquest`)
Expected: FAIL with `undefined: StatusOnHold` (the constant doesn't exist yet — this is the RED state; a compile failure referencing the new symbol is the expected failure here, not a runtime assertion failure)

- [ ] **Step 3: Add the `StatusOnHold` constant**

In `internal/ticket/model.go`, change:

```go
const (
	StatusPending         Status = "pending"
	StatusAssigned        Status = "assigned"
	StatusInProgress      Status = "in_progress"
	StatusCompleted       Status = "completed"
	StatusFailed          Status = "failed"
	StatusCancelled       Status = "cancelled"
	StatusPendingApproval Status = "pending_approval"
	StatusPendingRevision Status = "pending_revision"
)
```

to:

```go
const (
	StatusPending         Status = "pending"
	StatusAssigned        Status = "assigned"
	StatusInProgress      Status = "in_progress"
	StatusCompleted       Status = "completed"
	StatusFailed          Status = "failed"
	StatusCancelled       Status = "cancelled"
	StatusPendingApproval Status = "pending_approval"
	StatusPendingRevision Status = "pending_revision"
	// StatusOnHold marks a ticket intentionally paused (goq-ticket-on-hold-status).
	// Non-terminal: does not count as "settled" for AllSubtasksTerminal (a paused
	// child must keep blocking its parent's auto-complete), but IS excluded from
	// ListOverdue so the SLA worker never auto-fails a ticket someone deliberately
	// held. The only allowed outgoing transition is back to StatusPending.
	StatusOnHold Status = "on_hold"
)
```

- [ ] **Step 4: Add the transition table entries**

In `internal/ticket/store_pg.go`, change the `transitions` map:

```go
var transitions = map[Status][]Status{
	StatusPending:         {StatusAssigned, StatusCancelled, StatusPendingApproval},
	StatusAssigned:        {StatusInProgress, StatusPending, StatusCancelled, StatusPendingApproval},
	StatusInProgress:      {StatusCompleted, StatusFailed, StatusPending, StatusPendingApproval},
	StatusCompleted:       {},
	StatusFailed:          {StatusPending},
	StatusCancelled:       {},
	StatusPendingApproval: {StatusAssigned, StatusCancelled, StatusPendingRevision},
	StatusPendingRevision: {StatusInProgress, StatusCancelled, StatusPendingApproval},
}
```

to:

```go
var transitions = map[Status][]Status{
	StatusPending:         {StatusAssigned, StatusCancelled, StatusPendingApproval, StatusOnHold},
	StatusAssigned:        {StatusInProgress, StatusPending, StatusCancelled, StatusPendingApproval, StatusOnHold},
	StatusInProgress:      {StatusCompleted, StatusFailed, StatusPending, StatusPendingApproval, StatusOnHold},
	StatusCompleted:       {},
	StatusFailed:          {StatusPending},
	StatusCancelled:       {},
	StatusPendingApproval: {StatusAssigned, StatusCancelled, StatusPendingRevision},
	StatusPendingRevision: {StatusInProgress, StatusCancelled, StatusPendingApproval},
	StatusOnHold:          {StatusPending},
}
```

- [ ] **Step 5: Add the migration file**

Create `internal/migrate/sql/023_on_hold_status.sql`:

```sql
-- 023_on_hold_status.sql
-- Add on_hold ticket status for intentionally-paused work (goq-ticket-on-hold-status).
-- Idempotent.

ALTER TYPE ticket_status ADD VALUE IF NOT EXISTS 'on_hold';

-- NOTE: ALTER TYPE ADD VALUE cannot be rolled back in PostgreSQL.
-- To undo this migration, the enum type must be dropped and recreated manually.
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go build ./... && go vet ./... && go test ./internal/ticket/... -run 'TestValidateTransition|TestStateMachine_QualityGate' -v`
Expected: PASS — all `TestValidateTransition` cases (including the 8 new ones) and `TestStateMachine_QualityGate` green.

- [ ] **Step 7: Commit**

```bash
git add internal/ticket/model.go internal/ticket/store_pg.go internal/ticket/statemachine_quality_test.go internal/ticket/store_transitions_test.go internal/migrate/sql/023_on_hold_status.sql
git commit -m "feat(ticket): add on_hold status + transitions (goq-ticket-on-hold-status)"
```

---

### Task 2: Exclude `on_hold` from the SLA auto-fail scan + lock in the parent auto-complete invariant

**Files:**
- Modify: `internal/ticket/store_pg.go` (the `ListOverdue` SQL, ~line 890-897)
- Modify: `internal/ticket/store_pg_test.go` (new tests)

**Interfaces:**
- Consumes: `ticket.StatusOnHold` from Task 1; `ticket.Store.ListOverdue(ctx)` and `ticket.Store.AllSubtasksTerminal(ctx, parentID)` (already-existing interface methods on `ticket.Store`, both implemented by `*pgStore`).
- Produces: nothing new — this task only changes SQL filtering behavior and adds regression coverage.

- [ ] **Step 1: Write the failing tests**

Add to `internal/ticket/store_pg_test.go` (same file, same package `ticket_test`). This file already defines `newTestStore` (line 14), `createTicket` (line 22), and `createSubTicket` (line 42, takes a parent id and sets `ParentID` at Create time) — reuse all three rather than mutating a ticket struct after `createTicket` already called `store.Create` on it (that would insert once via the helper, then a second `store.Create` call on the same id would be a bug). To change status/due_at on an already-created ticket, go through `store.Update`: it DOES call `validateTransition` (`store_pg.go:209`), but only when `req.Status != nil`, and `pending`→`on_hold` is a valid transition once Task 1 lands — so `createTicket` (status `pending`) → `store.Update(..., Status: &onHoldStatus)` is a legal transition, not a validation bypass:

```go
func TestListOverdue_ExcludesOnHold(t *testing.T) {
	ctx, store, cleanup := newTestStore(t)
	defer cleanup()

	past := time.Now().Add(-24 * time.Hour)
	onHoldStatus := ticket.StatusOnHold

	onHold := createTicket(t, ctx, store, "on hold, past due")
	_, err := store.Update(ctx, onHold.ID, ticket.UpdateTicketReq{Status: &onHoldStatus, DueAt: &past})
	require.NoError(t, err)

	active := createTicket(t, ctx, store, "pending, past due")
	_, err = store.Update(ctx, active.ID, ticket.UpdateTicketReq{DueAt: &past})
	require.NoError(t, err)

	overdue, err := store.ListOverdue(ctx)
	require.NoError(t, err)

	var ids []uuid.UUID
	for _, tk := range overdue {
		ids = append(ids, tk.ID)
	}
	assert.NotContains(t, ids, onHold.ID, "on_hold ticket must not be returned as overdue (SLA worker would auto-fail it)")
	assert.Contains(t, ids, active.ID, "a genuinely overdue pending ticket must still be returned")
}

func TestAllSubtasksTerminal_OnHoldChildBlocksParent(t *testing.T) {
	ctx, store, cleanup := newTestStore(t)
	defer cleanup()

	parent := createTicket(t, ctx, store, "parent epic")
	child := createSubTicket(t, ctx, store, parent.ID, "child, on hold")

	onHoldStatus := ticket.StatusOnHold
	_, err := store.Update(ctx, child.ID, ticket.UpdateTicketReq{Status: &onHoldStatus})
	require.NoError(t, err)

	terminal, err := store.AllSubtasksTerminal(ctx, parent.ID)
	require.NoError(t, err)
	assert.False(t, terminal, "an on_hold child must keep the parent from being considered all-terminal")
}
```

Add `"time"` to the import block in `internal/ticket/store_pg_test.go` if not already present (check the existing import list first — `uuid`, `assert`, `require`, `ticket` are already imported per the file's current header).

- [ ] **Step 2: Run test to verify it fails (or skips)**

Run: `GOQUEST_TEST_DATABASE_URL=$GOQUEST_TEST_DATABASE_URL go test ./internal/ticket/... -run 'TestListOverdue_ExcludesOnHold|TestAllSubtasksTerminal_OnHoldChildBlocksParent' -v`

If `GOQUEST_TEST_DATABASE_URL` is unset in this environment, both tests will report `SKIP` with the message `GOQUEST_TEST_DATABASE_URL not set; skipping PG integration test` — that is expected in this sandboxed pod (see Global Constraints) and is NOT a failure to chase down. If it IS set (e.g. in CI), expect `TestListOverdue_ExcludesOnHold` to FAIL before Step 3 (the on_hold ticket would currently be included in `overdue`, since Task 1 alone doesn't touch `ListOverdue`'s SQL). `TestAllSubtasksTerminal_OnHoldChildBlocksParent` is expected to PASS immediately even before Step 3, since `on_hold` was never in that query's terminal list to begin with — this test exists purely to lock in that already-correct behavior as a regression guard, not to drive a code change.

- [ ] **Step 3: Update the `ListOverdue` SQL**

In `internal/ticket/store_pg.go`, change:

```go
func (s *pgStore) ListOverdue(ctx context.Context) ([]*Ticket, error) {
	rows, err := s.db.Query(ctx,
		ticketSelectSQL+
			`WHERE t.due_at IS NOT NULL
			   AND t.due_at < NOW()
			   AND t.status NOT IN ('completed', 'cancelled', 'failed', 'pending_approval', 'pending_revision')
			`+ticketGroupBySQL)
```

to:

```go
func (s *pgStore) ListOverdue(ctx context.Context) ([]*Ticket, error) {
	rows, err := s.db.Query(ctx,
		ticketSelectSQL+
			`WHERE t.due_at IS NOT NULL
			   AND t.due_at < NOW()
			   AND t.status NOT IN ('completed', 'cancelled', 'failed', 'pending_approval', 'pending_revision', 'on_hold')
			`+ticketGroupBySQL)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `GOQUEST_TEST_DATABASE_URL=$GOQUEST_TEST_DATABASE_URL go test ./internal/ticket/... -run 'TestListOverdue_ExcludesOnHold|TestAllSubtasksTerminal_OnHoldChildBlocksParent' -v`
Expected: PASS (or the same expected SKIP behavior described in Step 2 if the env var is unset locally).

- [ ] **Step 5: Run the full package suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS across the whole repo (or the pre-existing DB-gated SKIPs if `GOQUEST_TEST_DATABASE_URL` is unset — same as before this change, no new skips introduced by unrelated packages).

- [ ] **Step 6: Commit**

```bash
git add internal/ticket/store_pg.go internal/ticket/store_pg_test.go
git commit -m "fix(ticket): exclude on_hold from SLA auto-fail scan (goq-ticket-on-hold-status)"
```

---

## Self-Review Notes (for the plan author, not a task)

- Spec §상태 머신 (transitions in/out of on_hold) → Task 1.
- Spec §Overdue 처리 (ListOverdue exclusion) → Task 2 Step 3.
- Spec §B goquest — `AllSubtasksTerminal`은 코드 변경 불필요, 회귀 테스트만 → Task 2's `TestAllSubtasksTerminal_OnHoldChildBlocksParent` (explicitly documented as a locked-in-behavior test, not a driver for a code change).
- Spec's migration filename corrected to `023_on_hold_status.sql` (spec originally said `011`, fixed after discovering migrations already run through `022_org.sql`).
- goquest-ui and goquest-mcp stages are explicitly OUT of scope for this plan — separate plans follow this one's merge+deploy, per the spec's staged rollout.
