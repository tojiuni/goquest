// cmd/backfill-spicedb: one-off command to populate SpiceDB with existing
// goquest relationships. Idempotent — all writes use TOUCH semantics.
// Run once before enabling SPICEDB_ENFORCE.
package main

import (
	"context"
	"log"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tojiuni/goquest/internal/config"
	"github.com/tojiuni/goquest/internal/spicedb"
)

func main() {
	ctx := context.Background()
	cfg := config.Load()

	// config.Load() defaults SpiceDBEndpoint to localhost:50051, so guard on the
	// raw env var: backfill must target an explicitly-configured SpiceDB and never
	// silently fall back to localhost in a pod.
	if os.Getenv("SPICEDB_ENDPOINT") == "" {
		log.Fatal("SPICEDB_ENDPOINT is not set; cannot run backfill")
	}
	log.Printf("backfill connecting to SpiceDB at %s", cfg.SpiceDBEndpoint)

	// ── DB pool ──────────────────────────────────────────────────────────
	pool, err := pgxpool.New(ctx, cfg.PostgresDSN())
	if err != nil {
		log.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("postgres ping: %v", err)
	}

	// ── SpiceDB writer ───────────────────────────────────────────────────
	client, err := spicedb.NewClient(cfg.SpiceDBEndpoint, cfg.SpiceDBToken, cfg.SpiceDBInsecure)
	if err != nil {
		log.Fatalf("spicedb.NewClient: %v", err)
	}
	w := client.Writer()

	var wsMemberCount, projectCount, pjMemberCount, wsOrgCount, ticketCount, errCount int

	// ── 1. Workspace members ─────────────────────────────────────────────
	// SELECT all workspace_members rows directly; no list-all-workspaces store method.
	{
		rows, err := pool.Query(ctx, `SELECT workspace_id, user_id, role FROM workspace_members`)
		if err != nil {
			log.Fatalf("query workspace_members: %v", err)
		}
		for rows.Next() {
			var wsID uuid.UUID
			var userID, role string
			if err := rows.Scan(&wsID, &userID, &role); err != nil {
				log.Printf("ERR scan workspace_member: %v", err)
				errCount++
				continue
			}
			if err := w.WriteWorkspaceMember(ctx, wsID, userID, role); err != nil {
				log.Printf("ERR WriteWorkspaceMember ws=%s user=%s role=%s: %v", wsID, userID, role, err)
				errCount++
				continue
			}
			wsMemberCount++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			log.Fatalf("rows error workspace_members: %v", err)
		}
	}

	// ── 2. Projects → workspace links + project members ──────────────────
	// SELECT all projects for workspace links; then all project_members for member writes.
	{
		rows, err := pool.Query(ctx, `SELECT id, workspace_id FROM projects`)
		if err != nil {
			log.Fatalf("query projects: %v", err)
		}
		for rows.Next() {
			var projectID, workspaceID uuid.UUID
			if err := rows.Scan(&projectID, &workspaceID); err != nil {
				log.Printf("ERR scan project: %v", err)
				errCount++
				continue
			}
			if err := w.WriteProjectWorkspace(ctx, projectID, workspaceID); err != nil {
				log.Printf("ERR WriteProjectWorkspace project=%s ws=%s: %v", projectID, workspaceID, err)
				errCount++
				continue
			}
			projectCount++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			log.Fatalf("rows error projects: %v", err)
		}
	}
	{
		rows, err := pool.Query(ctx, `SELECT project_id, user_id, role FROM project_members`)
		if err != nil {
			log.Fatalf("query project_members: %v", err)
		}
		for rows.Next() {
			var projectID uuid.UUID
			var userID, role string
			if err := rows.Scan(&projectID, &userID, &role); err != nil {
				log.Printf("ERR scan project_member: %v", err)
				errCount++
				continue
			}
			if err := w.WriteProjectMember(ctx, projectID, userID, role); err != nil {
				log.Printf("ERR WriteProjectMember project=%s user=%s role=%s: %v", projectID, userID, role, err)
				errCount++
				continue
			}
			pjMemberCount++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			log.Fatalf("rows error project_members: %v", err)
		}
	}

	// ── 2.5 Workspace → org links ────────────────────────────────────────
	{
		rows, err := pool.Query(ctx, `SELECT id, org_id FROM workspaces WHERE org_id IS NOT NULL`)
		if err != nil {
			log.Fatalf("query workspace orgs: %v", err)
		}
		for rows.Next() {
			var wsID uuid.UUID
			var orgID string
			if err := rows.Scan(&wsID, &orgID); err != nil {
				log.Printf("ERR scan workspace org: %v", err)
				errCount++
				continue
			}
			if err := w.WriteWorkspaceOrg(ctx, wsID, orgID); err != nil {
				log.Printf("ERR WriteWorkspaceOrg ws=%s org=%s: %v", wsID, orgID, err)
				errCount++
				continue
			}
			wsOrgCount++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			log.Fatalf("rows error workspace orgs: %v", err)
		}
	}

	// ── 3. Tickets with a non-null project_id ────────────────────────────
	// assignee_id is TEXT NOT NULL DEFAULT '' — pass "" when empty; writer handles it.
	// requester_id is TEXT NOT NULL.
	{
		rows, err := pool.Query(ctx, `
			SELECT id, project_id, requester_id, assignee_id
			FROM tickets
			WHERE project_id IS NOT NULL`)
		if err != nil {
			log.Fatalf("query tickets: %v", err)
		}
		for rows.Next() {
			var ticketID, projectID uuid.UUID
			var requesterID, assigneeID string
			if err := rows.Scan(&ticketID, &projectID, &requesterID, &assigneeID); err != nil {
				log.Printf("ERR scan ticket: %v", err)
				errCount++
				continue
			}
			if err := w.WriteTicket(ctx, ticketID, projectID, requesterID, assigneeID); err != nil {
				log.Printf("ERR WriteTicket ticket=%s project=%s: %v", ticketID, projectID, err)
				errCount++
				continue
			}
			ticketCount++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			log.Fatalf("rows error tickets: %v", err)
		}
	}

	log.Printf("backfill complete: ws_members=%d projects=%d pj_members=%d ws_orgs=%d tickets=%d errors=%d",
		wsMemberCount, projectCount, pjMemberCount, wsOrgCount, ticketCount, errCount)
}
