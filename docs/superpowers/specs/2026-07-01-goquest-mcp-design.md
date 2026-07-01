# goquest-mcp: 에이전트용 MCP 서버 (Phase 2)

- 날짜: 2026-07-01
- 대상 repo: **goquest-mcp** (신규, tojiuni) — 구현 단계에서 스캐폴딩
- 작성자: lyckabc
- 선행: goquest REST 에이전트 친화 보강 Phase 1 (`docs/superpowers/specs/2026-06-30-goquest-rest-agent-friendly-design.md`) — 본 spec이 소비하는 OpenAPI 계약·이름 기반 API를 제공
- geneso 근거: `neunexus/troubleshooting/project-abbr-table.md`, `neunexus/troubleshooting/ticket-workflow.md`, `goquest/reference/ticket-api.md`, `goquest/troubleshooting/2026-06-30-whoami-e2e-ticket-conventions.md`

## 배경 / 동기

모든 서비스 작업을 goquest 티켓으로 관리하며 1차 사용 주체는 **AI 에이전트/봇**이다. Phase 1에서 goquest REST를 이름 기반·멱등·OpenAPI 서빙까지 보강했다. Phase 2는 그 계약 위에 **MCP 서버**를 올려, LLM이 REST 세부(엔드포인트·헤더·UUID)를 몰라도 도구 호출만으로 티켓을 생성·조회·관리하게 한다.

핵심 요구:
- LLM이 goquest가 제공하는 API를 **런타임에 조회**(discovery)할 수 있어야 한다.
- LLM이 **티켓 작성 규약**(title_name 포맷, 이름 기반 필드, dedup/parent, 상태머신)에 맞게 생성하도록 **가이드**를 제공해야 한다 — geneso에 정리된 규약을 진실 공급원으로 한다.

## 조직 컨벤션 (gopedia_mcp 미러)

기존 `gopedia_mcp`(레포 `/workspace/repos/gopedia_mcp`)를 패턴 기준으로 삼는다:
- **TypeScript** 단일 파일 서버 + `@modelcontextprotocol/sdk` + `tsx` 런타임.
- 백엔드와 **HTTP `fetch`** 연동, `BASE_URL`은 env로 구성.
- **stdio 기본 + `MCP_HTTP_PORT` 지정 시 StreamableHTTP** 양쪽 transport.
- `Dockerfile` + `deploy/`(k8s) + `ci`/`ci.yaml` + `scripts/*smoke*.ts` + `README.md`.

## 목표

1. goquest REST(Phase-1 계약)를 프록시하는 **코어 티켓 도구 6종** 노출 (이름 기반·멱등).
2. **API 디스커버리 도구** — Phase-1 `/api/v1/openapi.json`을 조회해 오퍼레이션 목록 반환.
3. **티켓 작성 가이드 도구/리소스** — geneso 규약 기반, LLM이 규약대로 생성하도록 유도.

## 비목표 (범위 밖)

- goquest 서버 코드 변경 (Phase 1에서 완료).
- workspaces/projects/contracts/tags 전체 API의 도구화 (티켓 중심만; 점진 확장).
- Vault API key 배포 자동화 / CLI 바이너리 (별도 영역).
- goquest 인증 모델 변경.
- OpenAPI 스펙에서 도구를 **자동 생성**하는 코드젠 (도구 정의는 손으로 작성).

## 설계

### 1. 아키텍처

상태 비저장(stateless) 프록시. MCP 도구 호출 → goquest REST(`/api/v1/*`)로 `fetch`, 응답을 도구 결과로 변환. goquest가 유일한 진실 공급원(상태·유효성·권한 모두 goquest가 판정).

**레포 레이아웃** (`goquest-mcp`):
```
goquest-mcp-server.ts     # 단일 파일 서버 (도구 정의 + transport)
openapi_spec.yaml (선택)   # 참조용; 런타임은 goquest /openapi.json 조회
ticket-guide.md            # embed되는 작성 가이드 (geneso에서 동기화)
package.json / tsconfig.json
Dockerfile / deploy/ / ci.yaml
scripts/mcp-smoke-test.ts
README.md
```

### 2. MCP 도구 (총 8종)

모든 도구 스키마는 **손으로 작성**(에이전트 친화 description·enum). 이름 기반 인자를 우선 노출해 UUID를 요구하지 않는다.

**코어 티켓 도구 6종** — Phase-1 계약 매핑:

| 도구 | goquest 엔드포인트 | 핵심 인자 / 동작 |
|---|---|---|
| `create_ticket` | `POST /tickets` | `title`(필수), `title_name?`, `workspace`/`project`(이름) 또는 `project_id`, `tag_names[]`, `type`/`priority`/`domain`/`parent_id?`. **멱등**: 동일 project 내 동일 `title_name`은 기존 티켓 반환(결과에 `replayed: true`). |
| `get_ticket` | `GET /tickets/{id}` | `id` |
| `get_ticket_by_name` | `GET /tickets/by-name/{title_name}` | `title_name`, `workspace`, `project` |
| `list_tickets` | `GET /tickets` | `title_name?`, `tag[]?`, `status?`, `domain?`, `project_id?`, `limit`/`offset` |
| `update_ticket` | `PATCH /tickets/{id}` | `id`, `status?`(상태 전이)/`title?`/`description?`/… |
| `comment` | `POST /tickets/{id}/comments` | `id`, `body` |

