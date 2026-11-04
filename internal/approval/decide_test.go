package approval

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type decideEnv struct {
	n     int
	svc   *Service
	store *MemStore
	rules *MemRuleStore
	tid   uuid.UUID
	ws    uuid.UUID
}

func newDecideEnv() *decideEnv {
	st, rs := NewMemStore(), NewMemRuleStore()
	return &decideEnv{svc: NewService(st).WithRules(rs), store: st, rules: rs, tid: uuid.New(), ws: uuid.New()}
}

func (e *decideEnv) request(t *testing.T, req NewReq) *Approval {
	t.Helper()
	req.TicketID = e.tid
	if req.RequestedBy == "" {
		req.RequestedBy = "goquest-session"
	}
	a, err := e.store.Create(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (e *decideEnv) merge(t *testing.T) *Approval {
	e.n++ // distinct PR each call so identical-pending dedupe never trips
	return e.request(t, NewReq{Kind: KindMerge, Subject: Subject{Repo: "tojiuni/goquest", PR: e.n, HeadSHA: "a"}})
}

func (e *decideEnv) rule(t *testing.T, mut func(*DelegationRule)) *DelegationRule {
	t.Helper()
	r := &DelegationRule{WorkspaceID: e.ws, Principal: "desk", Kinds: []Kind{KindMerge}, UserRef: "ticket#1", CreatedBy: "user"}
	if mut != nil {
		mut(r)
	}
	if err := e.rules.Create(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	return r
}

func (e *decideEnv) decide(a *Approval, actor Actor, userRef string) (*Approval, error) {
	return e.svc.Decide(context.Background(), DecideInput{TicketID: e.tid, WorkspaceID: e.ws, ApprovalID: a.ID,
		Decision: StatusApproved, Note: "ok", UserRef: userRef}, actor)
}

func wantDenied(t *testing.T, err error, reason DenyReason) {
	t.Helper()
	var de *DeniedError
	if !errors.As(err, &de) || de.Reason != reason {
		t.Fatalf("want denied(%s), got %v", reason, err)
	}
}

func TestDecide_HumanIsUserDirect(t *testing.T) {
	e := newDecideEnv()
	for _, a := range []*Approval{
		e.merge(t),
		e.request(t, NewReq{Kind: KindInfra, Subject: Subject{Target: "vault:x"}}),
		e.request(t, NewReq{Kind: KindDesign, Subject: Subject{Doc: "d.md"}}),
	} {
		got, err := e.decide(a, Actor{ID: "user@x.io", Kind: ActorHuman}, "")
		if err != nil {
			t.Fatalf("%s: %v", a.Kind, err)
		}
		if got.Status != StatusApproved || *got.DecidedBy != "user@x.io" || *got.Basis != BasisUserDirect || got.DelegationRuleID != nil {
			t.Errorf("%s: %+v", a.Kind, got)
		}
	}
}

func TestDecide_DelegatedHappyPath(t *testing.T) {
	e := newDecideEnv()
	r := e.rule(t, nil)
	got, err := e.decide(e.merge(t), Actor{ID: "desk", Kind: ActorSession}, "chat 2026-10-30 user ok")
	if err != nil {
		t.Fatal(err)
	}
	if *got.Basis != BasisDeskDelegate || got.DelegationRuleID == nil || *got.DelegationRuleID != r.ID || *got.UserRef == "" || *got.DecidedBy != "desk" {
		t.Errorf("%+v", got)
	}
}

func TestDecide_DelegationCeiling(t *testing.T) {
	e := newDecideEnv()
	// A rule listing every kind can only exist by bypassing Validate and the DB
	// CHECK; the ceiling in Decide must hold even then.
	e.rules.rules = append(e.rules.rules, &DelegationRule{ID: uuid.New(), WorkspaceID: e.ws, Principal: "desk",
		Kinds: []Kind{KindMerge, KindDeviation, KindInfra, KindDesign, KindOps, KindTicket}, UserRef: "x", CreatedBy: "u"})
	yes, no := true, false
	denied := []*Approval{
		e.request(t, NewReq{Kind: KindInfra, Subject: Subject{Target: "t"}}),
		e.request(t, NewReq{Kind: KindDesign, Subject: Subject{Doc: "d"}}),
		e.request(t, NewReq{Kind: KindOps, Subject: Subject{Target: "t"}}),
		e.request(t, NewReq{Kind: KindDeviation, Subject: Subject{Doc: "d"}, WithinDirection: &no}),
	}
	for _, a := range denied {
		_, err := e.decide(a, Actor{ID: "desk", Kind: ActorSession}, "ref")
		wantDenied(t, err, ReasonKindNotDelegable)
		if got, _ := e.store.Get(context.Background(), a.ID); got.Status != StatusPending {
			t.Errorf("%s must stay pending", a.Kind)
		}
	}
	ok := e.request(t, NewReq{Kind: KindDeviation, Subject: Subject{Doc: "d2"}, WithinDirection: &yes})
	if _, err := e.decide(ok, Actor{ID: "desk", Kind: ActorSession}, "ref"); err != nil {
		t.Errorf("deviation within direction should be delegable: %v", err)
	}
}

func TestDecide_RuleMatching(t *testing.T) {
	now := time.Now().UTC()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	cases := []struct {
		name  string
		mut   func(*DelegationRule)
		actor Actor
		want  DenyReason // "" = allowed
	}{
		{"no rule at all", nil, Actor{ID: "other-session", Kind: ActorSession}, ReasonNoMatchingRule},
		{"rule for another kind", func(r *DelegationRule) { r.Kinds = []Kind{KindDeviation} }, Actor{ID: "desk", Kind: ActorSession}, ReasonNoMatchingRule},
		{"expired", func(r *DelegationRule) { r.ExpiresAt = &past }, Actor{ID: "desk", Kind: ActorSession}, ReasonNoMatchingRule},
		{"revoked", func(r *DelegationRule) { r.RevokedAt = &past }, Actor{ID: "desk", Kind: ActorSession}, ReasonNoMatchingRule},
		{"not yet expired", func(r *DelegationRule) { r.ExpiresAt = &future }, Actor{ID: "desk", Kind: ActorSession}, ""},
		{"repo glob match", func(r *DelegationRule) { r.Repos = []string{"tojiuni/*"} }, Actor{ID: "desk", Kind: ActorSession}, ""},
		{"repo mismatch", func(r *DelegationRule) { r.Repos = []string{"tojiuni/other"} }, Actor{ID: "desk", Kind: ActorSession}, ReasonNoMatchingRule},
		{"unknown condition fails closed", func(r *DelegationRule) { r.Conditions = map[string]any{"vibes": true} }, Actor{ID: "desk", Kind: ActorSession}, ReasonNoMatchingRule},
		{"required evidence missing", func(r *DelegationRule) { r.Conditions = map[string]any{"require_evidence_types": []any{"ci_run"}} }, Actor{ID: "desk", Kind: ActorSession}, ReasonNoMatchingRule},
	}
	for _, c := range cases {
		e := newDecideEnv()
		if c.mut != nil {
			e.rule(t, c.mut)
		}
		_, err := e.decide(e.merge(t), c.actor, "ref")
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
			continue
		}
		var de *DeniedError
		if !errors.As(err, &de) || de.Reason != c.want {
			t.Errorf("%s: want %s, got %v", c.name, c.want, err)
		}
	}
}

func TestDecide_RuleInOtherWorkspaceDoesNotApply(t *testing.T) {
	e := newDecideEnv()
	e.rule(t, func(r *DelegationRule) { r.WorkspaceID = uuid.New() })
	_, err := e.decide(e.merge(t), Actor{ID: "desk", Kind: ActorSession}, "ref")
	wantDenied(t, err, ReasonNoMatchingRule)
}

func TestDecide_RequiredEvidencePresent(t *testing.T) {
	e := newDecideEnv()
	e.rule(t, func(r *DelegationRule) { r.Conditions = map[string]any{"require_evidence_types": []any{"ci_run"}} })
	a := e.request(t, NewReq{Kind: KindMerge, Subject: Subject{Repo: "o/r", PR: 2, HeadSHA: "b"},
		Evidence: []EvidenceItem{{Type: "ci_run", Ref: "https://ci/1"}}})
	if _, err := e.decide(a, Actor{ID: "desk", Kind: ActorSession}, "ref"); err != nil {
		t.Errorf("evidence present: %v", err)
	}
}

func TestDecide_SelfApprovalAndPrincipals(t *testing.T) {
	e := newDecideEnv()
	e.rule(t, func(r *DelegationRule) { r.Principal = "goquest-session" })
	// the requesting session cannot approve its own request, rule or not
	_, err := e.decide(e.merge(t), Actor{ID: "goquest-session", Kind: ActorSession}, "ref")
	wantDenied(t, err, ReasonSelfApproval)

	e.rule(t, nil)
	for _, k := range []ActorKind{ActorOther, ""} {
		_, err := e.decide(e.merge(t), Actor{ID: "desk", Kind: k}, "ref")
		wantDenied(t, err, ReasonPrincipalCannotDecide)
	}
	_, err = e.decide(e.merge(t), Actor{ID: "", Kind: ActorHuman}, "")
	wantDenied(t, err, ReasonPrincipalCannotDecide)
}

func TestDecide_ValidationAndState(t *testing.T) {
	e := newDecideEnv()
	e.rule(t, nil)
	human := Actor{ID: "u", Kind: ActorHuman}
	a := e.merge(t)
	in := func(d Status, note, ref string) DecideInput {
		return DecideInput{TicketID: e.tid, WorkspaceID: e.ws, ApprovalID: a.ID, Decision: d, Note: note, UserRef: ref}
	}
	for _, bad := range []DecideInput{
		in(StatusWithdrawn, "x", ""), in(StatusSuperseded, "x", ""), in(StatusPending, "x", ""),
		in(StatusRejected, " ", ""), in(StatusRevisionRequested, "", ""),
	} {
		if _, err := e.svc.Decide(context.Background(), bad, human); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("decision %q note %q: want ErrInvalidInput, got %v", bad.Decision, bad.Note, err)
		}
	}
	// delegated decision without user_ref
	if _, err := e.decide(a, Actor{ID: "desk", Kind: ActorSession}, ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("missing user_ref: %v", err)
	}
	if got, _ := e.store.Get(context.Background(), a.ID); got.Status != StatusPending {
		t.Fatal("failed attempts must not change status")
	}
	if _, err := e.svc.Decide(context.Background(), in(StatusRejected, "no", ""), human); err != nil {
		t.Fatal(err)
	}
	// already decided
	if _, err := e.svc.Decide(context.Background(), in(StatusApproved, "ok", ""), human); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("redecide: %v", err)
	}
	// other ticket's id is not found
	b := e.merge(t)
	other := DecideInput{TicketID: uuid.New(), ApprovalID: b.ID, Decision: StatusApproved}
	if _, err := e.svc.Decide(context.Background(), other, human); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-ticket: %v", err)
	}
}

