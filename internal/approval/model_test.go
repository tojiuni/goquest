package approval

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func ptr[T any](v T) *T { return &v }

func TestCanTransition(t *testing.T) {
	all := []Status{StatusPending, StatusApproved, StatusRejected, StatusRevisionRequested, StatusWithdrawn, StatusSuperseded}
	allowed := map[[2]Status]bool{
		{StatusPending, StatusApproved}:             true,
		{StatusPending, StatusRejected}:             true,
		{StatusPending, StatusRevisionRequested}:    true,
		{StatusPending, StatusWithdrawn}:            true,
		{StatusPending, StatusSuperseded}:           true,
		{StatusApproved, StatusSuperseded}:          true,
		{StatusRevisionRequested, StatusSuperseded}: true,
	}
	for _, from := range all {
		for _, to := range all {
			if got, want := CanTransition(from, to), allowed[[2]Status{from, to}]; got != want {
				t.Errorf("CanTransition(%s,%s)=%v want %v", from, to, got, want)
			}
		}
	}
	for _, s := range []Status{StatusRejected, StatusWithdrawn, StatusSuperseded} {
		if !s.Terminal() {
			t.Errorf("%s should be terminal", s)
		}
	}
	if StatusPending.Terminal() || StatusApproved.Terminal() {
		t.Error("pending/approved are not terminal")
	}
}

func TestSubjectValidate(t *testing.T) {
	cases := []struct {
		name string
		kind Kind
		s    Subject
		wd   *bool
		ok   bool
	}{
		{"merge ok", KindMerge, Subject{Repo: "o/r", PR: 1, HeadSHA: "abc"}, nil, true},
		{"merge missing sha", KindMerge, Subject{Repo: "o/r", PR: 1}, nil, false},
		{"design ok", KindDesign, Subject{Doc: "goquest/spec/x.md"}, nil, true},
		{"design missing doc", KindDesign, Subject{}, nil, false},
		{"infra target", KindInfra, Subject{Target: "vault:tekton-ci"}, nil, true},
		{"infra repo+pr", KindOps, Subject{Repo: "o/r", PR: 2}, nil, true},
		{"infra empty", KindInfra, Subject{}, nil, false},
		{"deviation ok", KindDeviation, Subject{Doc: "d.md"}, ptr(true), true},
		{"deviation no direction", KindDeviation, Subject{Doc: "d.md"}, nil, false},
		{"deviation no doc", KindDeviation, Subject{}, ptr(false), false},
		{"ticket ok", KindTicket, Subject{Ticket: "t"}, nil, true},
		{"unknown", Kind("x"), Subject{}, nil, false},
	}
	for _, c := range cases {
		err := c.s.Validate(c.kind, c.wd)
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v ok=%v", c.name, err, c.ok)
		}
		if err != nil && !errors.Is(err, ErrInvalidSubject) {
			t.Errorf("%s: want ErrInvalidSubject, got %v", c.name, err)
		}
	}
}

func TestDelegableCeiling(t *testing.T) {
	cases := []struct {
		kind Kind
		wd   *bool
		want bool
	}{
		{KindMerge, nil, true},
		{KindDeviation, ptr(true), true},
		{KindDeviation, ptr(false), false},
		{KindDeviation, nil, false},
		{KindDesign, nil, false},
		{KindInfra, nil, false},
		{KindOps, nil, false},
		{KindTicket, nil, false},
	}
	for _, c := range cases {
		if got := Delegable(c.kind, c.wd); got != c.want {
			t.Errorf("Delegable(%s,%v)=%v want %v", c.kind, c.wd, got, c.want)
		}
	}
}

func mergeReq(tid uuid.UUID, sha string) NewReq {
	return NewReq{TicketID: tid, Kind: KindMerge, RequestedBy: "desk",
		Subject: Subject{Repo: "o/r", PR: 7, HeadSHA: sha}}
}

func TestMemStore_CreateAndDecide(t *testing.T) {
	ctx := context.Background()
	st, tid := NewMemStore(), uuid.New()
	a, err := st.Create(ctx, mergeReq(tid, "a1"))
	if err != nil || a.Status != StatusPending {
		t.Fatalf("create: %v %v", a, err)
	}
	d := &Decision{DecidedBy: "user", Basis: BasisUserDirect, Note: "ok"}
	got, err := st.Transition(ctx, a.ID, StatusPending, StatusApproved, "user", "ok", d)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusApproved || got.DecidedBy == nil || *got.DecidedBy != "user" || len(got.Events) != 1 {
		t.Fatalf("decision not recorded: %+v", got)
	}
	// terminal / stale: deciding again from pending fails because the row moved
	if _, err := st.Transition(ctx, a.ID, StatusPending, StatusRejected, "user", "", d); !errors.Is(err, ErrStale) {
		t.Errorf("want ErrStale, got %v", err)
	}
	// graph forbids rejected -> approved even with matching from
	if _, err := st.Transition(ctx, a.ID, StatusRejected, StatusApproved, "user", "", d); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("want ErrInvalidTransition, got %v", err)
	}
	if _, err := st.Transition(ctx, uuid.New(), StatusPending, StatusApproved, "u", "", d); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestMemStore_CreateValidates(t *testing.T) {
	st := NewMemStore()
	if _, err := st.Create(context.Background(), NewReq{TicketID: uuid.New(), Kind: KindMerge, RequestedBy: "x"}); !errors.Is(err, ErrInvalidSubject) {
		t.Errorf("want ErrInvalidSubject, got %v", err)
	}
	r := mergeReq(uuid.New(), "a")
	r.RequestedBy = " "
	if _, err := st.Create(context.Background(), r); err == nil {
		t.Error("blank requested_by must fail")
	}
}

