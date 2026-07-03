package idempotency

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeStore is an in-memory Store mirroring RedisStore semantics: Claim sets the
// marker on first sight, returns the stored value on repeats.
type fakeStore struct {
	mu       sync.Mutex
	data     map[string][]byte
	claimErr error
}

func newFakeStore() *fakeStore { return &fakeStore{data: map[string][]byte{}} }

func (f *fakeStore) Claim(_ context.Context, key string, _ time.Duration) (bool, []byte, error) {
	if f.claimErr != nil {
		return false, nil, f.claimErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.data[key]; ok {
		return false, v, nil
	}
	f.data[key] = []byte(marker)
	return true, nil, nil
}

func (f *fakeStore) Save(_ context.Context, key string, val []byte, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[key] = val
	return nil
}

func (f *fakeStore) Release(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.data, key)
	return nil
}

// countingHandler returns 201 + a body, counting invocations.
func countingHandler(calls *int, status int, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
}

func do(mw func(http.Handler) http.Handler, h http.Handler, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/tickets", nil)
	if key != "" {
		r.Header.Set(HeaderKey, key)
	}
	rec := httptest.NewRecorder()
	mw(h).ServeHTTP(rec, r)
	return rec
}

func TestMiddleware_NoHeaderPassesThrough(t *testing.T) {
	calls := 0
	mw := Middleware(newFakeStore())
	h := countingHandler(&calls, 201, `{"id":"a"}`)
	if rec := do(mw, h, ""); rec.Code != 201 {
		t.Fatalf("want 201, got %d", rec.Code)
	}
	if rec := do(mw, h, ""); rec.Code != 201 {
		t.Fatalf("want 201, got %d", rec.Code)
	}
	if calls != 2 {
		t.Errorf("no key: handler must run every time, ran %d times", calls)
	}
}

func TestMiddleware_ReplaysSameKey(t *testing.T) {
	calls := 0
	mw := Middleware(newFakeStore())
	h := countingHandler(&calls, 201, `{"id":"abc"}`)

	first := do(mw, h, "key-1")
	if first.Code != 201 || first.Body.String() != `{"id":"abc"}` {
		t.Fatalf("first: got %d %q", first.Code, first.Body.String())
	}
	if first.Header().Get("Idempotent-Replay") != "" {
		t.Errorf("first request must not be marked as replay")
	}

	second := do(mw, h, "key-1")
	if second.Code != 201 || second.Body.String() != `{"id":"abc"}` {
		t.Fatalf("replay: got %d %q", second.Code, second.Body.String())
	}
	if second.Header().Get("Idempotent-Replay") != "true" {
		t.Errorf("replay must set Idempotent-Replay: true")
	}
	if calls != 1 {
		t.Errorf("handler must run exactly once across the key, ran %d", calls)
	}
}

func TestMiddleware_DifferentKeysBothRun(t *testing.T) {
	calls := 0
	mw := Middleware(newFakeStore())
	h := countingHandler(&calls, 201, `{"id":"x"}`)
	_ = do(mw, h, "key-a")
	_ = do(mw, h, "key-b")
	if calls != 2 {
		t.Errorf("distinct keys must each run, ran %d", calls)
	}
}

func TestMiddleware_InFlightReturns409(t *testing.T) {
	store := newFakeStore()
	// Simulate an in-flight request: claim the key (sets marker) without saving.
	claimed, _, _ := store.Claim(context.Background(), "idem::POST:key-2", lockTTL)
	if !claimed {
		t.Fatal("setup: expected to claim")
	}
	calls := 0
	mw := Middleware(store)
	h := countingHandler(&calls, 201, `{}`)
	rec := do(mw, h, "key-2")
	if rec.Code != http.StatusConflict {
		t.Errorf("in-flight: want 409, got %d", rec.Code)
	}
	if calls != 0 {
		t.Errorf("in-flight: handler must not run, ran %d", calls)
	}
}

func TestMiddleware_ServerErrorNotCached(t *testing.T) {
	store := newFakeStore()
	calls := 0
	mw := Middleware(store)
	h := countingHandler(&calls, 500, `boom`)

	if rec := do(mw, h, "key-3"); rec.Code != 500 {
		t.Fatalf("want 500, got %d", rec.Code)
	}
	// A retry after a 5xx must re-process (the claim was released).
	h2 := countingHandler(&calls, 201, `{"ok":true}`)
	rec := do(mw, h2, "key-3")
	if rec.Code != 201 {
		t.Errorf("retry after 5xx: want 201, got %d", rec.Code)
	}
	if calls != 2 {
		t.Errorf("5xx must not be cached; want 2 runs, got %d", calls)
	}
}

func TestMiddleware_StoreErrorFailsOpen(t *testing.T) {
	store := newFakeStore()
	store.claimErr = errors.New("redis down")
	calls := 0
	mw := Middleware(store)
	h := countingHandler(&calls, 201, `{}`)
	if rec := do(mw, h, "key-4"); rec.Code != 201 {
		t.Errorf("fail-open: want 201, got %d", rec.Code)
	}
	if calls != 1 {
		t.Errorf("fail-open: handler must run, ran %d", calls)
	}
}