**디스커버리 도구 (`list_api`)**:
- `GET /api/v1/openapi.json`을 서버가 파싱.
- 기본: **압축 목록** — 오퍼레이션별 `method + path + summary`(+ 주요 파라미터) 한 줄 요약.
- 인자 `full: true` → OpenAPI 스펙 원본(JSON) 그대로 반환.

**가이드 도구/리소스 (`ticket_guide`)**:
- 레포에 embed된 `ticket-guide.md`를 반환 (인자 없음).
- MCP **resource** `goquest://ticket-guide`로도 등록(리소스를 노출하는 클라이언트 대응).
- 내용(geneso 근거):
  - `title_name` 규약: `^[a-z][a-z0-9]*(-[a-z0-9]+)+$` — **소문자·하이픈 1개 이상 필수**. 대문자/하이픈 없는 값은 422. prefix 표(`goq-`, `who-`, `gop-`, …) ← `project-abbr-table.md`.
  - **이름 기반 필드 우선**: `workspace`/`project`/`tag_names`로 지정(UUID 불필요), 미존재 태그는 get-or-create.
  - **멱등**: 동일 `title_name` create는 기존 티켓 replay.
  - **dedup/parent 절차**: 생성 전 오픈 티켓(`status pending|in_progress` + 태그·domain 일치) 조회 → 있으면 `parent_id` 지정(sub-ticket), 없으면 new ← `ticket-workflow.md`.
  - **상태머신**: `pending→assigned→in_progress→(pending_approval)→completed`, terminal(`completed`/`failed`/`cancelled`). `pending→in_progress` 직행 불가(assigned 경유). ← `ticket-api.md`.
  - domain enum, 필수/권장 필드.
- `create_ticket` 도구 description에도 title_name 규약 **요약**을 포함 → 가이드 미호출 시에도 올바른 생성 유도.

### 3. 설정 / 인증 / transport

- env:
  - `GOQUEST_API_URL` — 기본 `http://goquest-service.goquest.svc.cluster.local:8080` (in-cluster). 외부는 `https://goquest.toji.homes`.
  - `GOQUEST_API_KEY` — **Vault `secret/neunexus/workspace/goquest#api_key`** 에서 배포 시 주입. 모든 goquest 호출에 `X-API-Key` 헤더 부착.
  - `MCP_HTTP_PORT?` — 지정 시 StreamableHTTP, 미지정 시 stdio.
- 인증 모델: **env/Vault 단일 키**. 키의 프로젝트 스코프가 도달 범위를 결정. 다른 스코프가 필요하면 인스턴스를 분리 배포.

### 4. 에러 처리

goquest HTTP 상태 → MCP 도구 에러(`isError`)로 매핑하되 goquest 메시지를 보존하고 교정 힌트를 첨부:

| goquest | 처리 |
|---|---|
| `200 X-Idempotent-Replay` | 정상 — 결과에 `replayed: true` |
| `400` | 잘못된 입력. title_name 포맷 위반 시 규약 힌트 첨부 |
| `404` | 없음 (get/by-name) |
| `409` | title_name 충돌 — create는 멱등 replay 경로라 정상 200으로 귀결(직접 409 노출 회피) |
| `422` | 허용 안 된 상태 전이 — 유효 전이 힌트 첨부 |
| `5xx` / 네트워크 | 인프라 오류 — `base_url` 포함해 그대로 표면화 |

### 5. 테스트

`scripts/mcp-smoke-test.ts` (gopedia_mcp 패턴, `tsx`): mock 또는 실 goquest 대상으로 각 도구 1회 호출.
- `create_ticket` 이름 해석(workspace/project/tag_names) + 멱등 replay(`replayed:true`).
- `get_ticket_by_name` 404 경로.
- `list_tickets` 이름 기반 필터.
- `list_api` 축약 목록 + `full` 원본.
- `ticket_guide` 반환(규약 문자열 포함) + resource 조회.
- 에러 매핑(400 title_name, 422 전이) 스모크.

### 6. 배포

- `Dockerfile`(node + tsx) + `deploy/`(k8s Deployment/Service; `GOQUEST_API_URL`·`GOQUEST_API_KEY`(Vault) 주입) + `ci`/`ci.yaml`.
- 사용 등록: `~/.mcp.json`에 `goquest` 항목 추가(stdio 로컬 또는 in-cluster HTTP). gopedia `gp`와 동일 방식.

### 7. geneso 동기화

- geneso에 **`goquest/guide/ticket-authoring-guide.md`** 신규 작성(위 가이드 내용 정본) → gopedia ingest.
- goquest-mcp 레포의 `ticket-guide.md`는 이 정본에서 **동기화된 복사본**(Phase-1의 `openapi_spec.yaml` embed 방식과 동일). 스펙/README에 "정본=geneso, 복사본 동기화 의무" 명시.

## 데이터 흐름 (티켓 생성)

```
LLM → MCP create_ticket {title, title_name, workspace, project, tag_names[], domain}
  → goquest-mcp: build POST /api/v1/tickets  (X-API-Key)
  → goquest: ResolveProjectID + ResolveTagIDs + 멱등 insert
      → 201 created  | 200 X-Idempotent-Replay
  → MCP 결과: {ticket, replayed}
```

## 미해결 / 추후

- 추가 도구(approve/reject 승인 게이트, subtask/attempts, refs) — 필요 시 후속 확장.
- workspaces/projects/tags 관리 도구화 — 점진.
- OpenAPI→도구 코드젠 — 도구 수 증가 시 재검토(현재는 손 작성).
