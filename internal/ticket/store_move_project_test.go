package ticket_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tojiuni/goquest/internal/ticket"
)

// insertWorkspace / insertProject create minimal fixture rows directly — the
// ticket store tests must not depend on the workspace/project packages.
func insertWorkspace(t *testing.T, ctx context.Context, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	now := time.Now().UTC()
	_, err := pool.Exec(ctx, `
		INSERT INTO workspaces (id, name, slug, description, type, owner_id, created_at, updated_at)
		VALUES ($1,$2,$3,'','general',$4,$5,$5)`,
		id, "ws-"+id.String()[:8], "ws-"+id.String()[:8], "test@example.com", now)
	require.NoError(t, err)
	return id
}

func insertProject(t *testing.T, ctx context.Context, pool *pgxpool.Pool, workspaceID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	now := time.Now().UTC()
	_, err := pool.Exec(ctx, `
		INSERT INTO projects (id, workspace_id, name, description, status, reward_xp, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,'','active',0,$4,$5,$5)`,
		id, workspaceID, "proj-"+id.String()[:8], "test@example.com", now)
	require.NoError(t, err)
	return id
}

func createTicketInProject(t *testing.T, ctx context.Context, store ticket.Store, projectID *uuid.UUID, titleName string) *ticket.Ticket {
	t.Helper()
	tk := &ticket.Ticket{
		ID:           uuid.New(),
		Title:        "move test " + titleName,
		Type:         ticket.TypeTask,
		Status:       ticket.StatusPending,
		Priority:     ticket.PriorityNormal,
		AssigneeType: ticket.AssigneeHuman,
		RequesterID:  "test@example.com",
		Visibility:   ticket.VisibilityProject,
		ProjectID:    projectID,
	}
	if titleName != "" {
		tk.TitleName = titleName
	}
	require.NoError(t, store.Create(ctx, tk))
	return tk
}

func TestUpdate_MoveProject(t *testing.T) {
	pool, store := setupTestDB(t)
	ctx := context.Background()

	ws := insertWorkspace(t, ctx, pool)
	src := insertProject(t, ctx, pool, ws)
	dst := insertProject(t, ctx, pool, ws)

	t.Run("moves ticket to another project in the same workspace", func(t *testing.T) {
		tk := createTicketInProject(t, ctx, store, &src, "")
		got, err := store.Update(ctx, tk.ID, ticket.UpdateTicketReq{ProjectID: &dst})
		require.NoError(t, err)
		require.NotNil(t, got.ProjectID)
		assert.Equal(t, dst, *got.ProjectID)
	})

	t.Run("moving to the current project is a no-op success", func(t *testing.T) {
		tk := createTicketInProject(t, ctx, store, &src, "")
		got, err := store.Update(ctx, tk.ID, ticket.UpdateTicketReq{ProjectID: &src})
		require.NoError(t, err)
		assert.Equal(t, src, *got.ProjectID)
	})

	t.Run("adopts a floating ticket into a project", func(t *testing.T) {
		tk := createTicketInProject(t, ctx, store, nil, "")
		got, err := store.Update(ctx, tk.ID, ticket.UpdateTicketReq{ProjectID: &dst})
		require.NoError(t, err)
		require.NotNil(t, got.ProjectID)
		assert.Equal(t, dst, *got.ProjectID)
	})

	t.Run("rejects a cross-workspace move", func(t *testing.T) {
		otherWS := insertWorkspace(t, ctx, pool)
		otherProj := insertProject(t, ctx, pool, otherWS)
		tk := createTicketInProject(t, ctx, store, &src, "")
		_, err := store.Update(ctx, tk.ID, ticket.UpdateTicketReq{ProjectID: &otherProj})
		assert.ErrorIs(t, err, ticket.ErrCrossWorkspaceMove)
	})

	t.Run("rejects an unknown target project", func(t *testing.T) {
		tk := createTicketInProject(t, ctx, store, &src, "")
		missing := uuid.New()
		_, err := store.Update(ctx, tk.ID, ticket.UpdateTicketReq{ProjectID: &missing})
		assert.ErrorIs(t, err, ticket.ErrProjectNotFound)
	})

	t.Run("rejects a move that collides on title_name in the target project", func(t *testing.T) {
		name := "goq-move-collision-" + uuid.New().String()[:8]
		createTicketInProject(t, ctx, store, &dst, name)
		tk := createTicketInProject(t, ctx, store, &src, name)
		_, err := store.Update(ctx, tk.ID, ticket.UpdateTicketReq{ProjectID: &dst})
		assert.ErrorIs(t, err, ticket.ErrDuplicateTitleName)
	})
}
