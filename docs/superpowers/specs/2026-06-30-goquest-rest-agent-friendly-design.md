# goquest: REST 에이전트 친화 보강 (Phase 1)

- 날짜: 2026-06-30
- 대상 repo: goquest
- 작성자: lyckabc
- 관련: geneso `ops-troubleshooting-티켓-발급-절차`, `프로젝트-약어-태그-매핑-테이블`; goquest `2026-05-23-ticket-name-resolution-design.md`
- 후속: goquest-mcp 신규 레포 (별도 spec, Phase 2)

## 배경 / 동기

앞으로 모든 서비스 작업을 goquest 티켓으로 관리한다. 1차 사용 주체는 **AI 에이전트/봇**이며, ssh-workspace에서 REST API로 티켓을 생성·관리한다. 현재 발급 절차(geneso 문서)에서 드러난 에이전트 마찰 지점:

- 태그를 `tag_id`(UUID)로만 지정해야 함 → 에이전트가 매번 `프로젝트-약어-태그-매핑-테이블`에서 UUID를 찾아야 함.
- 중복 티켓 차단이 **호출측 수동 dedup 조회**에 의존 (Step 1 절차).
- 티켓을 `title_name`(예: `GOQ-vault-portforward`)으로 참조하려 해도 단일 조회/필터가 UUID 기반.
- 기계가 읽을 계약(스키마) 부재 → Phase 2 MCP 서버가 소비할 안정 계약이 없음.

이 spec은 위 네 가지를 **하위호환 유지**하며 보강한다. Phase 2(goquest-mcp)는 이 보강된 REST + OpenAPI 위에 올린다.

## 현 상태에서 확인한 사실

- `title_name`은 이미 존재: `internal/migrate/sql/011_title_name.sql` — `tickets.title_name VARCHAR(255) NULL`, 부분 UNIQUE 인덱스 `tickets_project_title_name_key ON tickets(project_id, title_name) WHERE project_id IS NOT NULL AND title_name IS NOT NULL`. **DB 차원 유일성은 이미 보장.** 단, create 핸들러는 충돌 시 기존 티켓 반환이 아니라 에러를 낸다(개선 대상).
- 프로젝트/워크스페이스 이름 해석은 이미 구현됨(`ResolveProjectID`, `workspace_name`/`workspace_slug`/`project_name`). tag 이름 해석만 미구현.
- `internal/ticket/model.go`의 `ListTicketsFilter`는 `TagIDs []uuid.UUID`만 받고 tag 이름·`title_name` 필터가 없다.
- `internal/tag` 서비스/스토어: `Create`, `Get`, `Update`, `Delete`, `ListByWorkspace`만 존재. `GetByName` 없음. tag에 `(workspace_id, lower(name))` 유일 제약 없음(중복 태그명 생성 가능).
- 인증: `X-API-Key`(프로젝트 스코프, capability/origin/rate-limit). 보강은 인증 모델을 바꾸지 않는다.
- 라우트: `POST /api/v1/tickets`, `GET /api/v1/tickets`, `GET /api/v1/tickets/doc/{doc_id}`, `GET|PATCH|DELETE /api/v1/tickets/{id}` 등 (`cmd/service/main.go`).

## 목표

1. **이름 기반 태그** — create 시 `tag_names`로 지정, 미존재 태그는 자동 생성(get-or-create).
2. **title_name dedup/멱등** — 같은 프로젝트 내 동일 `title_name` create 시 에러 대신 기존 티켓을 `200`으로 반환.
3. **조회/필터 강화** — `title_name`·tag 이름·domain/status 복합 필터, `title_name`으로 단일 티켓 직접 조회.
4. **OpenAPI 스펙** — 티켓 중심 수작성 스펙을 엔드포인트로 서빙(Phase 2 MCP 계약 기반).

## 비목표 (범위 밖)

- goquest-mcp 레포 (Phase 2 별도 spec).
- `Idempotency-Key` 헤더 방식 멱등 (title_name 유일성으로 충분).
- 전체 API(workspaces·projects·contracts 등) OpenAPI 문서화 — 티켓·조회·태그만.
- Vault API key 배포 자동화 / CLI 바이너리 (별도 영역).
- 인증 모델 변경.

## 설계

### 1. 이름 기반 태그 (get-or-create)

**API 변경** — `CreateTicketReq` (`internal/ticket/model.go`)에 옵션 필드 추가:

