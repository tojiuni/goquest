package mentionnotify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/directory"
	"github.com/tojiuni/goquest/internal/mention"
	"github.com/tojiuni/goquest/internal/ticket"
)

// Shared signing vector: the receiver (motor-iim) must produce the same value.
// The secret is used as its literal UTF-8 text, never hex-decoded.
const (
	vecSecret = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	vecTS     = "1700000000"
	vecBody   = `{"event":"mention","mention_id":"m1"}`
	vecSig    = "sha256=e354dd2ecf1b011791a7de56b9abde534ea2448d838b30902fb324a6ad5da2dc"
	wrongSig  = "sha256=0503eaf0321dee953715aac8fed394ec05236b09f6f4004b567e076cd9da95d2" // key hex-decoded
)

func TestSignVector(t *testing.T) {
	if got := Sign(vecSecret, vecTS, []byte(vecBody)); got != vecSig {
		t.Fatalf("Sign = %s, want %s", got, vecSig)
	}
	if Sign(vecSecret, vecTS, []byte(vecBody)) == wrongSig {
		t.Fatal("secret must not be hex-decoded")
	}
	if Sign(vecSecret, "1700000001", []byte(vecBody)) == vecSig {
		t.Fatal("the timestamp must be part of the signed text")
	}
}

type fakeDir struct {
	actors []directory.Actor
	err    error
}

func (f fakeDir) Lookup(_ context.Context, ids []string) ([]directory.Actor, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []directory.Actor
	for _, a := range f.actors {
		for _, id := range ids {
			if a.ID == id || (a.Email != "" && a.Email == id) {
				out = append(out, a)
				break
			}
		}
	}
	return out, nil
}

type fakeDM struct {
	mu   sync.Mutex
	sent []struct{ to, content string }
	fail int
}

func (f *fakeDM) SendDM(_ context.Context, to, content string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail > 0 {
		f.fail--
		return errors.New("zulip down")
	}
	f.sent = append(f.sent, struct{ to, content string }{to, content})
	return nil
}

type hookCall struct {
	body []byte
	ts   string
	sig  string
}

