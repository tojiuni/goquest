package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/directory"
	"github.com/tojiuni/goquest/internal/project"
	"github.com/tojiuni/goquest/internal/ticket"
	"github.com/tojiuni/goquest/internal/workspace"
)

type requesterTestProject struct{ workspace uuid.UUID }

func (p requesterTestProject) Get(context.Context, uuid.UUID) (*project.Project, error) {
	return &project.Project{WorkspaceID: p.workspace}, nil
}

type requesterTestMember struct{ member bool }

func (m requesterTestMember) GetMember(context.Context, uuid.UUID, string) (*workspace.Member, error) {
	if m.member {
		return &workspace.Member{}, nil
	}
	return nil, errors.New("not a member")
}

func TestRequesterTenantLookup(t *testing.T) {
	const host = "tenant.example.test"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Host != host || r.Method != http.MethodPost {
			t.Errorf("unexpected request %s host %s", r.Method, r.Host)
		}
		switch r.URL.Path {
		case "/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "expires_in": 3600})
		case "/v1/actors/lookup":
			if r.Header.Get("Authorization") != "Bearer token" {
				t.Error("missing bearer")
			}
			var body struct {
				IDs []string `json:"ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.IDs) != 1 {
				t.Errorf("bad body: %+v %v", body, err)
				return
			}
			if body.IDs[0] == "alice@example.com" {
				_ = json.NewEncoder(w).Encode([]directory.Actor{{ID: "actor", DisplayName: "Alice", Email: "alice@example.com"}})
			} else {
				_ = json.NewEncoder(w).Encode([]directory.Actor{})
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := directory.NewClient(server.URL, host, "client", "secret", server.Client())
	pid := uuid.New()
	tk := &ticket.Ticket{ProjectID: &pid}
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(auth.WithRequesterID(r.Context(), "caller"))
	for _, tc := range []struct {
		name, host, raw       string
		member                bool
		actor, display, email string
	}{
		{"resolved actor", host, "alice@example.com", true, "actor", "Alice", "alice@example.com"},
		{"unknown id", host, "unknown", true, "", "unknown", ""},
		{"config missing", "", "alice@example.com", true, "", "alice@example.com", ""},
		{"non-member caller", host, "alice@example.com", false, "actor", "Alice", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := calls
			resolver := NewRequesterResolver(requesterTestProject{uuid.New()}, requesterTestMember{tc.member}, NewTenantRequesterAccounts(tc.host, client))
			got := resolver.resolve(r, tk, tc.raw)
			if got.ActorID != tc.actor || got.DisplayName != tc.display || got.Email != tc.email {
				t.Fatalf("got %+v", got)
			}
			if tc.host == "" && calls != before {
				t.Fatal("lookup called without tenant host")
			}
		})
	}
}
