package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/comment"
	"github.com/tojiuni/goquest/internal/directory"
	"github.com/tojiuni/goquest/internal/project"
	"github.com/tojiuni/goquest/internal/ticket"
	"github.com/tojiuni/goquest/internal/workspace"
)

type requesterTestProject struct{ workspace uuid.UUID }

func (p requesterTestProject) Get(context.Context, uuid.UUID) (*project.Project, error) {
	return &project.Project{WorkspaceID: p.workspace}, nil
}

type requesterTestMember struct{ member bool }

func (m requesterTestMember) GetMember(context.Context, uuid.UUID, string) (*workspace.Member, error) {
	if m.member {
		return &workspace.Member{}, nil
	}
	return nil, errors.New("not a member")
}

func TestRequesterTenantLookup(t *testing.T) {
	const host = "tenant.example.test"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Host != host || r.Method != http.MethodPost {
			t.Errorf("unexpected request %s host %s", r.Method, r.Host)
		}
		switch r.URL.Path {
		case "/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "expires_in": 3600})
		case "/v1/actors/lookup":
			if r.Header.Get("Authorization") != "Bearer token" {
				t.Error("missing bearer")
			}
			var body struct {
				IDs []string `json:"ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.IDs) != 1 {
				t.Errorf("bad body: %+v %v", body, err)
				return
			}
			if body.IDs[0] == "alice@example.com" {
				_ = json.NewEncoder(w).Encode([]directory.Actor{{ID: "actor", DisplayName: "Alice", Email: "alice@example.com"}})
			} else {
				_ = json.NewEncoder(w).Encode([]directory.Actor{})
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := directory.NewClient(server.URL, host, "client", "secret", server.Client())
	pid := uuid.New()
	tk := &ticket.Ticket{ProjectID: &pid}
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(auth.WithRequesterID(r.Context(), "caller"))
	for _, tc := range []struct {
		name, host, raw       string
		member                bool
		actor, display, email string
	}{
		{"resolved actor", host, "alice@example.com", true, "actor", "Alice", "alice@example.com"},
		{"unknown id", host, "unknown", true, "", "unknown", ""},
		{"config missing", "", "alice@example.com", true, "", "alice@example.com", ""},
		{"non-member caller", host, "alice@example.com", false, "actor", "Alice", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := calls
			resolver := NewRequesterResolver(requesterTestProject{uuid.New()}, requesterTestMember{tc.member}, NewTenantRequesterAccounts(tc.host, client))
			got := resolver.resolve(r, tk, tc.raw)
			if got.ActorID != tc.actor || got.DisplayName != tc.display || got.Email != tc.email {
				t.Fatalf("got %+v", got)
			}
			if tc.host == "" && calls != before {
				t.Fatal("lookup called without tenant host")
			}
		})
	}
}

type requesterLookupFunc func(context.Context, []string) ([]directory.Actor, error)

func (f requesterLookupFunc) Lookup(ctx context.Context, ids []string) ([]directory.Actor, error) {
	return f(ctx, ids)
}

func TestRequesterRejectsMismatchedLookup(t *testing.T) {
	for _, tc := range []struct {
		name   string
		actors []directory.Actor
	}{
		{"prefix", []directory.Actor{{ID: "alice-more", Email: "alice-more@example.com"}}},
		{"two results", []directory.Actor{{ID: "alice", Email: "alice@example.com"}, {ID: "other"}}},
		{"different id", []directory.Actor{{ID: "other", Email: "other@example.com"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pid := uuid.New()
			tk := &ticket.Ticket{ProjectID: &pid}
			lookup := requesterLookupFunc(func(context.Context, []string) ([]directory.Actor, error) { return tc.actors, nil })
			resolver := NewRequesterResolver(requesterTestProject{uuid.New()}, requesterTestMember{true}, NewTenantRequesterAccounts("tenant.example", lookup))
			r := httptest.NewRequest("GET", "/", nil)
			r = r.WithContext(auth.WithRequesterID(r.Context(), "caller"))
			got := resolver.resolve(r, tk, "alice")
			if got.ActorID != "" || got.Email != "" || got.DisplayName != "alice" {
				t.Fatalf("unexpected requester: %+v", got)
			}
		})
	}
}

type requesterCommentSvc struct{ list []*comment.Comment }

func (s requesterCommentSvc) ListByTicket(context.Context, uuid.UUID, bool) ([]*comment.Comment, error) {
	return s.list, nil
}
func (s requesterCommentSvc) Create(context.Context, uuid.UUID, string, comment.AuthorKind, comment.CreateReq) (*comment.Comment, error) {
	return nil, nil
}
func (s requesterCommentSvc) Update(context.Context, uuid.UUID, string, comment.UpdateReq) (*comment.Comment, error) {
	return nil, nil
}
func (s requesterCommentSvc) Delete(context.Context, uuid.UUID, string) error { return nil }
func (s requesterCommentSvc) Publish(context.Context, uuid.UUID, comment.AuthorKind) error {
	return nil
}

type requesterCountingMember struct {
	calls  int
	member bool
}

func (m *requesterCountingMember) GetMember(context.Context, uuid.UUID, string) (*workspace.Member, error) {
	m.calls++
	if m.member {
		return &workspace.Member{}, nil
	}
	return nil, errors.New("not member")
}

func TestRequesterTicketAndCommentResponses(t *testing.T) {
	pid, tid := uuid.New(), uuid.New()
	tk := &ticket.Ticket{ID: tid, ProjectID: &pid, RequesterID: "alice"}
	calls := 0
	lookup := requesterLookupFunc(func(_ context.Context, ids []string) ([]directory.Actor, error) {
		calls++
		return []directory.Actor{{ID: ids[0], DisplayName: "Alice", Email: "alice@example.com"}}, nil
	})
	members := &requesterCountingMember{member: true}
	resolver := NewRequesterResolver(requesterTestProject{uuid.New()}, members, NewTenantRequesterAccounts("tenant.example", lookup))
	request := func() *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		ctx := auth.WithTicket(r.Context(), tk)
		ctx = auth.WithRequesterID(ctx, "caller")
		route := chi.NewRouteContext()
		route.URLParams.Add("id", tid.String())
		ctx = context.WithValue(ctx, chi.RouteCtxKey, route)
		return r.WithContext(ctx)
	}
	ticketHandler := NewTicketHandler(nil, nil, nil, nil, nil, nil, nil, nil).WithRequesterResolver(resolver)
	rec := httptest.NewRecorder()
	ticketHandler.Get(rec, request())
	var ticketBody struct {
		Requester requesterIdentity `json:"requester"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ticketBody); err != nil || rec.Code != http.StatusOK || ticketBody.Requester.ActorID != "alice" || ticketBody.Requester.Email != "alice@example.com" {
		t.Fatalf("ticket: %d %s %v", rec.Code, rec.Body.String(), err)
	}
	calls = 0
	members.calls = 0
	commentHandler := NewCommentHandler(requesterCommentSvc{[]*comment.Comment{{AuthorID: "alice"}, {AuthorID: "alice"}}}, nil).WithRequesterResolver(resolver)
	rec = httptest.NewRecorder()
	commentHandler.List(rec, request())
	var body struct {
		Comments []struct {
			Requester requesterIdentity `json:"requester"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK || len(body.Comments) != 2 {
		t.Fatalf("comments: %d %s %v", rec.Code, rec.Body.String(), err)
	}
	for _, c := range body.Comments {
		if c.Requester.ActorID != "alice" || c.Requester.Email != "alice@example.com" {
			t.Fatalf("comment requester: %+v", c.Requester)
		}
	}
	if calls != 1 || members.calls != 2 {
		t.Fatalf("lookups=%d membership checks=%d", calls, members.calls)
	}
	members.member = false
	rec = httptest.NewRecorder()
	commentHandler.List(rec, request())
	body.Comments = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Comments[0].Requester.Email != "" || calls != 2 {
		t.Fatalf("membership or request cache leaked: %s", rec.Body.String())
	}
}