func TestDecide_NoRuleStoreMeansSessionsNeverDecide(t *testing.T) {
	st := NewMemStore()
	svc := NewService(st)
	tid := uuid.New()
	a, _ := st.Create(context.Background(), NewReq{TicketID: tid, Kind: KindMerge, RequestedBy: "r", Subject: Subject{Repo: "o/r", PR: 1, HeadSHA: "a"}})
	_, err := svc.Decide(context.Background(), DecideInput{TicketID: tid, WorkspaceID: uuid.New(), ApprovalID: a.ID, Decision: StatusApproved, UserRef: "x"}, Actor{ID: "desk", Kind: ActorSession})
	wantDenied(t, err, ReasonNoMatchingRule)
}

func TestDecide_PersonIDNeverActsOnARule(t *testing.T) {
	e := newDecideEnv()
	// a stale/forged rule naming a person (impossible through Create) still must not work
	e.rules.rules = append(e.rules.rules, &DelegationRule{ID: uuid.New(), WorkspaceID: e.ws, Principal: "lyckabc@gmail.com",
		Kinds: []Kind{KindMerge}, UserRef: "x", CreatedBy: "u"})
	_, err := e.decide(e.merge(t), Actor{ID: "lyckabc@gmail.com", Kind: ActorSession}, "ref")
	wantDenied(t, err, ReasonPrincipalCannotDecide)
}

