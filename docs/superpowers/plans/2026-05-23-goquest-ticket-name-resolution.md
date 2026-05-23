# 이름/slug 기반 티켓 생성 + 워크스페이스 slug 핸들 강화 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `project_id` 없이 `workspace_slug|workspace_name` + `project_name`으로 티켓을 생성하고, 워크스페이스 이름을 유일 제약으로 승격하며 slug를 URL-safe·유일·안정 핸들로 완성한다.

**Architecture:** 워크스페이스/프로젝트 store에 이름 조회 메서드를 추가하고, 둘을 조합하는 독립 `internal/resolve` 패키지가 ref→project_id 해석을 담당한다. 핸들러는 narrow 인터페이스로 resolver를 주입받아 thin하게 유지한다. slug 생성은 랜덤 hex suffix를 결정적 `-N` suffix + degenerate fallback으로 교체한다.

**Tech Stack:** Go, pgx/v5, chi, 표준 `testing`. Postgres 마이그레이션은 `internal/migrate/sql/*.sql`.

**Spec:** `docs/superpowers/specs/2026-05-23-goquest-ticket-name-resolution-design.md`

**작업 브랜치:** `feat/ticket-name-resolution` (이미 생성됨, origin/main 기준)

---

## File Structure

- `internal/project/store.go` — Store 인터페이스에 `GetByWorkspaceAndName` 추가
- `internal/project/store_pg.go` — pg 구현
- `internal/project/service_test.go` — mockStore에 메서드 추가
- `internal/workspace/store.go` — Store 인터페이스에 `GetByName` 추가
- `internal/workspace/store_pg.go` — pg 구현 + Update의 23505→ErrDuplicateName
- `internal/workspace/service.go` — `normalizeSlug` 신설, `create()` slug 로직 교체, `randomHex`는 invitation 토큰 등에서 계속 사용되므로 유지
- `internal/workspace/service_test.go` — mockStore에 `GetByName` 추가 + slug/충돌 테스트
- `internal/workspace/errors.go` (신규 또는 store_pg.go 내) — `ErrDuplicateName` sentinel
- `internal/migrate/sql/012_workspace_name_unique.sql` — 유니크 인덱스
- `internal/resolve/resolve.go` (신규 패키지) — `Ref`, `Resolver`, sentinels, narrow lookup 인터페이스
- `internal/resolve/resolve_test.go` (신규) — resolver 단위 테스트
- `internal/ticket/model.go` — `CreateTicketReq`에 필드 추가
- `internal/handler/ticket.go` — `ProjectResolver` 인터페이스 + Create 해석 분기 + 409 매핑
- `internal/handler/workspace.go` — Update에서 `ErrDuplicateName`→409
- `internal/handler/workspace_test.go` — 409 테스트
- `cmd/service/main.go` — resolver 생성·주입

---

## Task 1: project store `GetByWorkspaceAndName`

워크스페이스 ID + 프로젝트명으로 프로젝트를 조회한다. `UNIQUE(workspace_id, name)`이라 0/1행.

**Files:**
- Modify: `internal/project/store.go` (Store 인터페이스)
- Modify: `internal/project/store_pg.go` (pg 구현, `Get` 바로 아래)
- Modify: `internal/project/service_test.go` (mockStore)

- [ ] **Step 1: 인터페이스에 메서드 추가**

`internal/project/store.go`의 Store 인터페이스 `Get` 줄 아래에 추가:

```go
	GetByWorkspaceAndName(ctx context.Context, workspaceID uuid.UUID, name string) (*Project, error)
```

- [ ] **Step 2: pg 구현 추가**

`internal/project/store_pg.go`의 `Get` 메서드 바로 아래에 추가 (컬럼 목록은 기존 `Get`과 동일):

