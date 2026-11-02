package approval

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

type sentDM struct{ to, content string }

type fakeMessenger struct {
	mu       sync.Mutex
	sent     []sentDM
	failures int // fail this many calls first
	calls    int
}

func (f *fakeMessenger) SendDM(_ context.Context, to, content string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.failures > 0 {
		f.failures--
		return errors.New("zulip down")
	}
	f.sent = append(f.sent, sentDM{to, content})
	return nil
}

func newDispatcher(m *fakeMessenger, owner string) (*Dispatcher, uuid.UUID) {
	ws := uuid.New()
	d := NewDispatcher(m, func(_ context.Context, w uuid.UUID) (string, error) {
		if w != ws {
			return "", errors.New("unknown workspace")
		}
		return owner, nil
	}, "https://goquest.example/approvals/")
	d.Sync, d.Backoff = true, 0
	return d, ws
}

func approvalOf(kind Kind, mut func(*Approval)) *Approval {
	a := &Approval{ID: uuid.New(), TicketID: uuid.New(), Kind: kind, Status: StatusPending, RequestedBy: "tokenhub-forge",
		Subject: Subject{Repo: "o/r", PR: 4, HeadSHA: "0123456789abcdef", Doc: "d.md", Target: "t"}}
	if kind == KindDeviation {
		no := false
		a.WithinDirection = &no
	}
	if mut != nil {
		mut(a)
	}
	return a
}

func TestRequested_RecipientPolicy(t *testing.T) {
	yes := true
	cases := []struct {
		name string
		a    *Approval
		opt  func(*Dispatcher)
		want int
	}{
		{"infra goes to the owner", approvalOf(KindInfra, nil), nil, 1},
		{"design goes to the owner", approvalOf(KindDesign, nil), nil, 1},
		{"direction-changing deviation goes to the owner", approvalOf(KindDeviation, nil), nil, 1},
		{"merge is for the desk session, not the owner", approvalOf(KindMerge, nil), nil, 0},
		{"deviation within direction is delegable too", approvalOf(KindDeviation, func(a *Approval) { a.WithinDirection = &yes }), nil, 0},
		{"merge reaches the owner when the option is on", approvalOf(KindMerge, nil), func(d *Dispatcher) { d.NotifyUserOnDelegable = true }, 1},
		{"owner who requested it is not messaged", approvalOf(KindInfra, func(a *Approval) { a.RequestedBy = "owner@x.io" }), nil, 0},
	}
	for _, c := range cases {
		m := &fakeMessenger{}
		d, ws := newDispatcher(m, "owner@x.io")
		if c.opt != nil {
			c.opt(d)
		}
		d.Requested(context.Background(), c.a, TicketRef{TitleName: "goq-x", WorkspaceID: ws})
		if len(m.sent) != c.want {
			t.Errorf("%s: sent %d want %d", c.name, len(m.sent), c.want)
			continue
		}
		if c.want == 1 && m.sent[0].to != "owner@x.io" {
			t.Errorf("%s: sent to %s", c.name, m.sent[0].to)
		}
	}
}

func TestRequested_NothingToSendWhenOwnerUnknown(t *testing.T) {
	m := &fakeMessenger{}
	d, ws := newDispatcher(m, "")
	d.Requested(context.Background(), approvalOf(KindInfra, nil), TicketRef{WorkspaceID: ws})
	d.Requested(context.Background(), approvalOf(KindInfra, nil), TicketRef{WorkspaceID: uuid.New()}) // resolver errors
	d.Requested(context.Background(), approvalOf(KindInfra, nil), TicketRef{})                        // ticket without a project
	var nilD *Dispatcher
	nilD.Requested(context.Background(), approvalOf(KindInfra, nil), TicketRef{})
	nonEmailOwner, ws2 := newDispatcher(m, "user-id-123")
	nonEmailOwner.Requested(context.Background(), approvalOf(KindInfra, nil), TicketRef{WorkspaceID: ws2})
	if len(m.sent) != 0 {
		t.Errorf("sent %d, want none", len(m.sent))
	}
}

