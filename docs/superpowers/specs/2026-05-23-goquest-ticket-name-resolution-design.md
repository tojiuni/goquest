# goquest: 이름/slug 기반 티켓 생성 + 워크스페이스 slug 핸들 강화

- 날짜: 2026-05-23
- 대상 repo: goquest
- 관련: PR #25 (재설계 — 기존 구현은 참고만, main 기준 새로 구현)

## 배경 / 동기

티켓 생성 시 `project_id`(UUID)를 알아야 한다. 그러나 실제 호출자(bot/LLM, CLI/curl, 외부 클라이언트)는 UUID가 아니라 사람이 쓰는 이름(워크스페이스명·프로젝트명)을 안다. UUID 조회 단계 없이 이름만으로 티켓을 만들 수 있게 한다.

이 과정에서 워크스페이스 식별의 정확성 문제와 slug의 미완성 상태가 드러나, 함께 정리한다.

### 현 상태에서 확인한 사실

- `workspaces.name`은 **UNIQUE가 아니다** (UNIQUE는 `slug`만, `002_workspace.sql`).
- `projects`는 `UNIQUE (workspace_id, name)` — 워크스페이스가 정해지면 프로젝트명은 유일.
- `slug = toSlug(name)`은 생성 시 1회 파생되며 **끝에 랜덤 hex suffix**(`randomHex(3)` → 6자)를 붙인다 (예: `my-workspace-a3f9c1`). 즉 slug는 name의 결정적 함수가 **아니다.** → 같은 이름의 워크스페이스도 slug가 서로 달라 **현재 중복 생성이 가능하다** (workspace name은 사실상 유일하지 않음). 또 랜덤 suffix는 사람이 읽기 어려운 핸들을 만든다.
- `toSlug`는 소문자화 후 `[a-z0-9-]` 외 문자를 `-`로 치환하고 `Trim("-")` → **한글 등 비ASCII 이름은 정규화 결과가 빈 문자열**이 되어 slug가 `-a3f9c1`(앞 `-`만 남은 degenerate) 형태가 된다 (한국어 컨텍스트에서 실질적 URL-safe 구멍).
- `workspace.Update`는 `name`/`description`만 갱신하고 **slug는 건드리지 않는다** → rename에도 slug 불변(안정 핸들 속성이 우연히 존재).
- `GetBySlug`는 store 인터페이스에 정의돼 있으나 핸들러/라우트 호출자가 없다 (응답 JSON의 `slug` 필드로만 노출).
- 티켓 `Create` 핸들러는 insert 전 프로젝트 단위 authz를 하지 않는다 (SpiceDB 관계는 생성 후 async best-effort). → 이름 해석을 추가해도 신규 authz 구멍은 없다.

## 목표

1. `project_id` 없이 워크스페이스 식별자(`workspace_slug` 또는 `workspace_name`) + `project_name`으로 티켓 생성.
2. 워크스페이스 이름 해석을 **설계상 정확**하게 — `UNIQUE(LOWER(name))`로 승격.
3. slug를 의도대로 **URL-safe + 유일 + 안정(rename 불변) 핸들**로 완성.

## 비목표 (범위 밖)

- slug 컬럼 제거 (API 계약 변경 — 별도 작업).
- slug 기반 조회 라우트 신설(`GET /workspaces/{slug}` 등).
- 티켓 생성 경로에 신규 권한 검사 도입 (기존 동작과 동일 유지).
- 프로젝트에 slug 도입 (프로젝트는 `(workspace_id, name)` 유일로 충분).

## 설계

### 1. 티켓 생성 API — 이름/slug 해석

`CreateTicketReq` (internal/ticket/model.go)에 옵션 필드 추가:

```go
ProjectID     *uuid.UUID // 기존, 최우선
WorkspaceSlug string     // 신규
WorkspaceName string     // 신규
ProjectName   string     // 신규
```

**해석 우선순위 (핸들러 Create):**

