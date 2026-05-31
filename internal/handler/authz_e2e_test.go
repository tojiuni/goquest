package handler_test

// End-to-end tests for the centralized ticket-authorization slice (PR #37),
// driving the real chi middleware (auth.RequireTicketView) + real ticket handlers
// + a real ticket.Service over an in-memory store, at the HTTP boundary.
//
// The in-memory store reproduces the production store contract: its List applies
// the same visibility-aware 2-bucket predicate the SQL does
// ((visibility='project' AND id IN project-set) OR (visibility='restricted' AND id
// IN restricted-set)). The stub checker reproduces the SpiceDB contract
// (CanViewTicket / VisibleResourceIDs consistent per permission). Actual Postgres
// SQL and SpiceDB RPC transport are verified separately at cutover (see
// docs/runbooks/authz-core-slice-e2e-scenarios.md §라이브).
//
// Scenarios map 1:1 to docs/runbooks/authz-core-slice-e2e-scenarios.md (Group A/B/C/D).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/handler"
	"github.com/tojiuni/goquest/internal/resolve"
	"github.com/tojiuni/goquest/internal/spicedb"
	"github.com/tojiuni/goquest/internal/ticket"
)

// ── in-memory store (faithful to the production List visibility predicate) ──────

type memStore struct {
	byID  map[uuid.UUID]*ticket.Ticket
	order []uuid.UUID // deterministic List order
	refs  map[uuid.UUID][]ticket.TicketRef
}

func newMemStore(tickets ...*ticket.Ticket) *memStore {
	s := &memStore{
		byID: map[uuid.UUID]*ticket.Ticket{},
		refs: map[uuid.UUID][]ticket.TicketRef{},
	}
	for _, t := range tickets {
		s.byID[t.ID] = t
		s.order = append(s.order, t.ID)
	}
	return s
}

func (s *memStore) Get(_ context.Context, id uuid.UUID) (*ticket.Ticket, error) {
	if t, ok := s.byID[id]; ok {
		return t, nil
	}
	return nil, ticket.ErrNotFound
}

