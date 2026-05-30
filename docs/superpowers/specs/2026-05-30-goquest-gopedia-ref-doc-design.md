# goquest ↔ gopedia Ref Doc 설계

- 작성일: 2026-05-30
- 작성자: lyckabc (with Claude Opus 4.7)
- 상태: Design (구현 전)
- 관련 모듈: `internal/ticket/`, `internal/handler/`, `migrations/`, goquest-bot, gopedia

## 1. 배경 / 목적

goquest의 ticket 본문(`tickets.description`)에 풍부한 배경·스펙·런북을 누적하면, 같은 지식이 gopedia(L1=문서/파일, L2=섹션/메서드/클래스/테이블, L3=문장 — 실데이터는 L3에만 저장)와 중복된다. 본 설계는 다음을 목표로 한다.

- goquest는 **실행 상태 머신**으로 가볍게 유지한다.
- "지식·맥락"은 gopedia에 single-source로 둔다.
- ticket이 필요 시 gopedia L1 문서를 **옵션 첨부**로 참조한다 (강제 아님).
- AI agent (goquest-bot)는 ticket 실행 시 ref 문서를 활용해 풍부한 context를 끌어쓴다.

## 2. 결정사항 요약

| 항목 | 결정 |
|---|---|
| 본문 vs ref 관계 | 본문(`description`)은 자유, ref doc은 옵션 첨부 |
| ref 단위 | gopedia L1 (문서 전체)만 |
| 버전 정책 | Live link (항상 최신, 스냅샷·revision 고정 없음) |
| 저장 모양 | `gopedia_l1_id`만 보관. title/snippet 캐시 없음 |
| bot 활용 | goquest는 id 배열만 전달, bot이 자기 ZITADEL 토큰으로 gopedia lazy fetch (title·콘텐츠 모두) |
| goquest의 gopedia 런타임 의존성 | **없음** |
| 인가 | 완전 분리 — ticket 가시성(SpiceDB) ↔ doc 콘텐츠 접근(gopedia ACL). goquest는 gopedia ACL을 복제·검증하지 않음 |
| 역참조 (gopedia → linked tickets) | v1 포함 — read-only API 제공, caller의 ticket 가시성으로 post-filter |

## 3. 아키텍처

```
┌─────────────┐   id만 보관     ┌──────────────┐
│  goquest    │ ──────────────▶ │   gopedia    │
│  (ticket    │                 │   (L1 doc =   │
│   상태머신)  │  ◀── 권한 별개  │    실콘텐츠)  │
└─────┬───────┘                 └──────┬───────┘
      │ POST /execute                  │ search / read
      │ (ticket + ref_ids)             │ (caller's token)
      ▼                                ▲
┌─────────────┐  bot이 자기 권한으로 fetch
│  goquest-bot │ ──────────────────────┘
└─────────────┘
```

핵심 원칙:

- goquest는 ref doc의 **id 목록만** 보관. 메타·콘텐츠·권한 전부 gopedia에 위임.
- goquest → gopedia 런타임 호출 **없음**. gopedia가 다운돼도 ticket CRUD는 정상.
- bot은 ticket payload에서 받은 id로 자기 ZITADEL 토큰을 사용해 gopedia를 직접 fetch. title/content 둘 다 bot 책임.
- 인가 두 평면 완전 분리. goquest는 어느 쪽도 복제·우회하지 않음.
- 역참조는 goquest가 ticket 가시성으로 필터한 목록만 제공.

## 4. 데이터 모델

새 테이블 한 개만 추가. 기존 `tickets`는 손대지 않음.

```sql
-- migrations/002_ticket_refs.sql
CREATE TABLE ticket_refs (
    ticket_id       UUID NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    gopedia_l1_id   TEXT NOT NULL,
    position        INT  NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by      TEXT NOT NULL,
    PRIMARY KEY (ticket_id, gopedia_l1_id)
);

CREATE INDEX idx_ticket_refs_gopedia ON ticket_refs(gopedia_l1_id);
CREATE INDEX idx_ticket_refs_ticket  ON ticket_refs(ticket_id, position);
```

