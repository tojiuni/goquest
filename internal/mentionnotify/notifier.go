// Package mentionnotify tells people and session accounts that they were
// @mentioned in a ticket comment. A person gets a Zulip DM; a session account
// gets a signed webhook that motor-iim turns into a fixed line in the session.
//
// Neither channel carries the comment text: only the ticket name, who wrote
// it, a link and ids. Contract and rationale: geneso
// goquest/plan/2026-11-04-goquest-mention-notifications-plan.md.
package mentionnotify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/directory"
	"github.com/tojiuni/goquest/internal/mention"
	"github.com/tojiuni/goquest/internal/ticket"
)

// KindWorkspaceSession is the lymphhub actor kind of a session account.
const KindWorkspaceSession = "workspace_session"

// Messenger sends a direct message to one person by email (notify.Zulip).
type Messenger interface {
	SendDM(ctx context.Context, toEmail, content string) error
}

// WebhookConfig enables the session-account path. Both fields are required;
// with either empty the webhook path is off.
type WebhookConfig struct {
	URL    string
	Secret string
}

func (c WebhookConfig) enabled() bool { return c.URL != "" && c.Secret != "" }

type Notifier struct {
	dir     directory.Lookuper
	dm      Messenger
	hook    WebhookConfig
	uiBase  string
	hc      *http.Client
	now     func() time.Time
	backoff time.Duration
	sync    bool // tests: deliver on the caller's goroutine
}

func New(dir directory.Lookuper, dm Messenger, hook WebhookConfig, uiBase string) *Notifier {
	return &Notifier{
		dir: dir, dm: dm, hook: hook, uiBase: strings.TrimRight(uiBase, "/"),
		hc: &http.Client{Timeout: 8 * time.Second}, now: time.Now, backoff: 500 * time.Millisecond,
	}
}

const attempts = 3

// Sign returns the X-Goquest-Signature value for a webhook: "sha256=" plus
// hex(HMAC-SHA256(secret, "<timestamp>.<raw body>")). The timestamp is part of
// the signed text so it cannot be changed on a replay.
func Sign(secret, timestamp string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(timestamp))
	m.Write([]byte("."))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// Payload is the webhook body. It never contains comment text.
type Payload struct {
	Event            string `json:"event"`
	MentionID        string `json:"mention_id"`
	TicketID         string `json:"ticket_id"`
	TitleName        string `json:"title_name,omitempty"`
	CommentID        string `json:"comment_id,omitempty"`
	AuthorID         string `json:"author_id"`
	MentionedActorID string `json:"mentioned_actor_id"`
	MentionedUser    string `json:"mentioned_username,omitempty"`
	CreatedAt        string `json:"created_at"`
}

// Notify handles the mentions just recorded for one comment. It never blocks
// the caller for long and never returns an error: the stored mention is the
// source of truth, a lost notification is only logged.
func (n *Notifier) Notify(ctx context.Context, t *ticket.Ticket, commentID uuid.UUID, authorID string, created []*mention.Mention) {
	if n == nil || t == nil || len(created) == 0 {
		return
	}
	run := func() {
		bg, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		n.notify(bg, t, commentID, authorID, created)
	}
	if n.sync {
		run()
		return
	}
	go run()
}

func (n *Notifier) notify(ctx context.Context, t *ticket.Ticket, commentID uuid.UUID, authorID string, created []*mention.Mention) {
	ids := []string{authorID}
	for _, m := range created {
		ids = append(ids, m.MentionedActorID)
	}
	actors := map[string]directory.Actor{}
	if n.dir != nil {
		found, err := n.dir.Lookup(ctx, ids)
		if err != nil {
			log.Printf("mention notify: actor lookup: %v", err)
		}
		for _, a := range found {
			actors[a.ID] = a
			if a.Email != "" {
				actors[a.Email] = a
			}
		}
	}
	authorName := displayName(actors, authorID)
	for _, m := range created {
		a, known := actors[m.MentionedActorID]
		switch {
		case known && a.Kind == KindWorkspaceSession:
			n.webhook(ctx, t, commentID, authorID, m, a)
		default:
			// A person (or an actor an older lymphhub cannot classify). The id
			// may itself be an email when the actor is unknown to the lookup.
			email := a.Email
			if email == "" && strings.Contains(m.MentionedActorID, "@") {
				email = m.MentionedActorID
			}
			n.directMessage(ctx, t, authorName, email)
		}
	}
}