func TestDelegationRuleValidate(t *testing.T) {
	base := func() *DelegationRule {
		return &DelegationRule{WorkspaceID: uuid.New(), Principal: "tokenhub-geneso", Kinds: []Kind{KindMerge}, CreatedBy: "u"}
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("valid rule: %v", err)
	}
	for name, mut := range map[string]func(*DelegationRule){
		"email principal":  func(r *DelegationRule) { r.Principal = "lyckabc@gmail.com" },
		"blank principal":  func(r *DelegationRule) { r.Principal = "  " },
		"spaced principal": func(r *DelegationRule) { r.Principal = "a b" },
		"no kinds":         func(r *DelegationRule) { r.Kinds = nil },
		"infra kind":       func(r *DelegationRule) { r.Kinds = []Kind{KindMerge, KindInfra} },
		"ticket kind":      func(r *DelegationRule) { r.Kinds = []Kind{KindTicket} },
	} {
		r := base()
		mut(r)
		if err := r.Validate(); !errors.Is(err, ErrInvalidRule) {
			t.Errorf("%s: want ErrInvalidRule, got %v", name, err)
		}
		if err := NewMemRuleStore().Create(context.Background(), r); !errors.Is(err, ErrInvalidRule) {
			t.Errorf("%s: store.Create must validate, got %v", name, err)
		}
	}
}