설계 결정 근거:

- `gopedia_l1_id TEXT` — gopedia 식별자 형식(UUID·slug·NID 등)에 강결합 안 함.
- PK `(ticket_id, gopedia_l1_id)` — 같은 doc 중복 ref 금지. 순서 바꾸려면 `position`만 update.
- `position INT` — 사용자 정의 순서. 새 추가 시 `MAX(position)+1`.
- `created_by TEXT` — ZITADEL subject (email). 감사 목적. SpiceDB 인가 결정에는 안 씀.
- `ON DELETE CASCADE` — ticket 삭제 시 refs도 함께. 좀비 ref 없음.
- 가시성 검증 안 함 — 분리 인가 원칙. 권한 없는/없는 doc id도 등록 가능 (bot fetch 시 자연 실패).
- `tickets.description` 그대로 유지 — ref는 옵션 첨부.
- 캐시 컬럼 없음 — id-only 결정 반영.

Go 모델 (`internal/ticket/model.go`에 추가):

```go
type TicketRef struct {
    TicketID     uuid.UUID `json:"ticket_id"`
    GopediaL1ID  string    `json:"gopedia_l1_id"`
    Position     int       `json:"position"`
    CreatedAt    time.Time `json:"created_at"`
    CreatedBy    string    `json:"created_by"`
}
```

`CreateTicketReq` / `UpdateTicketReq`에 `RefIDs []string` 옵션 필드 추가 (생성·수정 시 일괄 지정).

## 5. HTTP API

기존 ticket 핸들러 패턴(`internal/handler/ticket.go`)을 따른다. 새 모듈 만들지 않고 `internal/ticket/`와 `internal/handler/`에 추가.

### 엔드포인트

| Method | Path | 목적 | 인가 |
|---|---|---|---|
| `GET`    | `/api/v1/tickets/{id}/refs`            | ticket의 ref 목록 (position 순)        | `RequireTicketView` |
| `POST`   | `/api/v1/tickets/{id}/refs`            | ref 추가 (단건 또는 배치)              | `RequireTicketView` |
| `DELETE` | `/api/v1/tickets/{id}/refs/{l1_id}`    | ref 제거                              | `RequireTicketView` |
| `PATCH`  | `/api/v1/tickets/{id}/refs/reorder`    | position 일괄 갱신                    | `RequireTicketView` |
| `GET`    | `/api/v1/refs?gopedia_l1_id=X`         | 역참조 — 해당 doc을 ref하는 ticket id | caller의 ticket 가시성으로 post-filter |

ticket 단위 4개 엔드포인트는 모두 기존 `RequireTicketView` 미들웨어 (`internal/auth/ticket_view.go`)만 사용한다. 이는 현행 ticket `Update`/`Delete` 핸들러와 동일한 모델 — view = 작업 권한. 추후 view/edit가 분리되면 refs도 자연스럽게 그 분리를 상속한다.

ticket 생성/수정 시 한 번에: `POST /api/v1/tickets`와 `PATCH /api/v1/tickets/{id}` body에 `ref_ids: ["l1-abc", "l1-def"]` 옵션 추가. position은 배열 순서.

### 요청/응답 예시

```http
POST /api/v1/tickets/{id}/refs
Content-Type: application/json

{ "ref_ids": ["l1-abc", "l1-def"] }
```

```json
{
  "refs": [
    { "gopedia_l1_id": "l1-abc", "position": 0, "created_at": "...", "created_by": "lyckabc@..." },
    { "gopedia_l1_id": "l1-def", "position": 1, "created_at": "...", "created_by": "lyckabc@..." }
  ]
}
```

```http
GET /api/v1/refs?gopedia_l1_id=l1-abc
```

```json
{ "ticket_ids": ["uuid-1", "uuid-7"] }
```

