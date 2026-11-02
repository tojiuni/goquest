package handler_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/approval"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/handler"
	"github.com/tojiuni/goquest/internal/spicedb"
	"github.com/tojiuni/goquest/internal/ticket"
)

// approveStore serves one pending_approval ticket; Approve only needs Get/Update.
type approveStore struct {
	ticket.Store
	t        *ticket.Ticket
	lastMeta map[string]any
}

func (s *approveStore) Get(_ context.Context, id uuid.UUID) (*ticket.Ticket, error) {
	if id != s.t.ID {
		return nil, ticket.ErrNotFound
	}
	c := *s.t
	return &c, nil
}

func (s *approveStore) Update(_ context.Context, _ uuid.UUID, req ticket.UpdateTicketReq) (*ticket.Ticket, error) {
	s.lastMeta = req.Metadata
	c := *s.t
	if req.Status != nil {
		c.Status = *req.Status
	}
	c.Metadata = req.Metadata
	return &c, nil
}

type failingRecorder struct{ calls int }

func (f *failingRecorder) RecordTicketApproval(context.Context, uuid.UUID, string, string, approval.Actor, string) (*approval.Approval, error) {
	f.calls++
	return nil, errors.New("db down")
}

func approveRouter(t *testing.T, rec handler.ApproveRecorder, kind auth.PrincipalKind) (http.Handler, *approveStore) {
	t.Helper()
	st := &approveStore{t: &ticket.Ticket{ID: uuid.New(), Status: ticket.StatusPendingApproval}}
	svc := ticket.NewService(st, noopPublisher{}, nil, nil, nil, nil)
	h := handler.NewTicketHandler(svc, &spicedb.NoopChecker{}, &spicedb.NoopWriter{}, nilResolver{}, nil, nil, nil, nil).WithApprovalRecorder(rec)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := auth.WithPrincipalKind(auth.WithRequesterID(req.Context(), "caller@x.io"), kind)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Post("/tickets/{id}/approve", h.Approve)
	return r, st
}

func postApprove(h http.Handler, id uuid.UUID, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/tickets/"+id.String()+"/approve", bytes.NewBufferString(body)))
	return rec
}

func TestApprove_BodyOperatorIsIgnoredAndRecordMirrored(t *testing.T) {
	mem := approval.NewMemStore()
	h, st := approveRouter(t, approval.NewService(mem), auth.PrincipalHuman)
	rec := postApprove(h, st.t.ID, `{"decision":"approved","operator_id":"someone-else@x.io"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body)
	}
	if st.lastMeta["approval_operator_id"] != "caller@x.io" {
		t.Errorf("ticket operator must be the principal, got %v", st.lastMeta["approval_operator_id"])
	}
	got, _ := mem.List(context.Background(), approval.ListFilter{TicketID: &st.t.ID, Kind: approval.KindTicket})
	if len(got) != 1 {
		t.Fatalf("want 1 ticket approval record, got %d", len(got))
	}
	a := got[0]
	if a.Status != approval.StatusApproved || *a.DecidedBy != "caller@x.io" || !a.Legacy {
		t.Errorf("claimed operator differs from principal -> legacy record decided by the principal: %+v", a)
	}
	if a.Basis == nil || *a.Basis != approval.BasisUserDirect {
		t.Errorf("a person is user_direct even when the body claimed another operator: %+v", a.Basis)
	}
}

func TestApprove_PersonWithMatchingOperatorIsNotLegacy(t *testing.T) {
	mem := approval.NewMemStore()
	h, st := approveRouter(t, approval.NewService(mem), auth.PrincipalHuman)
	postApprove(h, st.t.ID, `{"decision":"rejected","revision_note":"no","operator_id":"caller@x.io"}`)
	got, _ := mem.List(context.Background(), approval.ListFilter{TicketID: &st.t.ID})
	if len(got) != 1 || got[0].Legacy || got[0].Status != approval.StatusRejected || *got[0].Basis != approval.BasisUserDirect {
		t.Errorf("%+v", got)
	}
}

func TestApprove_NonPersonIsRecordedWithoutBasis(t *testing.T) {
	for _, kind := range []auth.PrincipalKind{auth.PrincipalOther, auth.PrincipalSession} {
		mem := approval.NewMemStore()
		h, st := approveRouter(t, approval.NewService(mem), kind)
		if rec := postApprove(h, st.t.ID, `{"decision":"revision","revision_note":"redo"}`); rec.Code != http.StatusOK {
			t.Fatalf("%s: legacy endpoint must keep working: %d", kind, rec.Code)
		}
		got, _ := mem.List(context.Background(), approval.ListFilter{TicketID: &st.t.ID})
		if len(got) != 1 || got[0].Basis != nil || !got[0].Legacy || got[0].Status != approval.StatusRevisionRequested {
			t.Errorf("%s: an unattested caller must not get a basis: %+v", kind, got)
		}
	}
}

func TestApprove_RecorderFailureDoesNotFailApprove(t *testing.T) {
	f := &failingRecorder{}
	h, st := approveRouter(t, f, auth.PrincipalHuman)
	if rec := postApprove(h, st.t.ID, `{"decision":"approved"}`); rec.Code != http.StatusOK {
		t.Fatalf("approve must succeed when recording fails: %d %s", rec.Code, rec.Body)
	}
	if f.calls != 1 {
		t.Errorf("recorder calls = %d", f.calls)
	}
}

func TestApprove_NoRecorderStillWorks(t *testing.T) {
	h, st := approveRouter(t, nil, auth.PrincipalHuman)
	if rec := postApprove(h, st.t.ID, `{"decision":"approved"}`); rec.Code != http.StatusOK {
		t.Fatalf("%d", rec.Code)
	}
}