func TestRecordTicketApproval(t *testing.T) {
	ctx := context.Background()
	tid := uuid.New()
	st := NewMemStore()
	svc := NewService(st)

	a, err := svc.RecordTicketApproval(ctx, tid, "approved", "", Actor{ID: "u@x.io", Kind: ActorHuman}, "")
	if err != nil || a.Kind != KindTicket || a.Status != StatusApproved || a.Legacy || *a.Basis != BasisUserDirect || *a.DecidedBy != "u@x.io" {
		t.Fatalf("person: %v %+v", err, a)
	}
	// an unattested caller: decided_by is recorded, basis is not claimed
	b, err := svc.RecordTicketApproval(ctx, tid, "rejected", "nope", Actor{ID: "bot", Kind: ActorOther}, "boss@x.io")
	if err != nil || b.Basis != nil || !b.Legacy || *b.DecidedBy != "bot" || b.Status != StatusRejected {
		t.Fatalf("agent: %v %+v", err, b)
	}
	if got := *b.DecisionNote; got != "claimed operator_id=boss@x.io; nope" {
		t.Errorf("claimed operator must be noted, not trusted: %q", got)
	}
	// a stale pending record from an earlier failed attempt is reused, not duplicated
	st.Create(ctx, NewReq{TicketID: tid, Kind: KindTicket, RequestedBy: "u@x.io", Subject: Subject{Ticket: tid.String()}})
	c, err := svc.RecordTicketApproval(ctx, tid, "revision", "x", Actor{ID: "u@x.io", Kind: ActorHuman}, "")
	if err != nil || c.Status != StatusRevisionRequested {
		t.Fatalf("reuse pending: %v %+v", err, c)
	}
	all, _ := st.List(ctx, ListFilter{TicketID: &tid})
	if len(all) != 3 {
		t.Errorf("want 3 records, got %d", len(all))
	}
	for _, bad := range []string{"", "weird"} {
		if _, err := svc.RecordTicketApproval(ctx, tid, bad, "", Actor{ID: "u", Kind: ActorHuman}, ""); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("decision %q: %v", bad, err)
		}
	}
	if _, err := svc.RecordTicketApproval(ctx, tid, "approved", "", Actor{}, ""); !errors.Is(err, ErrForbidden) {
		t.Errorf("blank actor: %v", err)
	}
}

