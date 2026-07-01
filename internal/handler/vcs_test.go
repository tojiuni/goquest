package handler_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/tojiuni/goquest/internal/handler"
	"github.com/tojiuni/goquest/internal/vcs"
)

func ghSign(secret, body string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func TestGitHubWebhookRejectsBadSignature(t *testing.T) {
	h := handler.NewVCSHandler(nil, vcs.NewGitHub(), nil, "s3cr3t")
	r := chi.NewRouter()
	r.Post("/api/v1/vcs/webhooks/github", h.GitHubWebhook)

	body := `{"repository":{"full_name":"o/r"}}`
	req := httptest.NewRequest("POST", "/api/v1/vcs/webhooks/github", strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", ghSign("WRONG", body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestGitHubWebhookAcceptsSigned(t *testing.T) {
	h := handler.NewVCSHandler(nil, vcs.NewGitHub(), nil, "s3cr3t") // nil svc: no registered repo path still returns 200
	r := chi.NewRouter()
	r.Post("/api/v1/vcs/webhooks/github", h.GitHubWebhook)

	body := `{"ref":"refs/heads/goq-x","repository":{"full_name":"o/r"},"pusher":{"name":"a"},"commits":[]}`
	req := httptest.NewRequest("POST", "/api/v1/vcs/webhooks/github", strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", ghSign("s3cr3t", body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 && w.Code != 202 {
		t.Fatalf("want 200/202, got %d (%s)", w.Code, w.Body.String())
	}
}
