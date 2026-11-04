package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
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

func TestPatch_LogsIgnoredFieldNamesButNeverValues(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	h, st := patchRouter(t)
	req := httptest.NewRequest("PATCH", "/tickets/"+st.t.ID.String(),
		bytes.NewBufferString(`{"priority":"high","type":"SECRET-VALUE-1","not_a_field":{"x":"SECRET-VALUE-2"}}`))
	req.Header.Set("User-Agent", "task-iim/1.0")
	h.ServeHTTP(httptest.NewRecorder(), req)
	out := buf.String()
	for _, want := range []string{"ticket update: ignored fields", `"not_a_field"`, `"type"`, `"u@x.io"`, `ua="task-iim/1.0"`} {
		if !strings.Contains(out, want) {
			t.Errorf("log line missing %s: %q", want, out)
		}
	}
	if strings.Contains(out, "SECRET-VALUE") || strings.Contains(out, "high") {
		t.Errorf("values must never be logged: %q", out)
	}
	if strings.Count(out, "ticket update: ignored fields") != 1 {
		t.Errorf("exactly one line per PATCH: %q", out)
	}

	// nothing ignored -> nothing logged
	buf.Reset()
	doPatch(h, st.t.ID, `{"priority":"low"}`)
	if strings.Contains(buf.String(), "ignored fields") {
		t.Errorf("a clean PATCH must not log: %q", buf.String())
	}
}

func TestPatch_LogBoundsClientControlledText(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	h, st := patchRouter(t)
	long := strings.Repeat("a", 500)
	body := `{"` + long + `":1,"line\nbreak":2}`
	req := httptest.NewRequest("PATCH", "/tickets/"+st.t.ID.String(), bytes.NewBufferString(body))
	req.Header.Set("User-Agent", strings.Repeat("u", 500)+"\nINJECTED")
	h.ServeHTTP(httptest.NewRecorder(), req)
	out := strings.TrimSpace(buf.String())
	if strings.Count(out, "\n") != 0 {
		t.Errorf("client text must not be able to add log lines: %q", out)
	}
	if strings.Contains(out, long) || len(out) > 400 {
		t.Errorf("names and user-agent must be bounded (len %d)", len(out))
	}
}
