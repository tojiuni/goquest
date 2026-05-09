package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBearerToken(t *testing.T) {
	cases := []struct {
		header string
		want   string
	}{
		{"Bearer abc123", "abc123"},
		{"bearer abc123", "abc123"},
		{"BEARER abc123", "abc123"},
		{"", ""},
		{"Basic abc123", ""},
		{"Beareronly", ""},
		{"Bearer ", ""},
	}

	for _, tc := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if tc.header != "" {
			r.Header.Set("Authorization", tc.header)
		}
		got := bearerToken(r)
		if got != tc.want {
			t.Errorf("bearerToken(%q) = %q, want %q", tc.header, got, tc.want)
		}
	}
}

func TestMiddleware_SkipMode(t *testing.T) {
	mw := Middleware("", true)

	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		id := RequesterID(r)
		if id != "dev-user" {
			t.Errorf("expected requester_id=dev-user in skip mode, got %q", id)
		}
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if !called {
		t.Fatal("handler was not called in skip mode")
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestMiddleware_NoAuth_Returns401(t *testing.T) {
	// non-skip mode with empty JWKS URL — any token will fail validation,
	// but a missing token should return 401 before even attempting parse
	mw := Middleware("http://invalid-jwks-url-for-test", false)

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for missing auth header, got %d", w.Code)
	}
}

func TestRequesterID_Empty(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if id := RequesterID(r); id != "" {
		t.Errorf("expected empty requester_id with no context value, got %q", id)
	}
}
