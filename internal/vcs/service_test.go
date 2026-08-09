package vcs

import (
	"context"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/comment"
	"github.com/tojiuni/goquest/internal/ticket"
)

// --- fakes ---

type fakeStore struct {
	repos    map[string]*VcsRepo // key provider|repo
	links    []*VcsLink
	branchLk map[string]*VcsLink // key provider|repo|branch
}

func newFakeStore() *fakeStore {
	return &fakeStore{repos: map[string]*VcsRepo{}, branchLk: map[string]*VcsLink{}}
}
func (f *fakeStore) CreateRepo(_ context.Context, r *VcsRepo) error {
	f.repos[r.Provider+"|"+r.RepoFullName] = r
	return nil
}
func (f *fakeStore) GetRepo(_ context.Context, p, r string) (*VcsRepo, error) {
	if v, ok := f.repos[p+"|"+r]; ok {
		return v, nil
	}
	return nil, ErrNotFound
}
func (f *fakeStore) ListRepos(context.Context) ([]*VcsRepo, error) { return nil, nil }
func (f *fakeStore) UpsertLink(_ context.Context, l *VcsLink) (*VcsLink, error) {
	if l.ID == uuid.Nil {
		l.ID = uuid.New()
	}
	f.links = append(f.links, l)
	if l.Kind == KindBranch {
		f.branchLk[l.Provider+"|"+l.RepoFullName+"|"+l.ExternalRef] = l
	}
	return l, nil
}
func (f *fakeStore) GetBranchLink(_ context.Context, p, r, b string) (*VcsLink, error) {
	if v, ok := f.branchLk[p+"|"+r+"|"+b]; ok {
		return v, nil
	}
	return nil, ErrNotFound
}
func (f *fakeStore) LinksForTicket(context.Context, uuid.UUID) ([]VcsLink, error) { return nil, nil }

// fakeTickets ignores the projectID argument entirely — it is blind to
// per-project scoping and cannot exercise goq-vcs-link-cross-project-titlename
// or its ambiguity path. Use projectScopedFakeTickets (below) for anything
// project-scoping-sensitive; keep this one for tests that only care about
// whether a title_name resolves at all.
type fakeTickets struct {
	byName map[string]*ticket.Ticket // title_name -> ticket
	trans  []ticket.Status           // record of applied statuses
}

func (f *fakeTickets) GetByProjectAndTitleName(_ context.Context, _ uuid.UUID, tn string) (*ticket.Ticket, error) {
	if t, ok := f.byName[tn]; ok {
		return t, nil
	}
	return nil, ticket.ErrNotFound
}
func (f *fakeTickets) GetByTitleNameAnyProject(_ context.Context, tn string) (*ticket.Ticket, error) {
	if t, ok := f.byName[tn]; ok {
		return t, nil
	}
	return nil, ticket.ErrNotFound
}
func (f *fakeTickets) Update(_ context.Context, id uuid.UUID, req ticket.UpdateTicketReq, _ string) (*ticket.Ticket, error) {
	// find and mutate the ticket to emulate the state machine's forward moves
	for _, t := range f.byName {
		if t.ID == id && req.Status != nil {
			t.Status = *req.Status
			f.trans = append(f.trans, *req.Status)
			return t, nil
		}
	}
	return nil, ticket.ErrNotFound
}

type fakeComments struct{ n int }

func (f *fakeComments) Create(context.Context, uuid.UUID, string, comment.AuthorKind, comment.CreateReq) (*comment.Comment, error) {
	f.n++
	return &comment.Comment{}, nil
}

func repoFixture(auto bool) *VcsRepo {
	return &VcsRepo{ID: uuid.New(), Provider: "github", RepoFullName: "tojiuni/goquest",
		ProjectID: uuid.New(), AutoTransition: auto, MergeTarget: "completed", Active: true}
}