```go
func (s *pgStore) GetByWorkspaceAndName(ctx context.Context, workspaceID uuid.UUID, name string) (*Project, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, workspace_id, name, description, status, difficulty, reward_xp,
		       created_by, created_at, updated_at
		FROM projects
		WHERE workspace_id = $1 AND LOWER(name) = LOWER($2)`, workspaceID, name)
	return scanProject(row)
}
```

- [ ] **Step 3: mockStore에 구현 추가**

`internal/project/service_test.go`의 mockStore 메서드들 사이(예: `Get` 아래)에 추가:

```go
func (m *mockStore) GetByWorkspaceAndName(_ context.Context, wid uuid.UUID, name string) (*project.Project, error) {
	for _, p := range m.projects {
		if p.WorkspaceID == wid && strings.EqualFold(p.Name, name) {
			return p, nil
		}
	}
	return nil, project.ErrNotFound
}
```

그리고 `internal/project/service_test.go` import 블록에 `"strings"`가 없으면 추가한다.

- [ ] **Step 4: 빌드 + 기존 테스트 통과 확인**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go build ./... && go test ./internal/project/`
Expected: PASS (빌드 성공, project 테스트 통과)

- [ ] **Step 5: Commit**

```bash
cd /Users/dong-hoshin/Documents/dev/goquest
git add internal/project/store.go internal/project/store_pg.go internal/project/service_test.go
git commit -m "feat(project): GetByWorkspaceAndName store method"
```

---

## Task 2: workspace store `GetByName`

`LOWER(name)` 유일 인덱스(Task 4)를 전제로 0/1행 조회.

**Files:**
- Modify: `internal/workspace/store.go`
- Modify: `internal/workspace/store_pg.go` (`GetBySlug` 아래)
- Modify: `internal/workspace/service_test.go` (mockStore)

- [ ] **Step 1: 인터페이스에 메서드 추가**

`internal/workspace/store.go`의 `GetBySlug` 줄 아래에 추가:

```go
	GetByName(ctx context.Context, name string) (*Workspace, error)
```

- [ ] **Step 2: pg 구현 추가**

`internal/workspace/store_pg.go`의 `GetBySlug` 바로 아래:

```go
func (s *pgStore) GetByName(ctx context.Context, name string) (*Workspace, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, name, slug, description, type, owner_id, created_at, updated_at
		FROM workspaces WHERE LOWER(name) = LOWER($1)`, name)
	return scanWorkspace(row)
}
```

- [ ] **Step 3: mockStore에 구현 추가**

`internal/workspace/service_test.go`의 mockStore(예: `GetBySlug` 아래)에 추가:

```go
func (m *mockStore) GetByName(_ context.Context, name string) (*workspace.Workspace, error) {
	for _, ws := range m.workspaces {
		if strings.EqualFold(ws.Name, name) {
			return ws, nil
		}
	}
	return nil, workspace.ErrNotFound
}
```

mockStore의 워크스페이스 저장 필드명이 `m.workspaces`가 아니면(맵 필드명 확인) 그에 맞춘다. import에 `"strings"` 없으면 추가.

- [ ] **Step 4: 빌드 + 기존 테스트 통과 확인**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go build ./... && go test ./internal/workspace/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /Users/dong-hoshin/Documents/dev/goquest
git add internal/workspace/store.go internal/workspace/store_pg.go internal/workspace/service_test.go
git commit -m "feat(workspace): GetByName store method"
```

---

## Task 3: slug 핸들 강화 (결정적 suffix + degenerate fallback)

랜덤 hex suffix를 제거하고, 깨끗한 base slug → 충돌 시 `-2`,`-3` → 정규화 결과가 비면 `ws-<id8>` fallback.

**Files:**
- Modify: `internal/workspace/service.go`
- Test: `internal/workspace/service_test.go`

- [ ] **Step 1: 실패 테스트 작성**

`internal/workspace/service_test.go` 끝에 추가:

