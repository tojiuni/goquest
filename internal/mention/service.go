package mention

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
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
	matches := mentionRE.FindAllStringSubmatch(stripNonMentionText(body), -1)
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

var (
	fencedCodeRE = regexp.MustCompile("(?s)```.*?```")
	inlineCodeRE = regexp.MustCompile("`[^`\n]*`")
	quoteLineRE  = regexp.MustCompile(`(?m)^[ \t]*>.*$`)
)

// stripNonMentionText removes text a reader does not see as a live mention:
// fenced code blocks, inline code spans, and block-quote lines. A ticket title
// quoted in a code span must not wake a member whose id appears in it.
func stripNonMentionText(body string) string {
	body = fencedCodeRE.ReplaceAllString(body, " ")
	body = inlineCodeRE.ReplaceAllString(body, " ")
	return quoteLineRE.ReplaceAllString(body, " ")
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
// current members of projectID (unknown/non-member ids are not mentioned and
// the token stays as plain text; each such token is logged, capped per
// comment, never with the body), excludes a self-mention
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
	matched := make(map[string]bool, len(ids))
	logged, dropped := 0, 0
	for _, raw := range ids {
		id, reason := resolveCandidate(raw, memberSet, authorID)
		if id == "" {
			// A silent drop here hid goq-session-mention-row-not-created. Log the
			// token (never the body), masked and truncated, capped per comment.
			dropped++
			if logged < maxIgnoredLogsPerComment {
				logged++
				log.Printf("mention: ignored token %s on ticket %s: %s", safeTokenForLog(raw), ticketID, reason)
			}
			continue
		}
		if matched[id] {
			continue
		}
		matched[id] = true
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
	if dropped > logged {
		log.Printf("mention: %d more ignored tokens on ticket %s not logged (cap %d)", dropped-logged, ticketID, maxIgnoredLogsPerComment)
	}
	return created, nil
}

// maxIgnoredLogsPerComment caps per-token warn lines for one comment so a long
// or hostile body cannot flood the log.
const maxIgnoredLogsPerComment = 5

// maxTokenLogLen bounds how much of an ignored token is written to the log.
const maxTokenLogLen = 32

// safeTokenForLog returns a bounded, privacy-reduced form of a mention token:
// an email-shaped token keeps only its first local-part character and its
// domain, and anything longer than maxTokenLogLen is cut.
func safeTokenForLog(raw string) string {
	s := raw
	if i := strings.Index(s, "@"); i >= 0 && strings.Contains(s[i+1:], ".") {
		local, domain := s[:i], s[i+1:]
		if local != "" {
			local = local[:1] + "***"
		}
		s = local + "@" + domain
	}
	if r := []rune(s); len(r) > maxTokenLogLen {
		s = string(r[:maxTokenLogLen]) + "…"
	}
	return s
}

// trailingPunct is stripped from the end of a token. Only "." and "-" need it:
// mentionRE captures those, and no other trailing punctuation reaches here.
const trailingPunct = ".-"

// resolveCandidate maps one parsed token to an eligible member id. The exact
// token is tried first, then the token with trailing punctuation removed.
// Returns "" and a reason when nothing eligible matches.
func resolveCandidate(raw string, eligible map[string]bool, authorID string) (string, string) {
	candidates := []string{raw}
	if trimmed := strings.TrimRight(raw, trailingPunct); trimmed != raw && trimmed != "" {
		candidates = append(candidates, trimmed)
	}
	for _, c := range candidates {
		if c == authorID {
			return "", "self-mention"
		}
		if eligible[c] {
			return c, ""
		}
	}
	return "", "not an eligible member"
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