1. `ProjectID != nil` → 그대로 사용. (이름 필드 무시)
2. `ProjectID == nil`이고 워크스페이스 식별자 + `ProjectName`이 있으면 해석:
   - 워크스페이스: `WorkspaceSlug`가 있으면 slug로, 없으면 `WorkspaceName`으로 (둘 다 오면 slug 우선)
   - 프로젝트: 해석된 `workspace_id` 내에서 `ProjectName`으로
3. 워크스페이스 식별자만 있고 `ProjectName`이 없음(또는 그 반대) → `400 Bad Request`
4. 식별자/이름이 전혀 없고 `ProjectID`도 없음 → 기존 동작(프로젝트 미지정 티켓) 그대로

**에러 의미:**

- 워크스페이스 미존재 → `400`, "workspace not found: <식별자>"
- 프로젝트 미존재 → `400`, "project not found: <name> in workspace <식별자>"

### 2. 해석 컴포넌트 (디커플)

신규 store 메서드:

- `workspace.Store.GetByName(ctx, name) (*Workspace, error)` — `WHERE LOWER(name) = LOWER($1)` (UNIQUE(LOWER(name))로 0/1행 보장). 기존 `GetBySlug` 재사용.
- `project.Store.GetByWorkspaceAndName(ctx, workspaceID uuid.UUID, name string) (*Project, error)` — `WHERE workspace_id = $1 AND LOWER(name) = LOWER($2)` (`UNIQUE(workspace_id, name)`).

핸들러는 구체 서비스 대신 작은 인터페이스에 의존(테스트 용이):

```go
// internal/handler/ticket.go
type ProjectResolver interface {
    // ProjectID를 워크스페이스 식별자(slug 우선, 없으면 name) + 프로젝트명으로 해석.
    ResolveProjectID(ctx context.Context, ref ticket.ProjectRef) (*uuid.UUID, error)
}
```

`ProjectRef{WorkspaceSlug, WorkspaceName, ProjectName}`. 구현체는 workspace store + project store를 받아 1) 워크스페이스 해석 2) 프로젝트 해석을 순차 수행하고, 단계별로 구분된 sentinel 에러(`ErrWorkspaceNotFound`, `ErrProjectNotFound`)를 반환한다. 핸들러가 sentinel로 메시지를 분기한다.

`NewTicketHandler`에 resolver 인자를 추가하고 `cmd/service/main.go`에서 주입한다.

### 3. name 유일성 (마이그레이션)

신규 마이그레이션 (`internal/migrate/sql/012_workspace_name_unique.sql` — 현재 최신 011 다음):

```sql
CREATE UNIQUE INDEX IF NOT EXISTS workspaces_lower_name_key
  ON workspaces (LOWER(name));
```

- **사전 점검(런북/수동) — 중요:** 현재 slug에 랜덤 suffix가 붙어 **중복 name이 실제로 존재할 수 있다.** 적용 전 클러스터 DB에서 `SELECT LOWER(name), COUNT(*) FROM workspaces GROUP BY 1 HAVING COUNT(*) > 1;` 으로 반드시 중복 확인하고, 있으면 rename으로 해소 후 적용. 인덱스 생성이 실패하면 마이그레이션이 명확히 중단된다(loud fail).
- `workspace.Update`: name 변경이 이 인덱스를 위반하면 → 서비스/핸들러에서 **409 Conflict**로 변환 (`ErrDuplicateName` sentinel). pg unique violation(SQLSTATE 23505) 감지.

### 4. slug 핸들 강화

위치: `internal/workspace/service.go`의 `toSlug`/`create`.

**(a) URL-safe 견고화** — `toSlug` 정규화 보강:
- 소문자화 → `[a-z0-9-]` 외 `-` 치환 (기존)
- 연속 `-` 1개로 collapse, 앞뒤 `-` trim
- 결과가 빈 문자열이면 호출부에서 fallback 처리

**(b) 빈/degenerate fallback** — `create()`에서 정규화 slug가 비면 `ws-<짧은id>` 사용 (예: 워크스페이스 UUID 앞 8자). 한글 전용 이름이 안정적·유일 핸들을 갖는다.