```go
TagIDs   []uuid.UUID // 기존 (유지)
TagNames []string    // 신규
```

- 둘 다 오면 **합집합** (해석된 tag_id 기준 중복 제거).
- 해석 시점: create 핸들러가 `project_id`를 확정한 직후 → `project.workspace_id` 도출 → 각 `tag_name`을 해당 워크스페이스에서 get-or-create → tag_id로 변환 → 기존 부착 로직 재사용.

**신규 store/service 메서드** (`internal/tag`):

```go
// store
GetByName(ctx, workspaceID uuid.UUID, name string) (*Tag, error) // WHERE workspace_id=$1 AND LOWER(name)=LOWER($2); ErrNotFound 0행
// service
GetOrCreate(ctx, workspaceID uuid.UUID, creatorID, name string) (*Tag, error)
```

- `GetOrCreate`: `GetByName` → 있으면 반환, 없으면 `Create`. 동시 생성 경합은 아래 유일 제약(23505) → 충돌 시 `GetByName` 재조회로 흡수.

**핸들러 디커플** — 핸들러는 구체 서비스 대신 작은 인터페이스에 의존(테스트 용이):

```go
// internal/handler/ticket.go
type TagResolver interface {
    // 워크스페이스 내에서 tag 이름들을 tag_id로 해석(미존재 시 생성).
    ResolveTagIDs(ctx context.Context, workspaceID uuid.UUID, names []string) ([]uuid.UUID, error)
}
```

`NewTicketHandler`에 `TagResolver` 주입, `cmd/service/main.go`에서 와이어링.

**에러 의미:** project_id가 없어 워크스페이스를 도출할 수 없는데 `tag_names`가 오면 → `400 Bad Request`, "tag_names requires a resolvable project".

### 2. title_name dedup / 멱등

create 시 `title_name`이 주어지고 같은 프로젝트 내 이미 존재 → **기존 티켓을 `200 OK`로 반환**(멱등). 신규 생성은 `201 Created`.

- 구현: insert가 `tickets_project_title_name_key` 위반(SQLSTATE 23505)을 던지면 핸들러/서비스가 감지 → `(project_id, title_name)`으로 기존 티켓 조회 후 반환.
- 신규/기존 구분: 응답 헤더 `X-Idempotent-Replay: true`(기존 반환 시). 바디는 동일한 Ticket 객체.
- `project_id`가 없는(미지정) 티켓은 부분 인덱스 정의상 dedup 비대상 — 기존 동작(항상 신규 생성) 유지.
- store에 `GetByProjectAndTitleName(ctx, projectID uuid.UUID, titleName string) (*Ticket, error)` 추가.

**서비스 흐름:**
```
svc.Create(req):
  insert → 성공: (ticket, created=true)
        → 23505 on tickets_project_title_name_key:
             existing = store.GetByProjectAndTitleName(req.ProjectID, req.TitleName)
             return (existing, created=false)
        → 그 외 에러: 전파
handler: created ? 201 : 200(+X-Idempotent-Replay)
```

다른 unique 위반(예: 향후 다른 제약)은 23505라도 인덱스명으로 구분하여 dedup 경로로 빠지지 않게 한다(constraint name 검사).

### 3. 조회 / 필터 강화

**(a) List 필터 확장** — `GET /api/v1/tickets` 쿼리 파라미터:
- 신규: `title_name=<exact>` (부분일치 아님, 정확 매칭), `tag=<name>`(반복 가능, 이름; 내부에서 tag_id 해석).
- 기존 유지: `status`, `type`, `priority`, `domain`, `assignee_type`, `assignee_id`, `parent_id`, `project_id`, `workspace_id`, 정렬/페이지.
- `ListTicketsFilter`에 `TitleName *string`, `TagNames []string` 추가. store 쿼리에서 `title_name = $n` 및 tag 이름 → tag_id 해석 후 기존 `TagIDs` 경로와 합류.
- 미존재 `tag` 이름 필터는 빈 결과(에러 아님).

**(b) title_name 단일 조회 라우트** — 신규:
```
GET /api/v1/tickets/by-name/{title_name}?workspace=<name|slug>&project=<name>
```
- 워크스페이스/프로젝트 식별자로 project_id 해석(기존 `ResolveProjectID` 재사용) → `(project_id, title_name)`으로 단일 티켓 조회.
- 매칭 0건 → `404`. 식별자 누락/미해석 → `400`.
- 기존 `GET /tickets/{id}`(UUID) 라우트와 공존. `by-name`은 고정 prefix라 `{id}` 패턴과 충돌 없음.
- 권한 검사는 기존 ticket Get 경로와 동일하게 적용.

