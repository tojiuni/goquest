package device_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/device"
)

type fakeStore struct {
	tokens map[uuid.UUID]*device.Token
}

func newFakeStore() *fakeStore { return &fakeStore{tokens: make(map[uuid.UUID]*device.Token)} }

func (s *fakeStore) Upsert(_ context.Context, userID string, req device.RegisterReq) (*device.Token, error) {
	for _, t := range s.tokens {
		if t.UserID == userID && t.APNSToken == req.APNSToken {
			t.AppVersion = req.AppVersion
			return t, nil
		}
	}
	t := &device.Token{
		ID:         uuid.New(),
		UserID:     userID,
		Platform:   req.Platform,
		APNSToken:  req.APNSToken,
		AppVersion: req.AppVersion,
	}
	s.tokens[t.ID] = t
	return t, nil
}
func (s *fakeStore) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := s.tokens[id]; !ok {
		return device.ErrNotFound
	}
	delete(s.tokens, id)
	return nil
}
func (s *fakeStore) ListByUser(_ context.Context, userID string) ([]*device.Token, error) {
	var out []*device.Token
	for _, t := range s.tokens {
		if t.UserID == userID {
			out = append(out, t)
		}
	}
	return out, nil
}

func TestService_Register_ValidatesInput(t *testing.T) {
	svc := device.NewService(newFakeStore())
	if _, err := svc.Register(context.Background(), "", device.RegisterReq{Platform: device.PlatformIOS, APNSToken: "abc"}); err == nil {
		t.Fatal("expected error for empty user")
	}
	if _, err := svc.Register(context.Background(), "u1", device.RegisterReq{Platform: device.PlatformIOS, APNSToken: ""}); err == nil {
		t.Fatal("expected error for empty token")
	}
	if _, err := svc.Register(context.Background(), "u1", device.RegisterReq{Platform: "macos", APNSToken: "abc"}); err == nil {
		t.Fatal("expected error for unknown platform")
	}
}

func TestService_Register_IsIdempotent(t *testing.T) {
	store := newFakeStore()
	svc := device.NewService(store)
	tok1, err := svc.Register(context.Background(), "u1", device.RegisterReq{
		Platform: device.PlatformIOS, APNSToken: "abc", AppVersion: "1.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	tok2, err := svc.Register(context.Background(), "u1", device.RegisterReq{
		Platform: device.PlatformIOS, APNSToken: "abc", AppVersion: "1.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if tok1.ID != tok2.ID {
		t.Fatalf("re-registering same token should reuse row id; got %s vs %s", tok1.ID, tok2.ID)
	}
	if tok2.AppVersion != "1.0.1" {
		t.Fatalf("app_version not updated: %q", tok2.AppVersion)
	}
}

func TestService_ListByUser(t *testing.T) {
	svc := device.NewService(newFakeStore())
	for _, tok := range []string{"a", "b", "c"} {
		_, _ = svc.Register(context.Background(), "u1", device.RegisterReq{Platform: device.PlatformIOS, APNSToken: tok})
	}
	_, _ = svc.Register(context.Background(), "u2", device.RegisterReq{Platform: device.PlatformIOS, APNSToken: "x"})
	list, err := svc.List(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 tokens for u1, got %d", len(list))
	}
}
