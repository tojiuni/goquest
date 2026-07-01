package vcs

import (
	"context"
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
func (f *fakeTickets) Update(_ context.Context, id uuid.UUID, req ticket.UpdateTicketReq) (*ticket.Ticket, error) {
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
		Kind: PRMerged, Repo: "tojiuni/goquest", Branch: "feature/goq-foo",
		PRNumber: 12, PRTitle: "goq-foo done", PRMerged: true, URL: "https://gh/pr/12"})
	if err != nil {
		t.Fatal(err)
	}
	if tk.Status != ticket.StatusCompleted {
		t.Fatalf("PR merge did not complete ticket: %s", tk.Status)
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
