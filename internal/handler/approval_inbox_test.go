package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/approval"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/spicedb"
)

// visChecker answers VisibleResourceIDs from fixed sets; every other method is
// unused by the inbox and left nil on purpose (it would panic if it were called).
type visChecker struct {
	spicedb.PermissionChecker
	proj, restr []string
	unrestr     bool
	err         error
}

func (c *visChecker) VisibleResourceIDs(_ context.Context, _, perm, _ string) ([]string, bool, error) {
	if c.err != nil {
		return nil, false, c.err
	}
	if c.unrestr {
		return nil, true, nil
	}
	if perm == "can_view_restricted" {
		return c.restr, false, nil
	}
	return c.proj, false, nil
}

type inboxEnv struct {
	store        *approval.MemStore
	ws, otherWS  uuid.UUID
	open, secret *approval.Approval // on a project ticket / on a restricted ticket
	foreign      *approval.Approval // on a ticket in another workspace
	openT, secT  uuid.UUID
}

func newInboxEnv(t *testing.T) *inboxEnv {
	t.Helper()
	e := &inboxEnv{store: approval.NewMemStore(), ws: uuid.New(), otherWS: uuid.New(), openT: uuid.New(), secT: uuid.New()}
	foreignT := uuid.New()
	e.store.AddTicket(e.ws, approval.TicketSummary{ID: e.openT, TitleName: "goq-open", Title: "Open", Visibility: "project"})
	e.store.AddTicket(e.ws, approval.TicketSummary{ID: e.secT, TitleName: "goq-secret", Title: "Secret", Visibility: "restricted"})
	e.store.AddTicket(e.otherWS, approval.TicketSummary{ID: foreignT, TitleName: "goq-foreign", Title: "Foreign", Visibility: "project"})
	mk := func(tid uuid.UUID, by, doc string) *approval.Approval {
		a, err := e.store.Create(context.Background(), approval.NewReq{TicketID: tid, Kind: approval.KindDesign, RequestedBy: by, Subject: approval.Subject{Doc: doc}})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	e.open, e.secret, e.foreign = mk(e.openT, "alice@x.io", "a.md"), mk(e.secT, "bob@x.io", "b.md"), mk(foreignT, "alice@x.io", "c.md")
	return e
}

func (e *inboxEnv) get(chk spicedb.PermissionChecker, user string, customer bool, path string) *httptest.ResponseRecorder {
	h := NewApprovalHandler(approval.NewService(e.store), nil)
	if chk != nil {
		h.WithVisibility(chk)
	}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := auth.WithRequesterID(req.Context(), user)
			if customer {
				ctx = auth.WithCustomerCtx(ctx, &auth.CustomerCtx{})
			}
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/workspaces/{wid}/approvals", h.ListWorkspace)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

type inboxResp struct {
	Approvals []struct {
		ID     uuid.UUID `json:"id"`
		Ticket struct {
			TitleName string `json:"title_name"`
		} `json:"ticket"`
	} `json:"approvals"`
	NextOffset int  `json:"next_offset"`
	HasMore    bool `json:"has_more"`
}

func decodeInbox(t *testing.T, rec *httptest.ResponseRecorder) inboxResp {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var r inboxResp
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestInbox_ScopedToWorkspaceAndTicketVisibility(t *testing.T) {
	e := newInboxEnv(t)
	base := "/workspaces/" + e.ws.String() + "/approvals"

	// sees the project ticket only: the restricted ticket's approval is hidden
	chk := &visChecker{proj: []string{e.openT.String()}}
	got := decodeInbox(t, e.get(chk, "u", false, base))
	if len(got.Approvals) != 1 || got.Approvals[0].ID != e.open.ID || got.Approvals[0].Ticket.TitleName != "goq-open" {
		t.Fatalf("want only the project-visible approval with its ticket summary, got %+v", got.Approvals)
	}

	// can view the restricted ticket too
	chk = &visChecker{proj: []string{e.openT.String()}, restr: []string{e.secT.String()}}
	if got := decodeInbox(t, e.get(chk, "u", false, base)); len(got.Approvals) != 2 {
		t.Errorf("want 2, got %d", len(got.Approvals))
	}
	// a restricted ticket needs the restricted permission, not just project view
	chk = &visChecker{proj: []string{e.secT.String()}}
	if got := decodeInbox(t, e.get(chk, "u", false, base)); len(got.Approvals) != 0 {
		t.Errorf("project permission must not reveal a restricted ticket: %+v", got.Approvals)
	}
	// shadow mode (unrestricted) shows everything in the workspace, never another workspace
	got = decodeInbox(t, e.get(&visChecker{unrestr: true}, "u", false, base))
	if len(got.Approvals) != 2 {
		t.Errorf("unrestricted: want the 2 workspace approvals, got %d", len(got.Approvals))
	}
	for _, a := range got.Approvals {
		if a.ID == e.foreign.ID {
			t.Error("another workspace's approval leaked")
		}
	}
	// empty result is [] not null
	rec := e.get(&visChecker{}, "u", false, base)
	if !jsonHas(rec, `"approvals":[]`) {
		t.Errorf("empty must be []: %s", rec.Body)
	}
}

func jsonHas(rec *httptest.ResponseRecorder, s string) bool {
	return strings.Contains(rec.Body.String(), s)
}

func TestInbox_FiltersAndPaging(t *testing.T) {
	e := newInboxEnv(t)
	chk := &visChecker{unrestr: true}
	base := "/workspaces/" + e.ws.String() + "/approvals"
	if got := decodeInbox(t, e.get(chk, "alice@x.io", false, base+"?mine=true")); len(got.Approvals) != 1 || got.Approvals[0].ID != e.open.ID {
		t.Errorf("mine=true must return only alice's request: %+v", got.Approvals)
	}
	if got := decodeInbox(t, e.get(chk, "u", false, base+"?kind=merge")); len(got.Approvals) != 0 {
		t.Errorf("kind filter: %+v", got.Approvals)
	}
	if got := decodeInbox(t, e.get(chk, "u", false, base+"?status=approved")); len(got.Approvals) != 0 {
		t.Errorf("status filter: %+v", got.Approvals)
	}
	page := decodeInbox(t, e.get(chk, "u", false, base+"?limit=1"))
	if len(page.Approvals) != 1 || !page.HasMore || page.NextOffset != 1 {
		t.Errorf("first page: %+v", page)
	}
	next := decodeInbox(t, e.get(chk, "u", false, base+"?limit=1&offset=1"))
	if len(next.Approvals) != 1 || next.Approvals[0].ID == page.Approvals[0].ID {
		t.Errorf("second page must differ: %+v", next)
	}
}

func TestInbox_Refusals(t *testing.T) {
	e := newInboxEnv(t)
	base := "/workspaces/" + e.ws.String() + "/approvals"
	if rec := e.get(&visChecker{unrestr: true}, "c", true, base); rec.Code != http.StatusForbidden {
		t.Errorf("customer token: got %d want 403", rec.Code)
	}
	if rec := e.get(nil, "u", false, base); rec.Code != http.StatusInternalServerError {
		t.Errorf("no checker must fail closed, got %d", rec.Code)
	}
	if rec := e.get(&visChecker{err: errors.New("spicedb down")}, "u", false, base); rec.Code != http.StatusInternalServerError {
		t.Errorf("checker error must fail closed, got %d", rec.Code)
	}
	if rec := e.get(&visChecker{unrestr: true}, "u", false, "/workspaces/not-a-uuid/approvals"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad workspace id: got %d", rec.Code)
	}
}