func contains(ids []uuid.UUID, id uuid.UUID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func (s *memStore) List(_ context.Context, f ticket.ListTicketsFilter) ([]*ticket.Ticket, int, error) {
	var matched []*ticket.Ticket
	for _, id := range s.order {
		t := s.byID[id]
		if f.VisibleFilter != nil {
			// Mirror the SQL predicate exactly.
			vis := t.Visibility
			ok := (vis == ticket.VisibilityProject && contains(f.VisibleFilter.Project, t.ID)) ||
				(vis == ticket.VisibilityRestricted && contains(f.VisibleFilter.Restricted, t.ID))
			if !ok {
				continue
			}
		}
		matched = append(matched, t)
	}
	total := len(matched) // exact, pre-pagination — matches the count query
	// pagination
	lo := f.Offset
	if lo > len(matched) {
		lo = len(matched)
	}
	hi := len(matched)
	if f.Limit > 0 && lo+f.Limit < hi {
		hi = lo + f.Limit
	}
	return matched[lo:hi], total, nil
}

func (s *memStore) ListSubtasks(_ context.Context, parentID uuid.UUID) ([]*ticket.Ticket, error) {
	var out []*ticket.Ticket
	for _, id := range s.order {
		t := s.byID[id]
		if t.ParentID != nil && *t.ParentID == parentID {
			out = append(out, t)
		}
	}
	return out, nil
}

func (s *memStore) ListAttempts(_ context.Context, _ uuid.UUID) ([]*ticket.Attempt, error) {
	return nil, nil
}

// remaining Store methods are unused by the read paths under test.
func (s *memStore) Create(_ context.Context, _ *ticket.Ticket) error { return nil }
func (s *memStore) Update(_ context.Context, _ uuid.UUID, _ ticket.UpdateTicketReq) (*ticket.Ticket, error) {
	return nil, nil
}
func (s *memStore) AllSubtasksTerminal(_ context.Context, _ uuid.UUID) (bool, error) {
	return false, nil
}
func (s *memStore) SetTicketTags(_ context.Context, _ uuid.UUID, _ []uuid.UUID) error { return nil }
func (s *memStore) CreateAttempt(_ context.Context, _ *ticket.Attempt) error          { return nil }
func (s *memStore) CountAttempts(_ context.Context, _ uuid.UUID, _ string) (int, error) {
	return 0, nil
}
func (s *memStore) ListOverdue(_ context.Context) ([]*ticket.Ticket, error) { return nil, nil }

// refs: real in-memory behavior so HTTP-level handler tests can exercise the
// service+store contract hermetically (no PG dependency). Mirrors the PG store
// semantics: append-with-conflict on AddRefs, idempotent RemoveRef, ReorderRefs
// must cover the exact existing set.
func (s *memStore) AddRefs(_ context.Context, ticketID uuid.UUID, gopediaL1IDs []string, createdBy string) ([]ticket.TicketRef, error) {
	existing := s.refs[ticketID]
	seen := map[string]bool{}
	for _, r := range existing {
		seen[r.GopediaL1ID] = true
	}
	for _, id := range gopediaL1IDs {
		if seen[id] {
			return nil, ticket.ErrRefConflict
		}
	}
	pos := len(existing)
	added := make([]ticket.TicketRef, 0, len(gopediaL1IDs))
	now := time.Now().UTC()
	for _, id := range gopediaL1IDs {
		r := ticket.TicketRef{
			TicketID:    ticketID,
			GopediaL1ID: id,
			Position:    pos,
			CreatedAt:   now,
			CreatedBy:   createdBy,
		}
		existing = append(existing, r)
		added = append(added, r)
		pos++
	}
	s.refs[ticketID] = existing
	return added, nil
}
func (s *memStore) ListRefs(_ context.Context, ticketID uuid.UUID) ([]ticket.TicketRef, error) {
	out := make([]ticket.TicketRef, len(s.refs[ticketID]))
	copy(out, s.refs[ticketID])
	return out, nil
}
func (s *memStore) RemoveRef(_ context.Context, ticketID uuid.UUID, gopediaL1ID string) error {
	existing := s.refs[ticketID]
	kept := existing[:0]
	for _, r := range existing {
		if r.GopediaL1ID == gopediaL1ID {
			continue
		}
		kept = append(kept, r)
	}
	// reindex positions to remain contiguous
	for i := range kept {
		kept[i].Position = i
	}
	s.refs[ticketID] = kept
	return nil
}
func (s *memStore) ReorderRefs(_ context.Context, ticketID uuid.UUID, orderedL1IDs []string) error {
	existing := s.refs[ticketID]
	if len(existing) != len(orderedL1IDs) {
		return errors.New("reorder: id set mismatch")
	}
	byID := map[string]ticket.TicketRef{}
	for _, r := range existing {
		byID[r.GopediaL1ID] = r
	}
	out := make([]ticket.TicketRef, 0, len(orderedL1IDs))
	for i, id := range orderedL1IDs {
		r, ok := byID[id]
		if !ok {
			return errors.New("reorder: unknown id " + id)
		}
		r.Position = i
		out = append(out, r)
	}
	s.refs[ticketID] = out
	return nil
}
func (s *memStore) CountRefs(_ context.Context, ticketID uuid.UUID) (int, error) {
	return len(s.refs[ticketID]), nil
}
func (s *memStore) ListTicketIDsByRef(_ context.Context, gopediaL1ID string) ([]uuid.UUID, error) {
	var out []uuid.UUID
	for tid, rs := range s.refs {
		for _, r := range rs {
			if r.GopediaL1ID == gopediaL1ID {
				out = append(out, tid)
				break
			}
		}
	}
	return out, nil
}
func (s *memStore) ListRefIDsForTickets(_ context.Context, ticketIDs []uuid.UUID) (map[uuid.UUID][]string, error) {
	out := map[uuid.UUID][]string{}
	for _, tid := range ticketIDs {
		for _, r := range s.refs[tid] {
			out[tid] = append(out[tid], r.GopediaL1ID)
		}
	}
	return out, nil
}

// ── stub checker simulating SpiceDB enforce ─────────────────────────────────────

// worldChecker models, per user, which ticket ids are viewable under each permission.
// It is internally consistent with the store's bucket predicate. failErr, if set,
// makes every method error (to exercise fail-closed).
type worldChecker struct {
	proj    map[string]map[uuid.UUID]bool // can_view_project
	restr   map[string]map[uuid.UUID]bool // can_view_restricted
	failErr error
}

func (c *worldChecker) CanViewTicket(_ context.Context, ticketID uuid.UUID, userID string, vis spicedb.VisibilityMode) (bool, error) {
	if c.failErr != nil {
		return false, c.failErr
	}
	if vis == spicedb.VisibilityRestricted {
		return c.restr[userID][ticketID], nil
	}
	return c.proj[userID][ticketID], nil
}

func (c *worldChecker) VisibleResourceIDs(_ context.Context, _ string, permission, userID string) ([]string, bool, error) {
	if c.failErr != nil {
		return nil, false, c.failErr
	}
	var set map[uuid.UUID]bool
	if permission == "can_view_restricted" {
		set = c.restr[userID]
	} else {
		set = c.proj[userID]
	}
	var ids []string
	for id, ok := range set {
		if ok {
			ids = append(ids, id.String())
		}
	}
	return ids, false, nil // enforce: restricted (not unrestricted)
}

// unused by read paths but required by the interface.
func (c *worldChecker) CanManageTicket(_ context.Context, _ uuid.UUID, _ string) (bool, error) {
	return false, nil
}
func (c *worldChecker) CanViewProject(_ context.Context, _ uuid.UUID, _ string) (bool, error) {
	return false, nil
}
func (c *worldChecker) Check(_ context.Context, _, _, _, _ string) (bool, error) { return false, nil }

// ── router wiring mirroring cmd/service/main.go ─────────────────────────────────

type nilResolver struct{}

func (nilResolver) ResolveProjectID(_ context.Context, _ resolve.Ref) (*uuid.UUID, error) {
	return nil, nil
}

// e2eRouter builds the production-shaped route tree for the ticket read paths.
func e2eRouter(store ticket.Store, checker spicedb.PermissionChecker) http.Handler {
	svc := ticket.NewService(store, nil, nil, nil, nil, nil)
	h := handler.NewTicketHandler(svc, checker, &spicedb.NoopWriter{}, nilResolver{})

	r := chi.NewRouter()
	r.Get("/tickets", h.List)
	r.Route("/tickets/{id}", func(r chi.Router) {
		r.Use(auth.RequireTicketView(svc, checker))
		r.Get("/", h.Get)
		r.Get("/subtasks", h.ListSubtasks)
		r.Get("/attempts", h.ListAttempts)
		// comment routes share the same group gate; a stand-in proves coverage.
		r.Get("/comments", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
		// refs routes (Task 7): same RequireTicketView gate as comments/subtasks.
		r.Get("/refs", h.ListRefs)
		r.Post("/refs", h.AddRefs)
		r.Patch("/refs/reorder", h.ReorderRefs)
		r.Delete("/refs/{l1_id}", h.RemoveRef)
	})
	return r
}

// principal helpers: set the request context the way JWT / CustomerToken middleware would.
func asUser(req *http.Request, userID string) *http.Request {
	return req.WithContext(auth.WithRequesterID(req.Context(), userID))
}
func asCustomer(req *http.Request, bound *uuid.UUID) *http.Request {
	return req.WithContext(auth.WithCustomerCtx(req.Context(), &auth.CustomerCtx{Email: "c@x.com", TicketID: bound}))
}

func do(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func get(path string) *http.Request { return httptest.NewRequest("GET", path, nil) }

// listResp / subtaskResp decode the handler JSON envelopes.
type idOnly struct {
	ID uuid.UUID `json:"id"`
}
type listResp struct {
	Tickets []idOnly `json:"tickets"`
	Total   int      `json:"total"`
}
type subtaskResp struct {
	Subtasks []idOnly `json:"subtasks"`
}

func decodeList(t *testing.T, rec *httptest.ResponseRecorder) listResp {
	t.Helper()
	var lr listResp
	if err := json.Unmarshal(rec.Body.Bytes(), &lr); err != nil {
		t.Fatalf("decode list: %v (body=%s)", err, rec.Body.String())
	}
	return lr
}

func hasID(items []idOnly, id uuid.UUID) bool {
	for _, it := range items {
		if it.ID == id {
			return true
		}
	}
	return false
}

func pid() *uuid.UUID { p := uuid.New(); return &p }

// project-visibility ticket
func tProject(project *uuid.UUID) *ticket.Ticket {
	return &ticket.Ticket{ID: uuid.New(), ProjectID: project, Visibility: ticket.VisibilityProject}
}

// restricted-visibility ticket
func tRestricted(project *uuid.UUID) *ticket.Ticket {
	return &ticket.Ticket{ID: uuid.New(), ProjectID: project, Visibility: ticket.VisibilityRestricted}
}

// ── Group A: deploy-time (enforce-independent) ──────────────────────────────────

func TestE2E_GroupA_DeployTime(t *testing.T) {
	proj := pid()
	t1 := tProject(proj)
	t2 := tProject(proj)
	store := newMemStore(t1, t2)
	// shadow checker — proves Group A is independent of enforce.
	router := e2eRouter(store, &spicedb.NoopChecker{})

	cases := []struct {
		name string
		req  *http.Request
		want int
	}{
		{"A1 customer bound to T1 → T1 200", asCustomer(get("/tickets/"+t1.ID.String()+"/"), &t1.ID), 200},
		{"A2 customer bound to T1 → T2 403", asCustomer(get("/tickets/"+t2.ID.String()+"/"), &t1.ID), 403},
		{"A3 customer unbound → 403", asCustomer(get("/tickets/"+t1.ID.String()+"/"), nil), 403},
		{"A4 nonexistent ticket → 404", asUser(get("/tickets/"+uuid.New().String()+"/"), "anyone"), 404},
		{"A5 customer bound → T1 comments 200", asCustomer(get("/tickets/"+t1.ID.String()+"/comments"), &t1.ID), 200},
		{"A6 customer bound to T1 → T2 comments 403", asCustomer(get("/tickets/"+t2.ID.String()+"/comments"), &t1.ID), 403},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if rec := do(router, c.req); rec.Code != c.want {
				t.Errorf("want %d, got %d (body=%s)", c.want, rec.Code, rec.Body.String())
			}
		})
	}
}

// ── Group B: shadow (no behavior change vs pre-branch) ──────────────────────────

func TestE2E_GroupB_Shadow(t *testing.T) {
	proj := pid()
	tp := tProject(proj)
	tr := tRestricted(proj)
	ts := tRestricted(proj)
	ts.ParentID = &tp.ID
	store := newMemStore(tp, tr, ts)
	router := e2eRouter(store, &spicedb.NoopChecker{}) // shadow

	t.Run("B1 non-member sees restricted ticket (allow-all)", func(t *testing.T) {
		if rec := do(router, asUser(get("/tickets/"+tr.ID.String()+"/"), "stranger")); rec.Code != 200 {
			t.Errorf("want 200, got %d", rec.Code)
		}
	})
	t.Run("B2 list returns all, exact total, no filter", func(t *testing.T) {
		rec := do(router, asUser(get("/tickets"), "stranger"))
		if rec.Code != 200 {
			t.Fatalf("want 200, got %d", rec.Code)
		}
		lr := decodeList(t, rec)
		if lr.Total != 3 || len(lr.Tickets) != 3 {
			t.Errorf("want 3 tickets/total, got %d/%d", len(lr.Tickets), lr.Total)
		}
	})
	t.Run("B3 subtasks unfiltered", func(t *testing.T) {
		rec := do(router, asUser(get("/tickets/"+tp.ID.String()+"/subtasks"), "stranger"))
		var sr subtaskResp
		_ = json.Unmarshal(rec.Body.Bytes(), &sr)
		if rec.Code != 200 || len(sr.Subtasks) != 1 {
			t.Errorf("want 200 + 1 subtask, got %d + %d", rec.Code, len(sr.Subtasks))
		}
	})
	t.Run("B4 attempts 200", func(t *testing.T) {
		if rec := do(router, asUser(get("/tickets/"+tp.ID.String()+"/attempts"), "anyone")); rec.Code != 200 {
			t.Errorf("want 200, got %d", rec.Code)
		}
	})
}

// ── Group C: enforce ────────────────────────────────────────────────────────────

func TestE2E_GroupC_Enforce(t *testing.T) {
	proj := pid()
	tp := tProject(proj)        // project-visibility
	tr := tRestricted(proj)     // restricted; requester bob
	ts := tRestricted(proj)     // restricted subtask of tp; requester bob
	ts.ParentID = &tp.ID
	store := newMemStore(tp, tr, ts)

	// world: alice = project member (sees project-visibility tickets via can_view_project);
	// bob = requester of tr and ts (can_view_restricted) and also a project member (so bob
	// can reach the parent tp for the subtask scenario). carol unrelated.
	checker := &worldChecker{
		proj: map[string]map[uuid.UUID]bool{
			"alice": {tp.ID: true, tr.ID: true, ts.ID: true}, // project member can_view_project includes all project tickets
			"bob":   {tp.ID: true, tr.ID: true, ts.ID: true},
		},
		restr: map[string]map[uuid.UUID]bool{
			"bob": {tr.ID: true, ts.ID: true}, // requester of the restricted tickets
		},
	}
	router := e2eRouter(store, checker)

	t.Run("C1 member views project ticket → 200", func(t *testing.T) {
		if rec := do(router, asUser(get("/tickets/"+tp.ID.String()+"/"), "alice")); rec.Code != 200 {
			t.Errorf("want 200, got %d", rec.Code)
		}
	})
	t.Run("C2 member CANNOT view restricted ticket → 403", func(t *testing.T) {
		// alice is in can_view_project for tr, but tr's visibility=restricted → middleware
		// checks can_view_restricted, where alice is absent. This is the no-leak invariant.
		if rec := do(router, asUser(get("/tickets/"+tr.ID.String()+"/"), "alice")); rec.Code != 403 {
			t.Errorf("want 403, got %d", rec.Code)
		}
	})
	t.Run("C3 requester views restricted ticket → 200", func(t *testing.T) {
		if rec := do(router, asUser(get("/tickets/"+tr.ID.String()+"/"), "bob")); rec.Code != 200 {
			t.Errorf("want 200, got %d", rec.Code)
		}
	})
	t.Run("C4 non-member → 403", func(t *testing.T) {
		if rec := do(router, asUser(get("/tickets/"+tp.ID.String()+"/"), "stranger")); rec.Code != 403 {
			t.Errorf("want 403, got %d", rec.Code)
		}
	})
	t.Run("C5 member list excludes restricted, exact total", func(t *testing.T) {
		lr := decodeList(t, do(router, asUser(get("/tickets"), "alice")))
		// alice sees only project-visibility tp (tr/ts are restricted, alice not in restr set).
		if hasID(lr.Tickets, tr.ID) || hasID(lr.Tickets, ts.ID) {
			t.Errorf("restricted ticket leaked to project member: %+v", lr.Tickets)
		}
		if !hasID(lr.Tickets, tp.ID) {
			t.Errorf("project ticket missing for member: %+v", lr.Tickets)
		}
		if lr.Total != 1 {
			t.Errorf("want exact total 1, got %d", lr.Total)
		}
	})
	t.Run("C6 requester list includes their restricted tickets", func(t *testing.T) {
		lr := decodeList(t, do(router, asUser(get("/tickets"), "bob")))
		// bob: project tp + restricted tr + restricted ts = 3.
		if !hasID(lr.Tickets, tr.ID) {
			t.Errorf("requester's restricted ticket missing: %+v", lr.Tickets)
		}
		if lr.Total != 3 {
			t.Errorf("want total 3 for bob, got %d", lr.Total)
		}
	})
	t.Run("C7 member subtasks excludes restricted child", func(t *testing.T) {
		rec := do(router, asUser(get("/tickets/"+tp.ID.String()+"/subtasks"), "alice"))
		var sr subtaskResp
		_ = json.Unmarshal(rec.Body.Bytes(), &sr)
		if rec.Code != 200 {
			t.Fatalf("want 200, got %d", rec.Code)
		}
		if len(sr.Subtasks) != 0 {
			t.Errorf("restricted subtask leaked to project member: %+v", sr.Subtasks)
		}
	})
	t.Run("C8 requester subtasks includes their restricted child", func(t *testing.T) {
		rec := do(router, asUser(get("/tickets/"+tp.ID.String()+"/subtasks"), "bob"))
		var sr subtaskResp
		_ = json.Unmarshal(rec.Body.Bytes(), &sr)
		if rec.Code != 200 || len(sr.Subtasks) != 1 || sr.Subtasks[0].ID != ts.ID {
			t.Errorf("want 200 + [ts], got %d + %+v", rec.Code, sr.Subtasks)
		}
	})

	// fail-closed
	failRouter := e2eRouter(store, &worldChecker{failErr: errors.New("spicedb down")})
	t.Run("C9 list fail-closed on checker error → 500", func(t *testing.T) {
		if rec := do(failRouter, asUser(get("/tickets"), "alice")); rec.Code != 500 {
			t.Errorf("want 500, got %d", rec.Code)
		}
	})
	t.Run("C10 single-ticket fail-closed → 500", func(t *testing.T) {
		if rec := do(failRouter, asUser(get("/tickets/"+tp.ID.String()+"/"), "alice")); rec.Code != 500 {
			t.Errorf("want 500, got %d", rec.Code)
		}
	})
}

// ── Group D: known gap (subtask without project invisible under enforce) ────────

func TestE2E_GroupD_SubtaskNoProject(t *testing.T) {
	proj := pid()
	tp := tProject(proj)
	orphan := &ticket.Ticket{ID: uuid.New(), ProjectID: nil, Visibility: ticket.VisibilityProject}
	orphan.ParentID = &tp.ID // subtask with no project → no SpiceDB tuple
	store := newMemStore(tp, orphan)

	// enforce: alice sees tp; the orphan is in NO visibility set (no tuple exists).
	checker := &worldChecker{
		proj:  map[string]map[uuid.UUID]bool{"alice": {tp.ID: true}},
		restr: map[string]map[uuid.UUID]bool{},
	}
	router := e2eRouter(store, checker)

	rec := do(router, asUser(get("/tickets/"+tp.ID.String()+"/subtasks"), "alice"))
	var sr subtaskResp
	_ = json.Unmarshal(rec.Body.Bytes(), &sr)
	// Documented gap: orphan subtask is invisible to everyone under enforce until backfill
	// writes its project tuple.
	if rec.Code != 200 || len(sr.Subtasks) != 0 {
		t.Errorf("D1: expected orphan subtask filtered out (0 subtasks), got %d + %+v", rec.Code, sr.Subtasks)
	}
}