func TestMemStore_CreateSupersedesOlderHead(t *testing.T) {
	ctx := context.Background()
	st, tid := NewMemStore(), uuid.New()
	otherPR := mergeReq(tid, "x")
	otherPR.Subject.PR = 8
	other, _ := st.Create(ctx, otherPR)
	otherTicket, _ := st.Create(ctx, mergeReq(uuid.New(), "old1"))

	old1, _ := st.Create(ctx, mergeReq(tid, "old1"))
	old2, err := st.Create(ctx, mergeReq(tid, "old2")) // new head: old1 superseded
	if err != nil {
		t.Fatal(err)
	}
	if old2.SupersedesID == nil || *old2.SupersedesID != old1.ID {
		t.Fatalf("old2 should supersede old1: %+v", old2.SupersedesID)
	}
	if a, _ := st.Get(ctx, old1.ID); a.Status != StatusSuperseded || len(a.Events) != 1 {
		t.Fatalf("old1 not superseded: %+v", a)
	}
	if _, err := st.Transition(ctx, old2.ID, StatusPending, StatusApproved, "u", "", &Decision{DecidedBy: "u", Basis: BasisUserDirect}); err != nil {
		t.Fatal(err)
	}
	fresh, _ := st.Create(ctx, mergeReq(tid, "new")) // approved old2 is superseded too
	if fresh.SupersedesID == nil || *fresh.SupersedesID != old2.ID {
		t.Fatalf("fresh should supersede old2: %+v", fresh.SupersedesID)
	}
	a, _ := st.Get(ctx, old2.ID)
	if a.Status != StatusSuperseded || a.DecidedBy == nil || len(a.Events) != 2 {
		t.Errorf("approved record must keep its decision: %+v", a)
	}
	for id, want := range map[uuid.UUID]Status{fresh.ID: StatusPending, other.ID: StatusPending, otherTicket.ID: StatusPending} {
		if a, _ := st.Get(ctx, id); a.Status != want {
			t.Errorf("%s: status %s want %s", id, a.Status, want)
		}
	}
	// nothing left to supersede
	if ids, _ := st.SupersedeOpenMerge(ctx, tid, "o/r", 7, "new", "desk"); len(ids) != 0 {
		t.Errorf("second call superseded %v", ids)
	}
}

func TestMemStore_DuplicatePending(t *testing.T) {
	ctx := context.Background()
	st, tid := NewMemStore(), uuid.New()
	first, _ := st.Create(ctx, mergeReq(tid, "a"))
	if _, err := st.Create(ctx, mergeReq(tid, "a")); !errors.Is(err, ErrDuplicatePending) {
		t.Fatalf("want ErrDuplicatePending, got %v", err)
	}
	// once decided, the same subject may be requested again
	st.Transition(ctx, first.ID, StatusPending, StatusRejected, "u", "", &Decision{DecidedBy: "u", Basis: BasisUserDirect})
	if _, err := st.Create(ctx, mergeReq(tid, "a")); err != nil {
		t.Errorf("re-request after decision: %v", err)
	}
}

func TestMemStore_ListFilters(t *testing.T) {
	ctx := context.Background()
	st, tid := NewMemStore(), uuid.New()
	st.Create(ctx, mergeReq(tid, "a"))
	d := NewReq{TicketID: tid, Kind: KindDesign, RequestedBy: "x", Subject: Subject{Doc: "d.md"}}
	st.Create(ctx, d)
	if got, _ := st.List(ctx, ListFilter{TicketID: &tid, Kind: KindDesign}); len(got) != 1 {
		t.Errorf("kind filter: %d", len(got))
	}
	if got, _ := st.List(ctx, ListFilter{Repo: "o/r"}); len(got) != 1 {
		t.Errorf("repo filter: %d", len(got))
	}
	if got, _ := st.List(ctx, ListFilter{Status: StatusApproved}); len(got) != 0 {
		t.Errorf("status filter: %d", len(got))
	}
	if got, _ := st.List(ctx, ListFilter{Limit: 1}); len(got) != 1 {
		t.Errorf("limit: %d", len(got))
	}
}
