package vcs

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

type GitHub struct{}

func NewGitHub() *GitHub { return &GitHub{} }

func (*GitHub) Name() string { return "github" }

// Verify checks X-Hub-Signature-256 (hex HMAC-SHA256 of the raw body).
func (*GitHub) Verify(r *http.Request, body []byte, secret string) error {
	if secret == "" {
		return ErrBadSignature
	}
	got := r.Header.Get("X-Hub-Signature-256")
	if !strings.HasPrefix(got, "sha256=") {
		return ErrBadSignature
	}
	sig, err := hex.DecodeString(strings.TrimPrefix(got, "sha256="))
	if err != nil {
		return ErrBadSignature
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	if !hmac.Equal(mac.Sum(nil), sig) {
		return ErrBadSignature
	}
	return nil
}

func shortRef(ref string) string { return strings.TrimPrefix(ref, "refs/heads/") }

func (*GitHub) Parse(eventType string, body []byte) ([]VcsEvent, error) {
	switch eventType {
	case "push":
		var p struct {
			Ref        string `json:"ref"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
			Pusher struct {
				Name string `json:"name"`
			} `json:"pusher"`
			Commits []struct {
				ID      string `json:"id"`
				Message string `json:"message"`
				URL     string `json:"url"`
			} `json:"commits"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return nil, err
		}
		branch := shortRef(p.Ref)
		out := make([]VcsEvent, 0, len(p.Commits))
		for _, c := range p.Commits {
			out = append(out, VcsEvent{
				Kind: CommitPushed, Repo: p.Repository.FullName, Branch: branch,
				CommitSHA: c.ID, CommitMsg: c.Message, URL: c.URL, Actor: p.Pusher.Name,
			})
		}
		return out, nil

	case "create", "delete":
		var p struct {
			Ref        string `json:"ref"`
			RefType    string `json:"ref_type"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
			Sender struct {
				Login string `json:"login"`
			} `json:"sender"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return nil, err
		}
		if p.RefType != "branch" {
			return nil, nil // ignore tag create/delete
		}
		kind := BranchCreated
		if eventType == "delete" {
			kind = BranchDeleted
		}
		return []VcsEvent{{Kind: kind, Repo: p.Repository.FullName, Branch: shortRef(p.Ref), Actor: p.Sender.Login}}, nil

	case "pull_request":
		var p struct {
			Action      string `json:"action"`
			PullRequest struct {
				Number  int    `json:"number"`
				Title   string `json:"title"`
				Body    string `json:"body"`
				State   string `json:"state"`
				Merged  bool   `json:"merged"`
				HTMLURL string `json:"html_url"`
				Head    struct {
					Ref string `json:"ref"`
				} `json:"head"`
			} `json:"pull_request"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
			Sender struct {
				Login string `json:"login"`
			} `json:"sender"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return nil, err
		}
		kind := PRUpdated
		switch p.Action {
		case "opened", "reopened":
			kind = PROpened
		case "closed":
			if p.PullRequest.Merged {
				kind = PRMerged
			} else {
				kind = PRClosed
			}
		}
		return []VcsEvent{{
			Kind: kind, Repo: p.Repository.FullName, Branch: p.PullRequest.Head.Ref,
			PRNumber: p.PullRequest.Number, PRTitle: p.PullRequest.Title, PRBody: p.PullRequest.Body,
			PRState: p.PullRequest.State, PRMerged: p.PullRequest.Merged,
			URL: p.PullRequest.HTMLURL, Actor: p.Sender.Login,
		}}, nil
	}
	return nil, nil // unhandled event types are ignored
}

// prNumberString is used by the linker to build a link external_ref for a PR.
func prNumberString(n int) string { return strconv.Itoa(n) }