**(c) 충돌 suffix — 기존 랜덤 hex 대체** — 현재 `toSlug`는 끝에 `randomHex(3)`를 붙여 `my-workspace-a3f9c1` 같은 읽기 어려운 slug를 만든다. 이를 제거하고, `create()`에서 후보 slug가 이미 존재하면(`GetBySlug`로 확인) `-2`, `-3`… 을 붙여 빈 자리를 찾는 **결정적 suffix**로 대체한다 (첫 후보는 suffix 없이 깨끗하게). slug 유일성을 name 유일성과 독립적으로 보장하면서 사람이 읽기 좋게 만든다.
- 동시성: 드문 경합은 slug UNIQUE 제약이 최종 방어. 생성 시 23505 충돌이면 재시도(최대 3회) 후에도 실패하면 `ws-<id8>` fallback 핸들로.

**(d) 안정 핸들 명시화** — `Update`가 slug를 재생성하지 않음을 코드 주석 + 테스트로 못박는다. rename 후에도 slug 불변.

## 데이터 흐름 (티켓 생성)

```
POST /tickets {workspace_slug|workspace_name, project_name, ...}
  → handler.Create: decode
  → if ProjectID==nil && 식별자+project_name:
        resolver.ResolveProjectID(ref)
          → workspace: GetBySlug(slug) | GetByName(name)   // 0/1행
          → project:   GetByWorkspaceAndName(ws.ID, name)  // 0/1행
          → *uuid.UUID | ErrWorkspaceNotFound | ErrProjectNotFound
        req.ProjectID = 해석결과 (실패 시 400)
  → svc.Create(req)  // 이후 기존 흐름 동일 (SpiceDB async 등)
```

## 테스트 계획

단위 (외부 의존 없음):
- `toSlug`: 영문/한글/이모지/공백/앞뒤하이픈/연속하이픈/빈결과.
- `create` slug 충돌 suffix(`-2`), degenerate→`ws-<id>` fallback (mock store).
- rename(Update) 후 slug 불변.
- resolver 우선순위: `project_id` 우선, slug 우선(slug+name 동시), name 폴백, 짝 누락→에러, 미존재→sentinel 구분 (mock store).
- Update name 충돌 → `ErrDuplicateName`/409 (mock store가 23505 모사).

통합 (pg 테스트 하니스 존재 시):
- `GetByName`, `GetByWorkspaceAndName` 실제 쿼리.
- `UNIQUE(LOWER(name))` 위반 동작.

## 마이그레이션 / 배포 순서

1. 코드 + 마이그레이션 머지.
2. **배포 전 필수: 클러스터 DB 중복 사전 점검** (workspaces·projects 둘 다):
   - `SELECT LOWER(name), COUNT(*) FROM workspaces GROUP BY 1 HAVING COUNT(*)>1;`
   - `SELECT workspace_id, LOWER(name), COUNT(*) FROM projects GROUP BY 1,2 HAVING COUNT(*)>1;`
   중복이 있으면 rename으로 해소 후 배포.
3. 마이그레이션 적용(유니크 인덱스).
4. **배포 후 필수 확인:** `internal/migrate`의 러너는 마이그레이션 에러를 **로그만 남기고 계속 진행**한다(버전 테이블 없음). 따라서 중복 데이터로 인덱스 생성이 실패해도 부팅은 성공하고 유일성 불변식이 *조용히* 미적용된다. 배포 후 반드시 인덱스 존재를 확인한다:
   - `\d workspaces` → `workspaces_lower_name_key` 있는지
   - `\d projects` → `projects_workspace_lower_name_key` 있는지
   없으면 2번 중복 해소 후 재적용.
5. 신규 API 필드는 옵션 → 하위호환(기존 `project_id` 호출 무영향).

## 미해결/추후

- slug 조회 라우트·slug 제거는 별도 작업.
- 동시 생성 시 slug suffix 경합의 재시도 횟수는 구현 시 소수(예: 3회)로 고정, 초과 시 fallback 핸들.