func TestProcessBranchCreatedLinksAndStartsWork(t *testing.T) {
	st := newFakeStore()
	repo := repoFixture(true)
	_ = st.CreateRepo(context.Background(), repo)
	tk := &ticket.Ticket{ID: uuid.New(), TitleName: "goq-foo", Status: ticket.StatusPending}
	tickets := &fakeTickets{byName: map[string]*ticket.Ticket{"goq-foo": tk}}
	comments := &fakeComments{}
	svc := NewService(st, tickets, comments)

	err := svc.Process(context.Background(), "github", VcsEvent{
		Kind: BranchCreated, Repo: "tojiuni/goquest", Branch: "feature/goq-foo", Actor: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.links) != 1 || st.links[0].Kind != KindBranch {
		t.Fatalf("expected 1 branch link, got %+v", st.links)
	}
	// pending -> assigned -> in_progress (two forward moves, no direct edge)
	if len(tickets.trans) != 2 || tickets.trans[0] != ticket.StatusAssigned || tickets.trans[1] != ticket.StatusInProgress {
		t.Fatalf("expected assigned then in_progress, got %v", tickets.trans)
	}
}

func TestProcessCommitInheritsBranchTicket(t *testing.T) {
	st := newFakeStore()
	_ = st.CreateRepo(context.Background(), repoFixture(true))
	tk := &ticket.Ticket{ID: uuid.New(), TitleName: "goq-foo", Status: ticket.StatusInProgress}
	tickets := &fakeTickets{byName: map[string]*ticket.Ticket{"goq-foo": tk}}
	svc := NewService(st, tickets, &fakeComments{})
	// seed a branch link for feature/goq-foo
	_, _ = st.UpsertLink(context.Background(), &VcsLink{TicketID: tk.ID, Provider: "github",
		RepoFullName: "tojiuni/goquest", Kind: KindBranch, ExternalRef: "feature/goq-foo", State: StateOpen})

	// commit whose MESSAGE has no title_name, pushed to the linked branch
	err := svc.Process(context.Background(), "github", VcsEvent{
		Kind: CommitPushed, Repo: "tojiuni/goquest", Branch: "feature/goq-foo",
		CommitSHA: "abc123", CommitMsg: "no ref here"})
	if err != nil {
		t.Fatal(err)
	}
	var gotCommit bool
	for _, l := range st.links {
		if l.Kind == KindCommit && l.TicketID == tk.ID {
			gotCommit = true
		}
	}
	if !gotCommit {
		t.Fatalf("commit did not inherit branch ticket: %+v", st.links)
	}
}

func TestProcessPRMergedCompletes(t *testing.T) {
	st := newFakeStore()
	_ = st.CreateRepo(context.Background(), repoFixture(true))
	tk := &ticket.Ticket{ID: uuid.New(), TitleName: "goq-foo", Status: ticket.StatusInProgress}
	tickets := &fakeTickets{byName: map[string]*ticket.Ticket{"goq-foo": tk}}
	svc := NewService(st, tickets, &fakeComments{})
	err := svc.Process(context.Background(), "github", VcsEvent{
		Kind: PRMerged, Repo: "tojiuni/goquest", Branch: "feature/goq-foo", PRBaseRef: "main",
		PRNumber: 12, PRTitle: "goq-foo done", PRMerged: true, URL: "https://gh/pr/12"})
	if err != nil {
		t.Fatal(err)
	}
	if tk.Status != ticket.StatusCompleted {
		t.Fatalf("PR merge did not complete ticket: %s", tk.Status)
	}
	var prLink *VcsLink
	for _, l := range st.links {
		if l.Kind == KindPR {
			prLink = l
		}
	}
	if prLink == nil {
		t.Fatalf("expected a PR link, got %+v", st.links)
	}
	if prLink.SourceBranch != "feature/goq-foo" || prLink.TargetBranch != "main" {
		t.Fatalf("bad PR link branches: %+v", prLink)
	}
}

func TestProcessPRMergedHighRiskRoutesToPendingApproval(t *testing.T) {
	st := newFakeStore()
	_ = st.CreateRepo(context.Background(), repoFixture(true))
	tk := &ticket.Ticket{ID: uuid.New(), TitleName: "goq-foo", Status: ticket.StatusInProgress, RiskTier: ticket.RiskHigh}
	tickets := &fakeTickets{byName: map[string]*ticket.Ticket{"goq-foo": tk}}
	svc := NewService(st, tickets, &fakeComments{})
	err := svc.Process(context.Background(), "github", VcsEvent{
		Kind: PRMerged, Repo: "tojiuni/goquest", Branch: "feature/goq-foo", PRBaseRef: "main",
		PRNumber: 12, PRTitle: "goq-foo done", PRMerged: true, URL: "https://gh/pr/12"})
	if err != nil {
		t.Fatal(err)
	}
	if tk.Status != ticket.StatusPendingApproval {
		t.Fatalf("high-risk PR merge should route to pending_approval, got %s", tk.Status)
	}
}

func TestProcessPRMergedLowRiskCompletes(t *testing.T) {
	for _, rt := range []ticket.RiskTier{ticket.RiskLow, ""} {
		st := newFakeStore()
		_ = st.CreateRepo(context.Background(), repoFixture(true))
		tk := &ticket.Ticket{ID: uuid.New(), TitleName: "goq-foo", Status: ticket.StatusInProgress, RiskTier: rt}
		tickets := &fakeTickets{byName: map[string]*ticket.Ticket{"goq-foo": tk}}
		svc := NewService(st, tickets, &fakeComments{})
		err := svc.Process(context.Background(), "github", VcsEvent{
			Kind: PRMerged, Repo: "tojiuni/goquest", Branch: "feature/goq-foo", PRBaseRef: "main",
			PRNumber: 12, PRTitle: "goq-foo done", PRMerged: true, URL: "https://gh/pr/12"})
		if err != nil {
			t.Fatal(err)
		}
		if tk.Status != ticket.StatusCompleted {
			t.Fatalf("risk_tier=%q PR merge should complete, got %s", rt, tk.Status)
		}
	}
}

// Reproduces goq-vcs-mention-vs-impl: a PR that implements goq-impl (its branch)
// but merely *mentions* goq-other in prose must link both tickets yet transition
// only goq-impl. Before the fix, the backtick body mention auto-completed the
// wrong ticket (PR #60 → goq-lymphhub-cli-sso).
func TestProcessPRBodyMentionLinksButDoesNotTransition(t *testing.T) {
	st := newFakeStore()
	_ = st.CreateRepo(context.Background(), repoFixture(true))
	impl := &ticket.Ticket{ID: uuid.New(), TitleName: "goq-impl", Status: ticket.StatusInProgress}
	other := &ticket.Ticket{ID: uuid.New(), TitleName: "goq-other", Status: ticket.StatusInProgress}
	tickets := &fakeTickets{byName: map[string]*ticket.Ticket{"goq-impl": impl, "goq-other": other}}
	svc := NewService(st, tickets, &fakeComments{})

	err := svc.Process(context.Background(), "github", VcsEvent{
		Kind: PRMerged, Repo: "tojiuni/goquest", Branch: "feature/goq-impl", PRBaseRef: "main",
		PRNumber: 60, PRTitle: "implement goq-impl",
		PRBody:   "actor fix; the deeper solution is handled in `goq-other`.",
		PRMerged: true, URL: "https://gh/pr/60"})
	if err != nil {
		t.Fatal(err)
	}

	// Rich linking: both referenced tickets get a link.
	var linkedImpl, linkedOther bool
	for _, l := range st.links {
		if l.TicketID == impl.ID {
			linkedImpl = true
		}
		if l.TicketID == other.ID {
			linkedOther = true
		}
	}
	if !linkedImpl || !linkedOther {
		t.Fatalf("both referenced tickets should be linked: impl=%v other=%v", linkedImpl, linkedOther)
	}
	// Conservative transitions: only the head-ref ticket moves.
	if impl.Status != ticket.StatusCompleted {
		t.Fatalf("head-ref ticket should complete, got %s", impl.Status)
	}
	if other.Status != ticket.StatusInProgress {
		t.Fatalf("prose-mentioned ticket must NOT transition, got %s", other.Status)
	}
}

func TestProcessTerminalGuard(t *testing.T) {
	st := newFakeStore()
	_ = st.CreateRepo(context.Background(), repoFixture(true))
	tk := &ticket.Ticket{ID: uuid.New(), TitleName: "goq-foo", Status: ticket.StatusCompleted}
	tickets := &fakeTickets{byName: map[string]*ticket.Ticket{"goq-foo": tk}}
	svc := NewService(st, tickets, &fakeComments{})
	_ = svc.Process(context.Background(), "github", VcsEvent{
		Kind: PROpened, Repo: "tojiuni/goquest", Branch: "goq-foo", PRNumber: 3, PRTitle: "goq-foo"})
	if len(tickets.trans) != 0 {
		t.Fatalf("terminal ticket must not transition, got %v", tickets.trans)
	}
}

func TestProcessUnregisteredRepoIgnored(t *testing.T) {
	svc := NewService(newFakeStore(), &fakeTickets{byName: map[string]*ticket.Ticket{}}, &fakeComments{})
	if err := svc.Process(context.Background(), "github", VcsEvent{Kind: PROpened, Repo: "x/y"}); err != nil {
		t.Fatalf("unregistered repo should be a no-op, got %v", err)
	}
}

func TestProcessAutoTransitionOff(t *testing.T) {
	st := newFakeStore()
	_ = st.CreateRepo(context.Background(), repoFixture(false)) // auto_transition=false
	tk := &ticket.Ticket{ID: uuid.New(), TitleName: "goq-foo", Status: ticket.StatusPending}
	tickets := &fakeTickets{byName: map[string]*ticket.Ticket{"goq-foo": tk}}
	svc := NewService(st, tickets, &fakeComments{})
	_ = svc.Process(context.Background(), "github", VcsEvent{Kind: BranchCreated, Repo: "tojiuni/goquest", Branch: "goq-foo"})
	if len(st.links) != 1 {
		t.Fatalf("link should still be recorded")
	}
	if len(tickets.trans) != 0 {
		t.Fatalf("auto_transition=false must not transition")
	}
}

// projectScopedFakeTickets models real per-project title_name scoping —
// unlike fakeTickets above (which ignores the projectID argument
// entirely and so cannot reproduce goq-vcs-link-cross-project-titlename),
// this fake only returns a ticket from GetByProjectAndTitleName when it
// actually lives in the requested project, matching the real store's
// tickets_project_title_name_key semantics.
type projectScopedFakeTickets struct {
	byProject map[uuid.UUID]map[string]*ticket.Ticket // projectID -> title_name -> ticket
	trans     []ticket.Status
}

func (f *projectScopedFakeTickets) GetByProjectAndTitleName(_ context.Context, projectID uuid.UUID, tn string) (*ticket.Ticket, error) {
	if m, ok := f.byProject[projectID]; ok {
		if t, ok := m[tn]; ok {
			return t, nil
		}
	}
	return nil, ticket.ErrNotFound
}

func (f *projectScopedFakeTickets) GetByTitleNameAnyProject(_ context.Context, tn string) (*ticket.Ticket, error) {
	var found *ticket.Ticket
	count := 0
	for _, m := range f.byProject {
		if t, ok := m[tn]; ok {
			found = t
			count++
		}
	}
	switch count {
	case 0:
		return nil, ticket.ErrNotFound
	case 1:
		return found, nil
	default:
		return nil, ticket.ErrAmbiguousTitleName
	}
}

func (f *projectScopedFakeTickets) Update(_ context.Context, id uuid.UUID, req ticket.UpdateTicketReq, _ string) (*ticket.Ticket, error) {
	for _, m := range f.byProject {
		for _, t := range m {
			if t.ID == id && req.Status != nil {
				t.Status = *req.Status
				f.trans = append(f.trans, *req.Status)
				return t, nil
			}
		}
	}
	return nil, ticket.ErrNotFound
}

// TestProcessBranchCreated_TicketInDifferentProject_LinksViaFallback
// reproduces goq-vcs-link-cross-project-titlename: the repo is registered
// under one project, but the ticket the branch implements is filed under a
// different project (a normal case in this workspace — a repo's own
// project and the project a given ticket lives in are not always the
// same). Before the fix, GetByProjectAndTitleName's project-scoped miss
// silently dropped the ticket and nothing linked or transitioned; the
// resolveTicketByTitleName fallback must find it via
// GetByTitleNameAnyProject.
func TestProcessBranchCreated_TicketInDifferentProject_LinksViaFallback(t *testing.T) {
	st := newFakeStore()
	repo := repoFixture(true)
	_ = st.CreateRepo(context.Background(), repo)

	ticketProject := uuid.New() // deliberately NOT repo.ProjectID
	tk := &ticket.Ticket{ID: uuid.New(), TitleName: "goq-foo", Status: ticket.StatusPending}
	tickets := &projectScopedFakeTickets{byProject: map[uuid.UUID]map[string]*ticket.Ticket{
		ticketProject: {"goq-foo": tk},
	}}
	svc := NewService(st, tickets, &fakeComments{})

	err := svc.Process(context.Background(), "github", VcsEvent{
		Kind: BranchCreated, Repo: "tojiuni/goquest", Branch: "feature/goq-foo", Actor: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.links) != 1 || st.links[0].Kind != KindBranch {
		t.Fatalf("expected the ticket to link via cross-project fallback, got %+v", st.links)
	}
	// pending -> assigned -> in_progress, same as TestProcessBranchCreatedLinksAndStartsWork
	if len(tickets.trans) != 2 || tickets.trans[0] != ticket.StatusAssigned || tickets.trans[1] != ticket.StatusInProgress {
		t.Fatalf("expected assigned then in_progress via fallback, got %v", tickets.trans)
	}
}

// TestProcessBranchCreated_ProjectScopedLookupWinsOverFallback pins
// resolveTicketByTitleName's precedence: the repo's own project-scoped
// match must win even when the SAME title_name also exists in a different
// project. If the fallback were tried first (or the two were merged into
// one query), this would either hit ErrAmbiguousTitleName and link
// nothing, or link the wrong project's ticket — either way, the wrong
// project's ticket (repoTk, never the otherTk) must be the one that ends
// up linked and transitioned.
func TestProcessBranchCreated_ProjectScopedLookupWinsOverFallback(t *testing.T) {
	st := newFakeStore()
	repo := repoFixture(true)
	_ = st.CreateRepo(context.Background(), repo)

	repoTk := &ticket.Ticket{ID: uuid.New(), TitleName: "goq-foo", Status: ticket.StatusPending}
	otherTk := &ticket.Ticket{ID: uuid.New(), TitleName: "goq-foo", Status: ticket.StatusPending}
	otherProject := uuid.New()
	tickets := &projectScopedFakeTickets{byProject: map[uuid.UUID]map[string]*ticket.Ticket{
		repo.ProjectID: {"goq-foo": repoTk},
		otherProject:   {"goq-foo": otherTk},
	}}
	svc := NewService(st, tickets, &fakeComments{})

	err := svc.Process(context.Background(), "github", VcsEvent{
		Kind: BranchCreated, Repo: "tojiuni/goquest", Branch: "feature/goq-foo", Actor: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.links) != 1 || st.links[0].TicketID != repoTk.ID {
		t.Fatalf("expected the repo's own ticket %s to link, got %+v", repoTk.ID, st.links)
	}
	if len(tickets.trans) != 2 || tickets.trans[0] != ticket.StatusAssigned || tickets.trans[1] != ticket.StatusInProgress {
		t.Fatalf("expected assigned then in_progress on the repo's own ticket, got %v", tickets.trans)
	}
	if otherTk.Status != ticket.StatusPending {
		t.Fatalf("the other project's ticket must be untouched, got status %v", otherTk.Status)
	}
}

// TestProcessBranchCreated_AmbiguousAcrossProjects_SkipsLink verifies the
// fallback refuses to guess when the title_name exists in more than one
// project (neither of them the repo's own) — title_name is only unique
// within a project, so this is a real, expected collision case, not a bug
// in the fake. Silently linking the wrong ticket would be worse than not
// linking at all (see goq-vcs-mention-vs-impl for the precedent).
func TestProcessBranchCreated_AmbiguousAcrossProjects_SkipsLink(t *testing.T) {
	st := newFakeStore()
	repo := repoFixture(true)
	_ = st.CreateRepo(context.Background(), repo)

	otherProjectA, otherProjectB := uuid.New(), uuid.New()
	tickets := &projectScopedFakeTickets{byProject: map[uuid.UUID]map[string]*ticket.Ticket{
		otherProjectA: {"goq-foo": {ID: uuid.New(), TitleName: "goq-foo", Status: ticket.StatusPending}},
		otherProjectB: {"goq-foo": {ID: uuid.New(), TitleName: "goq-foo", Status: ticket.StatusPending}},
	}}
	svc := NewService(st, tickets, &fakeComments{})

	err := svc.Process(context.Background(), "github", VcsEvent{
		Kind: BranchCreated, Repo: "tojiuni/goquest", Branch: "feature/goq-foo", Actor: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.links) != 0 {
		t.Fatalf("ambiguous cross-project match must not link anything, got %+v", st.links)
	}
	if len(tickets.trans) != 0 {
		t.Fatalf("ambiguous cross-project match must not transition anything, got %v", tickets.trans)
	}
}

// TestProcessBranchCreated_HyphenPrefixedBranch_ResolvesAndTransitions
// reproduces the real-world failure this fix addresses: a branch name like
// "worktree-goq-foo" (produced by this workspace's EnterWorktree tool,
// which prefixes with "worktree-" joined by a hyphen, not a slash) causes
// ExtractTitleNames to greedily match the WHOLE string as one token
// ("worktree-goq-foo"), which never resolves to any ticket directly. Before
// this fix, the ticket only ever linked via prose (the PR title matching
// the bare title_name), which is headRef=false and so never drove a status
// transition -- confirmed live against tojiuni/goquest with a real branch,
// PR, and merge (see goq-vcs-resolve-hyphen-prefixed-branch). Verifies both
// linking AND transitioning now work from the branch name alone.
func TestProcessBranchCreated_HyphenPrefixedBranch_ResolvesAndTransitions(t *testing.T) {
	st := newFakeStore()
	repo := repoFixture(true)
	_ = st.CreateRepo(context.Background(), repo)

	tk := &ticket.Ticket{ID: uuid.New(), TitleName: "goq-foo", Status: ticket.StatusPending}
	tickets := &fakeTickets{byName: map[string]*ticket.Ticket{"goq-foo": tk}}
	svc := NewService(st, tickets, &fakeComments{})

	err := svc.Process(context.Background(), "github", VcsEvent{
		Kind: BranchCreated, Repo: "tojiuni/goquest", Branch: "worktree-goq-foo", Actor: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.links) != 1 || st.links[0].Kind != KindBranch {
		t.Fatalf("expected the ticket to link from the hyphen-prefixed branch name, got %+v", st.links)
	}
	if len(tickets.trans) != 2 || tickets.trans[0] != ticket.StatusAssigned || tickets.trans[1] != ticket.StatusInProgress {
		t.Fatalf("expected assigned then in_progress from the hyphen-prefixed branch name, got %v", tickets.trans)
	}
}

// TestTitleNameCandidates covers the suffix-stripping helper directly: the
// full token always comes first (so an unprefixed title_name resolves on
// the first try, unchanged from before this fix), followed by
// progressively shorter suffixes, stopping once fewer than 2 hyphen-joined
// parts remain (a single trailing word like "test" or "bar" is never a
// valid title_name shape and would only add false-positive-link risk).
func TestTitleNameCandidates(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"no prefix", "goq-foo", []string{"goq-foo"}},
		{"one prefix segment", "worktree-goq-foo", []string{"worktree-goq-foo", "goq-foo"}},
		{"many prefix segments", "worktree-goq-vcs-merge-autocomplete-test", []string{
			"worktree-goq-vcs-merge-autocomplete-test",
			"goq-vcs-merge-autocomplete-test",
			"vcs-merge-autocomplete-test",
			"merge-autocomplete-test",
			"autocomplete-test",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := titleNameCandidates(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("titleNameCandidates(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
