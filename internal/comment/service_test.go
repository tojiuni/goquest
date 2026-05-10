package comment_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/comment"
)

// fakeStore is an in-memory Store for testing.
type fakeStore struct {
	items map[uuid.UUID]*comment.Comment
}

func newFakeStore() *fakeStore {
	return &fakeStore{items: make(map[uuid.UUID]*comment.Comment)}
}

func (s *fakeStore) Create(_ context.Context, c *comment.Comment) error {
	s.items[c.ID] = c
	return nil
}

func (s *fakeStore) Get(_ context.Context, id uuid.UUID) (*comment.Comment, error) {
	c, ok := s.items[id]
	if !ok {
		return nil, comment.ErrNotFound
	}
	return c, nil
}

func (s *fakeStore) ListByTicket(_ context.Context, ticketID uuid.UUID, includeInternal bool) ([]*comment.Comment, error) {
	var out []*comment.Comment
	for _, c := range s.items {
		if c.TicketID != ticketID {
			continue
		}
		if !includeInternal && c.IsInternal {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

func (s *fakeStore) Update(_ context.Context, id uuid.UUID, body string) (*comment.Comment, error) {
	c, ok := s.items[id]
	if !ok {
		return nil, comment.ErrNotFound
	}
	c.Body = body
	return c, nil
}

func (s *fakeStore) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := s.items[id]; !ok {
		return comment.ErrNotFound
	}
	delete(s.items, id)
	return nil
}

func (s *fakeStore) Publish(_ context.Context, id uuid.UUID) error {
	c, ok := s.items[id]
	if !ok {
		return comment.ErrNotFound
	}
	c.IsDraft = false
	return nil
}

func TestService_Create_RejectsEmptyBody(t *testing.T) {
	svc := comment.NewService(newFakeStore())
	_, err := svc.Create(context.Background(), uuid.New(), "u1", comment.AuthorKindStaff,
		comment.CreateReq{Body: "  "})
	if err == nil {
		t.Fatal("expected error for empty body")
	}
}

func TestService_Create_CustomerCannotPostInternal(t *testing.T) {
	svc := comment.NewService(newFakeStore())
	_, err := svc.Create(context.Background(), uuid.New(), "cust1", comment.AuthorKindCustomer,
		comment.CreateReq{Body: "hi", IsInternal: true})
	if err == nil {
		t.Fatal("expected error: customers cannot post internal comments")
	}
}

func TestService_ListByTicket_FiltersInternal(t *testing.T) {
	store := newFakeStore()
	svc := comment.NewService(store)
	tid := uuid.New()

	if _, err := svc.Create(context.Background(), tid, "staff1", comment.AuthorKindStaff,
		comment.CreateReq{Body: "public reply"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(context.Background(), tid, "staff1", comment.AuthorKindStaff,
		comment.CreateReq{Body: "private memo", IsInternal: true}); err != nil {
		t.Fatal(err)
	}

	visible, err := svc.ListByTicket(context.Background(), tid, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(visible) != 1 {
		t.Fatalf("expected 1 customer-visible comment, got %d", len(visible))
	}
	if visible[0].IsInternal {
		t.Fatal("internal comment leaked to customer view")
	}

	all, err := svc.ListByTicket(context.Background(), tid, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 total, got %d", len(all))
	}
}

func TestService_Update_OnlyAuthor(t *testing.T) {
	store := newFakeStore()
	svc := comment.NewService(store)
	c, err := svc.Create(context.Background(), uuid.New(), "alice", comment.AuthorKindStaff,
		comment.CreateReq{Body: "hello"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.Update(context.Background(), c.ID, "bob", comment.UpdateReq{Body: "edited"})
	if !errors.Is(err, comment.ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}

	updated, err := svc.Update(context.Background(), c.ID, "alice", comment.UpdateReq{Body: "edited"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Body != "edited" {
		t.Fatalf("body not updated: %q", updated.Body)
	}
}

func TestService_Delete_OnlyAuthor(t *testing.T) {
	store := newFakeStore()
	svc := comment.NewService(store)
	c, err := svc.Create(context.Background(), uuid.New(), "alice", comment.AuthorKindStaff,
		comment.CreateReq{Body: "x"})
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.Delete(context.Background(), c.ID, "bob"); !errors.Is(err, comment.ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
	if err := svc.Delete(context.Background(), c.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(context.Background(), c.ID, "alice"); !errors.Is(err, comment.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}
