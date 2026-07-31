package mention

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// mentionRE matches "@<id>" tokens. Mention ids are opaque user identifiers
// (the same strings used as ticket requester_id/assignee_id and project
// Member.UserID — sometimes an email, sometimes a lymphhub actor id
// depending on auth source), so the token body allows the characters those
// values actually use: alnum, '.', '_', '-', '@' (emails).
var mentionRE = regexp.MustCompile(`@([\w.\-@]+)`)

// ParseMentionIDs extracts candidate mention ids from free text. It does not
// validate them — callers must resolve each id against real project members
// (see Service.CreateFromBody) before treating it as a real mention.
func ParseMentionIDs(body string) []string {
	matches := mentionRE.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	var ids []string
	for _, m := range matches {
		id := m[1]
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

// MemberLister is the subset of project.Service this package depends on, so
// mention validation can be unit-tested without a real project store.
type MemberLister interface {
	ListMembers(ctx context.Context, projectID uuid.UUID) ([]*ProjectMember, error)
}

// ProjectMember mirrors the fields of project.Member this package needs,
// avoiding an import of the project package (mention has no other business
// with it, and this keeps the dependency direction shallow).
type ProjectMember struct {
	UserID string
}

type Service struct {
	store   Store
	members MemberLister
}

func NewService(store Store, members MemberLister) *Service {
	return &Service{store: store, members: members}
}

// CreateFromBody parses body for @mention tokens, keeps only ids that are
// current members of projectID (unknown/non-member ids are silently
// ignored — the token stays as plain text), excludes a self-mention
// (authorID mentioning themselves), and persists one Mention row per
// remaining id. Returns the mentions actually created.
func (svc *Service) CreateFromBody(
	ctx context.Context,
	ticketID uuid.UUID,
	projectID uuid.UUID,
	commentID *uuid.UUID,
	source Source,
	authorID string,
	body string,
) ([]*Mention, error) {
	ids := ParseMentionIDs(body)
	if len(ids) == 0 {
		return nil, nil
	}

	members, err := svc.members.ListMembers(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("mention: list project members: %w", err)
	}
	memberSet := make(map[string]bool, len(members))
	for _, m := range members {
		memberSet[m.UserID] = true
	}

	now := time.Now().UTC()
	var created []*Mention
	for _, id := range ids {
		if id == authorID || !memberSet[id] {
			continue
		}
		m := &Mention{
			ID:               uuid.New(),
			TicketID:         ticketID,
			CommentID:        commentID,
			MentionedActorID: id,
			Source:           source,
			CreatedAt:        now,
			CreatedBy:        authorID,
		}
		if err := svc.store.Create(ctx, m); err != nil {
			return nil, fmt.Errorf("mention: create: %w", err)
		}
		created = append(created, m)
	}
	return created, nil
}

func (svc *Service) ListByActor(ctx context.Context, actorID string, limit, offset int) ([]*Mention, error) {
	return svc.store.ListByActor(ctx, actorID, limit, offset)
}

func (svc *Service) ListViewsByActor(ctx context.Context, actorID string, limit, offset int) ([]*View, error) {
	return svc.store.ListViewsByActor(ctx, actorID, limit, offset)
}

func (svc *Service) CountUnseenByActor(ctx context.Context, actorID string) (int, error) {
	return svc.store.CountUnseenByActor(ctx, actorID)
}

func (svc *Service) MarkSeen(ctx context.Context, id uuid.UUID, actorID string) error {
	return svc.store.MarkSeen(ctx, id, actorID)
}