```go
func TestCreate_SlugCleanNoRandomSuffix(t *testing.T) {
	svc := workspace.NewService(newMockStore())
	ws, err := svc.Create(context.Background(), "owner-1", workspace.CreateReq{Name: "My Workspace"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ws.Slug != "my-workspace" {
		t.Errorf("slug = %q, want %q (no random suffix)", ws.Slug, "my-workspace")
	}
}

func TestCreate_SlugCollisionDeterministicSuffix(t *testing.T) {
	store := newMockStore()
	svc := workspace.NewService(store)
	ctx := context.Background()
	a, _ := svc.Create(ctx, "o1", workspace.CreateReq{Name: "Dup"})
	b, _ := svc.Create(ctx, "o2", workspace.CreateReq{Name: "Dup"})
	if a.Slug != "dup" {
		t.Errorf("first slug = %q, want dup", a.Slug)
	}
	if b.Slug != "dup-2" {
		t.Errorf("second slug = %q, want dup-2", b.Slug)
	}
}

func TestCreate_SlugDegenerateFallbackToID(t *testing.T) {
	svc := workspace.NewService(newMockStore())
	ws, err := svc.Create(context.Background(), "o1", workspace.CreateReq{Name: "내 워크스페이스"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := "ws-" + ws.ID.String()[:8]
	if ws.Slug != want {
		t.Errorf("slug = %q, want %q (degenerate name → id fallback)", ws.Slug, want)
	}
}
```

- [ ] **Step 2: 테스트 실패 확인**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go test ./internal/workspace/ -run 'TestCreate_Slug' -v`
Expected: FAIL (현재 slug에 랜덤 hex가 붙어 `my-workspace-xxxxxx` 형태)

- [ ] **Step 3: `normalizeSlug` 신설 + `create()` 교체**

`internal/workspace/service.go`에서 기존 `toSlug` 함수를 다음 `normalizeSlug`로 **교체**한다 (랜덤 suffix 제거):

```go
// normalizeSlug returns a URL-safe base slug for name, or "" if name has no
// slug-able characters (e.g. all-Hangul). Collision/empty handling is done by create().
func normalizeSlug(name string) string {
	s := strings.ToLower(name)
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, s)
	s = slugRe.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}
```

그리고 `create()`에서 `slug := toSlug(req.Name)` 줄을 아래 블록으로 교체 (ws 구조체 생성 **전에** id를 먼저 만들어 fallback에 쓴다):

```go
	id := uuid.New()
	slug, err := svc.uniqueSlug(ctx, normalizeSlug(req.Name), id)
	if err != nil {
		return nil, err
	}
```

`create()` 내부의 `ID: uuid.New()`를 `ID: id`로 바꾼다. (`now := time.Now().UTC()`는 그대로)

`create()` 함수 아래에 헬퍼 추가:

```go
const maxSlugAttempts = 50

