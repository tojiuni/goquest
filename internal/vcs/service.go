package vcs

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/comment"
	"github.com/tojiuni/goquest/internal/ticket"
)

// TicketService is the narrow slice of *ticket.Service the linker needs.
type TicketService interface {
	GetByProjectAndTitleName(ctx context.Context, projectID uuid.UUID, titleName string) (*ticket.Ticket, error)
	// GetByTitleNameAnyProject is the cross-project fallback resolveTickets
	// uses when a title_name doesn't resolve in the repo's own registered
	// project — see resolveTickets for why that's a normal, expected case,
	// not an error.
	GetByTitleNameAnyProject(ctx context.Context, titleName string) (*ticket.Ticket, error)
	Update(ctx context.Context, id uuid.UUID, req ticket.UpdateTicketReq, actor string) (*ticket.Ticket, error)
}

// CommentService is the narrow slice of *comment.Service the linker needs.
type CommentService interface {
	Create(ctx context.Context, ticketID uuid.UUID, authorID string, kind comment.AuthorKind, req comment.CreateReq) (*comment.Comment, error)
}

type Service struct {
	store    Store
	tickets  TicketService
	comments CommentService
}

func NewService(store Store, tickets TicketService, comments CommentService) *Service {
	return &Service{store: store, tickets: tickets, comments: comments}
}

func (s *Service) RegisterRepo(ctx context.Context, r *VcsRepo) error {
	return s.store.CreateRepo(ctx, r)
}
func (s *Service) ListRepos(ctx context.Context) ([]*VcsRepo, error) { return s.store.ListRepos(ctx) }
func (s *Service) LinksForTicket(ctx context.Context, id uuid.UUID) ([]VcsLink, error) {
	return s.store.LinksForTicket(ctx, id)
}