func displayName(actors map[string]directory.Actor, id string) string {
	if a, ok := actors[id]; ok {
		switch {
		case a.DisplayName != "":
			return a.DisplayName
		case a.Username != "":
			return a.Username
		case a.Email != "":
			return a.Email
		}
	}
	return id
}

func (n *Notifier) directMessage(ctx context.Context, t *ticket.Ticket, authorName, email string) {
	if n.dm == nil || email == "" || !strings.Contains(email, "@") {
		return
	}
	var link string
	if n.uiBase != "" {
		link = fmt.Sprintf("\n%s/tickets/%s", n.uiBase, t.ID)
	}
	content := fmt.Sprintf("**멘션 알림**\n- 티켓: `%s`\n- 작성: `%s`%s", inline(ticketName(t), 80), inline(authorName, 60), link)
	var err error
	for i := 0; i < attempts; i++ {
		if err = n.dm.SendDM(ctx, email, content); err == nil {
			return
		}
		time.Sleep(time.Duration(i+1) * n.backoff)
	}
	log.Printf("mention notify: DM gave up after %d attempts: %v", attempts, err)
}

func ticketName(t *ticket.Ticket) string {
	if t.TitleName != "" {
		return t.TitleName
	}
	return t.Title
}

// inline makes untrusted text safe inside a Markdown code span: no newlines,
// backticks or @ (mentions), bounded length.
func inline(s string, max int) string {
	s = strings.NewReplacer("\r", " ", "\n", " ", "`", "'", "@", "＠").Replace(s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > max {
		s = string(r[:max]) + "…"
	}
	return s
}

func (n *Notifier) webhook(ctx context.Context, t *ticket.Ticket, commentID uuid.UUID, authorID string, m *mention.Mention, a directory.Actor) {
	if !n.hook.enabled() {
		return
	}
	p := Payload{
		Event: "mention", MentionID: m.ID.String(), TicketID: t.ID.String(), TitleName: t.TitleName,
		AuthorID: authorID, MentionedActorID: m.MentionedActorID, MentionedUser: a.Username,
		CreatedAt: m.CreatedAt.UTC().Format(time.RFC3339),
	}
	if m.CommentID != nil {
		p.CommentID = m.CommentID.String()
	} else if commentID != uuid.Nil {
		p.CommentID = commentID.String()
	}
	// Marshal once: the signature covers these exact bytes, and every retry
	// resends them unchanged with the same mention_id (the receiver rejects a
	// repeated mention_id/comment_id only after it has accepted one).
	body, err := json.Marshal(p)
	if err != nil {
		log.Printf("mention notify: marshal payload: %v", err)
		return
	}
	for i := 0; i < attempts; i++ {
		status, err := n.post(ctx, body)
		switch {
		case err == nil && status/100 == 2:
			return
		case err == nil && status/100 == 4:
			// rejected (bad signature/clock/duplicate): retrying cannot help
			log.Printf("mention notify: webhook rejected mention %s with status %d", p.MentionID, status)
			return
		}
		if err != nil {
			log.Printf("mention notify: webhook attempt %d for mention %s: %v", i+1, p.MentionID, err)
		}
		time.Sleep(time.Duration(i+1) * n.backoff)
	}
	log.Printf("mention notify: webhook gave up on mention %s after %d attempts", p.MentionID, attempts)
}

// post sends one signed attempt with a fresh timestamp and signature.
func (n *Notifier) post(ctx context.Context, body []byte) (int, error) {
	ts := strconv.FormatInt(n.now().Unix(), 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.hook.URL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goquest-Timestamp", ts)
	req.Header.Set("X-Goquest-Signature", Sign(n.hook.Secret, ts, body))
	resp, err := n.hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, nil
}
