package ticket_test

import (
	"context"
	"testing"
	"time"

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

// TestAggregate_GroupByStatus exercises the real GROUP BY against the store:
// counts per status within a unique requester scope (robust to shared-DB rows),
// and that an unwhitelisted group_by is rejected with ErrInvalidGroupBy.
func TestAggregate_GroupByStatus(t *testing.T) {
	ctx, store, cleanup := newTestStore(t)
	defer cleanup()

	requester := "agg-" + uuid.NewString() // unique scope isolates this test's rows
	mk := func(status ticket.Status) {
		tk := &ticket.Ticket{
			ID: uuid.New(), Title: "agg " + string(status),
			Type: ticket.TypeTask, Status: status, Priority: ticket.PriorityNormal,
			AssigneeType: ticket.AssigneeHuman, RequesterID: requester,
			Visibility: ticket.VisibilityProject,
		}
		require.NoError(t, store.Create(ctx, tk))
	}
	mk(ticket.StatusPending)
	mk(ticket.StatusPending)
	mk(ticket.StatusCompleted)

	buckets, err := store.Aggregate(ctx, "status", ticket.ListTicketsFilter{RequesterID: &requester})
	require.NoError(t, err)
	got := map[string]int{}
	total := 0
	for _, b := range buckets {
		got[b.Key] = b.Count
		total += b.Count
	}
	require.Equal(t, 3, total)
	assert.Equal(t, 2, got["pending"])
	assert.Equal(t, 1, got["completed"])

	_, err = store.Aggregate(ctx, "title; DROP TABLE tickets", ticket.ListTicketsFilter{RequesterID: &requester})
	assert.ErrorIs(t, err, ticket.ErrInvalidGroupBy)
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

// TestCreateAndUpdate_StartAt exercises start_at end-to-end: nil on create by
// default, settable on create, and independently updatable via PATCH without
// disturbing due_at (goq-roadmap-backend-support, GQT-SPC-2026080703 §1).
func TestCreateAndUpdate_StartAt(t *testing.T) {
	ctx, store, cleanup := newTestStore(t)
	defer cleanup()

	// Test: StartAt is nil by default when not set on create.
	tk := createTicket(t, ctx, store, "start_at default nil")
	if tk.StartAt != nil {
		t.Fatalf("want nil StartAt by default, got %v", tk.StartAt)
	}

	// Test: StartAt is settable on create via store.Create (INSERT path).
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	tkWithStart := &ticket.Ticket{
		ID:           uuid.New(),
		Title:        "ticket with start_at on create",
		Description:  "",
		Type:         ticket.TypeTask,
		Status:       ticket.StatusPending,
		Priority:     ticket.PriorityNormal,
		AssigneeType: ticket.AssigneeHuman,
		RequesterID:  "test@example.com",
		Visibility:   ticket.VisibilityProject,
		StartAt:      &start,
	}
	require.NoError(t, store.Create(ctx, tkWithStart))

	// Verify round-trip: Get the ticket and assert StartAt matches.
	retrieved, err := store.Get(ctx, tkWithStart.ID)
	require.NoError(t, err)
	require.NotNil(t, retrieved.StartAt)
	assert.True(t, retrieved.StartAt.Equal(start), "StartAt must round-trip via CREATE+GET")

	// Test: Update both StartAt and DueAt together.
	due := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	updated, err := store.Update(ctx, tk.ID, ticket.UpdateTicketReq{
		StartAt: &start,
		DueAt:   &due,
	})
	require.NoError(t, err)
	require.NotNil(t, updated.StartAt)
	assert.True(t, updated.StartAt.Equal(start))
	require.NotNil(t, updated.DueAt)
	assert.True(t, updated.DueAt.Equal(due))

	// Test: Updating only DueAt must not clobber the previously set StartAt.
	due2 := due.Add(24 * time.Hour)
	updated2, err := store.Update(ctx, tk.ID, ticket.UpdateTicketReq{DueAt: &due2})
	require.NoError(t, err)
	require.NotNil(t, updated2.StartAt)
	assert.True(t, updated2.StartAt.Equal(start))
}

// TestList_FilterByScheduled exercises the `scheduled=true` list filter
// against the real store: it must include tickets with either start_at or
// due_at (or both) set, and exclude tickets with neither set. Asserts
// membership by ID rather than exact result-set size, since the shared test
// DB may contain other rows from other tests.
func TestList_FilterByScheduled(t *testing.T) {
	ctx, store, cleanup := newTestStore(t)
	defer cleanup()

	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	due := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)

	withStartOnly := &ticket.Ticket{
		ID:           uuid.New(),
		Title:        "scheduled: start_at only",
		Type:         ticket.TypeTask,
		Status:       ticket.StatusPending,
		Priority:     ticket.PriorityNormal,
		AssigneeType: ticket.AssigneeHuman,
		RequesterID:  "test@example.com",
		Visibility:   ticket.VisibilityProject,
		StartAt:      &start,
	}
	require.NoError(t, store.Create(ctx, withStartOnly))

	withDueOnly := &ticket.Ticket{
		ID:           uuid.New(),
		Title:        "scheduled: due_at only",
		Type:         ticket.TypeTask,
		Status:       ticket.StatusPending,
		Priority:     ticket.PriorityNormal,
		AssigneeType: ticket.AssigneeHuman,
		RequesterID:  "test@example.com",
		Visibility:   ticket.VisibilityProject,
		DueAt:        &due,
	}
	require.NoError(t, store.Create(ctx, withDueOnly))

	withNeither := createTicket(t, ctx, store, "not scheduled")

	got, _, err := store.List(ctx, ticket.ListTicketsFilter{Scheduled: true})
	require.NoError(t, err)

	ids := map[uuid.UUID]bool{}
	for _, tk := range got {
		ids[tk.ID] = true
	}
	assert.True(t, ids[withStartOnly.ID], "ticket with only start_at set must be included")
	assert.True(t, ids[withDueOnly.ID], "ticket with only due_at set must be included")
	assert.False(t, ids[withNeither.ID], "ticket with neither start_at nor due_at set must be excluded")
}
