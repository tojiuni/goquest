package ticket_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tojiuni/goquest/internal/ticket"
)

func TestUpdate_Reparent(t *testing.T) {
	pool, store := setupTestDB(t)
	ctx := context.Background()

	ws := insertWorkspace(t, ctx, pool)
	proj := insertProject(t, ctx, pool, ws)
	otherProj := insertProject(t, ctx, pool, ws)

	t.Run("moves ticket under a new parent in the same project", func(t *testing.T) {
		parentA := createTicketInProject(t, ctx, store, &proj, "")
		parentB := createTicketInProject(t, ctx, store, &proj, "")
		child := createTicketInProject(t, ctx, store, &proj, "")
		_, err := store.Update(ctx, child.ID, ticket.UpdateTicketReq{ParentID: &parentA.ID})
		require.NoError(t, err)

		got, err := store.Update(ctx, child.ID, ticket.UpdateTicketReq{ParentID: &parentB.ID})
		require.NoError(t, err)
		require.NotNil(t, got.ParentID)
		assert.Equal(t, parentB.ID, *got.ParentID)
	})

	t.Run("adopts a parent-less ticket into a parent", func(t *testing.T) {
		parent := createTicketInProject(t, ctx, store, &proj, "")
		child := createTicketInProject(t, ctx, store, &proj, "")
		got, err := store.Update(ctx, child.ID, ticket.UpdateTicketReq{ParentID: &parent.ID})
		require.NoError(t, err)
		require.NotNil(t, got.ParentID)
		assert.Equal(t, parent.ID, *got.ParentID)
	})

	t.Run("reassigning to the current parent is a no-op success", func(t *testing.T) {
		parent := createTicketInProject(t, ctx, store, &proj, "")
		child := createTicketInProject(t, ctx, store, &proj, "")
		_, err := store.Update(ctx, child.ID, ticket.UpdateTicketReq{ParentID: &parent.ID})
		require.NoError(t, err)

		got, err := store.Update(ctx, child.ID, ticket.UpdateTicketReq{ParentID: &parent.ID})
		require.NoError(t, err)
		assert.Equal(t, parent.ID, *got.ParentID)
	})

	t.Run("rejects self-parenting", func(t *testing.T) {
		tk := createTicketInProject(t, ctx, store, &proj, "")
		_, err := store.Update(ctx, tk.ID, ticket.UpdateTicketReq{ParentID: &tk.ID})
		assert.ErrorIs(t, err, ticket.ErrParentCycle)
	})

	t.Run("rejects an unknown target parent", func(t *testing.T) {
		tk := createTicketInProject(t, ctx, store, &proj, "")
		missing := uuid.New()
		_, err := store.Update(ctx, tk.ID, ticket.UpdateTicketReq{ParentID: &missing})
		assert.ErrorIs(t, err, ticket.ErrParentNotFound)
	})

	t.Run("rejects a target parent in a different project", func(t *testing.T) {
		tk := createTicketInProject(t, ctx, store, &proj, "")
		otherParent := createTicketInProject(t, ctx, store, &otherProj, "")
		_, err := store.Update(ctx, tk.ID, ticket.UpdateTicketReq{ParentID: &otherParent.ID})
		assert.ErrorIs(t, err, ticket.ErrCrossProjectParent)
	})

	t.Run("rejects making a direct child into its own parent's parent", func(t *testing.T) {
		grandparent := createTicketInProject(t, ctx, store, &proj, "")
		parent := createTicketInProject(t, ctx, store, &proj, "")
		_, err := store.Update(ctx, parent.ID, ticket.UpdateTicketReq{ParentID: &grandparent.ID})
		require.NoError(t, err)

		// grandparent -> parent; now try to reparent grandparent under parent (immediate cycle).
		_, err = store.Update(ctx, grandparent.ID, ticket.UpdateTicketReq{ParentID: &parent.ID})
		assert.ErrorIs(t, err, ticket.ErrParentCycle)
	})

	t.Run("rejects a deep cycle through a grandchild", func(t *testing.T) {
		a := createTicketInProject(t, ctx, store, &proj, "")
		b := createTicketInProject(t, ctx, store, &proj, "")
		c := createTicketInProject(t, ctx, store, &proj, "")

		_, err := store.Update(ctx, b.ID, ticket.UpdateTicketReq{ParentID: &a.ID}) // a -> b
		require.NoError(t, err)
		_, err = store.Update(ctx, c.ID, ticket.UpdateTicketReq{ParentID: &b.ID}) // b -> c
		require.NoError(t, err)

		// a is c's grandparent; reparenting a under c would create a cycle.
		_, err = store.Update(ctx, a.ID, ticket.UpdateTicketReq{ParentID: &c.ID})
		assert.ErrorIs(t, err, ticket.ErrParentCycle)
	})

	t.Run("combined project_id+parent_id move validates against the DESTINATION project", func(t *testing.T) {
		parentInOtherProj := createTicketInProject(t, ctx, store, &otherProj, "")
		tk := createTicketInProject(t, ctx, store, &proj, "")

		// Moving to otherProj and parenting under a ticket already in otherProj
		// must succeed — the cross-project check must compare against the
		// ticket's post-move project, not its pre-move project.
		got, err := store.Update(ctx, tk.ID, ticket.UpdateTicketReq{
			ProjectID: &otherProj,
			ParentID:  &parentInOtherProj.ID,
		})
		require.NoError(t, err)
		require.NotNil(t, got.ProjectID)
		assert.Equal(t, otherProj, *got.ProjectID)
		require.NotNil(t, got.ParentID)
		assert.Equal(t, parentInOtherProj.ID, *got.ParentID)
	})

	t.Run("combined project_id+parent_id move rejects a parent left behind in the source project", func(t *testing.T) {
		parentInSourceProj := createTicketInProject(t, ctx, store, &proj, "")
		tk := createTicketInProject(t, ctx, store, &proj, "")

		// Moving to otherProj while parenting under a ticket that stays in the
		// original project must be rejected — after the move, ticket and parent
		// would be in different projects.
		_, err := store.Update(ctx, tk.ID, ticket.UpdateTicketReq{
			ProjectID: &otherProj,
			ParentID:  &parentInSourceProj.ID,
		})
		assert.ErrorIs(t, err, ticket.ErrCrossProjectParent)
	})
}
