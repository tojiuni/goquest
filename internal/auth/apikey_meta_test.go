package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func withKeyMeta(r *http.Request, meta *APIKeyMeta) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), apiKeyMetaCtxKey, meta))
}

func TestRequireAPIKeyCapability_RejectsJWTRequest(t *testing.T) {
	h := RequireAPIKeyCapability("ticket.relations.bulk_read")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil) // no KeyMeta in context => JWT/session path
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 for a non-API-key request, got %d", rec.Code)
	}
}

func TestRequireAPIKeyCapability_RejectsKeyWithoutCapability(t *testing.T) {
	h := RequireAPIKeyCapability("ticket.relations.bulk_read")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := withKeyMeta(httptest.NewRequest("GET", "/", nil), &APIKeyMeta{AllowedCapabilities: []string{"ticket.read"}})
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 for a key lacking the capability, got %d", rec.Code)
	}
}

func TestRequireAPIKeyCapability_AllowsKeyWithExactCapability(t *testing.T) {
	h := RequireAPIKeyCapability("ticket.relations.bulk_read")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := withKeyMeta(httptest.NewRequest("GET", "/", nil), &APIKeyMeta{AllowedCapabilities: []string{"ticket.relations.bulk_read"}})
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 for a key with the exact capability, got %d", rec.Code)
	}
}

func TestRequireAPIKeyCapability_AllowsWildcardKey(t *testing.T) {
	h := RequireAPIKeyCapability("ticket.relations.bulk_read")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := withKeyMeta(httptest.NewRequest("GET", "/", nil), &APIKeyMeta{AllowedCapabilities: []string{"*"}})
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 for a wildcard key, got %d", rec.Code)
	}
}
