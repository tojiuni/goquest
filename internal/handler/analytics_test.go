package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/handler"
	"github.com/tojiuni/goquest/internal/spicedb"
	"github.com/tojiuni/goquest/internal/ticket"
)

// aggStore makes memStore.Aggregate functional (the base stub returns nil,nil):
// it groups the in-memory tickets by a small whitelist, honoring the status +
// visibility filter, so the analytics handler is exercised end to end. Unknown
// keys return ErrInvalidGroupBy, mirroring the production store contract.
// lastFilter records the exact ListTicketsFilter the handler forwarded, so
// tests can assert OrgScope vs VisibleFilter wiring without a DB.
type aggStore struct {
	*memStore
	lastFilter ticket.ListTicketsFilter
}

var aggTestKey = map[string]func(*ticket.Ticket) string{
	"status":   func(t *ticket.Ticket) string { return string(t.Status) },
	"priority": func(t *ticket.Ticket) string { return string(t.Priority) },
}

func (s *aggStore) Aggregate(_ context.Context, groupBy string, f ticket.ListTicketsFilter) ([]ticket.AggBucket, error) {
	s.lastFilter = f
	keyOf, ok := aggTestKey[groupBy]
	if !ok {
		return nil, ticket.ErrInvalidGroupBy
	}
	counts := map[string]int{}
	for _, id := range s.order {
		t := s.byID[id]
		if f.Status != nil && t.Status != *f.Status {
			continue
		}
		if f.VisibleFilter != nil {
			vis := t.Visibility
			okv := (vis == ticket.VisibilityProject && contains(f.VisibleFilter.Project, t.ID)) ||
				(vis == ticket.VisibilityRestricted && contains(f.VisibleFilter.Restricted, t.ID))
			if !okv {
				continue
			}
		}
		counts[keyOf(t)]++
	}
	out := make([]ticket.AggBucket, 0, len(counts))
	for k, n := range counts {
		out = append(out, ticket.AggBucket{Key: k, Count: n})
	}
	return out, nil
}

func TestTicketAnalytics_GroupByAndValidation(t *testing.T) {
	mk := func(status ticket.Status) *ticket.Ticket {
		return &ticket.Ticket{
			ID: uuid.New(), Status: status,
			Priority: ticket.PriorityNormal, Visibility: ticket.VisibilityProject,
		}
	}
	store := &aggStore{memStore: newMemStore(mk(ticket.StatusPending), mk(ticket.StatusPending), mk(ticket.StatusCompleted))}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	// NoopChecker → unrestricted visibility → every ticket is counted.
	h := handler.NewTicketHandler(svc, &spicedb.NoopChecker{}, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil)
	r := chi.NewRouter()
	r.Get("/analytics/tickets", h.TicketAnalytics)

	do := func(query string) *httptest.ResponseRecorder {
		req := asUser(httptest.NewRequest(http.MethodGet, "/analytics/tickets"+query, nil), "alice")
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		return rr
	}

	// group_by=status → buckets pending:2, completed:1, total 3.
	rr := do("?group_by=status")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		GroupBy string             `json:"group_by"`
		Total   int                `json:"total"`
		Buckets []ticket.AggBucket `json:"buckets"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.GroupBy != "status" || resp.Total != 3 {
		t.Errorf("group_by=%q total=%d, want status/3", resp.GroupBy, resp.Total)
	}
	byKey := map[string]int{}
	for _, b := range resp.Buckets {
		byKey[b.Key] = b.Count
	}
	if byKey["pending"] != 2 || byKey["completed"] != 1 {
		t.Errorf("buckets = %+v, want pending:2 completed:1", byKey)
	}

	// missing group_by → 400
	if code := do("").Code; code != http.StatusBadRequest {
		t.Errorf("missing group_by: status = %d, want 400", code)
	}
	// unknown group_by → 400 (ErrInvalidGroupBy mapped)
	if code := do("?group_by=bogus").Code; code != http.StatusBadRequest {
		t.Errorf("unknown group_by: status = %d, want 400", code)
	}
	// invalid project_id → 400
	if code := do("?group_by=status&project_id=not-a-uuid").Code; code != http.StatusBadRequest {
		t.Errorf("bad project_id: status = %d, want 400", code)
	}
}

// TestTicketAnalytics_OrgCaller_ScopesByOrgNotSpiceDB proves the cross-tenant
// leak fix: an external-org caller's analytics query must be scoped by
// ListTicketsFilter.OrgScope (derived from the JWT org claim), never by the
// SpiceDB VisibleFilter path, which would aggregate over every org's tickets.
func TestTicketAnalytics_OrgCaller_ScopesByOrgNotSpiceDB(t *testing.T) {
	store := &aggStore{memStore: newMemStore()}
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	h := handler.NewTicketHandler(svc, &spicedb.NoopChecker{}, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil)
	r := chi.NewRouter()
	r.Get("/analytics/tickets", h.TicketAnalytics)

	orgID := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/analytics/tickets?group_by=status", nil)
	req = asUser(req, "member@corp.com")
	req = req.WithContext(auth.WithOrgCtx(req.Context(), &auth.OrgCtx{ID: orgID, Role: "member"}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if store.lastFilter.OrgScope == nil {
		t.Fatal("filter.OrgScope is nil, want it set from the caller's JWT org claim")
	}
	if store.lastFilter.OrgScope.OrgID != orgID {
		t.Errorf("OrgScope.OrgID = %v, want %v", store.lastFilter.OrgScope.OrgID, orgID)
	}
	if store.lastFilter.OrgScope.Role != "member" {
		t.Errorf("OrgScope.Role = %q, want member", store.lastFilter.OrgScope.Role)
	}
	if store.lastFilter.OrgScope.CallerID != "member@corp.com" {
		t.Errorf("OrgScope.CallerID = %q, want member@corp.com", store.lastFilter.OrgScope.CallerID)
	}
	if store.lastFilter.VisibleFilter != nil {
		t.Errorf("VisibleFilter must not be set for an org caller (that's the SpiceDB path), got %+v", store.lastFilter.VisibleFilter)
	}
}
