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
	// even a rule that (impossibly, via DB CHECK) lists every kind cannot lift the ceiling
	e.rule(t, func(r *DelegationRule) {
		r.Kinds = []Kind{KindMerge, KindDeviation, KindInfra, KindDesign, KindOps, KindTicket}
	})
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
