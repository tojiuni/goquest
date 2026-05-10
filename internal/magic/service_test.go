package magic_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/magic"
)

type fakeStore struct {
	tokens map[string]*magic.Token
}

func newFakeStore() *fakeStore {
	return &fakeStore{tokens: make(map[string]*magic.Token)}
}

func (s *fakeStore) Create(_ context.Context, t *magic.Token, hash string) error {
	s.tokens[hash] = t
	return nil
}
func (s *fakeStore) FindByHash(_ context.Context, hash string) (*magic.Token, error) {
	t, ok := s.tokens[hash]
	if !ok {
		return nil, magic.ErrNotFound
	}
	return t, nil
}
func (s *fakeStore) MarkUsed(_ context.Context, id uuid.UUID) error {
	for _, t := range s.tokens {
		if t.ID == id {
			now := t.CreatedAt
			t.UsedAt = &now
			return nil
		}
	}
	return magic.ErrNotFound
}

func TestService_Request_RejectsBadEmail(t *testing.T) {
	svc := magic.NewService(newFakeStore())
	if _, err := svc.Request(context.Background(), "not-an-email", nil); err == nil {
		t.Fatal("expected error for bad email")
	}
}

func TestService_RequestExchange_HappyPath(t *testing.T) {
	svc := magic.NewService(newFakeStore())
	plain, err := svc.Request(context.Background(), "Alice@example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plain, "ml_") {
		t.Fatalf("expected ml_ prefix, got %q", plain)
	}

	res, err := svc.Exchange(context.Background(), plain)
	if err != nil {
		t.Fatal(err)
	}
	if res.Email != "alice@example.com" {
		t.Fatalf("email should be lowercased, got %q", res.Email)
	}
	if !strings.HasPrefix(res.AccessToken, "ct_") {
		t.Fatalf("expected ct_ access token, got %q", res.AccessToken)
	}

	// Re-exchange should fail (single-use)
	if _, err := svc.Exchange(context.Background(), plain); !errors.Is(err, magic.ErrUsed) {
		t.Fatalf("expected ErrUsed on second exchange, got %v", err)
	}
}

func TestService_Exchange_RejectsUnknown(t *testing.T) {
	svc := magic.NewService(newFakeStore())
	if _, err := svc.Exchange(context.Background(), "ml_unknown"); !errors.Is(err, magic.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestService_Exchange_RejectsWrongPrefix(t *testing.T) {
	svc := magic.NewService(newFakeStore())
	if _, err := svc.Exchange(context.Background(), "wrong_prefix_token"); err == nil {
		t.Fatal("expected error for wrong prefix")
	}
}

func TestParseCustomerAccessToken_RoundTrip(t *testing.T) {
	svc := magic.NewService(newFakeStore())
	plain, err := svc.Request(context.Background(), "bob@example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.Exchange(context.Background(), plain)
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := magic.ParseCustomerAccessToken(res.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Email != "bob@example.com" {
		t.Fatalf("email mismatch: %q", parsed.Email)
	}
}

func TestParseCustomerAccessToken_RejectsTampered(t *testing.T) {
	svc := magic.NewService(newFakeStore())
	plain, _ := svc.Request(context.Background(), "carol@example.com", nil)
	res, _ := svc.Exchange(context.Background(), plain)

	// Flip a byte in the signature portion (after the dot).
	parts := strings.SplitN(res.AccessToken, ".", 2)
	if len(parts) != 2 {
		t.Fatalf("unexpected token format: %q", res.AccessToken)
	}
	tampered := parts[0] + "." + parts[1][1:] + "0"
	if _, err := magic.ParseCustomerAccessToken(tampered); err == nil {
		t.Fatal("expected error for tampered signature")
	}
}
