# goquest ↔ VCS Linking Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Receive GitHub webhooks and auto-link branches/commits/PRs to goquest tickets by `title_name`, with rule-based ticket status auto-transition, behind a provider-adapter module so other git hosts can reuse the same pipeline.

**Architecture:** New Go module `internal/vcs/` in goquest: a `Provider` interface (GitHub adapter first) normalizes webhook payloads into `VcsEvent`; a shared `linker` service resolves the mapped project, extracts `title_name` tokens from branch/commit/PR, upserts `vcs_links` idempotently (branch is a first-class anchor whose commits inherit its ticket), and drives status transitions through the existing `ticket.Service`. A public webhook handler is mounted outside the API-key wall. `vcs_links` are embedded in the ticket GET responses at the handler layer. goquest-mcp gets repo-registration tools and prompt conventions.

**Tech Stack:** Go, chi router, pgx/v5 (`pgxpool`), `crypto/hmac`+`crypto/sha256`, `github.com/google/uuid`. MCP piece: TypeScript (`@modelcontextprotocol/sdk`, zod, tsx) in the separate `goquest-mcp` repo.

## Global Constraints

- Migrations are idempotent SQL files `internal/migrate/sql/NNN_*.sql`, run whole-file on **every boot** with no version table → all DDL MUST use `IF NOT EXISTS` / `ADD COLUMN IF NOT EXISTS`. Next free number is **018**.
- `title_name` format is `^[a-z][a-z0-9]*(-[a-z0-9]+)+$` (lowercase, ≥1 hyphen). Token extraction uses the unanchored form `[a-z][a-z0-9]*(?:-[a-z0-9]+)+`.
- The ticket state machine has **no `pending → in_progress` edge**. To reach `in_progress` from `pending`, transition `pending → assigned` then `assigned → in_progress` (two `ticket.Service.Update` calls).
- Never transition terminal tickets (`completed`, `failed`, `cancelled`). Skip transition but still record the link.
- **No import cycle:** package `ticket` must NOT import `vcs`. `vcs` may import `ticket` and `comment`. Embedding `vcs_links` into ticket JSON happens in the `handler` package (which may import both), never in `internal/ticket`.
- pgx house style: manual `Scan` via a `rowScanner interface{ Scan(dest ...any) error }`; unique-violation detection via `errors.As(err, &pgErr)` + `pgErr.Code == "23505"`; `pgx.ErrNoRows → ErrNotFound`.
- Webhook endpoint is **public** (no `X-API-Key`); authenticity is enforced by GitHub `X-Hub-Signature-256` HMAC-SHA256 over the raw body using a provider-level secret from env `VCS_GITHUB_WEBHOOK_SECRET`. Empty secret ⇒ reject all (401), i.e. feature disabled.
- Run Go tests with `go test ./internal/vcs/... ./internal/handler/...` from `/workspace/repos/goquest`.

---

### Task 1: vcs models + title_name extraction

**Files:**
- Create: `internal/vcs/model.go`
- Create: `internal/vcs/extract.go`
- Test: `internal/vcs/extract_test.go`

**Interfaces:**
- Produces: `vcs.EventKind`/`vcs.LinkKind`/`vcs.LinkState` const sets; structs `vcs.VcsEvent`, `vcs.VcsRepo`, `vcs.VcsLink`; `vcs.ExtractTitleNames(texts ...string) []string`.

- [ ] **Step 1: Write the failing test** — `internal/vcs/extract_test.go`

```go
package vcs

import (
	"reflect"
	"testing"
)

func TestExtractTitleNames(t *testing.T) {
	cases := []struct {
		name  string
		texts []string
		want  []string
	}{
		{"branch plain", []string{"goq-vault-portforward"}, []string{"goq-vault-portforward"}},
		{"branch prefixed", []string{"feature/goq-vault-portforward"}, []string{"goq-vault-portforward"}},
		{"commit msg", []string{"fix login for goq-auth-bug and who-render"}, []string{"goq-auth-bug", "who-render"}},
		{"dedup across sources", []string{"goq-x", "did goq-x"}, []string{"goq-x"}},
		{"no hyphen -> none", []string{"goq", "main", "HEAD"}, nil},
		{"uppercase -> none", []string{"GOQ-FOO"}, nil}, // title_name is lowercase; a token needs a lowercase start + an internal hyphen
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ExtractTitleNames(c.texts...)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("ExtractTitleNames(%v) = %v, want %v", c.texts, got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/vcs/ -run TestExtractTitleNames`
Expected: FAIL — `undefined: ExtractTitleNames` (build error).

- [ ] **Step 3: Write `internal/vcs/model.go`**

```go
package vcs

import (
	"time"

	"github.com/google/uuid"
)

type EventKind string

const (
	BranchCreated EventKind = "branch_created"
	BranchDeleted EventKind = "branch_deleted"
	CommitPushed  EventKind = "commit_pushed"
	PROpened      EventKind = "pr_opened"
	PRUpdated     EventKind = "pr_updated"
	PRMerged      EventKind = "pr_merged"
	PRClosed      EventKind = "pr_closed"
)

type LinkKind string

const (
	KindBranch LinkKind = "branch"
	KindCommit LinkKind = "commit"
	KindPR     LinkKind = "pull_request"
)

type LinkState string

const (
	StateOpen    LinkState = "open"
	StateMerged  LinkState = "merged"
	StateClosed  LinkState = "closed"
	StateDeleted LinkState = "deleted"
)

// VcsEvent is the provider-agnostic normalized form a Provider.Parse emits.
type VcsEvent struct {
	Kind      EventKind
	Repo      string // full name, e.g. "tojiuni/goquest"
	Branch    string // short ref name
	CommitSHA string
	CommitMsg string
	PRNumber  int
	PRTitle   string
	PRBody    string
	PRState   string
	PRMerged  bool
	URL       string
	Actor     string
}

// VcsRepo maps a git repo to a goquest project for title_name resolution.
type VcsRepo struct {
	ID             uuid.UUID `json:"id"`
	Provider       string    `json:"provider"`
	RepoFullName   string    `json:"repo_full_name"`
	ProjectID      uuid.UUID `json:"project_id"`
	AutoTransition bool      `json:"auto_transition"`
	MergeTarget    string    `json:"merge_target"` // "completed" | "pending_approval"
	Active         bool      `json:"active"`
	CreatedAt      time.Time `json:"created_at"`
}

// VcsLink is one git object (branch/commit/PR) linked to a ticket.
type VcsLink struct {
	ID           uuid.UUID `json:"id"`
	TicketID     uuid.UUID `json:"ticket_id"`
	Provider     string    `json:"provider"`
	RepoFullName string    `json:"repo_full_name"`
	Kind         LinkKind  `json:"kind"`
	ExternalRef  string    `json:"external_ref"`
	URL          string    `json:"url"`
	State        LinkState `json:"state"`
	Title        string    `json:"title"`
	Actor        string    `json:"actor"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
```

- [ ] **Step 4: Write `internal/vcs/extract.go`**

```go
package vcs

import "regexp"

