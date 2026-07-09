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

// TestList_FilterByTitleName exercises the new title_name list filter against
// the real store. Uses a unique title_name so the assertion is robust to other
// rows the shared test DB may contain. (Tag-id filtering is covered by the
// handler resolve path; its store clause mirrors this one.)
func TestList_FilterByTitleName(t *testing.T) {
	ctx, store, cleanup := newTestStore(t)
	defer cleanup()

	mk := func(name string) *ticket.Ticket {
		tk := &ticket.Ticket{
			ID:           uuid.New(),
			Title:        name,
			TitleName:    name,
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
	a := mk("goq-list-filter-a")
	mk("goq-list-filter-b")

	name := "goq-list-filter-a"
	got, total, err := store.List(ctx, ticket.ListTicketsFilter{TitleName: &name})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, got, 1)
	assert.Equal(t, a.ID, got[0].ID)
}

// TestList_FilterByQuery exercises the free-text `q` filter against the real
// store: case-insensitive substring match across title and description, and AND
// composition with another filter. Uses a unique marker so assertions are robust
// to other rows in the shared test DB.
func TestList_FilterByQuery(t *testing.T) {
	ctx, store, cleanup := newTestStore(t)
	defer cleanup()

	marker := "zqx-" + uuid.NewString()[:8] // unique token, appears in no other row
	mk := func(title, desc string, status ticket.Status) *ticket.Ticket {
		tk := &ticket.Ticket{
			ID:           uuid.New(),
			Title:        title,
			Description:  desc,
			Type:         ticket.TypeTask,
			Status:       status,
			Priority:     ticket.PriorityNormal,
			AssigneeType: ticket.AssigneeHuman,
			RequesterID:  "test@example.com",
			Visibility:   ticket.VisibilityProject,
		}
		require.NoError(t, store.Create(ctx, tk))
		return tk
	}
	inTitle := mk("search hit "+marker, "", ticket.StatusPending)
	inDesc := mk("no token in title", "body mentions "+marker+" here", ticket.StatusAssigned)
	mk("unrelated ticket", "unrelated body", ticket.StatusPending)

	// Case-insensitive substring across title + description → both marker rows.
	q := "ZQX-" + marker[4:] // uppercase prefix proves case-insensitivity
	got, total, err := store.List(ctx, ticket.ListTicketsFilter{Query: &q})
	require.NoError(t, err)
	require.Equal(t, 2, total)
	ids := map[uuid.UUID]bool{got[0].ID: true, got[1].ID: true}
	assert.True(t, ids[inTitle.ID] && ids[inDesc.ID])

	// AND composition: q + status filter narrows to the single pending marker row.
	pending := ticket.StatusPending
	got, total, err = store.List(ctx, ticket.ListTicketsFilter{Query: &marker, Status: &pending})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, got, 1)
	assert.Equal(t, inTitle.ID, got[0].ID)

	// LIKE metacharacters are escaped → matched literally. A "_" in the term must
	// not act as a single-char wildcard: searching "lit_<rand>" matches only the
	// row containing that literal, not a "litZ<rand>" decoy.
	rand8 := uuid.NewString()[:8]
	underscoreTerm := "lit_" + rand8
	litHit := mk("x "+underscoreTerm+" y", "", ticket.StatusPending)
	mk("x litZ"+rand8+" y", "", ticket.StatusPending) // matches only if "_" is treated as a wildcard
	got, total, err = store.List(ctx, ticket.ListTicketsFilter{Query: &underscoreTerm})
	require.NoError(t, err)
	require.Equal(t, 1, total, "underscore must be escaped to a literal, not a wildcard")
	require.Len(t, got, 1)
	assert.Equal(t, litHit.ID, got[0].ID)
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
