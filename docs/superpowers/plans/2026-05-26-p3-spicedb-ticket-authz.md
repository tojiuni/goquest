# P3: goquest SpiceDB 티켓 가시성 강제 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** goquest를 공유 SpiceDB(`goquest/*` 타입)에 통합해 티켓 가시성(`CanViewTicket`)을 실제 강제하되, `SPICEDB_ENFORCE` 분리로 백필 전 거부를 방지하는 안전 롤아웃을 갖춘다.

**Architecture:** spicedb writer/checker가 `goquest/` prefixed 타입을 쓰도록 정합. `RelationshipWriter`를 workspace/project Service에 주입해 멤버·구조 관계를 dual-write. 백필 command로 기존 데이터를 채우고, connect 시 writer는 항상 실제·checker는 enforce 플래그로 분리해 shadow→백필→강제 순으로 롤아웃.

**Tech Stack:** Go, authzed-go(SpiceDB v1 API), 표준 testing. 배포 env는 neunexus deploy.

**Spec:** `docs/superpowers/specs/2026-05-26-p3-spicedb-ticket-authz-design.md`

**브랜치:** goquest `feat/p3-spicedb-ticket-authz` (생성됨). 코드 작업 — worktree 권장.

스키마 relation(참조): `goquest/workspace`{owner,admin,member,viewer}, `goquest/project`{workspace,admin,member,viewer}, `goquest/ticket`{project,requester,assignee,restricted_viewer}. subject `user`는 unprefixed 공유.

---

## File Structure
- `internal/spicedb/types.go` (신규) — object type 상수.
- `internal/spicedb/writer.go` — 타입 literal → 상수(prefixed); `DeleteProjectMember` 추가.
- `internal/spicedb/checker.go` — 타입 literal → 상수(prefixed).
- `internal/spicedb/interfaces.go` — `RelationshipWriter`에 `DeleteProjectMember` 추가.
- `internal/spicedb/noop.go` — `DeleteProjectMember` no-op.
- `internal/workspace/service.go` — `NewService(store, writer)`, 멤버 변경 시 dual-write.
- `internal/project/service.go` — `NewService(store, writer)`, Create/멤버 변경 시 dual-write.
- `internal/config/config.go` — `SpiceDBEnforce`.
- `cmd/service/main.go` — writer 주입(ws/pj Service), checker enforce 분리.
- `cmd/backfill-spicedb/main.go` (신규) — 백필 command.
- 테스트: 각 패키지 `*_test.go`.
- neunexus: `deploy/bots/.../goquest` deployment env(별도 Task).

---

## Task 1: object type prefix 정합 + DeleteProjectMember

**Files:** `internal/spicedb/types.go`(신규), `writer.go`, `checker.go`, `interfaces.go`, `noop.go`, `internal/spicedb/writer_test.go`(신규)

- [ ] **Step 1: 실패 테스트 작성**

`internal/spicedb/writer_test.go`:
```go
package spicedb

import (
	"testing"

	"github.com/google/uuid"
)

func TestObjectTypesArePrefixed(t *testing.T) {
	if TypeWorkspace != "goquest/workspace" || TypeProject != "goquest/project" || TypeTicket != "goquest/ticket" {
		t.Fatalf("types not prefixed: %s %s %s", TypeWorkspace, TypeProject, TypeTicket)
	}
	if TypeUser != "user" {
		t.Fatalf("user type must stay unprefixed, got %s", TypeUser)
	}
}

func TestTouchUsesPrefixedTypes(t *testing.T) {
	u := touch(TypeWorkspace, uuid.New().String(), "owner", TypeUser, "u1", "")
	if got := u.Relationship.Resource.ObjectType; got != "goquest/workspace" {
		t.Errorf("resource type = %s, want goquest/workspace", got)
	}
	if got := u.Relationship.Subject.Object.ObjectType; got != "user" {
		t.Errorf("subject type = %s, want user", got)
	}
}
```

- [ ] **Step 2: 테스트 실패(컴파일) 확인**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go test ./internal/spicedb/ 2>&1 | head`
Expected: FAIL — `TypeWorkspace` 등 미정의.

- [ ] **Step 3: 타입 상수 신설**

`internal/spicedb/types.go`:
```go
package spicedb

