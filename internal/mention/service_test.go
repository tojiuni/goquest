package mention_test

import (
	"bytes"
	"context"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/mention"
)

func TestParseMentionIDs(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"none", "no mentions here", nil},
		{"single email", "cc @lyckabc@gmail.com please", []string{"lyckabc@gmail.com"}},
		{"single handle", "hey @lyckabc check this", []string{"lyckabc"}},
		{"multiple, de-duped", "@a @b and again @a", []string{"a", "b"}},
		{"trailing comma not part of the id", "ping @lyckabc, thanks", []string{"lyckabc"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mention.ParseMentionIDs(c.body)
			if len(got) != len(c.want) {
				t.Fatalf("ParseMentionIDs(%q) = %v, want %v", c.body, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("ParseMentionIDs(%q) = %v, want %v", c.body, got, c.want)
				}
			}
		})
	}
}

type mockStore struct {
	created []*mention.Mention
}

func (m *mockStore) Create(_ context.Context, mm *mention.Mention) error {
	m.created = append(m.created, mm)
	return nil
}
func (m *mockStore) ListByActor(_ context.Context, actorID string, _, _ int) ([]*mention.Mention, error) {
	var out []*mention.Mention
	for _, mm := range m.created {
		if mm.MentionedActorID == actorID {
			out = append(out, mm)
		}
	}
	return out, nil
}
func (m *mockStore) CountUnseenByActor(_ context.Context, actorID string) (int, error) {
	n := 0
	for _, mm := range m.created {
		if mm.MentionedActorID == actorID && mm.SeenAt == nil {
			n++
		}
	}
	return n, nil
}
func (m *mockStore) ListViewsByActor(_ context.Context, actorID string, _, _ int) ([]*mention.View, error) {
	var out []*mention.View
	for _, mm := range m.created {
		if mm.MentionedActorID == actorID {
			out = append(out, &mention.View{Mention: *mm})
		}
	}
	return out, nil
}
func (m *mockStore) MarkSeen(_ context.Context, id uuid.UUID, actorID string) error {
	for _, mm := range m.created {
		if mm.ID == id && mm.MentionedActorID == actorID {
			return nil
		}
	}
	return mention.ErrNotFound
}

type mockMembers struct {
	members []*mention.ProjectMember
}

func (m *mockMembers) ListMembers(_ context.Context, _ uuid.UUID) ([]*mention.ProjectMember, error) {
	return m.members, nil
}

func TestCreateFromBody_OnlyMentionsProjectMembers(t *testing.T) {
	store := &mockStore{}
	members := &mockMembers{members: []*mention.ProjectMember{{UserID: "alice"}, {UserID: "bob"}}}
	svc := mention.NewService(store, members)

	created, err := svc.CreateFromBody(
		context.Background(), uuid.New(), uuid.New(), nil, mention.SourceComment,
		"carol", "hey @alice and @notamember, also @bob",
	)
	if err != nil {
		t.Fatalf("CreateFromBody: %v", err)
	}
	if len(created) != 2 {
		t.Fatalf("got %d mentions, want 2 (alice, bob); got %+v", len(created), created)
	}
	got := map[string]bool{created[0].MentionedActorID: true, created[1].MentionedActorID: true}
	if !got["alice"] || !got["bob"] {
		t.Fatalf("expected alice+bob mentioned, got %v", got)
	}
}

// goq-session-mention-row-not-created: a sentence-final "." or "-" must not
// stop an exact member id from matching.
func TestCreateFromBody_TrailingPunctuationStillMatches(t *testing.T) {
	store := &mockStore{}
	members := &mockMembers{members: []*mention.ProjectMember{{UserID: "alice"}}}
	svc := mention.NewService(store, members)

	created, err := svc.CreateFromBody(
		context.Background(), uuid.New(), uuid.New(), nil, mention.SourceComment,
		"carol", "확인 부탁해요 @alice. 그리고 @alice-",
	)
	if err != nil {
		t.Fatalf("CreateFromBody: %v", err)
	}
	if len(created) != 1 || created[0].MentionedActorID != "alice" {
		t.Fatalf("want exactly one alice row (deduped), got %+v", created)
	}
}

// ULID member keys keep working unchanged (the case that already passed).
func TestCreateFromBody_ULIDMentionUnchanged(t *testing.T) {
	const ulid = "01M518QYPETVGD07NA3H7CFNA7"
	store := &mockStore{}
	members := &mockMembers{members: []*mention.ProjectMember{{UserID: ulid}}}
	svc := mention.NewService(store, members)

	created, err := svc.CreateFromBody(
		context.Background(), uuid.New(), uuid.New(), nil, mention.SourceComment,
		"carol", "@"+ulid+" 확인",
	)
	if err != nil {
		t.Fatalf("CreateFromBody: %v", err)
	}
	if len(created) != 1 || created[0].MentionedActorID != ulid {
		t.Fatalf("want one ULID row, got %+v", created)
	}
}

// Restricted tickets: a workspace-only member must not become mentionable.
func TestCreateFromBody_RestrictedTicketHidesWorkspaceOnlyMember(t *testing.T) {
	store := &mockStore{}
	lister := mention.EligibleLister{
		Project:     func(context.Context, uuid.UUID) ([]string, error) { return []string{"alice"}, nil },
		WorkspaceOf: func(context.Context, uuid.UUID) (uuid.UUID, error) { return uuid.New(), nil },
		Workspace:   func(context.Context, uuid.UUID) ([]string, error) { return []string{"eve"}, nil },
	}
	svc := mention.NewService(store, lister)

	ctx := mention.WithProjectMembersOnly(context.Background())
	created, err := svc.CreateFromBody(
		ctx, uuid.New(), uuid.New(), nil, mention.SourceComment,
		"carol", "@eve @alice.",
	)
	if err != nil {
		t.Fatalf("CreateFromBody: %v", err)
	}
	if len(created) != 1 || created[0].MentionedActorID != "alice" {
		t.Fatalf("restricted ticket must mention only project member alice, got %+v", created)
	}
}