// titleNameTokenRe is the unanchored form of the title_name format
// ^[a-z][a-z0-9]*(-[a-z0-9]+)+$ used to scan tokens out of free text.
var titleNameTokenRe = regexp.MustCompile(`[a-z][a-z0-9]*(?:-[a-z0-9]+)+`)

// ExtractTitleNames returns the distinct title_name-shaped tokens found across
// all input texts, in first-seen order.
func ExtractTitleNames(texts ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range texts {
		for _, m := range titleNameTokenRe.FindAllString(t, -1) {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	return out
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/vcs/ -run TestExtractTitleNames`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/vcs/model.go internal/vcs/extract.go internal/vcs/extract_test.go
git commit -m "feat(vcs): models + title_name token extraction"
```

---

### Task 2: Provider interface + GitHub adapter (Verify + Parse)

**Files:**
- Create: `internal/vcs/provider.go`
- Create: `internal/vcs/github.go`
- Test: `internal/vcs/github_test.go`

**Interfaces:**
- Consumes: `VcsEvent`, `EventKind` (Task 1).
- Produces: `vcs.Provider` interface; `vcs.NewGitHub() *GitHub`; `GitHub.Name() string`; `GitHub.Verify(r *http.Request, body []byte, secret string) error`; `GitHub.Parse(eventType string, body []byte) ([]VcsEvent, error)`; sentinel `vcs.ErrBadSignature`.

- [ ] **Step 1: Write the failing test** — `internal/vcs/github_test.go`

```go
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
	r.Header.Set("X-Hub-Signature-256", sign("s3cr3t", body))
	if err := g.Verify(r, []byte(body), "s3cr3t"); err == nil {
		t.Fatal("expected mismatch to fail")
	}
	r.Header.Set("X-Hub-Signature-256", sign("s3cr3t", body))
	if err := g.Verify(r, []byte(body), "s3cr3t"); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
}

func TestGitHubParsePullRequestMerged(t *testing.T) {
	body := `{"action":"closed","pull_request":{"number":12,"title":"goq-foo done","body":"closes goq-foo","state":"closed","merged":true,"html_url":"https://gh/pr/12","head":{"ref":"feature/goq-foo"}},"repository":{"full_name":"tojiuni/goquest"},"sender":{"login":"alice"}}`
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/vcs/ -run TestGitHub`
Expected: FAIL — `undefined: NewGitHub`.

- [ ] **Step 3: Write `internal/vcs/provider.go`**

```go
package vcs

import (
	"errors"
	"net/http"
)

var ErrBadSignature = errors.New("vcs: bad signature")

// Provider normalizes a git host's webhooks. Adapters live per host; the linker
// pipeline is provider-agnostic.
type Provider interface {
	Name() string
	Verify(r *http.Request, body []byte, secret string) error
	Parse(eventType string, body []byte) ([]VcsEvent, error)
}
```

- [ ] **Step 4: Write `internal/vcs/github.go`**

```go
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
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/vcs/ -run TestGitHub`
Expected: PASS (all 4 subtests).

- [ ] **Step 6: Commit**

```bash
git add internal/vcs/provider.go internal/vcs/github.go internal/vcs/github_test.go
git commit -m "feat(vcs): Provider interface + GitHub adapter (verify + parse)"
```

---

### Task 3: DB migration + Store (interface + Postgres)

**Files:**
- Create: `internal/migrate/sql/018_vcs.sql`
- Create: `internal/vcs/store.go`
- Create: `internal/vcs/store_pg.go`
- Test: `internal/vcs/store_pg_test.go`

**Interfaces:**
- Consumes: `VcsRepo`, `VcsLink` (Task 1).
- Produces: `vcs.Store` interface; `vcs.NewPGStore(db *pgxpool.Pool) Store`; `vcs.ErrNotFound`. Store methods: `CreateRepo(ctx, *VcsRepo) error`, `GetRepo(ctx, provider, repoFullName string) (*VcsRepo, error)`, `ListRepos(ctx) ([]*VcsRepo, error)`, `UpsertLink(ctx, *VcsLink) (*VcsLink, error)`, `GetBranchLink(ctx, provider, repoFullName, branch string) (*VcsLink, error)`, `LinksForTicket(ctx, ticketID uuid.UUID) ([]VcsLink, error)`.

- [ ] **Step 1: Write `internal/migrate/sql/018_vcs.sql`** (idempotent per Global Constraints)

```sql
-- VCS linking: repo→project registration + branch/commit/PR links.
CREATE TABLE IF NOT EXISTS vcs_repos (
    id              UUID PRIMARY KEY,
    provider        TEXT NOT NULL,
    repo_full_name  TEXT NOT NULL,
    project_id      UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    auto_transition BOOLEAN NOT NULL DEFAULT TRUE,
    merge_target    TEXT NOT NULL DEFAULT 'completed',
    active          BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS vcs_repos_provider_repo_uq
    ON vcs_repos (provider, repo_full_name);

CREATE TABLE IF NOT EXISTS vcs_links (
    id             UUID PRIMARY KEY,
    ticket_id      UUID NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    provider       TEXT NOT NULL,
    repo_full_name TEXT NOT NULL,
    kind           TEXT NOT NULL,          -- branch | commit | pull_request
    external_ref   TEXT NOT NULL,          -- branch name | sha | PR number
    url            TEXT NOT NULL DEFAULT '',
    state          TEXT NOT NULL DEFAULT 'open',
    title          TEXT NOT NULL DEFAULT '',
    actor          TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS vcs_links_uq
    ON vcs_links (ticket_id, provider, repo_full_name, kind, external_ref);
CREATE INDEX IF NOT EXISTS vcs_links_ticket_idx ON vcs_links (ticket_id);
CREATE INDEX IF NOT EXISTS vcs_links_branch_idx
    ON vcs_links (provider, repo_full_name, external_ref) WHERE kind = 'branch';
```

- [ ] **Step 2: Write `internal/vcs/store.go`**

```go
package vcs

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("vcs: not found")

type Store interface {
	CreateRepo(ctx context.Context, repo *VcsRepo) error
	GetRepo(ctx context.Context, provider, repoFullName string) (*VcsRepo, error)
	ListRepos(ctx context.Context) ([]*VcsRepo, error)
	UpsertLink(ctx context.Context, l *VcsLink) (*VcsLink, error)
	GetBranchLink(ctx context.Context, provider, repoFullName, branch string) (*VcsLink, error)
	LinksForTicket(ctx context.Context, ticketID uuid.UUID) ([]VcsLink, error)
}
```

- [ ] **Step 3: Write `internal/vcs/store_pg.go`** (mirror `internal/tag/store_pg.go` + `internal/device/store_pg.go` upsert)

```go
package vcs

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pgStore struct{ db *pgxpool.Pool }

func NewPGStore(db *pgxpool.Pool) Store { return &pgStore{db: db} }

type rowScanner interface{ Scan(dest ...any) error }

func scanRepo(row rowScanner) (*VcsRepo, error) {
	var r VcsRepo
	if err := row.Scan(&r.ID, &r.Provider, &r.RepoFullName, &r.ProjectID,
		&r.AutoTransition, &r.MergeTarget, &r.Active, &r.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &r, nil
}

const repoCols = `id, provider, repo_full_name, project_id, auto_transition, merge_target, active, created_at`

func (s *pgStore) CreateRepo(ctx context.Context, r *VcsRepo) error {
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO vcs_repos (id, provider, repo_full_name, project_id, auto_transition, merge_target, active)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (provider, repo_full_name) DO UPDATE SET
			project_id = EXCLUDED.project_id,
			auto_transition = EXCLUDED.auto_transition,
			merge_target = EXCLUDED.merge_target,
			active = EXCLUDED.active`,
		r.ID, r.Provider, r.RepoFullName, r.ProjectID, r.AutoTransition, r.MergeTarget, r.Active)
	return err
}

func (s *pgStore) GetRepo(ctx context.Context, provider, repoFullName string) (*VcsRepo, error) {
	return scanRepo(s.db.QueryRow(ctx,
		`SELECT `+repoCols+` FROM vcs_repos WHERE provider=$1 AND repo_full_name=$2 AND active`, provider, repoFullName))
}

func (s *pgStore) ListRepos(ctx context.Context) ([]*VcsRepo, error) {
	rows, err := s.db.Query(ctx, `SELECT `+repoCols+` FROM vcs_repos ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*VcsRepo
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

const linkCols = `id, ticket_id, provider, repo_full_name, kind, external_ref, url, state, title, actor, created_at, updated_at`

func scanLink(row rowScanner) (*VcsLink, error) {
	var l VcsLink
	if err := row.Scan(&l.ID, &l.TicketID, &l.Provider, &l.RepoFullName, &l.Kind, &l.ExternalRef,
		&l.URL, &l.State, &l.Title, &l.Actor, &l.CreatedAt, &l.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &l, nil
}

func (s *pgStore) UpsertLink(ctx context.Context, l *VcsLink) (*VcsLink, error) {
	if l.ID == uuid.Nil {
		l.ID = uuid.New()
	}
	return scanLink(s.db.QueryRow(ctx, `
		INSERT INTO vcs_links (id, ticket_id, provider, repo_full_name, kind, external_ref, url, state, title, actor)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (ticket_id, provider, repo_full_name, kind, external_ref) DO UPDATE SET
			url = EXCLUDED.url, state = EXCLUDED.state, title = EXCLUDED.title,
			actor = EXCLUDED.actor, updated_at = now()
		RETURNING `+linkCols,
		l.ID, l.TicketID, l.Provider, l.RepoFullName, l.Kind, l.ExternalRef, l.URL, l.State, l.Title, l.Actor))
}

func (s *pgStore) GetBranchLink(ctx context.Context, provider, repoFullName, branch string) (*VcsLink, error) {
	return scanLink(s.db.QueryRow(ctx,
		`SELECT `+linkCols+` FROM vcs_links WHERE provider=$1 AND repo_full_name=$2 AND kind='branch' AND external_ref=$3 LIMIT 1`,
		provider, repoFullName, branch))
}

func (s *pgStore) LinksForTicket(ctx context.Context, ticketID uuid.UUID) ([]VcsLink, error) {
	rows, err := s.db.Query(ctx, `SELECT `+linkCols+` FROM vcs_links WHERE ticket_id=$1 ORDER BY created_at`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VcsLink
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Write `internal/vcs/store_pg_test.go`** (real-pg harness, mirroring `internal/ticket/store_pg_test.go`; skips when no DB)

```go
package vcs

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestUpsertLinkIdempotent(t *testing.T) {
	pool := testPool(t)
	s := NewPGStore(pool)
	ctx := context.Background()
	tid := uuid.New()
	// (test assumes a tickets row exists or FK deferred; if FK blocks, insert a fixture ticket first.)
	l := &VcsLink{TicketID: tid, Provider: "github", RepoFullName: "o/r", Kind: KindPR, ExternalRef: "12", State: StateOpen}
	first, err := s.UpsertLink(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	l2 := &VcsLink{TicketID: tid, Provider: "github", RepoFullName: "o/r", Kind: KindPR, ExternalRef: "12", State: StateMerged}
	second, err := s.UpsertLink(ctx, l2)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("upsert created a new row: %s != %s", first.ID, second.ID)
	}
	if second.State != StateMerged {
		t.Fatalf("state not updated: %s", second.State)
	}
}
```

- [ ] **Step 5: Run tests**

Run: `go build ./... && go test ./internal/vcs/`
Expected: PASS (`TestUpsertLinkIdempotent` SKIPs without `TEST_DATABASE_URL`; extract/github tests PASS). Build must succeed (migration file is embedded, no compile impact).

- [ ] **Step 6: Commit**

```bash
git add internal/migrate/sql/018_vcs.sql internal/vcs/store.go internal/vcs/store_pg.go internal/vcs/store_pg_test.go
git commit -m "feat(vcs): migration 018 + repo/link store (pgx upsert)"
```

---

### Task 4: Linker service (Process pipeline + register/list + LinksForTicket)

**Files:**
- Create: `internal/vcs/service.go`
- Test: `internal/vcs/service_test.go`

**Interfaces:**
- Consumes: `Store` (Task 3), `VcsEvent`/`VcsRepo`/`VcsLink`/kinds (Task 1), `ExtractTitleNames` (Task 1), and from goquest core: `ticket.Ticket`, `ticket.Status`, `ticket.UpdateTicketReq`, `ticket.Service.GetByProjectAndTitleName`, `ticket.Service.Update`, `comment.AuthorKindSystem`, `comment.CreateReq`, `comment.Service.Create`.
- Produces: `vcs.NewService(store Store, tickets TicketService, comments CommentService) *Service`; `Service.Process(ctx, provider string, ev VcsEvent) error`; `Service.RegisterRepo(ctx, *VcsRepo) error`; `Service.ListRepos(ctx) ([]*VcsRepo, error)`; `Service.LinksForTicket(ctx, uuid.UUID) ([]VcsLink, error)`. Small dependency interfaces `TicketService`, `CommentService` (fakeable).

- [ ] **Step 1: Write the failing test** — `internal/vcs/service_test.go`

```go
package vcs

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"goquest/internal/comment"
	"goquest/internal/ticket"
)

// --- fakes ---

type fakeStore struct {
	repos    map[string]*VcsRepo // key provider|repo
	links    []*VcsLink
	branchLk map[string]*VcsLink // key provider|repo|branch
}

func newFakeStore() *fakeStore {
	return &fakeStore{repos: map[string]*VcsRepo{}, branchLk: map[string]*VcsLink{}}
}
func (f *fakeStore) CreateRepo(_ context.Context, r *VcsRepo) error {
	f.repos[r.Provider+"|"+r.RepoFullName] = r
	return nil
}
func (f *fakeStore) GetRepo(_ context.Context, p, r string) (*VcsRepo, error) {
	if v, ok := f.repos[p+"|"+r]; ok {
		return v, nil
	}
	return nil, ErrNotFound
}
func (f *fakeStore) ListRepos(context.Context) ([]*VcsRepo, error) { return nil, nil }
func (f *fakeStore) UpsertLink(_ context.Context, l *VcsLink) (*VcsLink, error) {
	if l.ID == uuid.Nil {
		l.ID = uuid.New()
	}
	f.links = append(f.links, l)
	if l.Kind == KindBranch {
		f.branchLk[l.Provider+"|"+l.RepoFullName+"|"+l.ExternalRef] = l
	}
	return l, nil
}
func (f *fakeStore) GetBranchLink(_ context.Context, p, r, b string) (*VcsLink, error) {
	if v, ok := f.branchLk[p+"|"+r+"|"+b]; ok {
		return v, nil
	}
	return nil, ErrNotFound
}
func (f *fakeStore) LinksForTicket(context.Context, uuid.UUID) ([]VcsLink, error) { return nil, nil }

type fakeTickets struct {
	byName map[string]*ticket.Ticket // title_name -> ticket
	trans  []ticket.Status           // record of applied statuses
}

func (f *fakeTickets) GetByProjectAndTitleName(_ context.Context, _ uuid.UUID, tn string) (*ticket.Ticket, error) {
	if t, ok := f.byName[tn]; ok {
		return t, nil
	}
	return nil, ticket.ErrNotFound
}
func (f *fakeTickets) Update(_ context.Context, id uuid.UUID, req ticket.UpdateTicketReq) (*ticket.Ticket, error) {
	// find and mutate the ticket to emulate the state machine's forward moves
	for _, t := range f.byName {
		if t.ID == id && req.Status != nil {
			t.Status = *req.Status
			f.trans = append(f.trans, *req.Status)
			return t, nil
		}
	}
	return nil, ticket.ErrNotFound
}

type fakeComments struct{ n int }

func (f *fakeComments) Create(context.Context, uuid.UUID, string, comment.AuthorKind, comment.CreateReq) (*comment.Comment, error) {
	f.n++
	return &comment.Comment{}, nil
}

func repoFixture(auto bool) *VcsRepo {
	return &VcsRepo{ID: uuid.New(), Provider: "github", RepoFullName: "tojiuni/goquest",
		ProjectID: uuid.New(), AutoTransition: auto, MergeTarget: "completed", Active: true}
}

func TestProcessBranchCreatedLinksAndStartsWork(t *testing.T) {
	st := newFakeStore()
	repo := repoFixture(true)
	_ = st.CreateRepo(context.Background(), repo)
	tk := &ticket.Ticket{ID: uuid.New(), TitleName: "goq-foo", Status: ticket.StatusPending}
	tickets := &fakeTickets{byName: map[string]*ticket.Ticket{"goq-foo": tk}}
	comments := &fakeComments{}
	svc := NewService(st, tickets, comments)

	err := svc.Process(context.Background(), "github", VcsEvent{
		Kind: BranchCreated, Repo: "tojiuni/goquest", Branch: "feature/goq-foo", Actor: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.links) != 1 || st.links[0].Kind != KindBranch {
		t.Fatalf("expected 1 branch link, got %+v", st.links)
	}
	// pending -> assigned -> in_progress (two forward moves, no direct edge)
	if len(tickets.trans) != 2 || tickets.trans[0] != ticket.StatusAssigned || tickets.trans[1] != ticket.StatusInProgress {
		t.Fatalf("expected assigned then in_progress, got %v", tickets.trans)
	}
}

func TestProcessCommitInheritsBranchTicket(t *testing.T) {
	st := newFakeStore()
	_ = st.CreateRepo(context.Background(), repoFixture(true))
	tk := &ticket.Ticket{ID: uuid.New(), TitleName: "goq-foo", Status: ticket.StatusInProgress}
	tickets := &fakeTickets{byName: map[string]*ticket.Ticket{"goq-foo": tk}}
	svc := NewService(st, tickets, &fakeComments{})
	// seed a branch link for feature/goq-foo
	_, _ = st.UpsertLink(context.Background(), &VcsLink{TicketID: tk.ID, Provider: "github",
		RepoFullName: "tojiuni/goquest", Kind: KindBranch, ExternalRef: "feature/goq-foo", State: StateOpen})

	// commit whose MESSAGE has no title_name, pushed to the linked branch
	err := svc.Process(context.Background(), "github", VcsEvent{
		Kind: CommitPushed, Repo: "tojiuni/goquest", Branch: "feature/goq-foo",
		CommitSHA: "abc123", CommitMsg: "no ref here"})
	if err != nil {
		t.Fatal(err)
	}
	var gotCommit bool
	for _, l := range st.links {
		if l.Kind == KindCommit && l.TicketID == tk.ID {
			gotCommit = true
		}
	}
	if !gotCommit {
		t.Fatalf("commit did not inherit branch ticket: %+v", st.links)
	}
}

func TestProcessPRMergedCompletes(t *testing.T) {
	st := newFakeStore()
	_ = st.CreateRepo(context.Background(), repoFixture(true))
	tk := &ticket.Ticket{ID: uuid.New(), TitleName: "goq-foo", Status: ticket.StatusInProgress}
	tickets := &fakeTickets{byName: map[string]*ticket.Ticket{"goq-foo": tk}}
	svc := NewService(st, tickets, &fakeComments{})
	err := svc.Process(context.Background(), "github", VcsEvent{
		Kind: PRMerged, Repo: "tojiuni/goquest", Branch: "feature/goq-foo",
		PRNumber: 12, PRTitle: "goq-foo done", PRMerged: true, URL: "https://gh/pr/12"})
	if err != nil {
		t.Fatal(err)
	}
	if tk.Status != ticket.StatusCompleted {
		t.Fatalf("PR merge did not complete ticket: %s", tk.Status)
	}
}

func TestProcessTerminalGuard(t *testing.T) {
	st := newFakeStore()
	_ = st.CreateRepo(context.Background(), repoFixture(true))
	tk := &ticket.Ticket{ID: uuid.New(), TitleName: "goq-foo", Status: ticket.StatusCompleted}
	tickets := &fakeTickets{byName: map[string]*ticket.Ticket{"goq-foo": tk}}
	svc := NewService(st, tickets, &fakeComments{})
	_ = svc.Process(context.Background(), "github", VcsEvent{
		Kind: PROpened, Repo: "tojiuni/goquest", Branch: "goq-foo", PRNumber: 3, PRTitle: "goq-foo"})
	if len(tickets.trans) != 0 {
		t.Fatalf("terminal ticket must not transition, got %v", tickets.trans)
	}
}

func TestProcessUnregisteredRepoIgnored(t *testing.T) {
	svc := NewService(newFakeStore(), &fakeTickets{byName: map[string]*ticket.Ticket{}}, &fakeComments{})
	if err := svc.Process(context.Background(), "github", VcsEvent{Kind: PROpened, Repo: "x/y"}); err != nil {
		t.Fatalf("unregistered repo should be a no-op, got %v", err)
	}
}

func TestProcessAutoTransitionOff(t *testing.T) {
	st := newFakeStore()
	_ = st.CreateRepo(context.Background(), repoFixture(false)) // auto_transition=false
	tk := &ticket.Ticket{ID: uuid.New(), TitleName: "goq-foo", Status: ticket.StatusPending}
	tickets := &fakeTickets{byName: map[string]*ticket.Ticket{"goq-foo": tk}}
	svc := NewService(st, tickets, &fakeComments{})
	_ = svc.Process(context.Background(), "github", VcsEvent{Kind: BranchCreated, Repo: "tojiuni/goquest", Branch: "goq-foo"})
	if len(st.links) != 1 {
		t.Fatalf("link should still be recorded")
	}
	if len(tickets.trans) != 0 {
		t.Fatalf("auto_transition=false must not transition")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/vcs/ -run TestProcess`
Expected: FAIL — `undefined: NewService`.

- [ ] **Step 3: Write `internal/vcs/service.go`**

```go
package vcs

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"goquest/internal/comment"
	"goquest/internal/ticket"
)

// TicketService is the narrow slice of *ticket.Service the linker needs.
type TicketService interface {
	GetByProjectAndTitleName(ctx context.Context, projectID uuid.UUID, titleName string) (*ticket.Ticket, error)
	Update(ctx context.Context, id uuid.UUID, req ticket.UpdateTicketReq) (*ticket.Ticket, error)
}

// CommentService is the narrow slice of *comment.Service the linker needs.
type CommentService interface {
	Create(ctx context.Context, ticketID uuid.UUID, authorID string, kind comment.AuthorKind, req comment.CreateReq) (*comment.Comment, error)
}

type Service struct {
	store    Store
	tickets  TicketService
	comments CommentService
}

func NewService(store Store, tickets TicketService, comments CommentService) *Service {
	return &Service{store: store, tickets: tickets, comments: comments}
}

func (s *Service) RegisterRepo(ctx context.Context, r *VcsRepo) error { return s.store.CreateRepo(ctx, r) }
func (s *Service) ListRepos(ctx context.Context) ([]*VcsRepo, error)  { return s.store.ListRepos(ctx) }
func (s *Service) LinksForTicket(ctx context.Context, id uuid.UUID) ([]VcsLink, error) {
	return s.store.LinksForTicket(ctx, id)
}

// Process links one normalized event to the ticket(s) it references and applies
// auto-transitions. Unregistered repos and unresolved refs are no-ops.
func (s *Service) Process(ctx context.Context, provider string, ev VcsEvent) error {
	repo, err := s.store.GetRepo(ctx, provider, ev.Repo)
	if errors.Is(err, ErrNotFound) {
		return nil // unregistered repo -> ignore
	}
	if err != nil {
		return err
	}

	tickets := s.resolveTickets(ctx, provider, repo, ev)
	kind, ref, state, title := linkShape(ev)
	for _, t := range tickets {
		link := &VcsLink{
			TicketID: t.ID, Provider: provider, RepoFullName: ev.Repo,
			Kind: kind, ExternalRef: ref, URL: ev.URL, State: state, Title: title, Actor: ev.Actor,
		}
		if _, err := s.store.UpsertLink(ctx, link); err != nil {
			return err
		}
		if repo.AutoTransition {
			if err := s.autoTransition(ctx, provider, repo, t, ev); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveTickets returns the distinct tickets an event references: title_name
// tokens found in branch/commit/PR text that resolve in the repo's project,
// plus (for commits) the ticket of a linked branch (inheritance).
func (s *Service) resolveTickets(ctx context.Context, provider string, repo *VcsRepo, ev VcsEvent) []*ticket.Ticket {
	seen := map[uuid.UUID]bool{}
	var out []*ticket.Ticket
	add := func(t *ticket.Ticket) {
		if t != nil && !seen[t.ID] {
			seen[t.ID] = true
			out = append(out, t)
		}
	}
	for _, tn := range ExtractTitleNames(ev.Branch, ev.CommitMsg, ev.PRTitle, ev.PRBody) {
		if t, err := s.tickets.GetByProjectAndTitleName(ctx, repo.ProjectID, tn); err == nil {
			add(t)
		}
	}
	if ev.Kind == CommitPushed {
		if bl, err := s.store.GetBranchLink(ctx, provider, ev.Repo, ev.Branch); err == nil {
			if t, err := s.tickets.GetByProjectAndTitleName(ctx, repo.ProjectID, branchTicketTitleName(bl)); err == nil {
				add(t)
			}
		}
	}
	return out
}

// branchTicketTitleName recovers the title_name a branch link was made for.
// A branch link's external_ref is the branch name; the title_name is embedded in it.
func branchTicketTitleName(bl *VcsLink) string {
	names := ExtractTitleNames(bl.ExternalRef)
	if len(names) > 0 {
		return names[0]
	}
	return bl.ExternalRef
}

func linkShape(ev VcsEvent) (LinkKind, string, LinkState, string) {
	switch ev.Kind {
	case BranchCreated:
		return KindBranch, ev.Branch, StateOpen, ev.Branch
	case BranchDeleted:
		return KindBranch, ev.Branch, StateDeleted, ev.Branch
	case CommitPushed:
		return KindCommit, ev.CommitSHA, StateOpen, firstLine(ev.CommitMsg)
	default: // PR*
		st := StateOpen
		if ev.PRMerged {
			st = StateMerged
		} else if ev.Kind == PRClosed {
			st = StateClosed
		}
		return KindPR, prNumberString(ev.PRNumber), st, ev.PRTitle
	}
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}

func (s *Service) autoTransition(ctx context.Context, provider string, repo *VcsRepo, t *ticket.Ticket, ev VcsEvent) error {
	if isTerminal(t.Status) {
		return nil
	}
	switch ev.Kind {
	case BranchCreated, PROpened:
		return s.moveToInProgress(ctx, t, ev, provider)
	case PRMerged:
		target := ticket.Status(repo.MergeTarget)
		if target == "" {
			target = ticket.StatusCompleted
		}
		return s.transitionAndNote(ctx, t, target, fmt.Sprintf("linked PR #%d (merged) → %s by vcs:%s", ev.PRNumber, target, provider))
	}
	return nil
}

func (s *Service) moveToInProgress(ctx context.Context, t *ticket.Ticket, ev VcsEvent, provider string) error {
	if t.Status == ticket.StatusInProgress {
		return nil
	}
	if t.Status == ticket.StatusPending {
		if err := s.transition(ctx, t, ticket.StatusAssigned); err != nil {
			return err
		}
	}
	if t.Status == ticket.StatusAssigned {
		return s.transitionAndNote(ctx, t, ticket.StatusInProgress,
			fmt.Sprintf("linked %s → in_progress by vcs:%s", ev.Kind, provider))
	}
	return nil
}

func (s *Service) transition(ctx context.Context, t *ticket.Ticket, to ticket.Status) error {
	updated, err := s.tickets.Update(ctx, t.ID, ticket.UpdateTicketReq{Status: &to})
	if err != nil {
		// invalid transitions are non-fatal for webhook processing
		if errors.Is(err, ticket.ErrInvalidTransition) {
			return nil
		}
		return err
	}
	t.Status = updated.Status
	return nil
}

func (s *Service) transitionAndNote(ctx context.Context, t *ticket.Ticket, to ticket.Status, note string) error {
	prev := t.Status
	if err := s.transition(ctx, t, to); err != nil {
		return err
	}
	if t.Status != prev {
		_, _ = s.comments.Create(ctx, t.ID, "vcs", comment.AuthorKindSystem, comment.CreateReq{Body: note, IsInternal: true})
	}
	return nil
}

func isTerminal(st ticket.Status) bool {
	return st == ticket.StatusCompleted || st == ticket.StatusFailed || st == ticket.StatusCancelled
}
```

> Implementer note: confirm the exact exported names against `internal/ticket/model.go` and `internal/comment/model.go` — `ticket.Status`, the `StatusPending/StatusAssigned/StatusInProgress/StatusCompleted/StatusFailed/StatusCancelled` constants, `ticket.UpdateTicketReq{Status *Status}`, `ticket.ErrNotFound`, `ticket.ErrInvalidTransition`, `comment.AuthorKindSystem`, `comment.CreateReq{Body, IsInternal}`. All were verified present in the survey; if a name differs, adjust the reference (do not rename the ticket/comment packages).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/vcs/ -run TestProcess`
Expected: PASS (all 6 subtests).

- [ ] **Step 5: Run the whole vcs package**

Run: `go test ./internal/vcs/`
Expected: PASS (extract, github, service; store_pg SKIPs without DB).

- [ ] **Step 6: Commit**

```bash
git add internal/vcs/service.go internal/vcs/service_test.go
git commit -m "feat(vcs): linker pipeline (resolve, upsert, auto-transition)"
```

---

### Task 5: HTTP handler (webhook + repo register/list) + config env + main wiring

**Files:**
- Create: `internal/handler/vcs.go`
- Modify: `internal/config/config.go` (add `VCSGitHubWebhookSecret`)
- Modify: `cmd/service/main.go` (construct + register routes)
- Test: `internal/handler/vcs_test.go`

**Interfaces:**
- Consumes: `vcs.Service` (Task 4), `vcs.NewGitHub`, `vcs.VcsRepo`, `resolve.Resolver.ResolveProjectID` (project name → id for registration).
- Produces: `handler.NewVCSHandler(svc *vcs.Service, gh vcs.Provider, resolver handler.ProjectResolver, githubSecret string) *VCSHandler`; methods `GitHubWebhook`, `RegisterRepo`, `ListRepos`.

- [ ] **Step 1: Write the failing test** — `internal/handler/vcs_test.go` (httptest, signed body; a stub vcs service)

```go
package handler_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"goquest/internal/handler"
	"goquest/internal/vcs"
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
```

> Implementer note: `GitHubWebhook` must tolerate a `nil` service only far enough to make the signature test meaningful; for the "accepts signed" test, an empty `commits` array yields zero events so `Process` is never called — no nil deref. If you prefer, inject a tiny fake `*vcs.Service` built on a fake store; either is acceptable as long as both assertions hold.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/handler/ -run TestGitHubWebhook`
Expected: FAIL — `undefined: handler.NewVCSHandler`.

- [ ] **Step 3: Add config field** — `internal/config/config.go`

In the `Config` struct (near the SMTP secret block) add:
```go
	VCSGitHubWebhookSecret string
```
In `Load()` add:
```go
		VCSGitHubWebhookSecret: os.Getenv("VCS_GITHUB_WEBHOOK_SECRET"),
```

- [ ] **Step 4: Write `internal/handler/vcs.go`**

```go
package handler

import (
	"encoding/json"
	"io"
	"net/http"

	"goquest/internal/resolve"
	"goquest/internal/vcs"
)

type VCSHandler struct {
	svc          *vcs.Service
	gh           vcs.Provider
	resolver     ProjectResolver
	githubSecret string
}

func NewVCSHandler(svc *vcs.Service, gh vcs.Provider, resolver ProjectResolver, githubSecret string) *VCSHandler {
	return &VCSHandler{svc: svc, gh: gh, resolver: resolver, githubSecret: githubSecret}
}

// GitHubWebhook verifies the signature, parses, and links. Public (no API key).
func (h *VCSHandler) GitHubWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20)) // 5MB cap
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	if err := h.gh.Verify(r, body, h.githubSecret); err != nil {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	events, err := h.gh.Parse(r.Header.Get("X-GitHub-Event"), body)
	if err != nil {
		http.Error(w, "parse error", http.StatusBadRequest)
		return
	}
	if h.svc != nil {
		for _, ev := range events {
			if perr := h.svc.Process(r.Context(), h.gh.Name(), ev); perr != nil {
				// log and continue; a failed link should not 500 the webhook
				continue
			}
		}
	}
	w.WriteHeader(http.StatusOK)
}

type registerRepoReq struct {
	Provider       string `json:"provider"`
	RepoFullName   string `json:"repo_full_name"`
	Workspace      string `json:"workspace"`
	WorkspaceSlug  string `json:"workspace_slug"`
	Project        string `json:"project"`
	AutoTransition *bool  `json:"auto_transition"`
	MergeTarget    string `json:"merge_target"`
}

// RegisterRepo maps (provider, repo) → project. Protected (inside API-key group).
func (h *VCSHandler) RegisterRepo(w http.ResponseWriter, r *http.Request) {
	var req registerRepoReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	pid, err := h.resolver.ResolveProjectID(r.Context(), resolve.Ref{
		WorkspaceSlug: req.WorkspaceSlug, WorkspaceName: req.Workspace, ProjectName: req.Project})
	if err != nil || pid == nil {
		http.Error(w, "project not resolved", http.StatusBadRequest)
		return
	}
	repo := &vcs.VcsRepo{
		Provider: orDefault(req.Provider, "github"), RepoFullName: req.RepoFullName,
		ProjectID: *pid, AutoTransition: req.AutoTransition == nil || *req.AutoTransition,
		MergeTarget: orDefault(req.MergeTarget, "completed"), Active: true,
	}
	if err := h.svc.RegisterRepo(r.Context(), repo); err != nil {
		http.Error(w, "register failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, repo)
}

func (h *VCSHandler) ListRepos(w http.ResponseWriter, r *http.Request) {
	repos, err := h.svc.ListRepos(r.Context())
	if err != nil {
		http.Error(w, "list failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, repos)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
```

> Implementer note: `ProjectResolver`, `resolve.Ref`, `writeJSON` already exist in the handler package (used by `ticket.go`). Reuse them; do not redefine.

- [ ] **Step 5: Wire in `cmd/service/main.go`**

After the comment service is constructed (it already is), add:
```go
	vcsStore := vcs.NewPGStore(pool)
	vcsSvc := vcs.NewService(vcsStore, svc /* *ticket.Service */, commentSvc)
	vcsH := handler.NewVCSHandler(vcsSvc, vcs.NewGitHub(), projectResolver, cfg.VCSGitHubWebhookSecret)
```
Add the **public** webhook route next to the magic-link routes (inside `r.Route("/api/v1", ...)`, before/outside the `r.Group` auth wall):
```go
		r.Post("/vcs/webhooks/github", vcsH.GitHubWebhook) // public: HMAC-verified
```
Add the **protected** repo routes inside the `r.Group` auth block (with the ticket routes):
```go
		r.Post("/vcs/repos", vcsH.RegisterRepo)
		r.Get("/vcs/repos", vcsH.ListRepos)
```
Add the import `"goquest/internal/vcs"`.

- [ ] **Step 6: Run tests + build**

Run: `go build ./... && go test ./internal/handler/ -run TestGitHubWebhook`
Expected: PASS; build succeeds.

- [ ] **Step 7: Commit**

```bash
git add internal/handler/vcs.go internal/config/config.go cmd/service/main.go internal/handler/vcs_test.go
git commit -m "feat(vcs): public GitHub webhook + repo register/list endpoints"
```

---

### Task 6: Embed `vcs_links` in ticket GET responses (handler layer)

**Files:**
- Modify: `internal/handler/ticket.go` (`Get`, `GetByName` — wrap response with links)
- Modify: `cmd/service/main.go` (pass `vcsSvc` into the ticket handler or a small links provider)
- Test: `internal/handler/ticket_vcs_test.go`

**Interfaces:**
- Consumes: `vcs.Service.LinksForTicket(ctx, uuid.UUID) ([]vcs.VcsLink, error)`.
- Produces: a handler-local response wrapper `ticketWithLinks` embedding `*ticket.Ticket` + `VcsLinks []vcs.VcsLink json:"vcs_links,omitempty"`. **No change to `internal/ticket` (import-cycle constraint).**

- [ ] **Step 1: Write the failing test** — `internal/handler/ticket_vcs_test.go` (internal `handler` package: exercises the unexported `ticketWithLinks` wrapper's JSON shape directly — no middleware needed)

```go
package handler

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"goquest/internal/ticket"
	"goquest/internal/vcs"
)

func TestTicketWithLinksJSON(t *testing.T) {
	tk := &ticket.Ticket{ID: uuid.New(), TitleName: "goq-foo", Title: "Do X"}
	wrapped := ticketWithLinks{
		Ticket: tk,
		VcsLinks: []vcs.VcsLink{
			{Provider: "github", RepoFullName: "tojiuni/goquest", Kind: vcs.KindPR, ExternalRef: "12", State: vcs.StateMerged},
		},
	}
	b, err := json.Marshal(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	// promoted ticket field is present
	if got["title_name"] != "goq-foo" {
		t.Fatalf("promoted ticket field missing: %v", got["title_name"])
	}
	// vcs_links embedded with one entry
	links, ok := got["vcs_links"].([]any)
	if !ok || len(links) != 1 {
		t.Fatalf("vcs_links not embedded correctly: %v", got["vcs_links"])
	}
}

func TestTicketWithLinksOmitsEmpty(t *testing.T) {
	b, _ := json.Marshal(ticketWithLinks{Ticket: &ticket.Ticket{ID: uuid.New(), TitleName: "goq-foo"}})
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	if _, present := got["vcs_links"]; present {
		t.Fatalf("empty vcs_links should be omitted, got %v", got["vcs_links"])
	}
}
```

> Implementer note: this proves the wrapper's JSON contract (promoted ticket fields + `vcs_links,omitempty`). The `Get`/`GetByName` handler wiring that populates `VcsLinks` from the `LinksProvider` is then covered by `go build` + the existing `ticket_test.go` httptest cases continuing to pass. If you want an end-to-end handler assertion too, model it on `ticket_test.go`'s `ticketRouter(h)` setup and inject a `LinksProvider` returning one link — but the wrapper test above is the required, self-contained gate.

- [ ] **Step 2: Define the wrapper + provider in `internal/handler/ticket.go`**

```go
// LinksProvider supplies a ticket's VCS links (implemented by *vcs.Service).
type LinksProvider interface {
	LinksForTicket(ctx context.Context, ticketID uuid.UUID) ([]vcs.VcsLink, error)
}

// ticketWithLinks is the GET response shape: the ticket's own JSON fields
// (promoted) plus vcs_links. Kept in the handler package to avoid a
// ticket→vcs import cycle.
type ticketWithLinks struct {
	*ticket.Ticket
	VcsLinks []vcs.VcsLink `json:"vcs_links,omitempty"`
}
```
Add `links LinksProvider` to `TicketHandler` and to `NewTicketHandler(...)` (append a parameter). In `Get` and `GetByName`, after obtaining `t *ticket.Ticket`:
```go
	var links []vcs.VcsLink
	if h.links != nil {
		links, _ = h.links.LinksForTicket(r.Context(), t.ID)
	}
	writeJSON(w, http.StatusOK, ticketWithLinks{Ticket: t, VcsLinks: links})
```
Add imports `"context"`, `"github.com/google/uuid"`, `"goquest/internal/vcs"` if not present.

- [ ] **Step 3: Wire in `cmd/service/main.go`**

Update the ticket handler construction to pass `vcsSvc` as the links provider (append the new last arg):
```go
	ticketH := handler.NewTicketHandler(svc, sdbChecker, sdbWriter, projectResolver, tagResolver, auditLogger, vcsSvc)
```
(`vcsSvc` must be constructed before `ticketH`; move its construction up if needed.)

- [ ] **Step 4: Fix all other `NewTicketHandler(...)` call sites**

Run: `grep -rn "NewTicketHandler(" --include=*.go .`
Update each (including `internal/handler/ticket_test.go`, which passes `nil`s) to add a trailing `nil` (tests) or `vcsSvc` (main).

- [ ] **Step 5: Run tests + build**

Run: `go build ./... && go test ./internal/handler/`
Expected: PASS. The new test asserts `vcs_links` appears; existing ticket tests still pass with the extra `nil` provider.

- [ ] **Step 6: Commit**

```bash
git add internal/handler/ticket.go internal/handler/ticket_vcs_test.go cmd/service/main.go
git commit -m "feat(vcs): embed vcs_links in ticket GET responses"
```

---

### Task 7: goquest-mcp — repo-registration tools + prompt conventions

**Repo:** `/workspace/repos/goquest-mcp` (separate TypeScript repo).

**Files:**
- Modify: `src/tools.ts` (add `vcsTools`: `register_vcs_repo`, `list_vcs_repos`)
- Modify: `src/goquest-mcp-server.ts` (include `vcsTools` in `buildServer`)
- Modify: `ticket-guide.md` and `src/tools.ts` `create_ticket` description (naming conventions)
- Modify: `scripts/mcp-smoke-test.ts` (assert new tool count)
- Test: `src/tools.test.ts` (map-to-body assertions for the new tools)

**Interfaces:**
- Consumes: `client.request("POST"|"GET", "/api/v1/vcs/repos", body)` from `src/client.ts`.
- Produces: `vcsTools: ToolDef[]` exported from `src/tools.ts`; server registers `readTools+writeTools+apiTools+guideTools+vcsTools` (tool count 8 → 10).

- [ ] **Step 1: Write the failing test** — append to `src/tools.test.ts`

```ts
test("register_vcs_repo POSTs mapped body", async () => {
  let seen = ""; let body: any;
  const c: GoquestClient = { async request(m, p, b) { seen = `${m} ${p}`; body = b; return { ok: true, status: 201, data: { id: "r1" } }; } };
  await vByName("register_vcs_repo").handler(
    { provider: "github", repo_full_name: "tojiuni/goquest", project: "goquest", workspace: "toji", auto_transition: true, merge_target: "completed" },
    c,
  );
  assert.equal(seen, "POST /api/v1/vcs/repos");
  assert.equal(body.repo_full_name, "tojiuni/goquest");
  assert.equal(body.project, "goquest");
  assert.equal(body.merge_target, "completed");
});
```
Add near the top of the test file, next to the existing `wByName` helper:
```ts
import { vcsTools } from "./tools.ts";
const vByName = (n: string) => vcsTools.find((t) => t.name === n)!;
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /workspace/repos/goquest-mcp && npm test`
Expected: FAIL — `vcsTools` is not exported / `register_vcs_repo` undefined.

- [ ] **Step 3: Add `vcsTools` to `src/tools.ts`**

```ts
export const vcsTools: ToolDef[] = [
  {
    name: "register_vcs_repo",
    description:
      "Map a git repository to a goquest project so pushes/PRs auto-link to tickets. When a branch/commit/PR contains a ticket's title_name, goquest links it and (if auto_transition) advances status: branch created / PR opened → in_progress, PR merged → merge_target.",
    inputSchema: {
      repo_full_name: z.string().describe("e.g. tojiuni/goquest"),
      project: z.string().optional().describe("Project name (resolved with workspace)"),
      workspace: z.string().optional(),
      workspace_slug: z.string().optional(),
      provider: z.enum(["github", "gitlab", "forgejo"]).optional().describe("default github"),
      auto_transition: z.boolean().optional().describe("default true"),
      merge_target: z.enum(["completed", "pending_approval"]).optional().describe("PR-merge target status, default completed"),
    },
    handler: async (a, client) => {
      const body: Record<string, unknown> = { repo_full_name: a.repo_full_name };
      for (const k of ["project", "workspace", "workspace_slug", "provider", "auto_transition", "merge_target"] as const) {
        if (a[k] !== undefined) body[k] = a[k];
      }
      const r = await client.request("POST", "/api/v1/vcs/repos", body);
      return fromResult(r);
    },
  },
  {
    name: "list_vcs_repos",
    description: "List git repositories registered for ticket auto-linking.",
    inputSchema: {},
    handler: async (_a, client) => fromResult(await client.request("GET", "/api/v1/vcs/repos")),
  },
];
```
(`fromResult`, `ToolDef`, `z` are already in `src/tools.ts`.)

- [ ] **Step 4: Register in `src/goquest-mcp-server.ts`**

Import and include `vcsTools`:
```ts
import { readTools, writeTools, vcsTools } from "./tools.ts";
...
  for (const t of [...readTools, ...writeTools, ...apiTools, ...guideTools, ...vcsTools]) {
```

- [ ] **Step 5: Add naming conventions to the prompt**

In `create_ticket`'s description (append):
```
Name your git branch with the title_name (e.g. goq-foo or feature/goq-foo) and put the title_name in PR titles/commit messages — goquest then auto-links branches/commits/PRs to this ticket and advances its status.
```
In `ticket-guide.md`, add a section:
```markdown
## git 연동 (VCS 자동 링크)
- 작업 branch는 **title_name을 포함**해 만든다 (예: `goq-foo` 또는 `feature/goq-foo`).
- PR 제목/본문·commit 메시지에도 title_name을 넣으면 해당 티켓에 자동 링크된다.
- 링크된 branch의 commit은 메시지에 title_name이 없어도 그 티켓에 상속된다.
- 자동 전이: branch 생성 / PR opened → in_progress, PR merged → completed(또는 repo 설정 pending_approval).
- 레포는 `register_vcs_repo`로 project에 1회 매핑해야 링크가 동작한다.
```
Keep the geneso source of truth in sync (`goquest/guide/ticket-authoring-guide.md`) as a follow-up doc commit.

- [ ] **Step 6: Update smoke test count** — `scripts/mcp-smoke-test.ts`

Change the asserted tool count from 8 to 10 and exercise `register_vcs_repo` once against a fake client.

- [ ] **Step 7: Run tests + smoke**

Run: `cd /workspace/repos/goquest-mcp && npm run typecheck && npm test && npm run smoke`
Expected: PASS; `smoke ok: 10 tools`.

- [ ] **Step 8: Commit (goquest-mcp repo)**

```bash
git add src/tools.ts src/goquest-mcp-server.ts ticket-guide.md scripts/mcp-smoke-test.ts src/tools.test.ts
git commit -m "feat(tools): VCS repo registration tools + git-linking conventions"
```

---

## Deployment / follow-up (not TDD — ops checklist)

These are required to make the feature live but are configuration/ops, tracked separately from the code tasks above:

1. **Webhook secret**: add `github_webhook_secret` to Vault `secret/neunexus/goquest`; map it to env `VCS_GITHUB_WEBHOOK_SECRET` in `neunexus/deploy/goquest/service/{vso-secrets,deployment}.yaml` (add a transformation template + `env`/`envFrom` entry). Reuse the shared toji-ci github-webhook HMAC or mint a dedicated one.
2. **GitHub webhook**: on each participating repo, add a webhook → `https://goquest.toji.homes/api/v1/vcs/webhooks/github`, content-type json, secret = the above, events: `push`, `pull_request`, `create`, `delete`. (goquest already has the external ingress; no new ingress needed.)
3. **Register repos**: `register_vcs_repo` (MCP) or `POST /api/v1/vcs/repos` for each repo→project mapping.
4. **geneso docs**: document the feature + conventions under `goquest/service/` and sync `goquest/guide/ticket-authoring-guide.md`; gopedia ingest.
5. **nuclex report**: report the goquest change via `pm.dependency.check` per the neunexus PR workflow (this is a goquest-repo change, so report domain `goquest`).

## Notes for the executor

- **Side effects on completion**: `ticket.Service.Update` to `completed` (PR-merge path) fires XP award, `callback_url`, and nuclex emit (survey §extra). That is desired for merge→completed; just be aware the linker triggers real downstream events.
- **Idempotency**: re-delivered webhooks are safe — `UpsertLink` is keyed unique, and transitions are no-ops once the ticket is already at/past the target (the terminal guard + `moveToInProgress`'s status checks).
- **No import cycle**: `internal/ticket` never imports `internal/vcs`; the embed lives in `internal/handler`.
- **Migration**: file `018_vcs.sql` re-runs every boot; all DDL uses `IF NOT EXISTS`.