// uniqueSlug picks a free slug: base, then base-2, base-3...; if base is empty
// (degenerate name) or no free suffix found, falls back to a stable id-based handle.
func (svc *Service) uniqueSlug(ctx context.Context, base string, id uuid.UUID) (string, error) {
	if base == "" {
		return "ws-" + id.String()[:8], nil
	}
	candidate := base
	for i := 2; i <= maxSlugAttempts; i++ {
		_, err := svc.store.GetBySlug(ctx, candidate)
		if errors.Is(err, ErrNotFound) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
	return "ws-" + id.String()[:8], nil
}
```

`internal/workspace/service.go` import에 `"errors"`, `"fmt"`가 이미 있는지 확인하고 없으면 추가한다. `unicode` import는 `normalizeSlug`에서 더는 쓰지 않으므로, 다른 사용처가 없다면 제거한다 (`go build`가 미사용 import를 에러로 잡는다).

- [ ] **Step 4: 테스트 통과 확인**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go test ./internal/workspace/ -run 'TestCreate_Slug' -v && go test ./internal/workspace/`
Expected: PASS (신규 3개 + 기존 테스트)

기존 테스트가 랜덤 suffix를 가정하면(예: slug 정확값 비교) 새 결정적 값으로 수정한다.

- [ ] **Step 5: Commit**

```bash
cd /Users/dong-hoshin/Documents/dev/goquest
git add internal/workspace/service.go internal/workspace/service_test.go
git commit -m "feat(workspace): deterministic slug + degenerate id fallback (replace random hex)"
```

---

## Task 4: 워크스페이스 name 유일성 (마이그레이션 + Update 409)

**Files:**
- Create: `internal/migrate/sql/012_workspace_name_unique.sql`
- Modify: `internal/workspace/store_pg.go` (`ErrDuplicateName` sentinel + Update 23505 감지)
- Modify: `internal/handler/workspace.go` (Update에서 409 매핑)
- Test: `internal/handler/workspace_test.go`

- [ ] **Step 1: 마이그레이션 작성**

`internal/migrate/sql/012_workspace_name_unique.sql`:

```sql
-- workspace name과 project name(워크스페이스 내)을 대소문자 무시 유일로 승격.
-- 이름 기반 해석이 LOWER(name) 매칭이므로 CI 유일성이 DB로 보장돼야 임의 행 반환을 막는다.
-- 적용 전 사전 점검(런북): 중복이 있으면 인덱스 생성이 실패한다.
--   SELECT LOWER(name), COUNT(*) FROM workspaces GROUP BY 1 HAVING COUNT(*) > 1;
--   SELECT workspace_id, LOWER(name), COUNT(*) FROM projects GROUP BY 1,2 HAVING COUNT(*) > 1;
CREATE UNIQUE INDEX IF NOT EXISTS workspaces_lower_name_key
  ON workspaces (LOWER(name));

CREATE UNIQUE INDEX IF NOT EXISTS projects_workspace_lower_name_key
  ON projects (workspace_id, LOWER(name));
```

(기존 `projects`의 `UNIQUE(workspace_id, name)`은 대소문자 구분이라 "Alpha"/"alpha" 중복을 막지 못한다. CI 인덱스가 `GetByWorkspaceAndName`의 `LOWER(name)` 매칭을 0/1행으로 보장하고 인덱스로 가속한다.)

- [ ] **Step 2: `ErrDuplicateName` sentinel + Update 감지 (구현)**

`internal/workspace/store_pg.go`의 `ErrNotFound` 선언 옆에 추가:

```go
var ErrDuplicateName = errors.New("workspace: name already in use")
```

import 블록에 `"github.com/jackc/pgx/v5/pgconn"`를 추가하고, `Update`의 에러 처리부를 교체:

```go
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrDuplicateName
		}
		return nil, fmt.Errorf("update workspace: %w", err)
	}
```

- [ ] **Step 3: 핸들러 409 매핑 — 실패 테스트 작성**

`internal/handler/workspace_test.go`에 추가 (stubWSSvc가 ErrDuplicateName을 반환하도록):

```go
func TestWorkspaceUpdate_DuplicateName409(t *testing.T) {
	svc := &stubWSSvc{updateErr: workspace.ErrDuplicateName}
	h := handler.NewWorkspaceHandler(svc)

	body, _ := json.Marshal(map[string]string{"name": "taken"})
	id := uuid.New()
	r := httptest.NewRequest(http.MethodPatch, "/workspaces/"+id.String(), bytes.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id.String())
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.Update(w, r)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
}
```

`stubWSSvc`에 `updateErr error` 필드가 없으면 추가하고, 그 `Update` 메서드가 `updateErr`를 반환하도록 수정:

```go
func (s *stubWSSvc) Update(_ context.Context, _ uuid.UUID, _ workspace.UpdateReq) (*workspace.Workspace, error) {
	if s.updateErr != nil {
		return nil, s.updateErr
	}
	return &workspace.Workspace{}, nil
}
```

import에 누락된 패키지(`bytes`, `encoding/json`, `net/http`, `net/http/httptest`, `chi`, `uuid`, `context`)가 있으면 추가한다 (대부분 이미 존재).

- [ ] **Step 4: 테스트 실패 확인**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go test ./internal/handler/ -run TestWorkspaceUpdate_DuplicateName409 -v`
Expected: FAIL (현재 핸들러는 ErrDuplicateName을 일반 에러로 처리 → 409 아님)

- [ ] **Step 5: 핸들러 Update에 409 매핑 구현**

`internal/handler/workspace.go`의 `Update`에서 `svc.Update` 호출의 에러 처리부에 분기를 추가한다 (sentinel import: 파일 상단에 `"errors"`가 없으면 추가):

```go
	ws, err := h.svc.Update(r.Context(), id, req)
	if err != nil {
		if errors.Is(err, workspace.ErrDuplicateName) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
```

기존 Update의 에러 처리 코드 형태에 맞춰 위 분기만 끼워 넣는다(기존 성공 경로/응답은 유지).

- [ ] **Step 6: 테스트 통과 + 빌드 확인**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go build ./... && go test ./internal/handler/ ./internal/workspace/`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
cd /Users/dong-hoshin/Documents/dev/goquest
git add internal/migrate/sql/012_workspace_name_unique.sql internal/workspace/store_pg.go internal/handler/workspace.go internal/handler/workspace_test.go
git commit -m "feat(workspace): UNIQUE(LOWER(name)) migration + Update 409 on duplicate"
```

---

## Task 5: `internal/resolve` 패키지 — ProjectResolver

워크스페이스 식별자(slug 우선, 없으면 name) + project_name → project_id. 짝 누락/미존재 sentinel.

**Files:**
- Create: `internal/resolve/resolve.go`
- Test: `internal/resolve/resolve_test.go`

- [ ] **Step 1: 실패 테스트 작성**

`internal/resolve/resolve_test.go`:

```go
package resolve_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/project"
	"github.com/tojiuni/goquest/internal/resolve"
	"github.com/tojiuni/goquest/internal/workspace"
)

type fakeWS struct {
	bySlug map[string]*workspace.Workspace
	byName map[string]*workspace.Workspace
}

func (f fakeWS) GetBySlug(_ context.Context, s string) (*workspace.Workspace, error) {
	if ws, ok := f.bySlug[s]; ok {
		return ws, nil
	}
	return nil, workspace.ErrNotFound
}
func (f fakeWS) GetByName(_ context.Context, n string) (*workspace.Workspace, error) {
	if ws, ok := f.byName[n]; ok {
		return ws, nil
	}
	return nil, workspace.ErrNotFound
}

type fakePJ struct{ byKey map[string]*project.Project } // key: wid|name

func (f fakePJ) GetByWorkspaceAndName(_ context.Context, wid uuid.UUID, name string) (*project.Project, error) {
	if p, ok := f.byKey[wid.String()+"|"+name]; ok {
		return p, nil
	}
	return nil, project.ErrNotFound
}

func newResolver() (*resolve.Resolver, uuid.UUID, uuid.UUID) {
	wid := uuid.New()
	pid := uuid.New()
	ws := &workspace.Workspace{ID: wid, Name: "Team", Slug: "team"}
	pj := &project.Project{ID: pid, WorkspaceID: wid, Name: "Alpha"}
	r := resolve.New(
		fakeWS{bySlug: map[string]*workspace.Workspace{"team": ws}, byName: map[string]*workspace.Workspace{"Team": ws}},
		fakePJ{byKey: map[string]*project.Project{wid.String() + "|Alpha": pj}},
	)
	return r, wid, pid
}

func TestResolve_BySlug(t *testing.T) {
	r, _, pid := newResolver()
	got, err := r.ResolveProjectID(context.Background(), resolve.Ref{WorkspaceSlug: "team", ProjectName: "Alpha"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got == nil || *got != pid {
		t.Fatalf("got %v, want %v", got, pid)
	}
}

func TestResolve_ByName(t *testing.T) {
	r, _, pid := newResolver()
	got, err := r.ResolveProjectID(context.Background(), resolve.Ref{WorkspaceName: "Team", ProjectName: "Alpha"})
	if err != nil || got == nil || *got != pid {
		t.Fatalf("got %v err %v, want %v", got, err, pid)
	}
}

func TestResolve_SlugPreferredOverName(t *testing.T) {
	r, _, pid := newResolver()
	// name이 틀려도 slug가 맞으면 해석 성공 (slug 우선)
	got, err := r.ResolveProjectID(context.Background(), resolve.Ref{WorkspaceSlug: "team", WorkspaceName: "Wrong", ProjectName: "Alpha"})
	if err != nil || got == nil || *got != pid {
		t.Fatalf("slug should win: got %v err %v", got, err)
	}
}

func TestResolve_EmptyRefReturnsNilNil(t *testing.T) {
	r, _, _ := newResolver()
	got, err := r.ResolveProjectID(context.Background(), resolve.Ref{})
	if err != nil || got != nil {
		t.Fatalf("empty ref: got %v err %v, want nil,nil", got, err)
	}
}

func TestResolve_IncompleteRef(t *testing.T) {
	r, _, _ := newResolver()
	_, err := r.ResolveProjectID(context.Background(), resolve.Ref{WorkspaceSlug: "team"}) // project_name 없음
	if !errors.Is(err, resolve.ErrIncompleteRef) {
		t.Fatalf("err = %v, want ErrIncompleteRef", err)
	}
}

func TestResolve_WorkspaceNotFound(t *testing.T) {
	r, _, _ := newResolver()
	_, err := r.ResolveProjectID(context.Background(), resolve.Ref{WorkspaceName: "Nope", ProjectName: "Alpha"})
	if !errors.Is(err, resolve.ErrWorkspaceNotFound) {
		t.Fatalf("err = %v, want ErrWorkspaceNotFound", err)
	}
}

func TestResolve_ProjectNotFound(t *testing.T) {
	r, _, _ := newResolver()
	_, err := r.ResolveProjectID(context.Background(), resolve.Ref{WorkspaceSlug: "team", ProjectName: "Ghost"})
	if !errors.Is(err, resolve.ErrProjectNotFound) {
		t.Fatalf("err = %v, want ErrProjectNotFound", err)
	}
}
```

- [ ] **Step 2: 테스트 실패(컴파일 에러) 확인**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go test ./internal/resolve/ 2>&1 | head`
Expected: FAIL — `package resolve` 없음 / 미정의 심볼 컴파일 에러

- [ ] **Step 3: resolve 패키지 구현**

`internal/resolve/resolve.go`:

```go
// Package resolve turns human-friendly workspace/project identifiers into a project UUID.
package resolve

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/project"
	"github.com/tojiuni/goquest/internal/workspace"
)

var (
	// ErrIncompleteRef: 워크스페이스 식별자만 있고 project_name이 없거나 그 반대.
	ErrIncompleteRef     = errors.New("resolve: workspace identifier and project_name must be provided together")
	ErrWorkspaceNotFound = errors.New("resolve: workspace not found")
	ErrProjectNotFound   = errors.New("resolve: project not found")
)

// Ref identifies a project by names instead of UUID.
type Ref struct {
	WorkspaceSlug string
	WorkspaceName string
	ProjectName   string
}

func (r Ref) hasWorkspace() bool {
	return strings.TrimSpace(r.WorkspaceSlug) != "" || strings.TrimSpace(r.WorkspaceName) != ""
}

// Empty reports whether the ref carries no identifying information at all.
func (r Ref) Empty() bool {
	return !r.hasWorkspace() && strings.TrimSpace(r.ProjectName) == ""
}

type workspaceLookup interface {
	GetBySlug(ctx context.Context, slug string) (*workspace.Workspace, error)
	GetByName(ctx context.Context, name string) (*workspace.Workspace, error)
}

type projectLookup interface {
	GetByWorkspaceAndName(ctx context.Context, workspaceID uuid.UUID, name string) (*project.Project, error)
}

type Resolver struct {
	ws workspaceLookup
	pj projectLookup
}

func New(ws workspaceLookup, pj projectLookup) *Resolver {
	return &Resolver{ws: ws, pj: pj}
}

// ResolveProjectID resolves ref to a project UUID.
//   - Empty ref → (nil, nil): caller leaves project unset.
//   - Workspace id present XOR project_name → ErrIncompleteRef.
//   - Workspace: slug preferred when both slug and name are set.
func (r *Resolver) ResolveProjectID(ctx context.Context, ref Ref) (*uuid.UUID, error) {
	if ref.Empty() {
		return nil, nil
	}
	if ref.hasWorkspace() == (strings.TrimSpace(ref.ProjectName) == "") {
		// 하나만 채워진 경우 (둘 다 채워졌으면 둘 다 != 빈, 둘 다 비면 Empty에서 걸러짐)
		return nil, ErrIncompleteRef
	}

	var (
		ws  *workspace.Workspace
		err error
	)
	if strings.TrimSpace(ref.WorkspaceSlug) != "" {
		ws, err = r.ws.GetBySlug(ctx, ref.WorkspaceSlug)
	} else {
		ws, err = r.ws.GetByName(ctx, ref.WorkspaceName)
	}
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			return nil, ErrWorkspaceNotFound
		}
		return nil, err
	}

	p, err := r.pj.GetByWorkspaceAndName(ctx, ws.ID, ref.ProjectName)
	if err != nil {
		if errors.Is(err, project.ErrNotFound) {
			return nil, ErrProjectNotFound
		}
		return nil, err
	}
	return &p.ID, nil
}
```

- [ ] **Step 4: 테스트 통과 확인**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go test ./internal/resolve/ -v`
Expected: PASS (8개 테스트)

- [ ] **Step 5: Commit**

```bash
cd /Users/dong-hoshin/Documents/dev/goquest
git add internal/resolve/
git commit -m "feat(resolve): ProjectResolver for workspace/project name resolution"
```

---

## Task 6: 티켓 API 필드 + 핸들러 해석 분기 + 주입

**Files:**
- Modify: `internal/ticket/model.go` (`CreateTicketReq`)
- Modify: `internal/handler/ticket.go` (`ProjectResolver` 인터페이스, `NewTicketHandler` 시그니처, `Create` 분기)
- Modify: `cmd/service/main.go` (resolver 생성·주입)

- [ ] **Step 1: CreateTicketReq에 필드 추가**

`internal/ticket/model.go`의 `CreateTicketReq`에서 `ProjectID` 줄 아래에 추가:

```go
	WorkspaceSlug  string         `json:"workspace_slug,omitempty"`
	WorkspaceName  string         `json:"workspace_name,omitempty"`
	ProjectName    string         `json:"project_name,omitempty"`
```

- [ ] **Step 2: 핸들러에 resolver 인터페이스 + 주입 + 분기**

`internal/handler/ticket.go` 상단(타입 선언부)에 추가:

```go
// ProjectResolver resolves a name-based project reference to a project UUID.
type ProjectResolver interface {
	ResolveProjectID(ctx context.Context, ref resolve.Ref) (*uuid.UUID, error)
}
```

import에 `"github.com/tojiuni/goquest/internal/resolve"`를 추가.

`TicketHandler` 구조체에 `resolver ProjectResolver` 필드를 추가하고, `NewTicketHandler` 시그니처를 다음으로 변경:

```go
func NewTicketHandler(svc *ticket.Service, checker spicedb.PermissionChecker, writer spicedb.RelationshipWriter, resolver ProjectResolver) *TicketHandler {
	return &TicketHandler{svc: svc, checker: checker, writer: writer, resolver: resolver}
}
```

`Create` 메서드에서 body decode 직후, `requesterID := auth.RequesterID(r)` **이전**에 해석 분기를 삽입:

```go
	if req.ProjectID == nil {
		ref := resolve.Ref{
			WorkspaceSlug: req.WorkspaceSlug,
			WorkspaceName: req.WorkspaceName,
			ProjectName:   req.ProjectName,
		}
		if !ref.Empty() {
			pid, rerr := h.resolver.ResolveProjectID(r.Context(), ref)
			if rerr != nil {
				writeError(w, http.StatusBadRequest, rerr.Error())
				return
			}
			req.ProjectID = pid
		}
	}
```

- [ ] **Step 3: main.go에서 resolver 생성·주입**

`cmd/service/main.go`에서 import에 `"github.com/tojiuni/goquest/internal/resolve"`를 추가하고, `pjStore`/`wsStore`가 이미 만들어진 지점(라인 91~95 부근) 이후에:

```go
	projectResolver := resolve.New(wsStore, pjStore)
```

그리고 `ticketH := handler.NewTicketHandler(svc, sdbChecker, sdbWriter)` 호출을 다음으로 변경:

```go
	ticketH := handler.NewTicketHandler(svc, sdbChecker, sdbWriter, projectResolver)
```

(`wsStore`는 `workspace.Store`, `pjStore`는 `project.Store` 구체 타입으로, resolve의 narrow 인터페이스를 만족한다.)

- [ ] **Step 4: 빌드 + 전체 핸들러/티켓 테스트 확인**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go build ./... && go vet ./... && go test ./internal/handler/ ./internal/ticket/`
Expected: PASS (빌드·vet 성공)

- [ ] **Step 5: Commit**

```bash
cd /Users/dong-hoshin/Documents/dev/goquest
git add internal/ticket/model.go internal/handler/ticket.go cmd/service/main.go
git commit -m "feat(ticket): create by workspace_slug|name + project_name"
```

---

## Task 7: 전체 검증 + 마무리

- [ ] **Step 1: 전체 빌드·vet·테스트**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go build ./... && go vet ./... && go test ./...`
Expected: 모두 PASS

- [ ] **Step 2: 마이그레이션 사전 점검 안내 확인**

`internal/migrate/sql/012_workspace_name_unique.sql` 상단 주석에 사전 점검 쿼리가 포함됐는지 확인. (배포 시 클러스터 DB에서 `SELECT LOWER(name), COUNT(*) FROM workspaces GROUP BY 1 HAVING COUNT(*)>1;` 실행 후 0행이어야 함.)

- [ ] **Step 3: 잔여 커밋 정리 + 푸시**

```bash
cd /Users/dong-hoshin/Documents/dev/goquest
git status
git push -u origin feat/ticket-name-resolution
```

---

## Self-Review 결과

- **Spec coverage:** §1 티켓 해석=Task1/2/5/6, §2 name 유일성=Task4, §3 slug 강화=Task3, 테스트=각 Task, 배포순서=Task7. 모두 매핑됨.
- **인터페이스 변경 영향:** project/workspace Store에 메서드 추가 → 구현체는 pgStore + 각 service_test mockStore뿐 (auth는 narrow 인터페이스라 무관). Task1/2에서 함께 추가.
- **타입 일관성:** `resolve.Ref`/`resolve.New`/`ResolveProjectID`/`ErrWorkspaceNotFound`/`ErrProjectNotFound`/`ErrIncompleteRef`, `workspace.ErrDuplicateName`, `project.GetByWorkspaceAndName`, `workspace.GetByName` — Task 간 명칭 일치 확인.
- **비ASCII:** `normalizeSlug`가 빈 문자열 반환 시 `uniqueSlug`가 `ws-<id8>` fallback (Task3 테스트로 검증).
