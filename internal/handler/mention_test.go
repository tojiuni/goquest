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
	"github.com/tojiuni/goquest/internal/directory"
	"github.com/tojiuni/goquest/internal/handler"
	"github.com/tojiuni/goquest/internal/mention"
)

// --- stubs ---

type stubMentionSvc struct {
	views      []*mention.View
	unseen     int
	gotActorID string
	gotLimit   int
	gotOffset  int
	seenID     uuid.UUID
	seenActor  string
	markErr    error
}

func (s *stubMentionSvc) ListViewsByActor(_ context.Context, actorID string, limit, offset int) ([]*mention.View, error) {
	s.gotActorID, s.gotLimit, s.gotOffset = actorID, limit, offset
	return s.views, nil
}

func (s *stubMentionSvc) CountUnseenByActor(_ context.Context, _ string) (int, error) {
	return s.unseen, nil
}

func (s *stubMentionSvc) MarkSeen(_ context.Context, id uuid.UUID, actorID string) error {
	s.seenID, s.seenActor = id, actorID
	return s.markErr
}

type stubLookuper struct{ actors []directory.Actor }

func (s stubLookuper) Lookup(_ context.Context, _ []string) ([]directory.Actor, error) {
	return s.actors, nil
}

func mentionRouter(h *handler.MentionHandler, requester string) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := req.Context()
			if requester != "" {
				ctx = auth.WithRequesterID(ctx, requester)
			}
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/users/mentions", h.ListMine)
	r.Post("/mentions/{id}/seen", h.MarkSeen)
	return r
}

// --- tests ---

func TestMentionListMine_ScopedToRequesterWithDefaults(t *testing.T) {
	svc := &stubMentionSvc{
		views: []*mention.View{
			{Mention: mention.Mention{ID: uuid.New(), MentionedActorID: "user-1", CreatedBy: "alice"}, TicketTitle: "T1"},
		},
		unseen: 3,
	}
	h := handler.NewMentionHandler(svc, stubLookuper{actors: []directory.Actor{{ID: "alice", DisplayName: "Alice Kim"}}})

	req := httptest.NewRequest(http.MethodGet, "/users/mentions", nil)
	rec := httptest.NewRecorder()
	mentionRouter(h, "user-1").ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if svc.gotActorID != "user-1" {
		t.Fatalf("actor id = %q, want user-1 (from auth, not params)", svc.gotActorID)
	}
	if svc.gotLimit != 20 || svc.gotOffset != 0 {
		t.Fatalf("limit/offset = %d/%d, want defaults 20/0", svc.gotLimit, svc.gotOffset)
	}
	var body struct {
		Mentions    []*mention.View `json:"mentions"`
		UnseenCount int             `json:"unseen_count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.UnseenCount != 3 || len(body.Mentions) != 1 {
		t.Fatalf("unexpected body: %+v", body)
	}
	if body.Mentions[0].CreatedByName != "Alice Kim" {
		t.Fatalf("created_by_name = %q, want Alice Kim", body.Mentions[0].CreatedByName)
	}
}

func TestMentionListMine_CapsLimit(t *testing.T) {
	svc := &stubMentionSvc{}
	h := handler.NewMentionHandler(svc, nil)

	req := httptest.NewRequest(http.MethodGet, "/users/mentions?limit=5000&offset=40", nil)
	rec := httptest.NewRecorder()
	mentionRouter(h, "user-1").ServeHTTP(rec, req)

	if svc.gotLimit != 100 || svc.gotOffset != 40 {
		t.Fatalf("limit/offset = %d/%d, want 100/40", svc.gotLimit, svc.gotOffset)
	}
}

func TestMentionListMine_Unauthorized(t *testing.T) {
	h := handler.NewMentionHandler(&stubMentionSvc{}, nil)
	req := httptest.NewRequest(http.MethodGet, "/users/mentions", nil)
	rec := httptest.NewRecorder()
	mentionRouter(h, "").ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestMentionMarkSeen_UsesAuthActor(t *testing.T) {
	svc := &stubMentionSvc{}
	h := handler.NewMentionHandler(svc, nil)
	id := uuid.New()

	req := httptest.NewRequest(http.MethodPost, "/mentions/"+id.String()+"/seen", nil)
	rec := httptest.NewRecorder()
	mentionRouter(h, "user-1").ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if svc.seenID != id || svc.seenActor != "user-1" {
		t.Fatalf("MarkSeen(%s, %s), want (%s, user-1)", svc.seenID, svc.seenActor, id)
	}
}

func TestMentionMarkSeen_NotFound(t *testing.T) {
	svc := &stubMentionSvc{markErr: mention.ErrNotFound}
	h := handler.NewMentionHandler(svc, nil)

	req := httptest.NewRequest(http.MethodPost, "/mentions/"+uuid.New().String()+"/seen", nil)
	rec := httptest.NewRecorder()
	mentionRouter(h, "user-1").ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
