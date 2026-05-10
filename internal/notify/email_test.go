package notify_test

import (
	"context"
	"testing"

	"github.com/tojiuni/goquest/internal/notify"
)

// In dev mode (Host empty) Send is a no-op. We just verify it doesn't error.
func TestMailer_DevMode_DoesNotError(t *testing.T) {
	m := notify.New(notify.Config{})
	if err := m.SendMagicLink(context.Background(), "test@example.com", "https://example.com/x"); err != nil {
		t.Fatal(err)
	}
	if err := m.SendTicketReceived(context.Background(), "test@example.com", "Hi", "https://x"); err != nil {
		t.Fatal(err)
	}
	if err := m.SendTicketReplied(context.Background(), "test@example.com", "Hi", "https://x"); err != nil {
		t.Fatal(err)
	}
}

func TestMailer_RejectsEmptyRecipient(t *testing.T) {
	m := notify.New(notify.Config{Host: "smtp.example.com", Port: 25})
	if err := m.Send(context.Background(), "", "subj", "body"); err == nil {
		t.Fatal("expected error for empty recipient")
	}
}
