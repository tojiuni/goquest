package vcs

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/comment"
	"github.com/tojiuni/goquest/internal/ticket"
)

// TicketService is the narrow slice of *ticket.Service the linker needs.
type TicketService interface {
	GetByProjectAndTitleName(ctx context.Context, projectID uuid.UUID, titleName string) (*ticket.Ticket, error)
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
	for _, t := range tickets {
		link := &VcsLink{
			TicketID: t.ID, Provider: provider, RepoFullName: ev.Repo,
			Kind: kind, ExternalRef: ref, URL: ev.URL, State: state, Title: title, Actor: ev.Actor,
		}
		if _, err := s.store.UpsertLink(ctx, link); err != nil {
			return err
		}
		if repo.AutoTransition {
			if err := s.autoTransition(ctx, provider, repo, t, ev); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveTickets returns the distinct tickets an event references: title_name
// tokens found in branch/commit/PR text that resolve in the repo's project,
// plus (for commits) the ticket of a linked branch (inheritance).
func (s *Service) resolveTickets(ctx context.Context, provider string, repo *VcsRepo, ev VcsEvent) []*ticket.Ticket {
	seen := map[uuid.UUID]bool{}
	var out []*ticket.Ticket
	add := func(t *ticket.Ticket) {
		if t != nil && !seen[t.ID] {
			seen[t.ID] = true
			out = append(out, t)
		}
	}
	for _, tn := range ExtractTitleNames(ev.Branch, ev.CommitMsg, ev.PRTitle, ev.PRBody) {
		if t, err := s.tickets.GetByProjectAndTitleName(ctx, repo.ProjectID, tn); err == nil {
			add(t)
		}
	}
	if ev.Kind == CommitPushed {
		if bl, err := s.store.GetBranchLink(ctx, provider, ev.Repo, ev.Branch); err == nil {
			if t, err := s.tickets.GetByProjectAndTitleName(ctx, repo.ProjectID, branchTicketTitleName(bl)); err == nil {
				add(t)
			}
		}
	}
	return out
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
