package webhook_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tojiuni/goquest/internal/webhook"
)

func TestDeliverer_Send_Success(t *testing.T) {
	var received atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected Content-Type: %s", r.Header.Get("Content-Type"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := webhook.New()
	err := d.Send(context.Background(), srv.URL, map[string]string{"status": "completed"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if received.Load() != 1 {
		t.Errorf("expected 1 request, got %d", received.Load())
	}
}

func TestDeliverer_Send_PayloadIsJSON(t *testing.T) {
	type payload struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}

	var decoded payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	d := webhook.New()
	err := d.Send(context.Background(), srv.URL, payload{ID: "abc", Status: "completed"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decoded.ID != "abc" || decoded.Status != "completed" {
		t.Errorf("unexpected payload: %+v", decoded)
	}
}

func TestDeliverer_Send_RetriesOnFailure(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	d := webhook.New()
	err := d.Send(context.Background(), srv.URL, "test")
	if err != nil {
		t.Fatalf("unexpected error after retries: %v", err)
	}
	if calls.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", calls.Load())
	}
}

func TestDeliverer_Send_FailsAfterMaxAttempts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	d := webhook.New()
	err := d.Send(context.Background(), srv.URL, "test")
	if err == nil {
		t.Fatal("expected error after max attempts")
	}
}

func TestDeliverer_Send_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	d := webhook.New()
	err := d.Send(ctx, srv.URL, "test")
	if err == nil {
		t.Fatal("expected error due to context cancellation")
	}
}
