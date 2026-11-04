package handler_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/handler"
	"github.com/tojiuni/goquest/internal/mention"
	"github.com/tojiuni/goquest/internal/ticket"
)

type mentionTicketGetter struct{ t *ticket.Ticket }

func (g mentionTicketGetter) Get(context.Context, uuid.UUID) (*ticket.Ticket, error) { return g.t, nil }

type fakeCreateMentionSvc struct {
	created []*mention.Mention
	err     error
	called  bool
}

func (s *fakeCreateMentionSvc) CreateFromBody(_ context.Context, ticketID, _ uuid.UUID, commentID *uuid.UUID, _ mention.Source, authorID, _ string) ([]*mention.Mention, error) {
	s.called = true
	if s.err != nil {
		return nil, s.err
	}
	out := make([]*mention.Mention, 0, len(s.created))
	for _, m := range s.created {
		c := *m
		c.TicketID, c.CommentID, c.CreatedBy = ticketID, commentID, authorID
		out = append(out, &c)
	}
	return out, nil
}

type recordingNotifier struct {
	calls   int
	gotID   uuid.UUID
	gotBy   string
	gotN    int
	ticketN string
}

func (r *recordingNotifier) Notify(_ context.Context, t *ticket.Ticket, commentID uuid.UUID, authorID string, created []*mention.Mention) {
	r.calls++
	r.gotID, r.gotBy, r.gotN, r.ticketN = commentID, authorID, len(created), t.TitleName
}

func postComment(t *testing.T, h *handler.CommentHandler, body string) *httptest.ResponseRecorder {
	t.Helper()
	srv := commentRouter(h)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/tickets/"+uuid.NewString()+"/comments", bytes.NewBufferString(body))
	srv.ServeHTTP(rec, req)
	return rec
}

func TestCommentCreate_NotifiesOnlyAfterMentionsAreStored(t *testing.T) {
	pid := uuid.New()
	tkt := &ticket.Ticket{ID: uuid.New(), TitleName: "goq-x", ProjectID: &pid}
	ms := &fakeCreateMentionSvc{created: []*mention.Mention{{ID: uuid.New(), MentionedActorID: "a"}, {ID: uuid.New(), MentionedActorID: "b"}}}
	n := &recordingNotifier{}
	svc := &stubCommentSvc{}
	h := handler.NewCommentHandler(svc, nil).WithMentions(mentionTicketGetter{tkt}, ms, nil).WithMentionNotifier(n)

	rec := postComment(t, h, `{"body":"hi @a @b"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d", rec.Code)
	}
	if n.calls != 1 || n.gotN != 2 || n.gotBy != "user-1" || n.gotID != svc.created.ID || n.ticketN != "goq-x" {
		t.Errorf("notifier not called with the stored mentions: %+v", n)
	}
}

func TestCommentCreate_NoNotificationWhenNothingStored(t *testing.T) {
	pid := uuid.New()
	tkt := &ticket.Ticket{ID: uuid.New(), ProjectID: &pid}
	for name, ms := range map[string]*fakeCreateMentionSvc{
		"no mentions created": {},
		"storing failed":      {err: errors.New("db down"), created: []*mention.Mention{{ID: uuid.New(), MentionedActorID: "a"}}},
	} {
		n := &recordingNotifier{}
		h := handler.NewCommentHandler(&stubCommentSvc{}, nil).WithMentions(mentionTicketGetter{tkt}, ms, nil).WithMentionNotifier(n)
		if rec := postComment(t, h, `{"body":"hi"}`); rec.Code != http.StatusCreated {
			t.Fatalf("%s: comment must still be created, got %d", name, rec.Code)
		}
		if n.calls != 0 {
			t.Errorf("%s: notifier must not run", name)
		}
	}
	// a ticket without a project cannot have mentions at all
	ms := &fakeCreateMentionSvc{created: []*mention.Mention{{ID: uuid.New()}}}
	n := &recordingNotifier{}
	h := handler.NewCommentHandler(&stubCommentSvc{}, nil).WithMentions(mentionTicketGetter{&ticket.Ticket{ID: uuid.New()}}, ms, nil).WithMentionNotifier(n)
	postComment(t, h, `{"body":"hi @a"}`)
	if ms.called || n.calls != 0 {
		t.Error("a ticket without a project must skip mention handling")
	}
}

func TestCommentCreate_WorksWithoutANotifier(t *testing.T) {
	pid := uuid.New()
	ms := &fakeCreateMentionSvc{created: []*mention.Mention{{ID: uuid.New(), MentionedActorID: "a"}}}
	h := handler.NewCommentHandler(&stubCommentSvc{}, nil).WithMentions(mentionTicketGetter{&ticket.Ticket{ID: uuid.New(), ProjectID: &pid}}, ms, nil)
	if rec := postComment(t, h, `{"body":"hi @a"}`); rec.Code != http.StatusCreated {
		t.Fatalf("status %d", rec.Code)
	}
}
