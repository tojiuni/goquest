// Package notify sends transactional emails (magic-link, ticket events).
// In production it speaks SMTP via an upstream relay; in dev/test the SMTP
// host can be unset to switch the package into log-only mode.
package notify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/smtp"
	"strings"
	"text/template"
)

// Config carries SMTP connection settings. All fields except Host are optional.
type Config struct {
	Host       string // SMTP relay hostname (e.g. smtp.example.com)
	Port       int    // 25 / 465 / 587
	Username   string // SMTP AUTH PLAIN username
	Password   string // SMTP AUTH PLAIN password (PROD: from Vault)
	From       string // RFC 5322 sender address (e.g. "Goquest <noreply@toji.homes>")
	UseStartTLS bool
}

// Mailer sends emails via SMTP, or logs them when Config.Host is empty.
type Mailer struct {
	cfg Config
}

func New(cfg Config) *Mailer { return &Mailer{cfg: cfg} }

// Send delivers a rendered email. If cfg.Host is empty (dev mode), the
// message is logged and discarded — sufficient for unit tests and local runs.
func (m *Mailer) Send(_ context.Context, to, subject, body string) error {
	if m.cfg.Host == "" {
		log.Printf("[notify:dev] To=%s Subject=%q Body=%s", to, subject, truncate(body, 200))
		return nil
	}
	if to == "" {
		return errors.New("recipient required")
	}
	from := m.cfg.From
	if from == "" {
		from = "noreply@toji.homes"
	}

	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", from)
	fmt.Fprintf(&msg, "To: %s\r\n", to)
	fmt.Fprintf(&msg, "Subject: %s\r\n", subject)
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
	msg.WriteString(body)

	addr := fmt.Sprintf("%s:%d", m.cfg.Host, m.cfg.Port)
	var auth smtp.Auth
	if m.cfg.Username != "" {
		auth = smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)
	}
	return smtp.SendMail(addr, auth, fromAddr(from), []string{to}, msg.Bytes())
}

// SendMagicLink satisfies handler.EmailDelivery so the magic-link handler
// can email the link instead of returning it in the API response.
func (m *Mailer) SendMagicLink(ctx context.Context, to, magicURL string) error {
	body, err := renderMagicLinkBody(magicURL)
	if err != nil {
		return err
	}
	return m.Send(ctx, to, "Goquest 로그인 링크", body)
}

// SendTicketReceived notifies the customer that a ticket was created.
func (m *Mailer) SendTicketReceived(ctx context.Context, to, ticketTitle, ticketURL string) error {
	body, err := renderTicketReceivedBody(ticketTitle, ticketURL)
	if err != nil {
		return err
	}
	return m.Send(ctx, to, "[Goquest] 문의 접수 완료: "+ticketTitle, body)
}

// SendTicketReplied notifies the customer that staff replied.
func (m *Mailer) SendTicketReplied(ctx context.Context, to, ticketTitle, ticketURL string) error {
	body, err := renderTicketRepliedBody(ticketTitle, ticketURL)
	if err != nil {
		return err
	}
	return m.Send(ctx, to, "[Goquest] 답변 도착: "+ticketTitle, body)
}

// --- templates --------------------------------------------------------------

const tmplMagic = `<p>안녕하세요,</p>
<p>아래 링크를 클릭하면 Goquest에 로그인할 수 있습니다 (15분 유효):</p>
<p><a href="{{.URL}}">{{.URL}}</a></p>
<p>본인이 요청하지 않았다면 이 메일을 무시하셔도 됩니다.</p>`

const tmplTicketReceived = `<p>문의가 접수되었습니다.</p>
<p>제목: <b>{{.Title}}</b></p>
<p>아래 링크에서 진행 상황을 확인하실 수 있습니다:</p>
<p><a href="{{.URL}}">{{.URL}}</a></p>`

const tmplTicketReplied = `<p>새로운 답변이 도착했습니다.</p>
<p>제목: <b>{{.Title}}</b></p>
<p><a href="{{.URL}}">{{.URL}}</a></p>`

func renderMagicLinkBody(url string) (string, error) {
	return render(tmplMagic, map[string]string{"URL": url})
}
func renderTicketReceivedBody(title, url string) (string, error) {
	return render(tmplTicketReceived, map[string]string{"Title": title, "URL": url})
}
func renderTicketRepliedBody(title, url string) (string, error) {
	return render(tmplTicketReplied, map[string]string{"Title": title, "URL": url})
}

func render(tmpl string, data any) (string, error) {
	t, err := template.New("email").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// fromAddr extracts the bare email from "Display <user@host>" or returns the
// input as-is if no angle brackets are present.
func fromAddr(s string) string {
	if i := strings.LastIndex(s, "<"); i >= 0 {
		if j := strings.LastIndex(s, ">"); j > i {
			return s[i+1 : j]
		}
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
