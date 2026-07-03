// Package idempotency provides an opt-in Idempotency-Key middleware so clients
// (agents, retrying networks) can safely retry unsafe mutations without creating
// duplicates. A request carrying an Idempotency-Key header is processed at most
// once; retries with the same key replay the original response.
package idempotency

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/tojiuni/goquest/internal/auth"
)

// HeaderKey is the request header carrying the client-chosen idempotency key.
const HeaderKey = "Idempotency-Key"

// marker is the sentinel value stored while a request is in flight. It is not
// valid JSON, so it can never collide with a cached storedResp payload.
const marker = "\x00idem-processing"

const (
	// lockTTL bounds how long an in-flight request holds the key before a retry
	// is allowed to re-process (guards against a crashed handler wedging the key).
	lockTTL = 60 * time.Second
	// resultTTL is how long a completed response is replayable.
	resultTTL = 24 * time.Hour
)

// Store persists idempotency state. Implementations must make Claim atomic.
type Store interface {
	// Claim atomically reserves key for the caller. claimed=true means this
	// caller is the first; prior is nil. claimed=false means the key already
	// exists; prior holds its current value (the in-flight marker or a cached
	// response payload).
	Claim(ctx context.Context, key string, ttl time.Duration) (claimed bool, prior []byte, err error)
	// Save overwrites key with the final response payload at the given ttl.
	Save(ctx context.Context, key string, val []byte, ttl time.Duration) error
	// Release deletes the key so a later retry re-processes (used for 5xx).
	Release(ctx context.Context, key string) error
}

type storedResp struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}

// Middleware replays responses for repeated Idempotency-Key values. Requests
// without the header (or when store is nil) pass straight through. It must be
// mounted after authentication so the requester scopes the key.
func Middleware(store Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get(HeaderKey)
			if key == "" || store == nil {
				next.ServeHTTP(w, r)
				return
			}
			storeKey := "idem:" + auth.RequesterID(r) + ":" + r.Method + ":" + key
			ctx := r.Context()

			claimed, prior, err := store.Claim(ctx, storeKey, lockTTL)
			if err != nil {
				// Fail open: a store outage must not block writes.
				next.ServeHTTP(w, r)
				return
			}
			if !claimed {
				if string(prior) == marker {
					w.Header().Set("Retry-After", "1")
					http.Error(w, "a request with this Idempotency-Key is still being processed", http.StatusConflict)
					return
				}
				var resp storedResp
				if json.Unmarshal(prior, &resp) == nil {
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Idempotent-Replay", "true")
					w.WriteHeader(resp.Status)
					_, _ = w.Write([]byte(resp.Body))
					return
				}
				// Corrupt payload — fall through and re-process rather than 500.
			}

			cap := &captureWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(cap, r)

			// Never cache server errors: release the claim so a retry re-runs.
			if cap.status >= 500 {
				_ = store.Release(ctx, storeKey)
				return
			}
			payload, mErr := json.Marshal(storedResp{Status: cap.status, Body: cap.buf.String()})
			if mErr != nil {
				_ = store.Release(ctx, storeKey)
				return
			}
			_ = store.Save(ctx, storeKey, payload, resultTTL)
		})
	}
}

// captureWriter tees the handler's response to the client while buffering the
// status and body so a successful response can be cached for replay.
type captureWriter struct {
	http.ResponseWriter
	status  int
	buf     bytes.Buffer
	written bool
}

func (c *captureWriter) WriteHeader(status int) {
	if !c.written {
		c.status = status
		c.written = true
	}
	c.ResponseWriter.WriteHeader(status)
}

func (c *captureWriter) Write(b []byte) (int, error) {
	c.written = true
	c.buf.Write(b)
	return c.ResponseWriter.Write(b)
}