caller가 볼 수 있는 ticket만 노출. 권한 없으면 빈 배열 (존재 누설 없음).

### 인가 적용 지점

- ticket 단위 엔드포인트 4개: 기존 `RequireTicketView` 그대로. 새 미들웨어·SpiceDB 권한 추가 없음.
- 역참조: SpiceDB `VisibleResourceIDs`로 ticket id 목록을 받은 뒤 `ticket_refs` 조회 결과와 교집합. `restricted semantics` 패턴 재사용.
- gopedia ACL 검증: 하지 않음. 등록 시점에 caller가 해당 doc 못 읽어도 ref는 가능 — 인가 분리 원칙.

### bot payload 변경

기존 `/execute` payload에 한 필드 추가:

```json
{
  "ticket": { "id": "...", "title": "...", "description": "...", "...": "..." },
  "ref_ids": ["l1-abc", "l1-def"]
}
```

goquest-service는 id 배열만 동봉. bot은 자기 ZITADEL 토큰으로 gopedia에 `read(l1_id)` 호출해 title·콘텐츠 fetch. **goquest는 gopedia를 호출하지 않음.**

## 6. 에러 처리 · 엣지 케이스

### goquest 측 (등록·조회)

| 상황 | 동작 |
|---|---|
| `ref_ids` 빈 배열 | 200 OK, no-op |
| 같은 doc을 한 ticket에 재등록 | 409 Conflict (PK 위반) |
| 같은 요청 내 중복 id | 400 Bad Request — dedupe는 클라이언트 책임 |
| 존재하지 않는 ticket | 404 (기존 핸들러와 동일) |
| 존재하지 않는 ref 삭제 | 204 No Content — 멱등 |
| `gopedia_l1_id` 형식 검증 | 길이만 (1–512). 형식 강결합 없음. typo도 통과 (의도) |
| ticket당 ref 개수 상한 | 50개. 초과 시 422. 초기 보수, 운영 보며 완화 |
| 역참조 — caller가 ticket 0개 볼 권한 | `{ "ticket_ids": [] }` |
| 역참조 — `gopedia_l1_id` 누락 | 400 |

### bot 측 (실행)

| 상황 | 동작 |
|---|---|
| ref doc fetch 권한 없음 (gopedia 403) | 해당 ref만 skip, 나머지 ref·본문은 정상. attempt 결과에 `skipped_refs: [{id, reason: "forbidden"}]` |
| ref doc 없음 (gopedia 404) | 동일 — `reason: "not_found"`. live link 정책의 자연스러운 부산물 |
| gopedia 다운 | 기본은 본문만으로 진행 + 경고 로깅 (가용성 우선) |
| ref 0개 ticket | 정상 — ref는 옵션 |

### 운영·일관성

- ticket 삭제 시 refs: `ON DELETE CASCADE`로 자동 정리.
- gopedia에서 doc 삭제 → goquest의 좀비 ref: GC 안 함. 다음 bot 실행 시 404로 자연 노출 (YAGNI).
- rate limit: bot이 한 ticket의 N개 ref 동시 fetch — gopedia 부하 우려. bot 측에서 동시성 제한(예: 5). bot 책임, goquest 변경 없음.
- 감사: ref add/remove는 기존 `internal/audit/`에 actor·doc_id·action 기록.

### 의도적으로 하지 않는 것

- gopedia 존재·권한 검증 (인가 분리 원칙)
- title 캐시·갱신·invalidation (id-only 원칙)
- gopedia → goquest 이벤트 수신 (단방향만)
- ref doc 권한 미러링 (SpiceDB 객체 추가 안 함)

## 7. 테스트 전략

기존 패턴을 따른다: store는 실 Postgres 통합, 서비스는 store 통합 + mock SpiceDB, 핸들러는 HTTP 레벨, bot은 별도.

### Unit / Store (`internal/ticket/refs_store_test.go`, 실 Postgres)

