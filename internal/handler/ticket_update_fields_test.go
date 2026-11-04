package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/handler"
	"github.com/tojiuni/goquest/internal/spicedb"
	"github.com/tojiuni/goquest/internal/ticket"
)

// patchStore serves one ticket and applies the priority/title of an update so
// the handler's response reflects what the store would have saved.
type patchStore struct {
	ticket.Store
	t        *ticket.Ticket
	lastReq  ticket.UpdateTicketReq
	gotCalls int
}

func (s *patchStore) Get(_ context.Context, id uuid.UUID) (*ticket.Ticket, error) {
	if id != s.t.ID {
		return nil, ticket.ErrNotFound
	}
	c := *s.t
	return &c, nil
}

func (s *patchStore) Update(_ context.Context, _ uuid.UUID, req ticket.UpdateTicketReq) (*ticket.Ticket, error) {
	s.gotCalls++
	s.lastReq = req
	c := *s.t
	if req.Priority != nil {
		c.Priority = *req.Priority
	}
	if req.Title != nil {
		c.Title = *req.Title
	}
	return &c, nil
}

func patchRouter(t *testing.T) (http.Handler, *patchStore) {
	t.Helper()
	st := &patchStore{t: &ticket.Ticket{ID: uuid.New(), Title: "T", Status: ticket.StatusInProgress, Priority: ticket.PriorityNormal}}
	svc := ticket.NewService(st, noopPublisher{}, nil, nil, nil, nil)
	h := handler.NewTicketHandler(svc, &spicedb.NoopChecker{}, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil, nil)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithRequesterID(req.Context(), "u@x.io")))
		})
	})
	r.Patch("/tickets/{id}", h.Update)
	return r, st
}

func doPatch(h http.Handler, id uuid.UUID, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("PATCH", "/tickets/"+id.String(), bytes.NewBufferString(body)))
	return rec
}

func TestPatch_PriorityIsApplied(t *testing.T) {
	h, st := patchRouter(t)
	rec := doPatch(h, st.t.ID, `{"priority":"high"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var got map[string]any
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got["priority"] != "high" {
		t.Errorf("priority must change, got %v", got["priority"])
	}
	if _, has := got["ignored_fields"]; has || rec.Header().Get("X-Goquest-Ignored-Fields") != "" {
		t.Error("nothing was ignored, so nothing should be reported")
	}
}

func TestPatch_InvalidPriorityIsRejected(t *testing.T) {
	h, st := patchRouter(t)
	for _, v := range []string{`"medium"`, `"HIGH"`, `""`, `"urgentt"`} {
		rec := doPatch(h, st.t.ID, `{"priority":`+v+`}`)
		if rec.Code != http.StatusBadRequest && rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("priority %s: got %d, want a 4xx validation error", v, rec.Code)
		}
	}
	if st.gotCalls != 0 {
		t.Errorf("an invalid priority must never reach the store (%d calls)", st.gotCalls)
	}
}

func TestPatch_UnknownFieldsAreReportedNotSilentlyDropped(t *testing.T) {
	h, st := patchRouter(t)
	rec := doPatch(h, st.t.ID, `{"priority":"high","type":"bug","not_a_field":1,"Title":"renamed"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("recognised fields are still applied with a 200: %d %s", rec.Code, rec.Body)
	}
	var got struct {
		Priority string   `json:"priority"`
		Title    string   `json:"title"`
		Ignored  []string `json:"ignored_fields"`
	}
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Priority != "high" || got.Title != "renamed" {
		t.Errorf("recognised fields (including a differently-cased key encoding/json accepts) must apply: %+v", got)
	}
	if len(got.Ignored) != 2 || got.Ignored[0] != "not_a_field" || got.Ignored[1] != "type" {
		t.Errorf("ignored_fields = %v, want [not_a_field type]", got.Ignored)
	}
	if h := rec.Header().Get("X-Goquest-Ignored-Fields"); h != "not_a_field,type" {
		t.Errorf("header = %q", h)
	}
}

func TestPatch_MalformedBodyStill400(t *testing.T) {
	h, st := patchRouter(t)
	for _, body := range []string{`{`, `[]`, `{"priority":5}`} {
		if rec := doPatch(h, st.t.ID, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d want 400", body, rec.Code)
		}
	}
}

func TestUpdateFieldNamesMatchTheRequestType(t *testing.T) {
	names := ticket.UpdateFieldNames()
	for _, must := range []string{"priority", "status", "title", "assignee_id", "due_at", "tag_ids", "ref_ids"} {
		if !names[must] {
			t.Errorf("%q must be an accepted update field", must)
		}
	}
	for _, mustNot := range []string{"type", "id", "requester_id", "created_at"} {
		if names[mustNot] {
			t.Errorf("%q is not updatable and must not be listed", mustNot)
		}
	}
	if strings.Contains(strings.Join(keys(names), ","), "-") {
		t.Error("tag options like omitempty or - must not leak into names")
	}
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
