package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/comment"
	"github.com/tojiuni/goquest/internal/handler"
)

// --- stub ---

type stubCommentSvc struct {
	created    *comment.Comment
	list       []*comment.Comment
	gotInclude bool
	err        error
}

func (s *stubCommentSvc) Create(_ context.Context, ticketID uuid.UUID, authorID string, kind comment.AuthorKind, req comment.CreateReq) (*comment.Comment, error) {
	if s.err != nil {
		return nil, s.err
	}
	c := &comment.Comment{
		ID:         uuid.New(),
		TicketID:   ticketID,
		AuthorID:   authorID,
		AuthorKind: kind,
		Body:       req.Body,
		IsInternal: req.IsInternal,
	}
	s.created = c
	return c, nil
}

func (s *stubCommentSvc) ListByTicket(_ context.Context, _ uuid.UUID, includeInternal bool) ([]*comment.Comment, error) {
	s.gotInclude = includeInternal
	return s.list, s.err
}

func (s *stubCommentSvc) Update(_ context.Context, _ uuid.UUID, _ string, req comment.UpdateReq) (*comment.Comment, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &comment.Comment{Body: req.Body}, nil
}

func (s *stubCommentSvc) Delete(_ context.Context, _ uuid.UUID, _ string) error { return s.err }

// --- helpers ---

func commentRouter(h *handler.CommentHandler) http.Handler {
	r := chi.NewRouter()
	// Inject a fake requester id into the request context (auth middleware would normally do this).
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := auth.WithRequesterID(req.Context(), "user-1")
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Route("/tickets/{id}/comments", func(r chi.Router) {
		r.Post("/", h.Create)
		r.Get("/", h.List)
		r.Patch("/{cid}", h.Update)
		r.Delete("/{cid}", h.Delete)
	})
	return r
}

// --- tests ---

func TestCommentHandler_Create_201(t *testing.T) {
	svc := &stubCommentSvc{}
	h := handler.NewCommentHandler(svc, nil)
	srv := httptest.NewServer(commentRouter(h))
	defer srv.Close()

	body, _ := json.Marshal(map[string]any{"body": "hello", "is_internal": false})
	res, err := http.Post(srv.URL+"/tickets/"+uuid.NewString()+"/comments", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", res.StatusCode)
	}
	if svc.created == nil || svc.created.AuthorKind != comment.AuthorKindStaff {
		t.Fatalf("expected staff comment, got %+v", svc.created)
	}
}

func TestCommentHandler_List_FiltersInternal_WhenCanSeeInternalFalse(t *testing.T) {
	svc := &stubCommentSvc{
		list: []*comment.Comment{{ID: uuid.New(), Body: "public"}},
	}
	canSee := func(*http.Request) bool { return false }
	h := handler.NewCommentHandler(svc, canSee)
	srv := httptest.NewServer(commentRouter(h))
	defer srv.Close()

	res, err := http.Get(srv.URL + "/tickets/" + uuid.NewString() + "/comments")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if svc.gotInclude {
		t.Fatal("expected includeInternal=false to be passed through")
	}

	var out struct {
		Comments []*comment.Comment `json:"comments"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(out.Comments))
	}
}

func TestCommentHandler_List_IncludesInternal_WhenCanSeeInternalTrue(t *testing.T) {
	svc := &stubCommentSvc{}
	h := handler.NewCommentHandler(svc, nil) // nil → defaults to true
	srv := httptest.NewServer(commentRouter(h))
	defer srv.Close()

	res, err := http.Get(srv.URL + "/tickets/" + uuid.NewString() + "/comments")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if !svc.gotInclude {
		t.Fatal("expected includeInternal=true to be passed through")
	}
}