### 4. OpenAPI 스펙 (수작성, 티켓 중심)

- `api/openapi.yaml` 수작성. 우선 커버 범위: `POST/GET /tickets`, `GET /tickets/{id}`, `PATCH /tickets/{id}`, `GET /tickets/by-name/{title_name}`, tag 관련(create 시 tag_names, 워크스페이스 태그 목록). 인증(`X-API-Key`) securityScheme 포함.
- 서빙: `GET /api/v1/openapi.json` (yaml을 JSON으로 변환해 정적 응답) + 선택적으로 `GET /api/v1/openapi.yaml` 원본. 인증 불필요(공개 계약) 여부는 구현 시 기존 health 라우트와 동일하게 그룹 밖 배치 검토.
- 스펙은 손으로 유지(코드 주석 생성기 미도입). 본 spec의 1~3 변경사항을 정확히 반영.

## 데이터 흐름 (티켓 생성, 보강 후)

```
POST /tickets {workspace_name|slug, project_name, title, title_name, tag_names[], domain, ...}
  → handler.Create: decode
  → projectResolver.ResolveProjectID(ref)            // 기존
  → tagResolver.ResolveTagIDs(ws.ID, tag_names)      // 신규: get-or-create → []tag_id
  → req.TagIDs = union(req.TagIDs, 해석된 tag_id)
  → svc.Create(req):
        insert
          → 201 created=true
          → 23505(tickets_project_title_name_key): GetByProjectAndTitleName → created=false
  → handler: 201 | 200(+X-Idempotent-Replay)
```

## 테스트 계획

단위 (mock store, 외부 의존 없음):
- `tag.GetOrCreate`: 존재(반환)·미존재(생성)·대소문자 무시 매칭·동시 23505→재조회 흡수.
- `TagResolver.ResolveTagIDs`: 다건·중복 제거·빈 입력·project 미해석 시 400.
- create dedup: 신규 `title_name`→201; 동일 `title_name`→200 + `X-Idempotent-Replay`; project_id 없는 티켓→항상 201.
- 23505 분기: title_name 제약 vs 그 외 제약 구분.
- List 필터: `title_name` 정확매칭, `tag` 이름 해석, 미존재 tag→빈 결과, 복합 필터.
- by-name 라우트: 정상 200, 미존재 404, 식별자 누락 400.

통합 (pg 테스트 하니스 존재 시):
- `tag.GetByName` 실제 쿼리, `(workspace_id, lower(name))` 유일 제약 위반.
- `tickets_project_title_name_key` 충돌 → 기존 티켓 반환.
- by-name 조회 실제 쿼리.

OpenAPI:
- 스펙 파일 YAML 파싱 유효성, 서빙 엔드포인트 200 + content-type.

## 마이그레이션 / 배포 순서

1. 신규 마이그레이션 (`internal/migrate/sql/017_tag_name_unique.sql`, 현재 최신 016 다음):
   ```sql
   CREATE UNIQUE INDEX IF NOT EXISTS tags_workspace_lower_name_key
     ON tags (workspace_id, LOWER(name));
   ```
   - **사전 점검:** 기존 중복 태그명 존재 가능 →
     `SELECT workspace_id, LOWER(name), COUNT(*) FROM tags GROUP BY 1,2 HAVING COUNT(*)>1;`
     중복이 있으면 rename/병합 후 적용. (러너가 마이그레이션 실패를 로그만 남기고 진행하므로 배포 후 인덱스 존재 확인 필수: `\d tags` → `tags_workspace_lower_name_key`.)
2. 코드 + 마이그레이션 머지·배포.
3. 신규 API 필드/라우트는 모두 옵션 → 하위호환(기존 `tag_ids`/UUID 조회 무영향).
4. OpenAPI 스펙 서빙 확인.

## 미해결 / 추후

- Phase 2: goquest-mcp 레포 — 본 OpenAPI 계약을 소비해 `create_ticket`/`list_tickets`/`get_ticket`/`comment` 등 MCP 도구 노출. 별도 spec.
- Vault API key 배포·CLI 편의(별도 영역).
- 전체 API OpenAPI 문서화(점진 확장).