// Process links one normalized event to the ticket(s) it references and applies
// auto-transitions. Unregistered repos and unresolved refs are no-ops.
func (s *Service) Process(ctx context.Context, provider string, ev VcsEvent) error {
	repo, err := s.store.GetRepo(ctx, provider, ev.Repo)
	if errors.Is(err, ErrNotFound) {
		return nil // unregistered repo -> ignore
	}
	if err != nil {
		return err
	}

	tickets := s.resolveTickets(ctx, provider, repo, ev)
	kind, ref, state, title := linkShape(ev)
	for _, rt := range tickets {
		t := rt.t
		link := &VcsLink{
			TicketID: t.ID, Provider: provider, RepoFullName: ev.Repo,
			Kind: kind, ExternalRef: ref, URL: ev.URL, State: state, Title: title, Actor: ev.Actor,
		}
		if kind == KindPR {
			link.SourceBranch = ev.Branch
			link.TargetBranch = ev.PRBaseRef
		}
		if _, err := s.store.UpsertLink(ctx, link); err != nil {
			return err
		}
		// Only tickets matched via the head-ref (branch) drive auto-transitions.
		// Prose-only matches (PR title/body, commit message) are linked but never
		// transitioned — see resolveTickets and goq-vcs-mention-vs-impl.
		if repo.AutoTransition && rt.headRef {
			if err := s.autoTransition(ctx, provider, repo, t, ev); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolvedTicket is a ticket an event references, tagged with whether the match
// came from the head-ref (branch) — the implementation signal that gates auto-
// transition — versus prose (PR title/body, commit message), which links only.
type resolvedTicket struct {
	t       *ticket.Ticket
	headRef bool
}

// resolveTickets returns the distinct tickets an event references: title_name
// tokens found in branch/commit/PR text that resolve in the repo's project,
// plus (for commits) the ticket of a linked branch (inheritance). Each result
// is tagged headRef=true when the match came from the branch/PR source ref
// (an implementation signal) rather than prose, so callers can link every
// reference but transition only the ones the change actually implements.
func (s *Service) resolveTickets(ctx context.Context, provider string, repo *VcsRepo, ev VcsEvent) []resolvedTicket {
	idx := map[uuid.UUID]int{}
	var out []resolvedTicket
	add := func(t *ticket.Ticket, headRef bool) {
		if t == nil {
			return
		}
		if i, ok := idx[t.ID]; ok {
			if headRef { // a head-ref match anywhere upgrades the ticket to eligible
				out[i].headRef = true
			}
			return
		}
		idx[t.ID] = len(out)
		out = append(out, resolvedTicket{t: t, headRef: headRef})
	}

	// The head-ref (ev.Branch, the branch / PR source ref) is the implementation
	// signal: a title_name there means this branch/PR implements the ticket.
	// Prose (PR title/body, commit message) merely references it and must not
	// drive a transition — a backtick mention of another ticket in a PR body
	// once auto-completed the wrong ticket. See goq-vcs-mention-vs-impl.
	headRef := map[string]bool{}
	for _, tn := range ExtractTitleNames(ev.Branch) {
		headRef[tn] = true
	}
	for _, tn := range ExtractTitleNames(ev.Branch, ev.CommitMsg, ev.PRTitle, ev.PRBody) {
		if t := s.resolveTicketByTitleName(ctx, repo, tn); t != nil {
			add(t, headRef[tn])
		}
	}
	if ev.Kind == CommitPushed {
		if bl, err := s.store.GetBranchLink(ctx, provider, ev.Repo, ev.Branch); err == nil {
			if t := s.resolveTicketByTitleName(ctx, repo, branchTicketTitleName(bl)); t != nil {
				add(t, true) // branch-link inheritance is a head-ref signal
			}
		}
	}
	return out
}

// resolveTicketByTitleName looks up tn — trying, in order, tn itself and
// then its titleNameCandidates suffixes (see that function's doc for why:
// ExtractTitleNames is unanchored and can't tell a "worktree-" style
// hyphen-joined prefix from part of the real title_name, so the raw token
// it hands us may need trimming before it matches anything) — first in
// repo's own registered project (the common, fast, unambiguous case;
// always tried first across ALL candidates before any cross-project
// lookup is attempted, so a title_name colliding across projects still
// resolves to the repo's own ticket rather than tripping the ambiguity
// fallback below), then falls back to a cross-project lookup — a repo's
// registered project and the project a given ticket is filed in are not
// always the same (e.g. a ticket filed under a parent epic in another
// project, in this workspace often a different workspace entirely; see
// goq-vcs-link-cross-project-titlename, where this fallback was added
// after a real branch/PR/merge sequence silently failed to link at every
// step). The fallback intentionally has no workspace/project restriction
// of its own — restricting it would have missed the exact bug it exists to
// fix — so it does widen this file's existing false-positive-link surface
// (see goq-vcs-mention-vs-impl) across every project in the instance, not
// just the repo's own; accepted for now since transitions still require a
// head-ref match (see headRef in resolveTickets), only linking/commenting
// gets wider.
//
// Returns nil (skip, don't link) if nothing resolves, or if a candidate's
// cross-project lookup is ambiguous — an ambiguous match must not guess,
// and stops the whole resolution rather than trying a shorter (riskier,
// more likely to collide) candidate. Every non-nil-error outcome is
// logged: an unexpected DB error on either lookup is exactly the kind of
// failure this function must not repeat the original bug's silence on.
func (s *Service) resolveTicketByTitleName(ctx context.Context, repo *VcsRepo, tn string) *ticket.Ticket {
	candidates := titleNameCandidates(tn)

	for _, c := range candidates {
		t, err := s.tickets.GetByProjectAndTitleName(ctx, repo.ProjectID, c)
		if err == nil {
			return t
		}
		if !errors.Is(err, ticket.ErrNotFound) {
			log.Printf("warn: vcs: project-scoped lookup of title_name %q in repo %s failed: %v", c, repo.RepoFullName, err)
			return nil
		}
	}

	for _, c := range candidates {
		t, err := s.tickets.GetByTitleNameAnyProject(ctx, c)
		switch {
		case err == nil:
			log.Printf("vcs: title_name %q resolved outside repo %s's registered project — linking ticket %s (project %s) via cross-project fallback", c, repo.RepoFullName, t.ID, projectIDString(t))
			return t
		case errors.Is(err, ticket.ErrAmbiguousTitleName):
			log.Printf("warn: vcs: title_name %q matches tickets in more than one project — skipping link (ambiguous)", c)
			return nil
		case errors.Is(err, ticket.ErrNotFound):
			continue
		default:
			log.Printf("warn: vcs: cross-project lookup of title_name %q failed: %v", c, err)
			return nil
		}
	}
	return nil
}

// titleNameCandidates returns tn itself, followed by progressively shorter
// suffixes formed by stripping one leading hyphen-joined segment at a time
// — e.g. "worktree-goq-foo-bar" yields ["worktree-goq-foo-bar",
// "goq-foo-bar", "foo-bar"]. Stops once fewer than 2 segments remain (a
// bare single word is never a valid title_name shape:
// ^[a-z][a-z0-9]*(-[a-z0-9]+)+$ requires at least one hyphen). The full
// token is always first, so an already-correct title_name (no prefix)
// resolves on the very first candidate — this only changes behavior when
// tn itself doesn't resolve to anything.
func titleNameCandidates(tn string) []string {
	parts := strings.Split(tn, "-")
	candidates := make([]string, 0, len(parts))
	for i := 0; i < len(parts)-1; i++ {
		candidates = append(candidates, strings.Join(parts[i:], "-"))
	}
	return candidates
}

// branchTicketTitleName recovers the title_name a branch link was made for.
// A branch link's external_ref is the branch name; the title_name is embedded in it.
func branchTicketTitleName(bl *VcsLink) string {
	names := ExtractTitleNames(bl.ExternalRef)
	if len(names) > 0 {
		return names[0]
	}
	return bl.ExternalRef
}

func linkShape(ev VcsEvent) (LinkKind, string, LinkState, string) {
	switch ev.Kind {
	case BranchCreated:
		return KindBranch, ev.Branch, StateOpen, ev.Branch
	case BranchDeleted:
		return KindBranch, ev.Branch, StateDeleted, ev.Branch
	case CommitPushed:
		return KindCommit, ev.CommitSHA, StateOpen, firstLine(ev.CommitMsg)
	default: // PR*
		st := StateOpen
		if ev.PRMerged {
			st = StateMerged
		} else if ev.Kind == PRClosed {
			st = StateClosed
		}
		return KindPR, prNumberString(ev.PRNumber), st, ev.PRTitle
	}
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}

func (s *Service) autoTransition(ctx context.Context, provider string, repo *VcsRepo, t *ticket.Ticket, ev VcsEvent) error {
	if isTerminal(t.Status) {
		return nil
	}
	switch ev.Kind {
	case BranchCreated, PROpened:
		return s.moveToInProgress(ctx, t, ev, provider)
	case PRMerged:
		target := ticket.Status(repo.MergeTarget)
		if target == "" {
			target = ticket.StatusCompleted
		}
		// Risk gate: medium/high tickets require human sign-off before completing,
		// so route the merge auto-transition to pending_approval instead.
		if t.RiskTier != "" && t.RiskTier != ticket.RiskLow {
			return s.transitionAndNote(ctx, t, ticket.StatusPendingApproval,
				fmt.Sprintf("linked PR #%d (merged) → pending_approval (awaiting approval: risk_tier=%s) by vcs:%s", ev.PRNumber, t.RiskTier, provider))
		}
		return s.transitionAndNote(ctx, t, target, fmt.Sprintf("linked PR #%d (merged) → %s by vcs:%s", ev.PRNumber, target, provider))
	}
	return nil
}

func (s *Service) moveToInProgress(ctx context.Context, t *ticket.Ticket, ev VcsEvent, provider string) error {
	if t.Status == ticket.StatusInProgress {
		return nil
	}
	if t.Status == ticket.StatusPending {
		if err := s.transition(ctx, t, ticket.StatusAssigned); err != nil {
			return err
		}
	}
	if t.Status == ticket.StatusAssigned {
		return s.transitionAndNote(ctx, t, ticket.StatusInProgress,
			fmt.Sprintf("linked %s → in_progress by vcs:%s", ev.Kind, provider))
	}
	return nil
}

func (s *Service) transition(ctx context.Context, t *ticket.Ticket, to ticket.Status) error {
	updated, err := s.tickets.Update(ctx, t.ID, ticket.UpdateTicketReq{Status: &to}, "system:vcs")
	if err != nil {
		// invalid transitions are non-fatal for webhook processing
		if errors.Is(err, ticket.ErrInvalidTransition) {
			return nil
		}
		return err
	}
	t.Status = updated.Status
	return nil
}

func (s *Service) transitionAndNote(ctx context.Context, t *ticket.Ticket, to ticket.Status, note string) error {
	prev := t.Status
	if err := s.transition(ctx, t, to); err != nil {
		return err
	}
	if t.Status != prev {
		_, _ = s.comments.Create(ctx, t.ID, "vcs", comment.AuthorKindSystem, comment.CreateReq{Body: note, IsInternal: true})
	}
	return nil
}

func isTerminal(st ticket.Status) bool {
	return st == ticket.StatusCompleted || st == ticket.StatusFailed || st == ticket.StatusCancelled
}

// projectIDString renders t.ProjectID for a log line ("none" if unset) —
// legacy tickets can have a nil ProjectID.
func projectIDString(t *ticket.Ticket) string {
	if t.ProjectID == nil {
		return "none"
	}
	return t.ProjectID.String()
}
