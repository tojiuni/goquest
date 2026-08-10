// cmd/backfill-calendar-sync: one-off command to (1) fill start_at/due_at
// for existing tickets missing either, and (2) push tickets that were
// missing due_at to tojihub's calendar, now that
// goquest-sync-tickets-without-due-at removed the due_at requirement from
// live calendar sync (which only fires going forward, on Create/Update/
// Reopen — it never touches tickets that aren't otherwise being written).
//
// Idempotent — the DB backfill only ever sets currently-NULL columns (a
// re-run matches zero rows), and tojihub's UpsertEvent is an
// ON CONFLICT-keyed upsert on object_id, so a repeated calendar sync
// updates the same event rather than duplicating it.
package main

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tojiuni/goquest/internal/calendarsync/tojihub"
	"github.com/tojiuni/goquest/internal/config"
	"github.com/tojiuni/goquest/internal/project"
	"github.com/tojiuni/goquest/internal/ticket"
	"github.com/tojiuni/goquest/internal/workspace"
)

const syncTimeout = 15 * time.Second

func main() {
	ctx := context.Background()
	cfg := config.Load()

	pool, err := pgxpool.New(ctx, cfg.PostgresDSN())
	if err != nil {
		log.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("postgres ping: %v", err)
	}

	// Validate the calendar-sync (step 2) config BEFORE running the DB
	// backfill (step 1) below. The DB backfill (start_at/due_at) is
	// independent of tojihub and always safe to run, so it's unconditional —
	// but if TOJIHUB_SERVICE_TOKEN is missing while TOJIHUB_CONNECT_URL is
	// set, that's a misconfiguration we want to catch before mutating
	// anything: step 1b's UPDATE ... RETURNING id captures the exact set of
	// tickets that need a calendar sync, and that set only exists once
	// (due_at goes from NULL to non-NULL). Fatal-ing after the DB backfill
	// already ran would leave those tickets backfilled but never synced,
	// with no way for a corrected re-run to find them again (RETURNING id
	// matches zero rows the second time). An empty TOJIHUB_CONNECT_URL is
	// not a misconfiguration — it just means calendar sync isn't deployed
	// here — so the DB backfill still runs unconditionally and only the
	// calendar-sync portion (step 2) is skipped for that case.
	syncCalendar := cfg.TojihubConnectURL != ""
	if syncCalendar && cfg.TojihubServiceToken == "" {
		log.Fatalf("TOJIHUB_CONNECT_URL set but TOJIHUB_SERVICE_TOKEN empty — cannot run calendar sync")
	}

	// ── 1a. Fill start_at for tickets missing it ──────────────────────────
	startAtTag, err := pool.Exec(ctx, `UPDATE tickets SET start_at = created_at WHERE start_at IS NULL`)
	if err != nil {
		log.Fatalf("backfill start_at: %v", err)
	}

	// ── 1b. Fill due_at for tickets missing it, capturing exactly which
	// ones needed it. Sets due_at = start_at + 12 hours, matching live
	// ticket.Service.Create default. This exact id set is what step 2 syncs
	// to tojihub. Must run after 1a: a ticket whose start_at was just filled
	// above needs that same value here.
	dueAtRows, err := pool.Query(ctx, `UPDATE tickets SET due_at = start_at + INTERVAL '12 hours' WHERE due_at IS NULL RETURNING id`)
	if err != nil {
		log.Fatalf("backfill due_at: %v", err)
	}
	var backfilledIDs []uuid.UUID
	for dueAtRows.Next() {
		var id uuid.UUID
		if err := dueAtRows.Scan(&id); err != nil {
			log.Fatalf("scan backfilled ticket id: %v", err)
		}
		backfilledIDs = append(backfilledIDs, id)
	}
	dueAtRows.Close()
	if err := dueAtRows.Err(); err != nil {
		log.Fatalf("rows error due_at backfill: %v", err)
	}

	log.Printf("backfilled start_at for %d ticket(s), due_at for %d ticket(s)",
		startAtTag.RowsAffected(), len(backfilledIDs))

	// ── 2. Push the tickets from step 1b to tojihub, skipping terminal
	// ones (they don't need a calendar entry — mirrors ticket.Service's
	// live Update path, which deletes rather than upserts for terminal
	// tickets) ─────────────────────────────────────────────────────────
	if !syncCalendar {
		log.Printf("TOJIHUB_CONNECT_URL not set — skipping calendar sync (DB backfill above already ran)")
		return
	}
	if len(backfilledIDs) == 0 {
		log.Printf("no tickets needed a due_at backfill — nothing to sync")
		return
	}

	store := ticket.NewPGStore(pool)
	// pub/nuclex/xp/webhook/magic are all nil: this binary only ever calls
	// store.Get directly and svc.SyncCalendarEventNow, neither of which
	// touches any of them (unlike Create/Update, SyncCalendarEventNow
	// doesn't publish, emit, award XP, or fire webhooks).
	svc := ticket.NewService(store, nil, nil, nil, nil, nil)
	svc.SetCalendarClient(tojihub.New(cfg.TojihubConnectURL, cfg.TojihubServiceToken))
	svc.SetProjectNameResolver(projectNameResolver{
		projects:   project.NewPGStore(pool),
		workspaces: workspace.NewPGStore(pool),
	})

	var syncedCount, skippedTerminal, errCount int
	for _, id := range backfilledIDs {
		t, err := store.Get(ctx, id)
		if err != nil {
			log.Printf("ERR get ticket %s: %v", id, err)
			errCount++
			continue
		}
		if t.Status.IsTerminal() {
			skippedTerminal++
			continue
		}
		syncCtx, cancel := context.WithTimeout(ctx, syncTimeout)
		err = svc.SyncCalendarEventNow(syncCtx, t)
		cancel()
		if err != nil {
			log.Printf("ERR sync calendar for ticket %s: %v", id, err)
			errCount++
			continue
		}
		syncedCount++
	}

	log.Printf("calendar backfill complete: synced=%d skipped_terminal=%d errors=%d",
		syncedCount, skippedTerminal, errCount)
}

// projectNameResolver mirrors cmd/service/main.go's adapter of the same
// name (project.Store + workspace.Store -> ticket.ProjectNameResolver) —
// duplicated here rather than shared, matching this repo's existing
// convention of each cmd/ binary owning its own small wiring-layer
// adapters (see cmd/backfill-spicedb, which doesn't import from
// cmd/service either).
type projectNameResolver struct {
	projects   project.Store
	workspaces workspace.Store
}

func (r projectNameResolver) ProjectName(ctx context.Context, id uuid.UUID) (workspaceName, projectName string, err error) {
	p, err := r.projects.Get(ctx, id)
	if err != nil {
		return "", "", err
	}
	ws, err := r.workspaces.Get(ctx, p.WorkspaceID)
	if err != nil {
		return "", p.Name, nil
	}
	return ws.Name, p.Name, nil
}