func TestSupersedeStaleMerge_AcrossTickets(t *testing.T) {
	ctx := context.Background()
	st := NewMemStore()
	svc := NewService(st)
	t1, t2 := uuid.New(), uuid.New()
	mk := func(tid uuid.UUID, pr int, sha string) *Approval {
		a, err := st.Create(ctx, NewReq{TicketID: tid, Kind: KindMerge, RequestedBy: "r", Subject: Subject{Repo: "o/r", PR: pr, HeadSHA: sha}})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	oldA, oldB := mk(t1, 5, "old"), mk(t2, 5, "old")
	st.Transition(ctx, oldB.ID, StatusPending, StatusApproved, "u", "", &Decision{DecidedBy: "u", Basis: BasisUserDirect})
	current := mk(t1, 5, "new")
	otherPR := mk(t1, 6, "old")
	rejected := mk(t2, 7, "old")
	st.Transition(ctx, rejected.ID, StatusPending, StatusRejected, "u", "", &Decision{DecidedBy: "u", Basis: BasisUserDirect})

	// mk(t1,5,"new") already retired oldA through Create; the webhook then catches the other ticket
	if err := svc.ObservePRHead(ctx, "o/r", 5, "new"); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[uuid.UUID]Status{
		oldA.ID: StatusSuperseded, oldB.ID: StatusSuperseded, current.ID: StatusPending,
		otherPR.ID: StatusPending, rejected.ID: StatusRejected,
	} {
		if a, _ := st.Get(ctx, id); a.Status != want {
			t.Errorf("%s: %s want %s", id, a.Status, want)
		}
	}
	if a, _ := st.Get(ctx, oldB.ID); a.DecidedBy == nil || a.Events[len(a.Events)-1].Actor != "vcs-webhook" {
		t.Errorf("approved decision must survive and the retirement be attributed to the webhook: %+v", a)
	}
	// idempotent, and ignores empty input
	if err := svc.ObservePRHead(ctx, "o/r", 5, "new"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []struct {
		repo string
		pr   int
		sha  string
	}{{"", 5, "x"}, {"o/r", 0, "x"}, {"o/r", 5, ""}} {
		if err := svc.ObservePRHead(ctx, bad.repo, bad.pr, bad.sha); err != nil {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	if a, _ := st.Get(ctx, otherPR.ID); a.Status != StatusPending {
		t.Error("empty input must not retire anything")
	}
}

func TestRequestTicketSignoff(t *testing.T) {
	ctx := context.Background()
	st := NewMemStore()
	svc := NewService(st)
	tid := uuid.New()
	if err := svc.RequestTicketSignoff(ctx, tid, "agent-1", "작업 완료 서명 대기"); err != nil {
		t.Fatal(err)
	}
	got, _ := st.List(ctx, ListFilter{TicketID: &tid})
	if len(got) != 1 || got[0].Kind != KindTicket || got[0].Status != StatusPending || got[0].RequestedBy != "agent-1" || got[0].Legacy {
		t.Fatalf("%+v", got)
	}
	// moving the ticket again while one is pending is not an error and does not duplicate
	if err := svc.RequestTicketSignoff(ctx, tid, "agent-2", "x"); err != nil {
		t.Errorf("duplicate pending must be tolerated: %v", err)
	}
	if got, _ := st.List(ctx, ListFilter{TicketID: &tid}); len(got) != 1 {
		t.Errorf("want 1, got %d", len(got))
	}
	// blank requester falls back to a name rather than failing
	if err := svc.RequestTicketSignoff(ctx, uuid.New(), " ", "n"); err != nil {
		t.Errorf("blank requester: %v", err)
	}
}

func TestDecideTicketApprovalAlsoMovesTheTicket(t *testing.T) {
	ctx := context.Background()
	for decision, wantNote := range map[Status]string{StatusApproved: "", StatusRejected: "no good", StatusRevisionRequested: "redo"} {
		st := NewMemStore()
		var gotDecision Status
		var gotNote, gotOp string
		var calls int
		svc := NewService(st).WithTicketDecider(func(_ context.Context, _ uuid.UUID, d Status, note, op string) error {
			calls++
			gotDecision, gotNote, gotOp = d, note, op
			return nil
		})
		tid := uuid.New()
		svc.RequestTicketSignoff(ctx, tid, "agent", "n")
		list, _ := st.List(ctx, ListFilter{TicketID: &tid})
		note := wantNote
		_, err := svc.Decide(ctx, DecideInput{TicketID: tid, ApprovalID: list[0].ID, Decision: decision, Note: note}, Actor{ID: "owner@x.io", Kind: ActorHuman})
		if err != nil {
			t.Fatalf("%s: %v", decision, err)
		}
		if calls != 1 || gotDecision != decision || gotNote != wantNote || gotOp != "owner@x.io" {
			t.Errorf("%s: decider calls=%d %v %q %q", decision, calls, gotDecision, gotNote, gotOp)
		}
	}
}

func TestDecideOtherKindsNeverTouchTickets(t *testing.T) {
	ctx := context.Background()
	st := NewMemStore()
	called := false
	svc := NewService(st).WithTicketDecider(func(context.Context, uuid.UUID, Status, string, string) error { called = true; return nil })
	tid := uuid.New()
	a, _ := st.Create(ctx, NewReq{TicketID: tid, Kind: KindDesign, RequestedBy: "r", Subject: Subject{Doc: "d.md"}})
	if _, err := svc.Decide(ctx, DecideInput{TicketID: tid, ApprovalID: a.ID, Decision: StatusApproved}, Actor{ID: "u@x.io", Kind: ActorHuman}); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("only kind=ticket approvals move the ticket")
	}
}

func TestDecideTicketApprovalSurvivesATicketSideFailure(t *testing.T) {
	ctx := context.Background()
	st := NewMemStore()
	svc := NewService(st).WithTicketDecider(func(context.Context, uuid.UUID, Status, string, string) error {
		return errors.New("ticket is no longer pending_approval")
	})
	tid := uuid.New()
	svc.RequestTicketSignoff(ctx, tid, "agent", "n")
	list, _ := st.List(ctx, ListFilter{TicketID: &tid})
	got, err := svc.Decide(ctx, DecideInput{TicketID: tid, ApprovalID: list[0].ID, Decision: StatusApproved}, Actor{ID: "u@x.io", Kind: ActorHuman})
	if err != nil || got.Status != StatusApproved {
		t.Errorf("the decision stands even if the ticket was already handled: %v %+v", err, got)
	}
}