func hookServer(t *testing.T, statuses ...int) (*httptest.Server, *[]hookCall) {
	var mu sync.Mutex
	var calls []hookCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, hookCall{b, r.Header.Get("X-Goquest-Timestamp"), r.Header.Get("X-Goquest-Signature")})
		i := len(calls) - 1
		mu.Unlock()
		code := http.StatusAccepted
		if i < len(statuses) {
			code = statuses[i]
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func newNotifier(dir directory.Lookuper, dm Messenger, hook WebhookConfig) *Notifier {
	n := New(dir, dm, hook, "https://goquest.example/")
	n.sync, n.backoff = true, 0
	return n
}

func tk() *ticket.Ticket {
	return &ticket.Ticket{ID: uuid.New(), TitleName: "goq-example", Title: "Example ticket"}
}

func mn(actor string) *mention.Mention {
	cid := uuid.New()
	return &mention.Mention{ID: uuid.New(), TicketID: uuid.New(), CommentID: &cid, MentionedActorID: actor,
		Source: mention.SourceComment, CreatedAt: time.Now().UTC(), CreatedBy: "author"}
}

var (
	human   = directory.Actor{ID: "01HUMAN", DisplayName: "Alice", Email: "alice@x.io", Kind: "human"}
	session = directory.Actor{ID: "01SESS", DisplayName: "ws goquest", Kind: KindWorkspaceSession, Username: "ws-goquest-ui"}
	author  = directory.Actor{ID: "01AUTH", DisplayName: "Bob", Email: "bob@x.io", Kind: "human"}
)

func TestPersonGetsADMWithNoCommentText(t *testing.T) {
	dm := &fakeDM{}
	n := newNotifier(fakeDir{actors: []directory.Actor{human, author}}, dm, WebhookConfig{})
	tkt := tk()
	m := mn("01HUMAN")
	n.Notify(context.Background(), tkt, *m.CommentID, "01AUTH", []*mention.Mention{m})
	if len(dm.sent) != 1 || dm.sent[0].to != "alice@x.io" {
		t.Fatalf("want one DM to alice@x.io, got %+v", dm.sent)
	}
	c := dm.sent[0].content
	for _, want := range []string{"멘션 알림", "goq-example", "Bob", "https://goquest.example/tickets/" + tkt.ID.String()} {
		if !strings.Contains(c, want) {
			t.Errorf("DM missing %q: %q", want, c)
		}
	}
}

func TestDMNeutralisesUntrustedText(t *testing.T) {
	dm := &fakeDM{}
	evil := directory.Actor{ID: "01EVIL", DisplayName: "x @**all** `y`\nz", Email: "evil@x.io"}
	n := newNotifier(fakeDir{actors: []directory.Actor{human, evil}}, dm, WebhookConfig{})
	tkt := &ticket.Ticket{ID: uuid.New(), TitleName: "goq-x`@**all**"}
	m := mn("01HUMAN")
	n.Notify(context.Background(), tkt, *m.CommentID, "01EVIL", []*mention.Mention{m})
	c := dm.sent[0].content
	if strings.Contains(c, "@**") || strings.Contains(c, "x `y`") || strings.Count(c, "`")%2 != 0 {
		t.Errorf("markup/mention not neutralised: %q", c)
	}
}

func TestSessionGetsASignedWebhook(t *testing.T) {
	srv, calls := hookServer(t)
	dm := &fakeDM{}
	n := newNotifier(fakeDir{actors: []directory.Actor{session, author}}, dm, WebhookConfig{URL: srv.URL, Secret: vecSecret})
	n.now = func() time.Time { return time.Unix(1700000000, 0) }
	tkt := tk()
	m := mn("01SESS")
	n.Notify(context.Background(), tkt, *m.CommentID, "01AUTH", []*mention.Mention{m})
	if len(*calls) != 1 || len(dm.sent) != 0 {
		t.Fatalf("webhook calls=%d dms=%d", len(*calls), len(dm.sent))
	}
	c := (*calls)[0]
	if c.ts != vecTS || c.sig != Sign(vecSecret, vecTS, c.body) {
		t.Errorf("bad headers ts=%q sig=%q", c.ts, c.sig)
	}
	var p map[string]any
	if err := json.Unmarshal(c.body, &p); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"event": "mention", "mention_id": m.ID.String(), "ticket_id": tkt.ID.String(), "title_name": "goq-example",
		"comment_id": m.CommentID.String(), "author_id": "01AUTH", "mentioned_actor_id": "01SESS", "mentioned_username": "ws-goquest-ui",
	}
	for k, v := range want {
		if p[k] != v {
			t.Errorf("%s = %v, want %v", k, p[k], v)
		}
	}
	if _, ok := p["created_at"].(string); !ok {
		t.Error("created_at missing")
	}
	for k := range p {
		if _, ok := want[k]; !ok && k != "created_at" {
			t.Errorf("unexpected field %q (the payload must not carry comment text)", k)
		}
	}
}

func TestWebhookRetriesKeepMentionIDAndResign(t *testing.T) {
	srv, calls := hookServer(t, 503, 503, 202)
	n := newNotifier(fakeDir{actors: []directory.Actor{session}}, &fakeDM{}, WebhookConfig{URL: srv.URL, Secret: vecSecret})
	var tick int64 = 1700000000
	n.now = func() time.Time { tick += 7; return time.Unix(tick, 0) }
	m := mn("01SESS")
	n.Notify(context.Background(), tk(), *m.CommentID, "01AUTH", []*mention.Mention{m})
	if len(*calls) != 3 {
		t.Fatalf("want 3 attempts, got %d", len(*calls))
	}
	first := (*calls)[0]
	seen := map[string]bool{}
	for _, c := range *calls {
		if string(c.body) != string(first.body) {
			t.Error("a retry must resend the identical body (same mention_id)")
		}
		if c.sig != Sign(vecSecret, c.ts, c.body) {
			t.Error("each attempt must be signed for its own timestamp")
		}
		if seen[c.ts] {
			t.Error("each attempt needs a fresh timestamp")
		}
		seen[c.ts] = true
	}
}