func TestDecided_OnlyPeopleAreMessaged(t *testing.T) {
	m := &fakeMessenger{}
	d, _ := newDispatcher(m, "owner@x.io")
	decidedBy := "owner@x.io"
	person := approvalOf(KindInfra, func(a *Approval) { a.RequestedBy = "dev@x.io"; a.Status = StatusApproved; a.DecidedBy = &decidedBy })
	d.Decided(context.Background(), person, TicketRef{TitleName: "goq-x"})
	session := approvalOf(KindInfra, func(a *Approval) { a.Status = StatusRejected; a.DecidedBy = &decidedBy })
	d.Decided(context.Background(), session, TicketRef{}) // requester tokenhub-forge polls instead
	self := approvalOf(KindInfra, func(a *Approval) { a.RequestedBy = "owner@x.io"; a.Status = StatusApproved; a.DecidedBy = &decidedBy })
	d.Decided(context.Background(), self, TicketRef{})
	if len(m.sent) != 1 || m.sent[0].to != "dev@x.io" || !strings.Contains(m.sent[0].content, "승인됨") {
		t.Fatalf("want exactly one DM to dev@x.io, got %+v", m.sent)
	}
}

func TestWithdrawn_MirrorsRequested(t *testing.T) {
	m := &fakeMessenger{}
	d, ws := newDispatcher(m, "owner@x.io")
	d.Withdrawn(context.Background(), approvalOf(KindInfra, nil), TicketRef{WorkspaceID: ws})
	d.Withdrawn(context.Background(), approvalOf(KindMerge, nil), TicketRef{WorkspaceID: ws}) // owner was never told
	if len(m.sent) != 1 || !strings.Contains(m.sent[0].content, "철회") {
		t.Errorf("%+v", m.sent)
	}
}

func TestMessageIsSafeAndOnlyInformational(t *testing.T) {
	m := &fakeMessenger{}
	d, ws := newDispatcher(m, "owner@x.io")
	a := approvalOf(KindInfra, func(a *Approval) {
		a.RequestNote = "ping @**all** `x`\nsecond line [click](http://evil)"
		a.RequestedByLabel = "desk @_**boss**"
		a.Evidence = []EvidenceItem{{Type: "comment", Ref: "TOPSECRET-EVIDENCE-BODY"}}
		a.Subject.Doc = "x`y"
	})
	d.Requested(context.Background(), a, TicketRef{TitleName: "goq-x", WorkspaceID: ws})
	if len(m.sent) != 1 {
		t.Fatal("no message")
	}
	c := m.sent[0].content
	if strings.Contains(c, "@**") || strings.Contains(c, "@_**") || strings.Contains(c, "@all") {
		t.Errorf("a mention can be triggered: %q", c)
	}
	if strings.Contains(c, "TOPSECRET-EVIDENCE-BODY") {
		t.Error("evidence must never be in a message")
	}
	if strings.Count(c, "`")%2 != 0 || strings.Contains(c, "x`y") {
		t.Errorf("backticks must not break out of code spans: %q", c)
	}
	if !strings.Contains(c, "https://goquest.example/approvals?approval="+a.ID.String()) {
		t.Errorf("missing inbox link: %q", c)
	}
	if !strings.Contains(c, "승인할 수 없습니다") {
		t.Errorf("must state it is not a way to approve: %q", c)
	}
}

func TestDeliver_RetriesThenGivesUp(t *testing.T) {
	m := &fakeMessenger{failures: 2}
	d, ws := newDispatcher(m, "owner@x.io")
	d.Requested(context.Background(), approvalOf(KindInfra, nil), TicketRef{WorkspaceID: ws})
	if m.calls != 3 || len(m.sent) != 1 {
		t.Errorf("want delivery on the 3rd attempt, calls=%d sent=%d", m.calls, len(m.sent))
	}
	m2 := &fakeMessenger{failures: 99}
	d2, ws2 := newDispatcher(m2, "owner@x.io")
	d2.Requested(context.Background(), approvalOf(KindInfra, nil), TicketRef{WorkspaceID: ws2})
	if m2.calls != 3 || len(m2.sent) != 0 {
		t.Errorf("must stop after 3 attempts, calls=%d", m2.calls)
	}
}
