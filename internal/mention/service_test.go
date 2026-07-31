package mention_test

import (
	"context"
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
