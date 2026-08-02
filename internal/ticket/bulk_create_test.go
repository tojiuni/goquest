package ticket_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/ticket"
)

// TestService_BulkCreate_InheritsParentProjectAndFields drives the happy path:
// every item should land under the parent's project, inheriting the parent's
// type/priority/domain (v1 has no per-item override, per design doc §3.1).
func TestService_BulkCreate_InheritsParentProjectAndFields(t *testing.T) {
	proj := uuid.New()
	parentID := uuid.New()
	store := &mockStore{getTicket: &ticket.Ticket{
		ID: parentID, ProjectID: &proj,
		Type: ticket.TypeFeature, Priority: ticket.PriorityHigh, Domain: ticket.DomainInfra,
	}}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	req := ticket.BulkCreateReq{
		ParentID: parentID,
		Items: []ticket.BulkCreateItem{
			{Title: "sub 1"},
			{Title: "sub 2", Description: "desc"},
		},
		Source:    ticket.BulkCreateSourceContentSplit,
		SourceRef: "doc:42",
	}
	resp, err := svc.BulkCreate(context.Background(), req, "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Created != 2 {
		t.Fatalf("Created = %d, want 2", resp.Created)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("len(Results) = %d, want 2", len(resp.Results))
	}
	for i, r := range resp.Results {
		if !r.OK || r.Error != "" || r.ID == "" {
			t.Errorf("result[%d] = %+v, want ok with an id", i, r)
		}
		if r.Index != i {
			t.Errorf("result[%d].Index = %d, want %d", i, r.Index, i)
		}
	}
	if len(store.created) != 2 {
		t.Fatalf("store.created len = %d, want 2", len(store.created))
	}
	for _, ct := range store.created {
		if ct.ProjectID == nil || *ct.ProjectID != proj {
			t.Errorf("created ticket did not inherit parent project: got %v, want %s", ct.ProjectID, proj)
		}
		if ct.Type != ticket.TypeFeature {
			t.Errorf("created ticket type = %s, want inherited %s", ct.Type, ticket.TypeFeature)
		}
		if ct.Priority != ticket.PriorityHigh {
			t.Errorf("created ticket priority = %s, want inherited %s", ct.Priority, ticket.PriorityHigh)
		}
		if ct.Domain != ticket.DomainInfra {
			t.Errorf("created ticket domain = %s, want inherited %s", ct.Domain, ticket.DomainInfra)
		}
		if ct.ParentID == nil || *ct.ParentID != parentID {
			t.Errorf("created ticket parent_id = %v, want %s", ct.ParentID, parentID)
		}
	}
}

// TestService_BulkCreate_PartialFailure asserts one item's validation failure
// (empty title) is reported in its own result row without aborting the rest —
// the same partial-failure contract BulkUpdate uses.
func TestService_BulkCreate_PartialFailure(t *testing.T) {
	proj := uuid.New()
	parentID := uuid.New()
	store := &mockStore{getTicket: &ticket.Ticket{ID: parentID, ProjectID: &proj}}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	req := ticket.BulkCreateReq{
		ParentID: parentID,
		Items: []ticket.BulkCreateItem{
			{Title: "good"},
			{Title: ""}, // empty title -> Create rejects
			{Title: "also good"},
		},
		Source: ticket.BulkCreateSourceManual,
	}
	resp, err := svc.BulkCreate(context.Background(), req, "user1")
	if err != nil {
		t.Fatalf("unexpected top-level error: %v", err)
	}
	if resp.Created != 2 {
		t.Fatalf("Created = %d, want 2", resp.Created)
	}
	if len(resp.Results) != 3 {
		t.Fatalf("len(Results) = %d, want 3", len(resp.Results))
	}
	if !resp.Results[0].OK || resp.Results[0].ID == "" {
		t.Errorf("result[0] = %+v, want ok", resp.Results[0])
	}
	if resp.Results[1].OK || resp.Results[1].Error == "" {
		t.Errorf("result[1] = %+v, want failed with an error", resp.Results[1])
	}
	if !resp.Results[2].OK || resp.Results[2].ID == "" {
		t.Errorf("result[2] = %+v, want ok", resp.Results[2])
	}
}

// TestService_BulkCreate_ParentNotFound propagates the store's ErrNotFound so
// the HTTP handler can translate it to 404.
func TestService_BulkCreate_ParentNotFound(t *testing.T) {
	store := &mockStore{getErr: ticket.ErrNotFound}
	svc := ticket.NewService(store, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	req := ticket.BulkCreateReq{
		ParentID: uuid.New(),
		Items:    []ticket.BulkCreateItem{{Title: "sub"}},
		Source:   ticket.BulkCreateSourceManual,
	}
	_, err := svc.BulkCreate(context.Background(), req, "user1")
	if !errors.Is(err, ticket.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestService_BulkCreate_InvalidSource rejects an unknown/empty source before
// touching the store — source is a required audit field (§3.1), not defaulted.
func TestService_BulkCreate_InvalidSource(t *testing.T) {
	svc := ticket.NewService(&mockStore{}, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	req := ticket.BulkCreateReq{
		ParentID: uuid.New(),
		Items:    []ticket.BulkCreateItem{{Title: "sub"}},
		Source:   "bogus",
	}
	_, err := svc.BulkCreate(context.Background(), req, "user1")
	if !errors.Is(err, ticket.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}

// TestService_BulkCreate_EmptyItems rejects a request with no items — an empty
// batch is a caller bug, not a valid "create nothing" no-op.
func TestService_BulkCreate_EmptyItems(t *testing.T) {
	svc := ticket.NewService(&mockStore{}, &mockPublisher{}, &mockNuclex{}, nil, nil, nil)

	req := ticket.BulkCreateReq{
		ParentID: uuid.New(),
		Items:    nil,
		Source:   ticket.BulkCreateSourceManual,
	}
	_, err := svc.BulkCreate(context.Background(), req, "user1")
	if !errors.Is(err, ticket.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}
