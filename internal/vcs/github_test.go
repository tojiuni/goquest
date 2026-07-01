package vcs

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"strings"
	"testing"
)

func sign(secret, body string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func TestGitHubVerify(t *testing.T) {
	g := NewGitHub()
	body := `{"zen":"x"}`
	r := httptest.NewRequest("POST", "/", strings.NewReader(body))
	r.Header.Set("X-Hub-Signature-256", sign("wrong", body))
	if err := g.Verify(r, []byte(body), "s3cr3t"); err == nil {
		t.Fatal("expected mismatch to fail")
	}
	r.Header.Set("X-Hub-Signature-256", sign("s3cr3t", body))
	if err := g.Verify(r, []byte(body), "s3cr3t"); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
}

func TestGitHubParsePullRequestMerged(t *testing.T) {
	body := `{"action":"closed","pull_request":{"number":12,"title":"goq-foo done","body":"closes goq-foo","state":"closed","merged":true,"html_url":"https://gh/pr/12","head":{"ref":"feature/goq-foo"},"base":{"ref":"main"}},"repository":{"full_name":"tojiuni/goquest"},"sender":{"login":"alice"}}`
	evs, err := NewGitHub().Parse("pull_request", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Kind != PRMerged {
		t.Fatalf("want 1 pr_merged, got %+v", evs)
	}
	e := evs[0]
	if e.Repo != "tojiuni/goquest" || e.PRNumber != 12 || e.Branch != "feature/goq-foo" || !e.PRMerged {
		t.Fatalf("bad event: %+v", e)
	}
	if e.PRBaseRef != "main" {
		t.Fatalf("bad PRBaseRef: %+v", e)
	}
}

func TestGitHubParsePushCommits(t *testing.T) {
	body := `{"ref":"refs/heads/feature/goq-foo","repository":{"full_name":"tojiuni/goquest"},"pusher":{"name":"alice"},"commits":[{"id":"abc123","message":"work on goq-foo","url":"https://gh/c/abc123"},{"id":"def456","message":"more","url":"https://gh/c/def456"}]}`
	evs, err := NewGitHub().Parse("push", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 {
		t.Fatalf("want 2 commit events, got %d", len(evs))
	}
	if evs[0].Kind != CommitPushed || evs[0].Branch != "feature/goq-foo" || evs[0].CommitSHA != "abc123" {
		t.Fatalf("bad commit event: %+v", evs[0])
	}
}

func TestGitHubParseCreateDeleteBranch(t *testing.T) {
	create := `{"ref":"feature/goq-foo","ref_type":"branch","repository":{"full_name":"tojiuni/goquest"},"sender":{"login":"alice"}}`
	evs, _ := NewGitHub().Parse("create", []byte(create))
	if len(evs) != 1 || evs[0].Kind != BranchCreated || evs[0].Branch != "feature/goq-foo" {
		t.Fatalf("bad create: %+v", evs)
	}
	del := `{"ref":"feature/goq-foo","ref_type":"branch","repository":{"full_name":"tojiuni/goquest"},"sender":{"login":"alice"}}`
	evs, _ = NewGitHub().Parse("delete", []byte(del))
	if len(evs) != 1 || evs[0].Kind != BranchDeleted {
		t.Fatalf("bad delete: %+v", evs)
	}
}
