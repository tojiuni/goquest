package ticket_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tojiuni/goquest/internal/ticket"
)

// newTestStore returns (ctx, store, cleanup) using the shared setupTestDB helper
// defined in refs_store_test.go (same package ticket_test).
func newTestStore(t *testing.T) (context.Context, ticket.Store, func()) {
	t.Helper()
	_, store := setupTestDB(t)
	ctx := context.Background()
	cleanup := func() {} // pool cleanup registered via t.Cleanup inside setupTestDB
	return ctx, store, cleanup
}

// createTicket creates a minimal ticket with the given title and returns it.
func createTicket(t *testing.T, ctx context.Context, store ticket.Store, title string) *ticket.Ticket {
	t.Helper()
	tk := &ticket.Ticket{
		ID:           uuid.New(),
		Title:        title,
		Description:  "",
		Type:         ticket.TypeTask,
		Status:       ticket.StatusPending,
		Priority:     ticket.PriorityNormal,
		AssigneeType: ticket.AssigneeHuman,
		RequesterID:  "test@example.com",
		Visibility:   ticket.VisibilityProject,
	}
	require.NoError(t, store.Create(ctx, tk))
	return tk
}

// createSubTicket creates a ticket whose parent is parentID.
func createSubTicket(t *testing.T, ctx context.Context, store ticket.Store, parentID uuid.UUID, title string) *ticket.Ticket {
	t.Helper()
	tk := &ticket.Ticket{
		ID:           uuid.New(),
		Title:        title,
		Description:  "",
		Type:         ticket.TypeTask,
		Status:       ticket.StatusPending,
		Priority:     ticket.PriorityNormal,
		AssigneeType: ticket.AssigneeHuman,
		RequesterID:  "test@example.com",
		Visibility:   ticket.VisibilityProject,
		ParentID:     &parentID,
	}
	require.NoError(t, store.Create(ctx, tk))
	return tk
}

// TestGetByProjectAndTitleName_NotFound exercises the new lookup's miss path
// against the real store (no project FK needed). The positive replay path is
// covered hermetically at the handler level
// (handler.TestCreate_DuplicateTitleNameReplays).
func TestGetByProjectAndTitleName_NotFound(t *testing.T) {
	ctx, store, cleanup := newTestStore(t)
	defer cleanup()

	_, err := store.GetByProjectAndTitleName(ctx, uuid.New(), "goq-no-such-name")
	require.ErrorIs(t, err, ticket.ErrNotFound)
}

func TestStoreGet_PopulatesParentTitle(t *testing.T) {
	ctx, store, cleanup := newTestStore(t)
	defer cleanup()

	parent := createTicket(t, ctx, store, "parent-title")
	sub := createSubTicket(t, ctx, store, parent.ID, "sub-title")

	got, err := store.Get(ctx, sub.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "parent-title", got.ParentTitle)
	assert.Equal(t, parent.ID, *got.ParentID)
}

func TestStoreGet_ParentTitleEmptyWhenNoParent(t *testing.T) {
	ctx, store, cleanup := newTestStore(t)
	defer cleanup()
	root := createTicket(t, ctx, store, "root")
	got, err := store.Get(ctx, root.ID)
	require.NoError(t, err)
	assert.Equal(t, "", got.ParentTitle)
}
