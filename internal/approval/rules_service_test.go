package approval

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func ruleSvc() (*Service, *MemRuleStore) {
	rs := NewMemRuleStore()
	return NewService(NewMemStore()).WithRules(rs), rs
}

var human = Actor{ID: "owner@x.io", Kind: ActorHuman}

func validRule() CreateRuleInput {
	return CreateRuleInput{Principal: "tokenhub-geneso", Kinds: []Kind{KindMerge}, UserRef: "chat 2026-11-02"}
}

func TestCreateRule_OnlyAPersonMay(t *testing.T) {
	svc, _ := ruleSvc()
	for _, k := range []ActorKind{ActorSession, ActorOther, ""} {
		_, err := svc.CreateRule(context.Background(), uuid.New(), validRule(), Actor{ID: "x", Kind: k})
		wantDenied(t, err, ReasonPrincipalCannotDecide)
	}
	if _, err := svc.CreateRule(context.Background(), uuid.New(), validRule(), Actor{ID: " ", Kind: ActorHuman}); err == nil {
		t.Error("blank creator must fail")
	}
	r, err := svc.CreateRule(context.Background(), uuid.New(), validRule(), human)
	if err != nil || r.CreatedBy != "owner@x.io" || r.ID == uuid.Nil {
		t.Fatalf("human create: %v %+v", err, r)
	}
}

func TestCreateRule_Validation(t *testing.T) {
	svc, _ := ruleSvc()
	past := time.Now().Add(-time.Minute)
	for name, mut := range map[string]func(*CreateRuleInput){
		"email principal":   func(i *CreateRuleInput) { i.Principal = "lyckabc@gmail.com" },
		"infra kind":        func(i *CreateRuleInput) { i.Kinds = []Kind{KindMerge, KindInfra} },
		"no kinds":          func(i *CreateRuleInput) { i.Kinds = nil },
		"missing user_ref":  func(i *CreateRuleInput) { i.UserRef = " " },
		"past expiry":       func(i *CreateRuleInput) { i.ExpiresAt = &past },
		"unknown condition": func(i *CreateRuleInput) { i.Conditions = map[string]any{"vibes": true} },
	} {
		in := validRule()
		mut(&in)
		if _, err := svc.CreateRule(context.Background(), uuid.New(), in, human); !errors.Is(err, ErrInvalidRule) {
			t.Errorf("%s: want ErrInvalidRule, got %v", name, err)
		}
	}
	in := validRule()
	in.Conditions = map[string]any{"require_evidence_types": []any{"ci_run"}}
	if _, err := svc.CreateRule(context.Background(), uuid.New(), in, human); err != nil {
		t.Errorf("known condition: %v", err)
	}
}

func TestRules_ListAndRevoke(t *testing.T) {
	svc, _ := ruleSvc()
	ws, other := uuid.New(), uuid.New()
	a, _ := svc.CreateRule(context.Background(), ws, validRule(), human)
	svc.CreateRule(context.Background(), other, validRule(), human)
	got, _ := svc.ListRules(context.Background(), ws)
	if len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("list must be workspace-scoped: %+v", got)
	}
	// a session cannot revoke; another workspace's id is not found
	wantDenied(t, svc.RevokeRule(context.Background(), ws, a.ID, Actor{ID: "s", Kind: ActorSession}), ReasonPrincipalCannotDecide)
	if err := svc.RevokeRule(context.Background(), other, a.ID, human); !errors.Is(err, ErrRuleNotFound) {
		t.Errorf("cross-workspace revoke: %v", err)
	}
	if err := svc.RevokeRule(context.Background(), ws, a.ID, human); err != nil {
		t.Fatal(err)
	}
	if err := svc.RevokeRule(context.Background(), ws, a.ID, human); !errors.Is(err, ErrRuleNotFound) {
		t.Errorf("second revoke: %v", err)
	}
	got, _ = svc.ListRules(context.Background(), ws)
	if len(got) != 1 || got[0].RevokedAt == nil || got[0].Active(time.Now()) {
		t.Errorf("revoked rule must stay listed but inactive: %+v", got)
	}
}

// A rule created through the API is what Decide later honours.
func TestCreatedRuleDrivesDelegatedDecision(t *testing.T) {
	st := NewMemStore()
	rs := NewMemRuleStore()
	svc := NewService(st).WithRules(rs)
	ws, tid := uuid.New(), uuid.New()
	rule, err := svc.CreateRule(context.Background(), ws, validRule(), human)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := st.Create(context.Background(), NewReq{TicketID: tid, Kind: KindMerge, RequestedBy: "requester",
		Subject: Subject{Repo: "o/r", PR: 1, HeadSHA: "a"}})
	got, err := svc.Decide(context.Background(), DecideInput{TicketID: tid, WorkspaceID: ws, ApprovalID: a.ID,
		Decision: StatusApproved, UserRef: "chat"}, Actor{ID: "tokenhub-geneso", Kind: ActorSession})
	if err != nil || *got.DelegationRuleID != rule.ID {
		t.Fatalf("decide via created rule: %v %+v", err, got)
	}
	// after revocation the same session can no longer decide
	if err := svc.RevokeRule(context.Background(), ws, rule.ID, human); err != nil {
		t.Fatal(err)
	}
	b, _ := st.Create(context.Background(), NewReq{TicketID: tid, Kind: KindMerge, RequestedBy: "requester",
		Subject: Subject{Repo: "o/r", PR: 2, HeadSHA: "a"}})
	_, err = svc.Decide(context.Background(), DecideInput{TicketID: tid, WorkspaceID: ws, ApprovalID: b.ID,
		Decision: StatusApproved, UserRef: "chat"}, Actor{ID: "tokenhub-geneso", Kind: ActorSession})
	wantDenied(t, err, ReasonNoMatchingRule)
}