- `Add` 단건/배치 — position 자동 할당
- `Add` 중복 → PK 위반 매핑 → `ErrRefConflict`
- `Remove` 존재/부재 모두 멱등
- `List(ticketID)` position 순 보장
- `ListTicketsByRef(l1_id)` 인덱스 사용 확인 (EXPLAIN 한 케이스)
- `Reorder` 트랜잭션 일괄 update, 부분 실패 시 롤백
- ticket CASCADE DELETE 검증

### Service (`internal/ticket/refs_service_test.go`)

- 50개 상한 enforce
- 빈 배열 no-op
- 같은 요청 내 중복 id → 400 매핑
- 역참조: `VisibleResourceIDs` mock → 교집합 정확성

### Handler (HTTP-level, `internal/handler/refs_http_test.go`)

`authz_e2e_test.go` 패턴 — chi 라우터 + 실 store + 모의 인증.

- `POST /tickets/{id}/refs` — 미인증 401, view 권한 없음 403, 정상 200
- `GET /tickets/{id}/refs` — view 권한 분기
- `DELETE` 멱등 (없는 id 204)
- `PATCH /refs/reorder` — position 순서
- `GET /refs?gopedia_l1_id=X` — 0 권한 `[]`, 일부 권한 교집합만, 없는 doc `[]`

### E2E (`internal/handler/refs_e2e_test.go`)

실 Postgres + 실 SpiceDB(testcontainers 또는 기존 E2E 하네스) + bot mock HTTP 서버.

1. **라이프사이클**: ticket 생성 → ref 등록 → 역참조 조회 → bot 호출 → ref 제거 → ticket 삭제
2. **인가 분리 검증**: caller가 ticket view 권한 있고 gopedia doc 권한 없는 상태에서 ref 등록 성공 (goquest는 gopedia ACL 안 봄)
3. **가시성 누설 방지**: A가 ref한 doc을 B가 역참조 조회 시 A의 ticket id 안 보임
4. **bot payload 검증**: mock bot이 받은 `/execute` body에 `ref_ids` 정확·순서·중복 없음
5. **bot fetch 실패**: mock gopedia 403/404 → bot skip + 결과 보고. attempt 정상 수신
6. **gopedia 다운**: mock gopedia 연결 거부 → bot 본문만으로 실행 + warning, ticket `completed` 가능
7. **CASCADE**: ticket DELETE 후 `ticket_refs` 0건
8. **org-axis 호환**: workspace→org dual-write 환경에서 ref 등록·역참조 정상 (회귀)

### 의도적으로 테스트 안 하는 것

- gopedia 측 동작 (gopedia 레포)
- bot의 gopedia fetch 로직 (bot 레포 단위 테스트)
- ZITADEL 토큰 발급 (기존 `internal/auth/` 커버)

### 회귀 안전망

- `TEST_REVIEW.md`에 항목 추가: refs 기능이 ticket CRUD·visibility·SpiceDB sync에 영향 없음 확인
- ref 0개 기존 ticket 흐름 불변 — 회귀 픽스

## 8. 범위 외 (Out of Scope, v1)

- L2/L3 단위 ref
- title/내용 스냅샷 또는 revision 고정
- gopedia → goquest 이벤트 (예: doc 삭제 알림)
- ref doc 자동 추천 (예: 본문 키워드 → 후보 doc)
- gopedia UI 변경 (역참조 API만 제공, 화면은 gopedia 측 별도 작업)
- ref doc 권한 미러링·SpiceDB 객체 추가

## 9. 마이그레이션·롤아웃

- DB 마이그레이션 1개 (`002_ticket_refs.sql`) — 빈 테이블 추가, 기존 데이터 영향 없음.
- bot payload 변경: `ref_ids` 필드 추가. bot이 미인식해도 무시 가능 (기존 동작 유지). bot 업데이트는 분리 배포 가능.
- 기능 플래그 없음 — 신규 첨가형이라 위험 낮음. 문제 발생 시 핸들러 4개만 비활성.
