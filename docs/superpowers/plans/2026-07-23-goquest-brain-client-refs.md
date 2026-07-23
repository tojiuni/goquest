# goquest brain gRPC client + ref doc REST (goq-refdoc-goquest-brain-client) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** goquest-service becomes the single mediator between goquest-ui and two external systems for ticket reference documents: brain (gRPC, `GraphService.TicketDocs`) for the doc list, and GitHub (geneso, private repo) for doc content — the browser never talks to either directly.

**Architecture:** `internal/brainpb` vendors brain's proto and generates Go client stubs. `internal/brainclient` wraps `brainpb.GraphServiceClient` with the caller-JWT-forward pattern (decode-only, mirrors `brain_mcp/src/callerAuth.ts` — brain re-verifies signature/exp/subject). `internal/githubapp` is a small GitHub App client (RS256 JWT → installation access token, cached) used to fetch raw file content from `tojiuni/geneso`. `internal/handler/refdocs.go` exposes both as REST inside goquest's existing authenticated route group.

**Tech Stack:** Go 1.25, `google.golang.org/grpc` (already a dependency via SpiceDB), `github.com/golang-jwt/jwt/v5` (already a dependency), `buf` + `protoc-gen-go` + `protoc-gen-go-grpc` (present on this pod's `$(go env GOPATH)/bin` — confirmed working during planning, no protoc binary needed).

## Global Constraints

- Spec: geneso `goquest/spec/2026-07-22-goquest-brain-client-refs-design.md` (this plan's source of truth for scope/decisions).
- Branch/commit/PR must contain `goq-refdoc-goquest-brain-client`. Work happens in the existing worktree `goquest/.worktrees/goquest-brain-client-refs` (branch `goquest-brain-client-refs`) — do not create a new one.
- brain's `GraphService.TicketDocs` RPC is live in production (brain PR #63, merged; `BRAIN_AUTH_MODE=required`). brain gRPC address: `brain.brain.svc.cluster.local:50051` (namespace `brain`, k8s Service selects `app.kubernetes.io/name: brain-prod`, container port name `grpc` → 50051). Confirmed via `/workspace/repos/brain/deploy/k8s/deployment.yaml`.
- brain auth: every RPC requires a Bearer JWT (lymphhub-issued) in gRPC metadata; brain verifies signature/iss/exp against `https://toji.idp.toji.homes/.well-known/jwks.json` and rejects (`PERMISSION_DENIED`) if the token's subject doesn't match the request's `user` field. **lymphhub tokens carry `sub` (actor UUID) but never `preferred_username`** (confirmed in `deployment.yaml` comment) — so `user` must be built as `"user:" + sub` in practice. Mirror `brain_mcp/src/callerAuth.ts`'s exact fallback (`preferred_username || sub`) for forward-compatibility, but do not assume `preferred_username` will ever be present.
- goquest is NOT in `BRAIN_AUTH_SERVICE_SUBS` (the impersonation allowlist) and must not be added for this feature — goquest forwards the caller's own JWT unchanged and sets `user` to match that same JWT's own subject, which is the non-impersonating case brain's auth already supports without an allowlist entry.
- No token/service-account fallback for callers without a JWT (spec §2.2, explicit YAGNI) — if the incoming request has no `Bearer <jwt>` Authorization header, both new endpoints must return 401, not attempt a brain/GitHub call.
- `/docs/content` fetches from `tojiuni/geneso` (private repo) via GitHub App installation token — Vault `secret/neunexus/workspace/github-app-tojiuni` fields: `app_id`, `installation_id`, `private_key_pem`, `client_secret` (only the first three are used here). No caching in v1 (spec §2.3 — add later if it becomes a problem).
- brain connection/timeout errors on `/tickets/{id}/docs` → 502 (spec §3: UI must distinguish "temporarily unavailable" from "no refs", so 502 ≠ 200 empty array).
- GitHub 404 (file moved/deleted) on `/docs/content` → 404.
- Both endpoints sit inside goquest's **existing authenticated route group** (the one already gating `/tickets`) — no new auth scheme. `/tickets/{id}/docs` additionally sits inside the `RequireTicketView`-gated `/tickets/{id}` subrouter (same view-floor as `ListRefs`/`GetDoc`).
- Verify: `go build ./... && go vet ./... && go test ./...` (pod-runnable — brainclient tests use `google.golang.org/grpc/test/bufconn`, an in-process fake gRPC server; githubapp tests use `httptest.NewServer`. No live network/Postgres needed).

## File Structure

- `internal/brainpb/brain.proto` — vendored copy of `brain/proto/brain.proto` (source of truth: `tojiuni/brain`, manual re-vendor on brain proto changes).
- `internal/brainpb/brain.pb.go`, `internal/brainpb/brain_grpc.pb.go` — generated, not hand-edited.
- `internal/brainclient/client.go` — `Client.TicketDocs`, caller-JWT decode helper.
- `internal/brainclient/client_test.go` — bufconn-based unit tests.
- `internal/githubapp/client.go` — `Client.Content` (installation-token caching + contents-API fetch).
- `internal/githubapp/client_test.go` — httptest-based unit tests.
- `internal/handler/refdocs.go` — `RefDocsHandler.TicketDocs`, `RefDocsHandler.Content`.
- `internal/handler/refdocs_test.go` — handler tests (fake `BrainLookuper`/`ContentFetcher`).
- `internal/config/config.go` — modify: add `BrainGRPCAddr`, `BrainTenant`, `GitHubAppID`, `GitHubAppInstallationID`, `GitHubAppPrivateKeyPEM`, `GenesoRepo`.
- `cmd/service/main.go` — modify: construct `brainclient.Client` + `githubapp.Client`, register routes.

---

### Task 1: Vendor brain.proto + generate Go stubs

**Files:**
- Create: `internal/brainpb/brain.proto` (copy of `brain/proto/brain.proto`, verbatim)
- Create (generated): `internal/brainpb/brain.pb.go`, `internal/brainpb/brain_grpc.pb.go`

**Interfaces:**
- Produces (Task 2 consumes):
  - `brainpb.GraphServiceClient` interface with `TicketDocs(ctx context.Context, in *brainpb.TicketDocsRequest, opts ...grpc.CallOption) (*brainpb.TicketDocsResponse, error)`
  - `brainpb.NewGraphServiceClient(cc grpc.ClientConnInterface) brainpb.GraphServiceClient`
  - `brainpb.TicketDocsRequest{ Tenant, User, TicketId string }`
  - `brainpb.TicketDocsResponse{ Nodes []*brainpb.RelatedNode }`
  - `brainpb.RelatedNode{ RecordId, Table string; Hops uint32; Path []*brainpb.PathEdge; Title, SourcePath string }`

This task has no application logic to test — it's a mechanical vendor+generate step. Verification is "it compiles" (Step 3).

- [ ] **Step 1: Vendor the proto file**

```bash
mkdir -p internal/brainpb
cp /workspace/repos/brain/proto/brain.proto internal/brainpb/brain.proto
```

- [ ] **Step 2: Generate Go stubs**

`protoc-gen-go`/`protoc-gen-go-grpc`/`buf` are already installed on this pod's `$(go env GOPATH)/bin` (confirmed during planning — no protoc binary needed, `buf` has its own proto parser). brain.proto has no `go_package` option, so the Go import path must be supplied via an `M`-flag.

Write a scratch buf template (not committed — it's a one-shot generation config):

```bash
cat > /tmp/buf.gen.yaml <<'EOF'
version: v2
plugins:
  - local: protoc-gen-go
    out: internal/brainpb
    opt:
      - paths=source_relative
      - Mbrain.proto=github.com/tojiuni/goquest/internal/brainpb
  - local: protoc-gen-go-grpc
    out: internal/brainpb
    opt:
      - paths=source_relative
      - Mbrain.proto=github.com/tojiuni/goquest/internal/brainpb
EOF
PATH="$(go env GOPATH)/bin:$PATH" buf generate --template /tmp/buf.gen.yaml internal/brainpb/brain.proto
```

Expected: two new files, `internal/brainpb/brain.pb.go` and `internal/brainpb/brain_grpc.pb.go`, both starting with `// Code generated by protoc-gen-go. DO NOT EDIT.` / `// Code generated by protoc-gen-go-grpc. DO NOT EDIT.`, package `brainpb`.

- [ ] **Step 3: Verify it compiles**

Run: `go build ./internal/brainpb/...`
Expected: no output, exit 0.

- [ ] **Step 4: Commit**

```bash
git add internal/brainpb/
git commit -m "feat(brainpb): vendor brain.proto + generate Go gRPC stubs (goq-refdoc-goquest-brain-client)"
```

---

### Task 2: `brainclient.Client` — caller-JWT-forwarding TicketDocs call (TDD)

**Files:**
- Create: `internal/brainclient/client.go`
- Test: `internal/brainclient/client_test.go`

**Interfaces:**
- Consumes: Task 1's `brainpb.GraphServiceClient`, `brainpb.NewGraphServiceClient`, `brainpb.TicketDocsRequest`, `brainpb.TicketDocsResponse`, `brainpb.RelatedNode`, `brainpb.GraphServiceServer`, `brainpb.RegisterGraphServiceServer`, `brainpb.UnimplementedGraphServiceServer`.
- Produces (Task 4 consumes):
  - `type RelatedDoc struct { Title string; SourcePath string }`
  - `type Client struct { ... }` (unexported fields)
  - `func New(addr, tenant string) (*Client, error)`
  - `func (c *Client) Close() error`
  - `func (c *Client) TicketDocs(ctx context.Context, ticketID, callerJWT string) ([]RelatedDoc, error)` — returns `(nil, ErrNoCallerJWT)` if `callerJWT == ""`.
  - `var ErrNoCallerJWT = errors.New("brainclient: no caller JWT to forward")`

- [ ] **Step 1: Write the failing test**

`internal/brainclient/client_test.go`:

```go
package brainclient

import (
	"context"
	"encoding/base64"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	"github.com/tojiuni/goquest/internal/brainpb"
)

const bufSize = 1024 * 1024

// fakeGraphService records the request it received (including incoming
// metadata) so tests can assert on what the client actually sent.
type fakeGraphService struct {
	brainpb.UnimplementedGraphServiceServer
	lastReq          *brainpb.TicketDocsRequest
	lastAuthHeader   string
	response         *brainpb.TicketDocsResponse
	err              error
}

func (f *fakeGraphService) TicketDocs(ctx context.Context, req *brainpb.TicketDocsRequest) (*brainpb.TicketDocsResponse, error) {
	f.lastReq = req
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		vs := md.Get("authorization")
		if len(vs) > 0 {
			f.lastAuthHeader = vs[0]
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.response, nil
}

// startFakeBrain runs fakeGraphService on an in-process bufconn listener and
// returns a dial function suitable for grpc.NewClient's WithContextDialer.
func startFakeBrain(t *testing.T, svc *fakeGraphService) func(context.Context, string) (net.Conn, error) {
	t.Helper()
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	brainpb.RegisterGraphServiceServer(srv, svc)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return func(context.Context, string) (net.Conn, error) { return lis.Dial() }
}

// newTestClient builds a Client whose conn dials the given bufconn dialer
// instead of a real address — bypasses New()'s real-address dial so tests
// don't need a live brain.
func newTestClient(t *testing.T, dialer func(context.Context, string) (net.Conn, error), tenant string) *Client {
	t.Helper()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return &Client{conn: conn, graph: brainpb.NewGraphServiceClient(conn), tenant: tenant}
}

// makeJWT builds an unsigned test JWT (decode-only consumer — TicketDocs
// never verifies the signature, brain does) using the same unpadded
// base64url encoding real JWTs use.
func makeJWT(t *testing.T, sub, preferredUsername string) string {
	t.Helper()
	header := `{"alg":"none","typ":"JWT"}`
	payload := `{"sub":"` + sub + `"`
	if preferredUsername != "" {
		payload += `,"preferred_username":"` + preferredUsername + `"`
	}
	payload += `}`
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(header)) + "." + enc([]byte(payload)) + ".sig"
}

func TestTicketDocs_ForwardsAuthAndBuildsUserFromSub(t *testing.T) {
	svc := &fakeGraphService{response: &brainpb.TicketDocsResponse{
		Nodes: []*brainpb.RelatedNode{{Title: "Design Doc", SourcePath: "goquest/spec/foo.md"}},
	}}
	dialer := startFakeBrain(t, svc)
	c := newTestClient(t, dialer, "toji")

	jwt := makeJWT(t, "01ACTORID", "")
	docs, err := c.TicketDocs(context.Background(), "ticket-123", jwt)
	require.NoError(t, err)
	assert.Equal(t, []RelatedDoc{{Title: "Design Doc", SourcePath: "goquest/spec/foo.md"}}, docs)

	require.NotNil(t, svc.lastReq)
	assert.Equal(t, "toji", svc.lastReq.Tenant)
	assert.Equal(t, "ticket-123", svc.lastReq.TicketId)
	assert.Equal(t, "user:01ACTORID", svc.lastReq.User, "lymphhub tokens have no preferred_username — user must fall back to sub")
	assert.Equal(t, "Bearer "+jwt, svc.lastAuthHeader, "the raw JWT must be forwarded verbatim so brain can verify it")
}

func TestTicketDocs_PrefersPreferredUsernameOverSub(t *testing.T) {
	svc := &fakeGraphService{response: &brainpb.TicketDocsResponse{}}
	dialer := startFakeBrain(t, svc)
	c := newTestClient(t, dialer, "toji")

	jwt := makeJWT(t, "01ACTORID", "lyckabc")
	_, err := c.TicketDocs(context.Background(), "ticket-123", jwt)
	require.NoError(t, err)
	assert.Equal(t, "user:lyckabc", svc.lastReq.User)
}

func TestTicketDocs_NoCallerJWT_ReturnsErrWithoutCallingBrain(t *testing.T) {
	svc := &fakeGraphService{response: &brainpb.TicketDocsResponse{}}
	dialer := startFakeBrain(t, svc)
	c := newTestClient(t, dialer, "toji")

	_, err := c.TicketDocs(context.Background(), "ticket-123", "")
	require.ErrorIs(t, err, ErrNoCallerJWT)
	assert.Nil(t, svc.lastReq, "brain must not be called when there is no caller JWT")
}

func TestTicketDocs_EmptyResponse_ReturnsEmptySlice(t *testing.T) {
	svc := &fakeGraphService{response: &brainpb.TicketDocsResponse{Nodes: nil}}
	dialer := startFakeBrain(t, svc)
	c := newTestClient(t, dialer, "toji")

	docs, err := c.TicketDocs(context.Background(), "ticket-123", makeJWT(t, "01ACTORID", ""))
	require.NoError(t, err)
	assert.Empty(t, docs)
}

func TestTicketDocs_BrainError_Propagates(t *testing.T) {
	svc := &fakeGraphService{err: assertErr{}}
	dialer := startFakeBrain(t, svc)
	c := newTestClient(t, dialer, "toji")

	_, err := c.TicketDocs(context.Background(), "ticket-123", makeJWT(t, "01ACTORID", ""))
	require.Error(t, err)
}

type assertErr struct{}

func (assertErr) Error() string { return "brain unavailable" }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/brainclient/... -v`
Expected: FAIL — package `brainclient` does not exist yet (`undefined: Client`, `undefined: RelatedDoc`, etc.)

- [ ] **Step 3: Write the implementation**

`internal/brainclient/client.go`:

```go
package brainclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/tojiuni/goquest/internal/brainpb"
)

// ErrNoCallerJWT is returned when TicketDocs is called with no caller JWT to
// forward. brain requires a Bearer token on every RPC (BRAIN_AUTH_MODE=required)
// and goquest deliberately has no service-account fallback for this endpoint
// (spec §2.2, YAGNI) — callers without a session token get this error, not a
// brain round trip that would fail anyway.
var ErrNoCallerJWT = errors.New("brainclient: no caller JWT to forward")

// RelatedDoc is the minimum brain returns for a ticket's reference documents.
type RelatedDoc struct {
	Title      string
	SourcePath string
}

// Client wraps brain's GraphService for ticket-scoped doc lookups.
type Client struct {
	conn   *grpc.ClientConn
	graph  brainpb.GraphServiceClient
	tenant string
}

// New dials brain's gRPC endpoint (plaintext — in-cluster, same trust model
// as the existing SpiceDB client in internal/spicedb/client.go).
func New(addr, tenant string) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("brainclient: dial %s: %w", addr, err)
	}
	return &Client{conn: conn, graph: brainpb.NewGraphServiceClient(conn), tenant: tenant}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

// TicketDocs resolves a ticket's reference documents via brain's TicketDocs
// RPC. callerJWT is the caller's own lymphhub-issued Bearer token (already
// verified by goquest's own auth middleware) — it is decoded here ONLY to
// derive the ReBAC `user` subject brain expects; the raw token is forwarded
// to brain verbatim so brain performs its own signature/exp/subject check
// (mirrors brain_mcp/src/callerAuth.ts's caller-JWT-forward pattern).
func (c *Client) TicketDocs(ctx context.Context, ticketID, callerJWT string) ([]RelatedDoc, error) {
	if callerJWT == "" {
		return nil, ErrNoCallerJWT
	}
	user, err := userFromJWT(callerJWT)
	if err != nil {
		return nil, fmt.Errorf("brainclient: decode caller JWT: %w", err)
	}

	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+callerJWT)
	resp, err := c.graph.TicketDocs(ctx, &brainpb.TicketDocsRequest{
		Tenant:   c.tenant,
		User:     user,
		TicketId: ticketID,
	})
	if err != nil {
		return nil, fmt.Errorf("brainclient: TicketDocs: %w", err)
	}

	docs := make([]RelatedDoc, 0, len(resp.Nodes))
	for _, n := range resp.Nodes {
		docs = append(docs, RelatedDoc{Title: n.Title, SourcePath: n.SourcePath})
	}
	return docs, nil
}

// userFromJWT decode-only parses the JWT payload (no signature verification —
// brain is the verifier) to build the ReBAC subject "user:<handle>". lymphhub
// tokens carry `sub` (an actor UUID) but never `preferred_username`
// (confirmed in brain's deploy/k8s/deployment.yaml comment) — preferred_username
// is preferred when present for forward-compatibility, matching
// brain_mcp/src/callerAuth.ts exactly.
func userFromJWT(raw string) (string, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return "", errors.New("malformed JWT")
	}
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode JWT payload: %w", err)
	}
	var claims struct {
		Sub               string `json:"sub"`
		PreferredUsername string `json:"preferred_username"`
	}
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return "", fmt.Errorf("parse JWT payload: %w", err)
	}
	if claims.Sub == "" {
		return "", errors.New("JWT has no sub claim")
	}
	handle := claims.PreferredUsername
	if handle == "" {
		handle = claims.Sub
	}
	return "user:" + handle, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/brainclient/... -v`
Expected: PASS (5/5).

- [ ] **Step 5: Commit**

```bash
git add internal/brainclient/
git commit -m "feat(brainclient): TicketDocs client — caller-JWT forward, brain-mcp-caller-auth pattern (goq-refdoc-goquest-brain-client)"
```

---

### Task 3: `githubapp.Client` — installation-token-cached content fetch (TDD)

**Files:**
- Create: `internal/githubapp/client.go`
- Test: `internal/githubapp/client_test.go`

**Interfaces:**
- Produces (Task 4 consumes):
  - `func NewClient(appID, installationID, privateKeyPEM string, httpClient *http.Client) (*Client, error)`
  - `func (c *Client) Content(ctx context.Context, owner, repo, path string) (string, error)` — returns `ErrNotFound` on a GitHub 404.
  - `var ErrNotFound = errors.New("githubapp: file not found")`

- [ ] **Step 1: Write the failing test**

`internal/githubapp/client_test.go`:

```go
package githubapp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der := x509.MarshalPKCS1PrivateKey(key)
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}
	return string(pem.EncodeToMemory(block))
}

// fakeGitHub serves both the installation-token endpoint and the contents
// endpoint, recording the App JWT it saw on the token exchange.
func fakeGitHub(t *testing.T, contentByPath map[string]string) (*httptest.Server, *string) {
	t.Helper()
	var seenAppAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/access_tokens"):
			seenAppAuth = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token":      "inst-tok-1",
				"expires_at": "2099-01-01T00:00:00Z",
			})
		case strings.Contains(r.URL.Path, "/contents/"):
			path := r.URL.Path[strings.Index(r.URL.Path, "/contents/")+len("/contents/"):]
			content, ok := contentByPath[path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]any{"message": "Not Found"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content":  base64.StdEncoding.EncodeToString([]byte(content)),
				"encoding": "base64",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return ts, &seenAppAuth
}

func TestContent_ReturnsDecodedFile(t *testing.T) {
	ts, seenAppAuth := fakeGitHub(t, map[string]string{"goquest/spec/foo.md": "# Foo\n"})
	defer ts.Close()

	c, err := newClientWithBaseURL(ts.URL, "app-1", "inst-1", testPrivateKeyPEM(t), ts.Client())
	require.NoError(t, err)

	content, err := c.Content(context.Background(), "tojiuni", "geneso", "goquest/spec/foo.md")
	require.NoError(t, err)
	assert.Equal(t, "# Foo\n", content)
	assert.Contains(t, *seenAppAuth, "Bearer ", "installation-token exchange must present the App JWT")
}

func TestContent_ReusesInstallationTokenAcrossCalls(t *testing.T) {
	tokenCalls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/access_tokens") {
			tokenCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "inst-tok-1", "expires_at": "2099-01-01T00:00:00Z"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": base64.StdEncoding.EncodeToString([]byte("x")), "encoding": "base64",
		})
	}))
	defer ts.Close()

	c, err := newClientWithBaseURL(ts.URL, "app-1", "inst-1", testPrivateKeyPEM(t), ts.Client())
	require.NoError(t, err)

	_, err = c.Content(context.Background(), "tojiuni", "geneso", "a.md")
	require.NoError(t, err)
	_, err = c.Content(context.Background(), "tojiuni", "geneso", "b.md")
	require.NoError(t, err)
	assert.Equal(t, 1, tokenCalls, "installation token must be cached and reused")
}

func TestContent_NotFound_ReturnsErrNotFound(t *testing.T) {
	ts, _ := fakeGitHub(t, map[string]string{})
	defer ts.Close()

	c, err := newClientWithBaseURL(ts.URL, "app-1", "inst-1", testPrivateKeyPEM(t), ts.Client())
	require.NoError(t, err)

	_, err = c.Content(context.Background(), "tojiuni", "geneso", "missing.md")
	require.ErrorIs(t, err, ErrNotFound)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/githubapp/... -v`
Expected: FAIL — `undefined: newClientWithBaseURL`, `undefined: ErrNotFound`.

- [ ] **Step 3: Write the implementation**

`internal/githubapp/client.go`:

```go
package githubapp

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrNotFound is returned when GitHub reports the path doesn't exist.
var ErrNotFound = errors.New("githubapp: file not found")

// Client fetches file content from private GitHub repos via a GitHub App
// installation access token (cached until shortly before expiry, same
// pattern as internal/directory.Client's lymphhub client_credentials cache).
type Client struct {
	baseURL        string
	appID          string
	installationID string
	privateKey     *rsa.PrivateKey
	http           *http.Client

	mu          sync.Mutex
	token       string
	tokenExpiry time.Time
}

// NewClient builds a githubapp Client against the real GitHub API.
func NewClient(appID, installationID, privateKeyPEM string, httpClient *http.Client) (*Client, error) {
	return newClientWithBaseURL("https://api.github.com", appID, installationID, privateKeyPEM, httpClient)
}

// newClientWithBaseURL is NewClient with an overridable base URL, so tests
// can point at an httptest.Server instead of the real GitHub API.
func newClientWithBaseURL(baseURL, appID, installationID, privateKeyPEM string, httpClient *http.Client) (*Client, error) {
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return nil, errors.New("githubapp: invalid PEM private key")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("githubapp: parse private key: %w", err)
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		baseURL:        strings.TrimRight(baseURL, "/"),
		appID:          appID,
		installationID: installationID,
		privateKey:     key,
		http:           httpClient,
	}, nil
}

// Content fetches a file's raw text content from owner/repo at path, using
// the GitHub Contents API (works for private repos, unlike raw.githubusercontent.com).
func (c *Client) Content(ctx context.Context, owner, repo, path string) (string, error) {
	tok, err := c.installationToken(ctx)
	if err != nil {
		return "", fmt.Errorf("githubapp: installation token: %w", err)
	}

	url := fmt.Sprintf("%s/repos/%s/%s/contents/%s", c.baseURL, owner, repo, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", ErrNotFound
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("githubapp: contents API: status %d", resp.StatusCode)
	}

	var body struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.Encoding != "base64" {
		return "", fmt.Errorf("githubapp: unexpected encoding %q", body.Encoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(body.Content, "\n", ""))
	if err != nil {
		return "", fmt.Errorf("githubapp: decode content: %w", err)
	}
	return string(decoded), nil
}

// installationToken exchanges a self-signed App JWT for an installation
// access token, reusing the cached one until shortly before it expires.
func (c *Client) installationToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" && time.Now().Before(c.tokenExpiry) {
		return c.token, nil
	}

	appJWT, err := c.appJWT()
	if err != nil {
		return "", fmt.Errorf("app JWT: %w", err)
	}

	url := fmt.Sprintf("%s/app/installations/%s/access_tokens", c.baseURL, c.installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("access_tokens endpoint: status %d", resp.StatusCode)
	}

	var tokResp struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokResp); err != nil {
		return "", err
	}
	expiry, err := time.Parse(time.RFC3339, tokResp.ExpiresAt)
	if err != nil {
		return "", fmt.Errorf("parse expires_at: %w", err)
	}

	c.token = tokResp.Token
	// Refresh 60s before actual expiry — same margin as directory.Client.
	c.tokenExpiry = expiry.Add(-60 * time.Second)
	return c.token, nil
}

// appJWT builds a short-lived (9-minute, under GitHub's 10-minute ceiling)
// RS256 JWT identifying this GitHub App, per GitHub App auth requirements.
func (c *Client) appJWT() (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Issuer:    c.appID,
		IssuedAt:  jwt.NewNumericDate(now.Add(-30 * time.Second)), // clock-skew allowance
		ExpiresAt: jwt.NewNumericDate(now.Add(9 * time.Minute)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(c.privateKey)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/githubapp/... -v`
Expected: PASS (3/3).

- [ ] **Step 5: Commit**

```bash
git add internal/githubapp/
git commit -m "feat(githubapp): GitHub App installation-token client for geneso raw content (goq-refdoc-goquest-brain-client)"
```

---

### Task 4: `RefDocsHandler` — `GET /tickets/{id}/docs` + `GET /docs/content` (TDD)

**Files:**
- Create: `internal/handler/refdocs.go`
- Test: `internal/handler/refdocs_test.go`

**Interfaces:**
- Consumes: Task 2's `brainclient.RelatedDoc`, Task 3's `githubapp.ErrNotFound`.
- Produces (Task 5 wires this): `NewRefDocsHandler(brain BrainLookuper, github ContentFetcher, genesoOwner, genesoRepo string) *RefDocsHandler`, `(*RefDocsHandler).TicketDocs`, `(*RefDocsHandler).Content`.

- [ ] **Step 1: Write the failing test**

`internal/handler/refdocs_test.go`:

```go
package handler_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/brainclient"
	"github.com/tojiuni/goquest/internal/githubapp"
	"github.com/tojiuni/goquest/internal/handler"
	"github.com/tojiuni/goquest/internal/spicedb"
	"github.com/tojiuni/goquest/internal/ticket"
)

type fakeBrainLookuper struct {
	docs []brainclient.RelatedDoc
	err  error
}

func (f *fakeBrainLookuper) TicketDocs(_ context.Context, _, _ string) ([]brainclient.RelatedDoc, error) {
	return f.docs, f.err
}

type fakeContentFetcher struct {
	content string
	err     error
}

func (f *fakeContentFetcher) Content(_ context.Context, _, _, _ string) (string, error) {
	return f.content, f.err
}

// refDocsRouter mounts TicketDocs inside the same RequireTicketView-gated
// /tickets/{id} group as production, and Content as a standalone route
// (mirrors cmd/service/main.go's authenticated group, minus API-key/JWT
// middleware — auth.WithRequesterID + a raw Authorization header stand in
// for it in tests, matching this package's existing test conventions).
func refDocsRouter(rd *handler.RefDocsHandler, svc *ticket.Service, checker spicedb.PermissionChecker) http.Handler {
	r := chi.NewRouter()
	r.Route("/tickets/{id}", func(r chi.Router) {
		r.Use(auth.RequireTicketView(svc, checker))
		r.Get("/docs", rd.TicketDocs)
	})
	r.Get("/docs/content", rd.Content)
	return r
}

func withBearerJWT(req *http.Request, jwt string) *http.Request {
	req.Header.Set("Authorization", "Bearer "+jwt)
	return req
}

func TestRefDocs_TicketDocs_ReturnsResolvedDocs(t *testing.T) {
	tk := &ticket.Ticket{
		ID: uuid.New(), Status: ticket.StatusPending, Priority: ticket.PriorityNormal,
		Visibility: ticket.VisibilityProject, AssigneeType: ticket.AssigneeHuman,
	}
	store := newMemStore(tk)
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	rd := handler.NewRefDocsHandler(
		&fakeBrainLookuper{docs: []brainclient.RelatedDoc{{Title: "Foo", SourcePath: "goquest/spec/foo.md"}}},
		&fakeContentFetcher{}, "tojiuni", "geneso",
	)
	r := refDocsRouter(rd, svc, &worldChecker{})

	req := httptest.NewRequest("GET", "/tickets/"+tk.ID.String()+"/docs", nil)
	req = asUser(req, "staff@toji.homes")
	req = withBearerJWT(req, "header.payload.sig")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, 200, rec.Code)
	assert.JSONEq(t, `{"docs":[{"title":"Foo","source_path":"goquest/spec/foo.md"}]}`, rec.Body.String())
}

func TestRefDocs_TicketDocs_NoBearerToken_Returns401(t *testing.T) {
	tk := &ticket.Ticket{
		ID: uuid.New(), Status: ticket.StatusPending, Priority: ticket.PriorityNormal,
		Visibility: ticket.VisibilityProject, AssigneeType: ticket.AssigneeHuman,
	}
	store := newMemStore(tk)
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	rd := handler.NewRefDocsHandler(&fakeBrainLookuper{}, &fakeContentFetcher{}, "tojiuni", "geneso")
	r := refDocsRouter(rd, svc, &worldChecker{})

	req := httptest.NewRequest("GET", "/tickets/"+tk.ID.String()+"/docs", nil)
	req = asUser(req, "staff@toji.homes")
	// No Authorization header set — API-key-authenticated caller, no JWT to forward.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, 401, rec.Code)
}

func TestRefDocs_TicketDocs_BrainError_Returns502(t *testing.T) {
	tk := &ticket.Ticket{
		ID: uuid.New(), Status: ticket.StatusPending, Priority: ticket.PriorityNormal,
		Visibility: ticket.VisibilityProject, AssigneeType: ticket.AssigneeHuman,
	}
	store := newMemStore(tk)
	svc := ticket.NewService(store, noopPublisher{}, nil, nil, nil, nil)
	rd := handler.NewRefDocsHandler(
		&fakeBrainLookuper{err: errors.New("brain unavailable")},
		&fakeContentFetcher{}, "tojiuni", "geneso",
	)
	r := refDocsRouter(rd, svc, &worldChecker{})

	req := withBearerJWT(asUser(httptest.NewRequest("GET", "/tickets/"+tk.ID.String()+"/docs", nil), "staff@toji.homes"), "header.payload.sig")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, 502, rec.Code)
}

func TestRefDocs_Content_ReturnsContent(t *testing.T) {
	rd := handler.NewRefDocsHandler(&fakeBrainLookuper{}, &fakeContentFetcher{content: "# Foo\n"}, "tojiuni", "geneso")
	r := refDocsRouter(rd, nil, &worldChecker{})

	req := httptest.NewRequest("GET", "/docs/content?source_path=goquest/spec/foo.md", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, 200, rec.Code)
	assert.JSONEq(t, `{"content":"# Foo\n","source_path":"goquest/spec/foo.md"}`, rec.Body.String())
}

func TestRefDocs_Content_StripsGenesoPrefix(t *testing.T) {
	fetcher := &fakeContentFetcher{content: "x"}
	rd := handler.NewRefDocsHandler(&fakeBrainLookuper{}, fetcher, "tojiuni", "geneso")
	r := refDocsRouter(rd, nil, &worldChecker{})

	req := httptest.NewRequest("GET", "/docs/content?source_path=geneso:goquest/spec/foo.md", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, 200, rec.Code)
	assert.JSONEq(t, `{"content":"x","source_path":"goquest/spec/foo.md"}`, rec.Body.String())
}

func TestRefDocs_Content_MissingSourcePath_Returns400(t *testing.T) {
	rd := handler.NewRefDocsHandler(&fakeBrainLookuper{}, &fakeContentFetcher{}, "tojiuni", "geneso")
	r := refDocsRouter(rd, nil, &worldChecker{})

	req := httptest.NewRequest("GET", "/docs/content", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, 400, rec.Code)
}

func TestRefDocs_Content_NotFound_Returns404(t *testing.T) {
	rd := handler.NewRefDocsHandler(&fakeBrainLookuper{}, &fakeContentFetcher{err: githubapp.ErrNotFound}, "tojiuni", "geneso")
	r := refDocsRouter(rd, nil, &worldChecker{})

	req := httptest.NewRequest("GET", "/docs/content?source_path=missing.md", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, 404, rec.Code)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/handler/... -run TestRefDocs -v`
Expected: FAIL — `undefined: handler.NewRefDocsHandler`.

- [ ] **Step 3: Write the implementation**

`internal/handler/refdocs.go`:

```go
package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/brainclient"
	"github.com/tojiuni/goquest/internal/githubapp"
)

// BrainLookuper resolves a ticket's reference documents via brain. Satisfied
// by *brainclient.Client; a narrow interface so handler tests can fake it.
type BrainLookuper interface {
	TicketDocs(ctx context.Context, ticketID, callerJWT string) ([]brainclient.RelatedDoc, error)
}

// ContentFetcher fetches a file's raw content from a GitHub repo. Satisfied
// by *githubapp.Client.
type ContentFetcher interface {
	Content(ctx context.Context, owner, repo, path string) (string, error)
}

// RefDocsHandler serves the goquest-mediated ticket reference document
// endpoints: GET /tickets/{id}/docs (brain-backed list) and
// GET /docs/content (GitHub-backed content), so the browser never talks to
// brain or GitHub directly (goq-refdoc-goquest-brain-client).
type RefDocsHandler struct {
	brain       BrainLookuper
	github      ContentFetcher
	genesoOwner string
	genesoRepo  string
}

func NewRefDocsHandler(brain BrainLookuper, github ContentFetcher, genesoOwner, genesoRepo string) *RefDocsHandler {
	return &RefDocsHandler{brain: brain, github: github, genesoOwner: genesoOwner, genesoRepo: genesoRepo}
}

// bearerJWT extracts the raw JWT from an "Authorization: Bearer <jwt>"
// header, or "" if absent/not bearer-shaped (e.g. an X-API-Key caller with
// no JWT at all).
func bearerJWT(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimPrefix(h, prefix)
}

type docJSON struct {
	Title      string `json:"title"`
	SourcePath string `json:"source_path"`
}

// TicketDocs handles GET /tickets/{id}/docs. RequireTicketView has already
// authorized the caller and stashed the ticket.
func (h *RefDocsHandler) TicketDocs(w http.ResponseWriter, r *http.Request) {
	tk := auth.TicketFrom(r)
	if tk == nil {
		writeError(w, http.StatusInternalServerError, "ticket context missing (RequireTicketView not applied)")
		return
	}

	jwt := bearerJWT(r)
	if jwt == "" {
		writeError(w, http.StatusUnauthorized, "brain-backed ticket docs require a user session token")
		return
	}

	related, err := h.brain.TicketDocs(r.Context(), tk.ID.String(), jwt)
	if err != nil {
		writeError(w, http.StatusBadGateway, "brain unavailable: "+err.Error())
		return
	}

	docs := make([]docJSON, 0, len(related))
	for _, d := range related {
		docs = append(docs, docJSON{Title: d.Title, SourcePath: d.SourcePath})
	}
	writeJSON(w, http.StatusOK, map[string]any{"docs": docs})
}

// Content handles GET /docs/content?source_path=<path>. Sits in goquest's
// existing authenticated route group (no ticket context needed — the path
// alone is the lookup key, matching the spec's design).
func (h *RefDocsHandler) Content(w http.ResponseWriter, r *http.Request) {
	sourcePath := r.URL.Query().Get("source_path")
	if sourcePath == "" {
		writeError(w, http.StatusBadRequest, "source_path is required")
		return
	}
	// ref_ids in production carry a mix of "geneso:"-prefixed and bare paths
	// (see brain/spec/2026-07-22-brain-ticket-docs-rpc-design.md §background) —
	// accept both.
	sourcePath = strings.TrimPrefix(sourcePath, "geneso:")

	content, err := h.github.Content(r.Context(), h.genesoOwner, h.genesoRepo, sourcePath)
	if err != nil {
		if errors.Is(err, githubapp.ErrNotFound) {
			writeError(w, http.StatusNotFound, "document not found")
			return
		}
		writeError(w, http.StatusBadGateway, "github fetch failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"content": content, "source_path": sourcePath})
}
```

`*brainclient.Client` and `*githubapp.Client` both already satisfy these exact signatures from Task 2/3 — no adapter needed.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/handler/... -run TestRefDocs -v`
Expected: PASS (7/7).

- [ ] **Step 5: Commit**

```bash
git add internal/handler/refdocs.go internal/handler/refdocs_test.go
git commit -m "feat(handler): GET /tickets/{id}/docs + GET /docs/content — brain/GitHub mediation (goq-refdoc-goquest-brain-client)"
```

---

### Task 5: Config + route wiring

**Files:**
- Modify: `internal/config/config.go`
- Modify: `cmd/service/main.go`

**Interfaces:**
- Consumes: Task 2's `brainclient.New`, Task 3's `githubapp.NewClient`, Task 4's `handler.NewRefDocsHandler`.
- Produces: live routes `GET /api/v1/tickets/{id}/docs`, `GET /api/v1/docs/content`.

- [ ] **Step 1: Add config fields**

In `internal/config/config.go`, add next to the existing lymphhub actor-lookup fields:

```go
	// brain gRPC (goq-refdoc-goquest-brain-client) — ticket reference-doc
	// lookup mediation. Empty BrainGRPCAddr disables /tickets/{id}/docs
	// (always returns {"docs":[]}).
	BrainGRPCAddr string
	BrainTenant   string

	// GitHub App (goq-refdoc-goquest-brain-client) — server-side raw content
	// fetch from the private tojiuni/geneso repo for /docs/content. Empty
	// GitHubAppID disables /docs/content (always 404s).
	GitHubAppID             string
	GitHubAppInstallationID string
	GitHubAppPrivateKeyPEM  string
	GenesoRepoOwner         string
	GenesoRepoName          string
```

And in `Load()`:

```go
		BrainGRPCAddr: os.Getenv("BRAIN_GRPC_ADDR"),
		BrainTenant:   envOrDefault("BRAIN_TENANT", "toji"),

		GitHubAppID:             os.Getenv("GITHUB_APP_ID"),
		GitHubAppInstallationID: os.Getenv("GITHUB_APP_INSTALLATION_ID"),
		GitHubAppPrivateKeyPEM:  os.Getenv("GITHUB_APP_PRIVATE_KEY_PEM"),
		GenesoRepoOwner:         envOrDefault("GENESO_REPO_OWNER", "tojiuni"),
		GenesoRepoName:          envOrDefault("GENESO_REPO_NAME", "geneso"),
```

- [ ] **Step 2: Wire the clients + handler in `cmd/service/main.go`**

Add imports `"github.com/tojiuni/goquest/internal/brainclient"` and `"github.com/tojiuni/goquest/internal/githubapp"` to the import block.

Near where `usersH` is constructed (goq-user-lookup-proxy's wiring — search for `handler.NewUsersHandler`), add:

```go
	// brain-backed ticket reference docs + GitHub-backed content
	// (goq-refdoc-goquest-brain-client). Both degrade to inert responses
	// when unconfigured — see noopBrainLookuper/noopContentFetcher below.
	var refDocsBrain handler.BrainLookuper = noopBrainLookuper{}
	if cfg.BrainGRPCAddr != "" {
		brainClient, err := brainclient.New(cfg.BrainGRPCAddr, cfg.BrainTenant)
		if err != nil {
			log.Printf("warn: brainclient.New: %v (ticket docs will be unavailable)", err)
		} else {
			refDocsBrain = brainClient
		}
	}
	var refDocsGitHub handler.ContentFetcher = noopContentFetcher{}
	if cfg.GitHubAppID != "" && cfg.GitHubAppPrivateKeyPEM != "" {
		ghClient, err := githubapp.NewClient(cfg.GitHubAppID, cfg.GitHubAppInstallationID, cfg.GitHubAppPrivateKeyPEM, http.DefaultClient)
		if err != nil {
			log.Printf("warn: githubapp.NewClient: %v (doc content will be unavailable)", err)
		} else {
			refDocsGitHub = ghClient
		}
	}
	refDocsH := handler.NewRefDocsHandler(refDocsBrain, refDocsGitHub, cfg.GenesoRepoOwner, cfg.GenesoRepoName)
```

Add the two `noop*` fallback types near `noopLookuper` (search for that type — same file, same pattern):

```go
// noopBrainLookuper is the /tickets/{id}/docs fallback when brain isn't
// configured — the route stays live and returns an empty list rather than
// being absent (goq-refdoc-goquest-brain-client).
type noopBrainLookuper struct{}

func (noopBrainLookuper) TicketDocs(context.Context, string, string) ([]brainclient.RelatedDoc, error) {
	return nil, nil
}

// noopContentFetcher is the /docs/content fallback when the GitHub App isn't
// configured — always reports not-found rather than erroring differently
// per-caller.
type noopContentFetcher struct{}

func (noopContentFetcher) Content(context.Context, string, string, string) (string, error) {
	return "", githubapp.ErrNotFound
}
```

- [ ] **Step 3: Register the routes**

Inside the `/tickets/{id}` route group (alongside `r.Get("/refs", ticketH.ListRefs)`):

```go
				r.Get("/docs", refDocsH.TicketDocs)
```

Inside the top-level authenticated group (alongside `r.Get("/users/lookup", usersH.Lookup)`):

```go
			r.Get("/docs/content", refDocsH.Content)
```

- [ ] **Step 4: Full verification**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: build clean, all tests pass (new `internal/brainpb`, `internal/brainclient`, `internal/githubapp`, and `internal/handler/refdocs_test.go` tests included; pre-existing Postgres-backed tests continue to skip locally exactly as before).

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go cmd/service/main.go
git commit -m "feat(handler): wire brain client + GitHub App client into goquest routes (goq-refdoc-goquest-brain-client)"
```

---

## Completed after

1. Push branch → PR title must contain `goq-refdoc-goquest-brain-client`. PR body should note both new endpoints degrade gracefully when unconfigured (empty `BRAIN_GRPC_ADDR`/`GITHUB_APP_ID` → inert responses, not errors) — safe to merge/deploy before the GitHub App and brain address are provisioned in this deployment's env.
2. Once merged: create the GitHub App installation for `tojiuni/geneso` if one doesn't already exist reusing `secret/neunexus/workspace/github-app-tojiuni` (already has `app_id`/`installation_id`/`private_key_pem` — confirmed via `lympht check` during planning), wire `BRAIN_GRPC_ADDR=brain.brain.svc.cluster.local:50051` + the GitHub App env vars into `neunexus:deploy/goquest/service/deployment.yaml` (same VSO + env pattern as `goq-user-lookup-proxy`'s neunexus PR #815), redeploy.
3. Manual smoke test: `curl -H "X-API-Key: ..." ".../tickets/<id>/docs"` (expect 401 — X-API-Key has no JWT to forward, this is correct per spec) and, with a real user session Bearer JWT, `curl -H "Authorization: Bearer <jwt>" ".../tickets/<id>/docs"` should return real docs for a ticket with `ref_ids`. `.../docs/content?source_path=<a known geneso path>` should return that file's content.
4. Once this PR is merged+deployed, goquest-ui's Session 3 (`goq-refdoc-ui-refdocviewer-rewrite`) can begin.