// SpiceDB object types. goquest resources are namespaced with the goquest/ prefix
// to coexist with other services' definitions in the shared platform schema.
// The subject type `user` is shared (unprefixed).
const (
	TypeWorkspace = "goquest/workspace"
	TypeProject   = "goquest/project"
	TypeTicket    = "goquest/ticket"
	TypeUser      = "user"
)
```

- [ ] **Step 4: writer.go의 literal을 상수로 교체 + DeleteProjectMember 추가**

`internal/spicedb/writer.go`에서 모든 bare type literal 교체:
- `touch("ticket", ...)` → `touch(TypeTicket, ...)`, ticket의 project subType `"project"` → `TypeProject`, requester/assignee/restricted subType `"user"` → `TypeUser`.
- `WriteWorkspaceMember`/`DeleteWorkspaceMember`: `"workspace"` → `TypeWorkspace`, `"user"` → `TypeUser`.
- `WriteProjectMember`: `"project"` → `TypeProject`, `"user"` → `TypeUser`.
- `WriteProjectWorkspace`: res `"project"` → `TypeProject`, subType `"workspace"` → `TypeWorkspace`.
- `AddTicketViewer`: `"ticket"` → `TypeTicket`, `"user"` → `TypeUser`.

그리고 `DeleteProjectMember` 추가(WriteProjectMember 아래):
```go
// DeleteProjectMember removes all direct role relations for a user in a project.
func (w *Writer) DeleteProjectMember(ctx context.Context, projectID uuid.UUID, userID string) error {
	for _, role := range []string{"admin", "member", "viewer"} {
		if err := w.delete(ctx, TypeProject, projectID.String(), role, TypeUser, userID, ""); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 5: checker.go literal 교체**

`internal/spicedb/checker.go`의 `check(...)` 호출에서 `"ticket"` → `TypeTicket`, `"project"` → `TypeProject`, subType `"user"` → `TypeUser`.

- [ ] **Step 6: interfaces.go + noop.go에 DeleteProjectMember**

`interfaces.go`의 `RelationshipWriter`에 추가:
```go
	DeleteProjectMember(ctx context.Context, projectID uuid.UUID, userID string) error
```
`noop.go`의 NoopWriter에 추가:
```go
func (n *NoopWriter) DeleteProjectMember(_ context.Context, _ uuid.UUID, _ string) error { return nil }
```

- [ ] **Step 7: 테스트 통과 + 빌드**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go build ./... && go test ./internal/spicedb/ -v`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
cd /Users/dong-hoshin/Documents/dev/goquest
git add internal/spicedb/
git commit -m "feat(spicedb): goquest/ prefixed object types + DeleteProjectMember"
```

---

## Task 2: workspace.Service dual-write

**Files:** `internal/workspace/service.go`, `cmd/service/main.go`, `internal/workspace/service_test.go`

- [ ] **Step 1: 실패 테스트 작성**

`internal/workspace/service_test.go`에 추가(기존 mockStore 활용). 먼저 파일 상단에 mock writer 추가:
```go
type mockWriter struct {
	wsWrites  []string // "wsID|userID|role"
	wsDeletes []string // "wsID|userID"
}

func (m *mockWriter) WriteTicket(_ context.Context, _, _ uuid.UUID, _, _ string) error { return nil }
func (m *mockWriter) WriteWorkspaceMember(_ context.Context, wid uuid.UUID, uid, role string) error {
	m.wsWrites = append(m.wsWrites, wid.String()+"|"+uid+"|"+role)
	return nil
}
func (m *mockWriter) DeleteWorkspaceMember(_ context.Context, wid uuid.UUID, uid string) error {
	m.wsDeletes = append(m.wsDeletes, wid.String()+"|"+uid)
	return nil
}
func (m *mockWriter) WriteProjectMember(_ context.Context, _ uuid.UUID, _, _ string) error  { return nil }
func (m *mockWriter) DeleteProjectMember(_ context.Context, _ uuid.UUID, _ string) error     { return nil }
func (m *mockWriter) WriteProjectWorkspace(_ context.Context, _, _ uuid.UUID) error          { return nil }
func (m *mockWriter) AddTicketViewer(_ context.Context, _ uuid.UUID, _ string) error          { return nil }
```
그리고 테스트:
```go
func TestCreate_DualWritesOwnerToSpiceDB(t *testing.T) {
	mw := &mockWriter{}
	svc := workspace.NewService(newMockStore(), mw)
	ws, err := svc.Create(context.Background(), "owner-1", workspace.CreateReq{Name: "WS"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := ws.ID.String() + "|owner-1|owner"
	if len(mw.wsWrites) != 1 || mw.wsWrites[0] != want {
		t.Errorf("wsWrites = %v, want [%s]", mw.wsWrites, want)
	}
}

func TestRemoveMember_DualDeletesFromSpiceDB(t *testing.T) {
	mw := &mockWriter{}
	store := newMockStore()
	svc := workspace.NewService(store, mw)
	wid := uuid.New()
	_ = svc // setup membership via store as the existing tests do, then:
	err := svc.RemoveMember(context.Background(), wid, "u2", workspace.RoleOwner)
	if err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	want := wid.String() + "|u2"
	if len(mw.wsDeletes) != 1 || mw.wsDeletes[0] != want {
		t.Errorf("wsDeletes = %v, want [%s]", mw.wsDeletes, want)
	}
}
```
(import에 `context`, `github.com/google/uuid` 확인. `import "github.com/tojiuni/goquest/internal/spicedb"`는 불필요 — mockWriter가 인터페이스를 구조적으로 만족.)

- [ ] **Step 2: 테스트 실패 확인**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go test ./internal/workspace/ -run 'DualWrite|DualDelete' 2>&1 | head`
Expected: FAIL — `NewService`가 1인자(writer 미수용).

- [ ] **Step 3: Service에 writer 주입 + dual-write**

`internal/workspace/service.go`:
- import에 `"github.com/tojiuni/goquest/internal/spicedb"` 추가.
- 구조체/생성자 변경:
```go
type Service struct {
	store  Store
	writer spicedb.RelationshipWriter
}

func NewService(store Store, writer spicedb.RelationshipWriter) *Service {
	return &Service{store: store, writer: writer}
}
```
- `create()`에서 오너 멤버 `store.AddMember` 성공 직후:
```go
	if err := svc.writer.WriteWorkspaceMember(ctx, ws.ID, ownerID, string(RoleOwner)); err != nil {
		log.Printf("spicedb dual-write workspace owner %s: %v (continuing)", ws.ID, err)
	}
```
(import `"log"` 추가)
- `SetMemberRole`에서 `store.SetMemberRole` 성공 후: 기존 역할 정리 + 새 역할 write:
```go
	_ = svc.writer.DeleteWorkspaceMember(ctx, workspaceID, targetUserID)
	if err := svc.writer.WriteWorkspaceMember(ctx, workspaceID, targetUserID, string(req.Role)); err != nil {
		log.Printf("spicedb dual-write ws role %s/%s: %v (continuing)", workspaceID, targetUserID, err)
	}
```
- `RemoveMember`에서 `store.RemoveMember` 성공 후:
```go
	if err := svc.writer.DeleteWorkspaceMember(ctx, workspaceID, targetUserID); err != nil {
		log.Printf("spicedb dual-delete ws member %s/%s: %v (continuing)", workspaceID, targetUserID, err)
	}
```
- 멤버를 추가하는 다른 경로(초대 수락 등 `store.AddMember` 호출부, line ~206)에도 동일하게 `WriteWorkspaceMember(ctx, wsID, userID, string(role))` 추가.

- [ ] **Step 4: NewService 호출부 수정 (컴파일)**

`grep -rn "workspace.NewService(" --include=*.go .` 로 모든 호출부 확인. `cmd/service/main.go`의 `wsSvc := workspace.NewService(wsStore)` → `workspace.NewService(wsStore, sdbWriter)`. 다른 호출부(테스트 제외)도 동일 수정.

- [ ] **Step 5: 테스트 통과 + 빌드**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go build ./... && go test ./internal/workspace/`
Expected: PASS (기존 테스트의 `NewService(...)` 호출도 mock writer 인자 추가 필요 — `&mockWriter{}` 또는 spicedb.NoopWriter 전달하도록 수정).

- [ ] **Step 6: Commit**

```bash
cd /Users/dong-hoshin/Documents/dev/goquest
git add internal/workspace/ cmd/service/main.go
git commit -m "feat(workspace): dual-write membership to SpiceDB"
```

---

## Task 3: project.Service dual-write

**Files:** `internal/project/service.go`, `cmd/service/main.go`, `internal/project/service_test.go`

- [ ] **Step 1: 실패 테스트 작성**

`internal/project/service_test.go`에 mock writer(Task 2와 동형: `WriteProjectWorkspace`/`WriteProjectMember`/`DeleteProjectMember` 기록) 추가 후:
```go
func TestCreate_DualWritesProjectWorkspaceAndCreator(t *testing.T) {
	mw := &mockWriter{}
	svc := project.NewService(newMockStore(), mw)
	wid := uuid.New()
	p, err := svc.Create(context.Background(), wid, "creator-1", project.CreateReq{Name: "P"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(mw.pwLinks) != 1 || mw.pwLinks[0] != p.ID.String()+"|"+wid.String() {
		t.Errorf("project->workspace link = %v", mw.pwLinks)
	}
	if len(mw.pjWrites) != 1 || mw.pjWrites[0] != p.ID.String()+"|creator-1|admin" {
		t.Errorf("creator member = %v", mw.pjWrites)
	}
}
```
(mock writer는 `pwLinks []string`(projectID|wsID), `pjWrites []string`(projectID|userID|role), `pjDeletes []string` 필드 + 해당 메서드 기록 구현.)

- [ ] **Step 2: 테스트 실패 확인**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go test ./internal/project/ -run DualWrite 2>&1 | head`
Expected: FAIL — `NewService` 1인자.

- [ ] **Step 3: Service에 writer 주입 + dual-write**

`internal/project/service.go`:
- import `"log"`, `"github.com/tojiuni/goquest/internal/spicedb"`.
- 구조체/생성자:
```go
type Service struct {
	store  Store
	writer spicedb.RelationshipWriter
}

func NewService(store Store, writer spicedb.RelationshipWriter) *Service {
	return &Service{store: store, writer: writer}
}
```
- `Create()`에서 생성자 멤버 `store.AddMember` 성공 직후:
```go
	if err := svc.writer.WriteProjectWorkspace(ctx, p.ID, workspaceID); err != nil {
		log.Printf("spicedb dual-write project->workspace %s: %v (continuing)", p.ID, err)
	}
	if err := svc.writer.WriteProjectMember(ctx, p.ID, creatorID, string(RoleAdmin)); err != nil {
		log.Printf("spicedb dual-write project admin %s: %v (continuing)", p.ID, err)
	}
```
- `AddMember`: `store.AddMember` 성공 후 `WriteProjectMember(ctx, projectID, targetUserID, string(req.Role))` (+에러 로깅).
- `SetMemberRole`: 성공 후 `DeleteProjectMember(ctx, projectID, targetUserID)` + `WriteProjectMember(ctx, projectID, targetUserID, string(req.Role))`.
- `RemoveMember`: 성공 후 `DeleteProjectMember(ctx, projectID, targetUserID)` (+에러 로깅).

- [ ] **Step 4: NewService 호출부 수정**

`cmd/service/main.go`의 `pjSvc := project.NewService(pjStore)` → `project.NewService(pjStore, sdbWriter)`. 기타 호출부 + 기존 테스트의 `NewService` 호출에 NoopWriter/mock 추가.

- [ ] **Step 5: 테스트 통과 + 빌드**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go build ./... && go test ./internal/project/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cd /Users/dong-hoshin/Documents/dev/goquest
git add internal/project/ cmd/service/main.go
git commit -m "feat(project): dual-write membership + project->workspace to SpiceDB"
```

---

## Task 4: config SPICEDB_ENFORCE + main.go checker 분리

**Files:** `internal/config/config.go`, `cmd/service/main.go`, `internal/config/config_test.go`(있으면)

- [ ] **Step 1: config에 SpiceDBEnforce 추가**

`internal/config/config.go`:
- 구조체에 `SpiceDBEnforce bool` (SpiceDBInsecure 옆).
- 로드부에 `SpiceDBEnforce: parseBool(os.Getenv("SPICEDB_ENFORCE"), false),`.

- [ ] **Step 2: main.go에서 writer는 항상 실제, checker는 enforce 분리**

`cmd/service/main.go`의 SpiceDB 연결 블록(`if cfg.SpiceDBEndpoint != "" { ... }`)에서 연결 성공(`else`) 부분을 교체:
```go
		} else {
			sdbWriter = sdbClient.Writer() // dual-write는 항상 활성
			if cfg.SpiceDBEnforce {
				sdbChecker = sdbClient.Checker()
				log.Printf("spicedb connected + ENFORCING: %s", cfg.SpiceDBEndpoint)
			} else {
				log.Printf("spicedb connected (shadow, checks permissive): %s", cfg.SpiceDBEndpoint)
			}
		}
```
(`sdbChecker`는 enforce=false면 기본 `NoopChecker` 유지 = 허용.)

- [ ] **Step 3: 빌드 + vet**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go build ./... && go vet ./...`
Expected: 통과.

- [ ] **Step 4: Commit**

```bash
cd /Users/dong-hoshin/Documents/dev/goquest
git add internal/config/config.go cmd/service/main.go
git commit -m "feat(spicedb): SPICEDB_ENFORCE flag — writer always real, checker gated"
```

---

## Task 5: 백필 command

**Files:** `cmd/backfill-spicedb/main.go`(신규)

- [ ] **Step 1: 백필 command 작성**

`cmd/backfill-spicedb/main.go`:
```go
// Command backfill-spicedb populates the shared SpiceDB with goquest relationships
// from the current database. Idempotent (TOUCH). Run as a one-off Job before enabling
// SPICEDB_ENFORCE.
package main

import (
	"context"
	"log"

	"github.com/tojiuni/goquest/internal/config"
	"github.com/tojiuni/goquest/internal/project"
	"github.com/tojiuni/goquest/internal/spicedb"
	"github.com/tojiuni/goquest/internal/ticket"
	"github.com/tojiuni/goquest/internal/workspace"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	ctx := context.Background()
	cfg := config.Load()
	if cfg.SpiceDBEndpoint == "" {
		log.Fatal("SPICEDB_ENDPOINT required")
	}
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL())
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()
	client, err := spicedb.NewClient(cfg.SpiceDBEndpoint, cfg.SpiceDBToken, cfg.SpiceDBInsecure)
	if err != nil {
		log.Fatalf("spicedb: %v", err)
	}
	w := client.Writer()

	wsStore := workspace.NewPGStore(pool)
	pjStore := project.NewPGStore(pool)
	tkStore := ticket.NewPGStore(pool)

	ws := 0
	for _, workspaceRow := range mustListWorkspaces(ctx, wsStore) {
		for _, m := range mustListWSMembers(ctx, wsStore, workspaceRow.ID) {
			if err := w.WriteWorkspaceMember(ctx, workspaceRow.ID, m.UserID, string(m.Role)); err != nil {
				log.Printf("ws member %s/%s: %v", workspaceRow.ID, m.UserID, err)
			}
		}
		for _, p := range mustListProjects(ctx, pjStore, workspaceRow.ID) {
			_ = w.WriteProjectWorkspace(ctx, p.ID, workspaceRow.ID)
			for _, pm := range mustListPJMembers(ctx, pjStore, p.ID) {
				_ = w.WriteProjectMember(ctx, p.ID, pm.UserID, string(pm.Role))
			}
		}
		ws++
	}
	for _, t := range mustListTickets(ctx, tkStore) {
		if t.ProjectID != nil {
			_ = w.WriteTicket(ctx, t.ID, *t.ProjectID, t.RequesterID, t.AssigneeID)
		}
	}
	log.Printf("backfill complete: %d workspaces processed", ws)
}
```
**주의:** 위 `mustList*` 헬퍼와 store의 "전체 나열" 메서드는 실제 store 인터페이스에 맞춰 구현해야 한다. **Step 1.5**에서 store에 백필용 나열 메서드가 있는지 확인하고(없으면 `ListAll*` 추가 또는 pgx로 직접 SELECT), 헬퍼를 실제 시그니처로 작성한다. 이 command는 *읽기 전용 store + writer*만 쓰므로 SpiceDB 외 부작용 없음.

- [ ] **Step 1.5: store 나열 메서드 확인/보강**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && grep -rn "func.*ListAll\|func.*List(" internal/workspace/store.go internal/project/store.go internal/ticket/store.go`
없으면: 백필 전용으로 각 store에 `ListAll(ctx) ([]*T, error)`(워크스페이스/티켓 전체), `ListMembers`(이미 있을 수 있음), `ListByWorkspace`(프로젝트, 존재) 사용. 부재한 나열은 pgxpool로 직접 `SELECT id ...` 쿼리로 백필 command 내에서 처리(읽기 전용).

- [ ] **Step 2: 빌드**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go build ./cmd/backfill-spicedb/`
Expected: 빌드 성공.

- [ ] **Step 3: Commit**

```bash
cd /Users/dong-hoshin/Documents/dev/goquest
git add cmd/backfill-spicedb/ internal/
git commit -m "feat(backfill): SpiceDB relationship backfill command"
```

---

## Task 6: 전체 검증 + 배포 config (neunexus)

- [ ] **Step 1: goquest 전체 빌드·vet·테스트**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go build ./... && go vet ./... && go test ./...`
Expected: 모두 PASS.

- [ ] **Step 2: 푸시 + goquest PR**

```bash
cd /Users/dong-hoshin/Documents/dev/goquest && git push -u origin feat/p3-spicedb-ticket-authz
gh pr create --base main --title "feat(authz): P3 goquest SpiceDB ticket-visibility (shadow-ready)" --body "prefix 정합(goquest/*), 멤버/구조 dual-write, 백필 command, SPICEDB_ENFORCE 분리. 배포는 shadow(enforce=false)→백필→enforce=true 순서."
```

- [ ] **Step 3: neunexus 배포 config (별도 브랜치/PR)**

neunexus repo의 goquest-service deployment env에 추가(VSO/Vault로 토큰):
```yaml
            - name: SPICEDB_ENDPOINT
              value: "spicedb.lymphhub.svc:50051"
            - name: SPICEDB_INSECURE
              value: "true"
            - name: SPICEDB_ENFORCE
              value: "false"   # 1단계 shadow; 백필 후 true
            - name: SPICEDB_TOKEN
              valueFrom: { secretKeyRef: { name: <goquest-secret>, key: spicedb_token } }
```
`SPICEDB_TOKEN`은 Vault `secret/neunexus/spicedb#preshared-key`를 goquest secret/VSO에 추가. deployment 파일 위치는 `deploy/...goquest...`에서 확인.

---

## Task 7: 롤아웃 (배포 후, 수동·검증)

- [ ] **Step 1: shadow 배포 검증** — goquest 재배포 후 로그에 `spicedb connected (shadow...)`. 기능 회귀 없음(체크 허용).
- [ ] **Step 2: 백필 실행** — `cmd/backfill-spicedb`를 일회성 Job(goquest 이미지)으로 실행. 로그 `backfill complete`.
- [ ] **Step 3: 표본 검증** — `zed relationship read goquest/workspace|goquest/project|goquest/ticket`(port-forward + 토큰)로 tuple 존재 확인.
- [ ] **Step 4: enforce 전환** — deployment `SPICEDB_ENFORCE=true` → 재배포, 로그 `ENFORCING`. requester/assignee/멤버 view 가능, 비멤버 거부 확인.

---

## Self-Review 결과
- **Spec coverage:** (a)prefix=Task1, (b)dual-write=Task2/3, (c)백필=Task5, (d)enforce+config=Task4, 배포=Task6, 롤아웃=Task7. 전부 매핑.
- **Placeholder scan:** Task5의 `mustList*`는 Step 1.5에서 실제 store 시그니처로 확정(읽기 전용 나열) — 구현 시 확인 필요로 명시. 그 외 placeholder 없음.
- **타입 일관성:** `TypeWorkspace/Project/Ticket/User`, `DeleteProjectMember`, `NewService(store, writer)`(ws/pj 공통), `SpiceDBEnforce` 명칭이 Task 간 일치. role은 `string(member.Role)`로 통일.
- **주의:** dual-write는 best-effort(로깅, DB가 source) — 드리프트는 백필 재실행으로 보정. enforce=false 기본이라 배포 즉시 거부 없음(안전).
