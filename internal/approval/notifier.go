package approval

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Messenger delivers a direct message to one person by email. Implemented by
// notify.Zulip; a disabled messenger is a no-op.
type Messenger interface {
	SendDM(ctx context.Context, toEmail, content string) error
}

// OwnerResolver returns the email of the person who decides user-only
// approvals for a workspace (its owner). "" means unknown: nobody is messaged.
type OwnerResolver func(ctx context.Context, workspaceID uuid.UUID) (string, error)

// TicketRef is the ticket context a message needs.
type TicketRef struct {
	TitleName   string
	WorkspaceID uuid.UUID
}

// Dispatcher decides who hears about an approval event and sends it. It only
// informs: it never accepts a reply as a decision.
type Dispatcher struct {
	msg      Messenger
	owner    OwnerResolver
	inboxURL string
	// NotifyUserOnDelegable also messages the owner for kinds a session can
	// decide (merge, deviation within direction). Off by default: the desk
	// session handles those and polls list_approvals.
	NotifyUserOnDelegable bool
	// Backoff is the base delay between send attempts (default 500ms).
	Backoff time.Duration
	// Sync sends on the caller's goroutine (tests). Default is a short-lived
	// goroutine so a slow Zulip never delays an approval request.
	Sync bool
}

func NewDispatcher(msg Messenger, owner OwnerResolver, inboxURL string) *Dispatcher {
	return &Dispatcher{msg: msg, owner: owner, inboxURL: strings.TrimRight(inboxURL, "/"), Backoff: 500 * time.Millisecond}
}

const sendAttempts = 3

func (d *Dispatcher) deliver(to, content string) {
	if d == nil || d.msg == nil || !isPerson(to) {
		return
	}
	run := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var err error
		for i := 0; i < sendAttempts; i++ {
			if err = d.msg.SendDM(ctx, to, content); err == nil {
				return
			}
			time.Sleep(time.Duration(i+1) * d.Backoff)
		}
		// the approval itself is already stored; a lost notification is only logged
		log.Printf("approval notify: giving up after %d attempts: %v", sendAttempts, err)
	}
	if d.Sync {
		run()
		return
	}
	go run()
}

// isPerson reports whether an id is a person's email. Session accounts are
// bare actor ids (never contain '@', see ValidatePrincipal) and poll instead.
func isPerson(id string) bool {
	return strings.Contains(id, "@") && !strings.ContainsAny(id, " \t\n")
}

func (d *Dispatcher) ownerFor(ctx context.Context, a *Approval, t TicketRef) string {
	if !d.shouldTellOwner(a) || d.owner == nil || t.WorkspaceID == uuid.Nil {
		return ""
	}
	email, err := d.owner(ctx, t.WorkspaceID)
	if err != nil {
		log.Printf("approval notify: resolve workspace owner: %v", err)
		return ""
	}
	return email
}

// shouldTellOwner: user-only kinds always reach the owner; delegable kinds only
// when NotifyUserOnDelegable is on.
func (d *Dispatcher) shouldTellOwner(a *Approval) bool {
	return !Delegable(a.Kind, a.WithinDirection) || d.NotifyUserOnDelegable
}

// Requested tells the decider(s) a new approval is waiting.
func (d *Dispatcher) Requested(ctx context.Context, a *Approval, t TicketRef) {
	if d == nil {
		return
	}
	to := d.ownerFor(ctx, a, t)
	if to == "" || to == a.RequestedBy {
		return
	}
	d.deliver(to, d.render("승인 요청", a, t, fmt.Sprintf("요청자 %s", who(a.RequestedBy, a.RequestedByLabel))))
}

// Decided tells a human requester the outcome. Sessions poll instead.
func (d *Dispatcher) Decided(ctx context.Context, a *Approval, t TicketRef) {
	if d == nil || a.DecidedBy == nil || a.RequestedBy == *a.DecidedBy {
		return
	}
	label := map[Status]string{StatusApproved: "승인됨", StatusRejected: "거부됨", StatusRevisionRequested: "수정 요청됨"}[a.Status]
	if label == "" {
		return
	}
	d.deliver(a.RequestedBy, d.render(label, a, t, fmt.Sprintf("결정 %s", *a.DecidedBy)))
}

// Withdrawn tells whoever was told about the request that it was pulled back.
func (d *Dispatcher) Withdrawn(ctx context.Context, a *Approval, t TicketRef) {
	if d == nil {
		return
	}
	to := d.ownerFor(ctx, a, t)
	if to == "" || to == a.RequestedBy {
		return
	}
	d.deliver(to, d.render("요청 철회됨", a, t, fmt.Sprintf("요청자 %s", who(a.RequestedBy, a.RequestedByLabel))))
}

func who(id, label string) string {
	if label != "" {
		return fmt.Sprintf("%s (%s)", id, safeInline(label, 40))
	}
	return id
}

// render builds the message. Everything agent- or user-supplied goes through
// safeInline so it cannot ping people (@**all**), open links or format text.
// Evidence bodies are never included.
func (d *Dispatcher) render(title string, a *Approval, t TicketRef, line string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**승인 알림 · %s**\n", title)
	fmt.Fprintf(&b, "- 종류: `%s`\n", a.Kind)
	if t.TitleName != "" {
		fmt.Fprintf(&b, "- 티켓: `%s`\n", safeInline(t.TitleName, 80))
	}
	if s := subjectSummary(a.Subject); s != "" {
		fmt.Fprintf(&b, "- 대상: `%s`\n", s)
	}
	if a.RequestNote != "" {
		fmt.Fprintf(&b, "- 사유: `%s`\n", safeInline(a.RequestNote, 140))
	}
	fmt.Fprintf(&b, "- %s\n", line)
	if a.DecisionNote != nil && *a.DecisionNote != "" {
		fmt.Fprintf(&b, "- 메모: `%s`\n", safeInline(*a.DecisionNote, 140))
	}
	if d.inboxURL != "" {
		fmt.Fprintf(&b, "\n%s?approval=%s\n", d.inboxURL, a.ID)
	}
	b.WriteString("\n_이 메시지로는 승인할 수 없습니다. goquest 화면에서 결정하세요._")
	return b.String()
}

func subjectSummary(s Subject) string {
	switch {
	case s.Repo != "" && s.PR > 0 && s.HeadSHA != "":
		return fmt.Sprintf("%s#%d@%s", safeInline(s.Repo, 60), s.PR, safeInline(shortSHA(s.HeadSHA), 12))
	case s.Repo != "" && s.PR > 0:
		return fmt.Sprintf("%s#%d", safeInline(s.Repo, 60), s.PR)
	case s.Doc != "":
		return safeInline(s.Doc, 120)
	case s.Target != "":
		return safeInline(s.Target, 120)
	}
	return ""
}

func shortSHA(s string) string {
	if len(s) > 10 {
		return s[:10]
	}
	return s
}

// safeInline makes untrusted text safe to place inside a Markdown code span:
// no newlines, no backticks (which would close the span), no @ (mentions), and
// bounded length.
func safeInline(s string, max int) string {
	s = strings.NewReplacer("\r", " ", "\n", " ", "`", "'", "@", "＠").Replace(s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > max {
		s = string(r[:max]) + "…"
	}
	return s
}