// A member id that itself ends with "." must still match exactly (raw first).
func TestCreateFromBody_MemberIdEndingInDotMatchesExactly(t *testing.T) {
	store := &mockStore{}
	members := &mockMembers{members: []*mention.ProjectMember{{UserID: "bot."}}}
	svc := mention.NewService(store, members)

	created, err := svc.CreateFromBody(
		context.Background(), uuid.New(), uuid.New(), nil, mention.SourceComment,
		"carol", "ping @bot.",
	)
	if err != nil {
		t.Fatalf("CreateFromBody: %v", err)
	}
	if len(created) != 1 || created[0].MentionedActorID != "bot." {
		t.Fatalf("want exact match bot., got %+v", created)
	}
}

// Ignored-token logs must not contain the comment body, must mask email-shaped
// tokens, must truncate long tokens, and must be capped per comment.
func TestCreateFromBody_IgnoredTokenLogIsBoundedAndPrivate(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	store := &mockStore{}
	members := &mockMembers{members: []*mention.ProjectMember{{UserID: "alice"}}}
	svc := mention.NewService(store, members)

	long := strings.Repeat("x", 200)
	body := "secret-body-text @secretuser@corp.example.com @" + long + " @a1 @a2 @a3 @a4 @a5 @a6"
	if _, err := svc.CreateFromBody(
		context.Background(), uuid.New(), uuid.New(), nil, mention.SourceComment,
		"carol", body,
	); err != nil {
		t.Fatalf("CreateFromBody: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "secret-body-text") {
		t.Fatalf("log leaked comment body: %s", out)
	}
	if strings.Contains(out, "secretuser@corp.example.com") {
		t.Fatalf("log leaked full email-shaped token: %s", out)
	}
	if strings.Contains(out, long) {
		t.Fatalf("log leaked untruncated long token")
	}
	if n := strings.Count(out, "mention: ignored token "); n != 5 {
		t.Fatalf("want 5 per-token log lines (cap), got %d: %s", n, out)
	}
	if !strings.Contains(out, "more ignored tokens") {
		t.Fatalf("want summary line for overflow: %s", out)
	}
}

func TestCreateFromBody_ExcludesSelfMention(t *testing.T) {
	store := &mockStore{}
	members := &mockMembers{members: []*mention.ProjectMember{{UserID: "alice"}, {UserID: "bob"}}}
	svc := mention.NewService(store, members)

	created, err := svc.CreateFromBody(
		context.Background(), uuid.New(), uuid.New(), nil, mention.SourceComment,
		"alice", "note to self @alice, also cc @bob",
	)
	if err != nil {
		t.Fatalf("CreateFromBody: %v", err)
	}
	if len(created) != 1 || created[0].MentionedActorID != "bob" {
		t.Fatalf("expected only bob (self-mention excluded), got %+v", created)
	}
}

func TestCreateFromBody_NoMentionsIsNoop(t *testing.T) {
	store := &mockStore{}
	members := &mockMembers{members: []*mention.ProjectMember{{UserID: "alice"}}}
	svc := mention.NewService(store, members)

	created, err := svc.CreateFromBody(context.Background(), uuid.New(), uuid.New(), nil, mention.SourceComment, "alice", "no mentions")
	if err != nil {
		t.Fatalf("CreateFromBody: %v", err)
	}
	if created != nil {
		t.Fatalf("expected nil (no-op), got %+v", created)
	}
	if len(store.created) != 0 {
		t.Fatalf("store should not have been written to, got %d rows", len(store.created))
	}
}

func TestCreateFromBody_DescriptionSourceHasNilCommentID(t *testing.T) {
	store := &mockStore{}
	members := &mockMembers{members: []*mention.ProjectMember{{UserID: "bob"}}}
	svc := mention.NewService(store, members)

	created, err := svc.CreateFromBody(context.Background(), uuid.New(), uuid.New(), nil, mention.SourceDescription, "alice", "@bob please review")
	if err != nil {
		t.Fatalf("CreateFromBody: %v", err)
	}
	if len(created) != 1 || created[0].CommentID != nil || created[0].Source != mention.SourceDescription {
		t.Fatalf("unexpected mention: %+v", created)
	}
}

// goq-mention-parser-ignore-code-span: @ids inside code spans, fenced blocks,
// or quote lines are not mentions; the same id outside them still is.
func TestCreateFromBody_IgnoresCodeSpanAndQuoteMentions(t *testing.T) {
	store := &mockStore{}
	members := &mockMembers{members: []*mention.ProjectMember{{UserID: "alice"}, {UserID: "bob"}}}
	svc := mention.NewService(store, members)

	body := "title `@alice 재정의` ok\n> quoted @alice\n```\n@alice in fence\n```\nreal @bob"
	created, err := svc.CreateFromBody(
		context.Background(), uuid.New(), uuid.New(), nil, mention.SourceComment,
		"carol", body,
	)
	if err != nil {
		t.Fatalf("CreateFromBody: %v", err)
	}
	if len(created) != 1 || created[0].MentionedActorID != "bob" {
		t.Fatalf("want only bob (code/quote mentions ignored), got %+v", created)
	}
}

func TestParseMentionIDs_KeepsMentionOutsideCode(t *testing.T) {
	got := mention.ParseMentionIDs("see `@x` and @alice")
	if len(got) != 1 || got[0] != "alice" {
		t.Fatalf("want [alice], got %v", got)
	}
}