func TestWebhookStopsOnRejectionAndGivesUp(t *testing.T) {
	for _, code := range []int{400, 401, 403} {
		srv, calls := hookServer(t, code)
		n := newNotifier(fakeDir{actors: []directory.Actor{session}}, &fakeDM{}, WebhookConfig{URL: srv.URL, Secret: vecSecret})
		m := mn("01SESS")
		n.Notify(context.Background(), tk(), *m.CommentID, "01AUTH", []*mention.Mention{m})
		if len(*calls) != 1 {
			t.Errorf("status %d must not be retried, got %d attempts", code, len(*calls))
		}
	}
	srv, calls := hookServer(t, 500, 500, 500, 500)
	n := newNotifier(fakeDir{actors: []directory.Actor{session}}, &fakeDM{}, WebhookConfig{URL: srv.URL, Secret: vecSecret})
	m := mn("01SESS")
	n.Notify(context.Background(), tk(), *m.CommentID, "01AUTH", []*mention.Mention{m})
	if len(*calls) != 3 {
		t.Errorf("must stop after 3 attempts, got %d", len(*calls))
	}
}

func TestWebhookOffWithoutConfig(t *testing.T) {
	srv, calls := hookServer(t)
	for _, cfg := range []WebhookConfig{{}, {URL: srv.URL}, {Secret: vecSecret}} {
		n := newNotifier(fakeDir{actors: []directory.Actor{session}}, &fakeDM{}, cfg)
		m := mn("01SESS")
		n.Notify(context.Background(), tk(), *m.CommentID, "01AUTH", []*mention.Mention{m})
	}
	if len(*calls) != 0 {
		t.Errorf("webhook must be off unless both URL and secret are set, got %d calls", len(*calls))
	}
}

func TestOlderLymphhubFallsBackToDMOnly(t *testing.T) {
	srv, calls := hookServer(t)
	dm := &fakeDM{}
	old := directory.Actor{ID: "01SESS", DisplayName: "ws goquest", Email: "ws@x.io"} // no kind: cannot be told apart
	n := newNotifier(fakeDir{actors: []directory.Actor{old}}, dm, WebhookConfig{URL: srv.URL, Secret: vecSecret})
	m := mn("01SESS")
	n.Notify(context.Background(), tk(), *m.CommentID, "01AUTH", []*mention.Mention{m})
	if len(*calls) != 0 {
		t.Error("without a confirmed kind the webhook must not fire")
	}
	if len(dm.sent) != 1 {
		t.Errorf("expected the DM fallback, got %+v", dm.sent)
	}
}

func TestUnknownActorWithEmailIDStillGetsADM(t *testing.T) {
	dm := &fakeDM{}
	n := newNotifier(fakeDir{}, dm, WebhookConfig{})
	m := mn("carol@x.io")
	n.Notify(context.Background(), tk(), *m.CommentID, "someone", []*mention.Mention{m})
	if len(dm.sent) != 1 || dm.sent[0].to != "carol@x.io" {
		t.Errorf("%+v", dm.sent)
	}
	m2 := mn("opaque-id-without-email")
	n.Notify(context.Background(), tk(), *m2.CommentID, "someone", []*mention.Mention{m2})
	if len(dm.sent) != 1 {
		t.Error("an unknown actor with no email cannot be messaged")
	}
}

func TestLookupFailureAndRetriesNeverPanic(t *testing.T) {
	dm := &fakeDM{fail: 2}
	n := newNotifier(fakeDir{err: errors.New("lymphhub down")}, dm, WebhookConfig{})
	m := mn("dave@x.io")
	n.Notify(context.Background(), tk(), *m.CommentID, "a", []*mention.Mention{m})
	if len(dm.sent) != 1 {
		t.Errorf("DM should succeed on the third attempt even with the directory down: %+v", dm.sent)
	}
	var nilN *Notifier
	nilN.Notify(context.Background(), tk(), uuid.New(), "a", []*mention.Mention{m})
	n.Notify(context.Background(), nil, uuid.New(), "a", []*mention.Mention{m})
	n.Notify(context.Background(), tk(), uuid.New(), "a", nil)
}
