# Goquest Backend — Helpdesk 통합 추가 기능

> 작성일: 2026-05-11
> 상위 문서: `geneso/universitas/helpdesk/architecture.md`
> 정책: `geneso/universitas/helpdesk/policy-workspace-visibility.md`
> 본 문서는 helpdesk 통합을 위해 **goquest backend에 신규로 필요한 기능**만 정리한다.
> 기존 멀티테넌트 구현은 `goquest-ui/docs/plan-workspace-project.md`를 따른다.

---

## 0. 선행 조건

- Phase 1~2 (멀티테넌트 + 티켓 고도화)가 머지된 상태여야 한다 (`goquest-ui/docs/todo.md` 기준).
- `customer-support`, `internal-helpdesk` workspace 시드가 마이그레이션 또는 운영 스크립트에 등록된 상태.

---

## 1. 데이터 모델 변경

### 1.1 `tickets` 테이블 추가 컬럼

```sql
ALTER TABLE tickets
  ADD COLUMN channel          TEXT NOT NULL DEFAULT 'agent'
                                   CHECK (channel IN ('web','email','telegram','agent','api')),
  ADD COLUMN requester_email  TEXT,
  ADD COLUMN requester_name   TEXT,
  ADD COLUMN locale           TEXT NOT NULL DEFAULT 'ko',
  ADD COLUMN metadata         JSONB NOT NULL DEFAULT '{}';

CREATE INDEX idx_tickets_requester_email ON tickets(requester_email);
```

> magic-link 토큰은 `tickets`에 두지 않고 별도 `magic_tokens` 테이블 (§1.5) 사용. 결정 근거: 다중 토큰·회전·revoke·감사 지원.

### 1.2 `ticket_comments` 테이블 (신규)

지금까지 댓글 모델이 별도로 없으면 신규. 있으면 컬럼만 추가.

```sql
CREATE TABLE ticket_comments (
  id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  ticket_id   UUID NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  author_id   TEXT NOT NULL,            -- ZITADEL sub 또는 'agent:<name>' 또는 'customer:<email>'
  author_kind TEXT NOT NULL CHECK (author_kind IN ('staff','agent','customer','system')),
  body        TEXT NOT NULL,
  is_internal BOOLEAN NOT NULL DEFAULT FALSE,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_ticket_comments_ticket ON ticket_comments(ticket_id, created_at);
```

### 1.3 `project_api_keys` 컬럼 추가

```sql
ALTER TABLE project_api_keys
  ADD COLUMN allowed_capabilities TEXT[] NOT NULL DEFAULT '{}',
  ADD COLUMN scope                TEXT NOT NULL DEFAULT 'private'
                                       CHECK (scope IN ('public','private')),
  ADD COLUMN rate_limit_per_min   INT NOT NULL DEFAULT 60,
  ADD COLUMN daily_quota          INT;
```

> public key는 반드시 `allowed_capabilities = {'ticket.create'}` 만 갖도록 service 레이어에서 강제.

### 1.5 `magic_tokens` (신규)

비로그인 고객이 이메일 링크로 티켓을 열 때 쓰는 1회용/단기 토큰. 결정 2026-05-11.

```sql
CREATE TABLE magic_tokens (
  id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  ticket_id   UUID NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  email       TEXT NOT NULL,
  token_hash  TEXT NOT NULL UNIQUE,    -- SHA-256(raw token)
  scope       TEXT[] NOT NULL DEFAULT '{ticket.read,comment.create}',
  expires_at  TIMESTAMPTZ NOT NULL,    -- 기본 7일
  used_at     TIMESTAMPTZ,             -- one-shot일 때만 갱신
  revoked_at  TIMESTAMPTZ,
  created_by  TEXT NOT NULL,           -- 발급자 ('system' | user_id)
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_magic_tokens_ticket ON magic_tokens(ticket_id, expires_at);
CREATE INDEX idx_magic_tokens_email  ON magic_tokens(email);
```

규칙:
- raw 토큰은 절대 저장하지 않는다. 발급 시 1회 노출 후 SHA-256만 저장.
- 검증 시: `token_hash` 매치 + `expires_at > now()` + `revoked_at IS NULL`.
- 재발송은 새 row 생성. 기존 row는 그대로 두되 expires 단축(15분)으로 invalidate.
- 만료 cleanup cron: 30일 경과 만료 토큰 hard-delete.

### 1.4 `audit_log` (신규)

```sql
CREATE TABLE audit_log (
  id          BIGSERIAL PRIMARY KEY,
  occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  actor       TEXT NOT NULL,             -- user_id or 'apikey:<id>'
  action      TEXT NOT NULL,             -- e.g. 'workspace.member.add'
  target_kind TEXT NOT NULL,
  target_id   TEXT NOT NULL,
  detail      JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_audit_log_target ON audit_log(target_kind, target_id);
```

---

## 2. 새 엔드포인트

### 2.1 댓글 / 대화

| Method | Path | 권한 |
|--------|------|------|
| `POST` | `/tickets/:id/comments` | member+ (staff) / customer-token / agent-key |
| `GET` | `/tickets/:id/comments` | viewer+ / customer-token (is_internal=false만 응답) |
| `PATCH` | `/tickets/:id/comments/:cid` | author + project admin |
| `DELETE` | `/tickets/:id/comments/:cid` | author + project admin |

응답 시 `is_internal=true` 댓글은 customer 컨텍스트에서 **반드시 필터**. 핸들러 단위가 아니라 store 레벨에서 caller-context를 받아 필터.

### 2.2 Magic-link / 고객 인증

| Method | Path | 설명 |
|--------|------|------|
| `POST` | `/auth/magic-link/request` | `{ email, ticket_id? }` → 메일 발송 |
| `POST` | `/auth/magic-link/exchange` | `{ token }` → short-lived access token (15m) + refresh |
| `POST` | `/auth/customer/signup` | ZITADEL self-signup 후 webhook 핸들 (creator 관계 승계) |

magic-link 토큰은 `magic_tokens` 테이블(§1.5) 사용. 단일 ticket scope (단일 ticket에 대한 read/comment만).

### 2.3 Webhook outbound

기존 `callback_url` 동작 유지. 추가 이벤트:
- `ticket.comment.added` (is_internal=false 한정)
- `ticket.public_token.issued`

### 2.4 SSE 필터

기존 `GET /api/v1/events`에 `?filter=ticket:<id>` 또는 `?filter=workspace:<wid>` 지원.
- customer-token 컨텍스트는 `filter=ticket:<own>`만 허용.
- workspace filter는 ws:viewer 이상만.

### 2.5 RAG 위임 헬퍼 (선택)

옵션 A — goquest가 gopedia를 호출하지 않고, frontend가 직접 gopedia RAG 호출 (default).
옵션 B — `POST /tickets/auto-resolve`로 ticket 생성과 RAG 답변 시도를 한 트랜잭션에 묶음.
> 1차 prototype은 옵션 A. B는 phase-2.

---

## 3. 인증 미들웨어 변경

### 3.1 API Key middleware

- `allowed_capabilities` 검사 추가. 호출 capability가 set에 없으면 403.
- `scope=public` 키는 `Origin` 헤더 화이트리스트 검사 (CORS 외에 미들웨어 단계에서도).
- rate-limit (`rate_limit_per_min`, `daily_quota`) 위반 시 429.

### 3.2 Customer-token middleware

- magic-link 교환 토큰을 `Authorization: Bearer <ct_...>`로 받음.
- 토큰 페이로드: `{ ticket_id, email, exp }`.
- 적용 capability: `ticket.read`(자기 것), `comment.create`(is_internal=false만), `comment.list`.

### 3.3 Identity unification

ZITADEL sub와 customer email이 같은 사람일 때:
- ZITADEL `email_verified=true` && goquest `tickets.requester_email` 매칭 → 자동 creator 관계 승계.
- 처리 위치: `internal/auth/customer.go`(신규).

---

## 4. 비즈니스 로직 추가

### 4.1 자동 라우팅

`POST /tickets` 후 즉시:
1. `domain` 미지정이면 **cogito**에 분류 위임 — **hermes inter-bot** 경로 사용 (KEDA interceptor proxy 경유).
   - capability: `plan.workflow` (auth: `read`)
   - host: `cogito.cogito.svc.cluster.local`
   - 응답의 `plan.steps[0].bot` 또는 `plan.summary`에서 domain 추출
2. 분류된 domain에 따라 `assignee_type=agent`, `assignee_id=hermes-<domain>` 자동 설정.
3. hermes agent가 답변 작성 후 → gardener 평가 → 통과/실패에 따라 상태 전이.

구현 위치: `internal/ticket/service.go` 안의 `OnCreated` hook + `internal/router/auto.go`(신규) + `internal/hermes/client.go`(신규, KEDA proxy 호출 래퍼).

```go
// internal/hermes/client.go (스케치)
req := ExecuteReq{
    TaskID:     uuid.NewString(),
    Capability: "plan.workflow",
    Payload:    map[string]any{"message": ticket.Description, "context": ticketCtx},
    Caller:     "goquest",
    AuthLevel:  "read",
}
// Host 헤더로 cogito.cogito.svc.cluster.local 지정해서 KEDA proxy로 POST
```

> **결정 (2026-05-11)**: HTTP/JWT 대신 inter-bot 채택. 근거 — 다른 bot 호출과 일관, JWT 라이프사이클 불필요, capability 권한 체계(`auth_level`) 재사용. 외부 디버깅 시에는 kubectl exec로 동일 요청 재현.

### 4.2 gardener 평가 게이트

- agent가 작성한 답변(comment, draft=true)은 즉시 고객에게 노출하지 않음.
- threshold는 **project 우선, workspace fallback** (결정 2026-05-11). 신규 테이블 필요:
  ```sql
  CREATE TABLE gardener_policy (
    workspace_id     UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    project_id       UUID REFERENCES projects(id) ON DELETE CASCADE,  -- NULL = workspace default
    threshold        NUMERIC(3,2) NOT NULL CHECK (threshold BETWEEN 0 AND 1),
    fallback_action  TEXT NOT NULL DEFAULT 'escalate-to-staff'
                          CHECK (fallback_action IN ('escalate-to-staff','auto-retry','dead-letter')),
    updated_by       TEXT NOT NULL,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (workspace_id, project_id)
  );
  CREATE UNIQUE INDEX gardener_policy_ws_default_uniq
    ON gardener_policy(workspace_id) WHERE project_id IS NULL;
  ```
- 조회 로직: `(ws,proj)` 행 → 없으면 `(ws, NULL)` 행 → 없으면 시스템 default(0.80).
- gardener score >= threshold → comment publish + ticket completed.
- 미달 → fallback_action 적용. 기본은 ticket reassign(`assignee_type=human`), draft 그대로 staff에게만 visible.
- admin 콘솔 (`goquest-ui /helpdesk/admin/ai-policy/gardener`)에서 CRUD.

### 4.3 SLA 기반 escalation

기존 SLA worker 확장:
- urgent customer 티켓이 `due_at` 30분 전 → `priority=urgent` 유지하되 staff 모두에게 SSE alert.
- `due_at` 초과 → 기본 `failed` 대신 customer 티켓은 자동 escalation(상위 group에 reassign + slack/Telegram alert).

### 4.4 외부 통지 (이메일)

- ticket 상태 전이 시 customer에게 이메일 (`requester_email`).
- SMTP는 별도 `internal/notify/email.go`(신규). lymphhub Vault에 SMTP credential 저장.
- 템플릿: `templates/email/ticket_received.{ko,en}.tmpl`, `ticket_replied.tmpl`, `ticket_completed.tmpl`.

---

## 5. 운영 / 보안

| 항목 | 내용 |
|------|------|
| Rate limit | API Key 단위 + IP 단위 두 층. Redis `INCR + EXPIRE` |
| 어뷰즈 차단 | requester_email 도메인 차단 리스트, 동일 IP 일일 티켓 상한 |
| Audit | workspace member 변경, role 변경, visibility 변경, API Key CRUD → `audit_log` 자동 기록 |
| CORS | `customer-support / web-inquiries` public key는 `toji.homes`, `gopedia.toji.homes` origin만 허용 |
| PII 마스킹 | 5년/3년 보존 후 `requester_email`, `requester_name` 익명화 잡 (cron) |

---

## 6. 신규 파일 (예상)

```
internal/migrate/sql/003_helpdesk.sql
internal/comment/model.go
internal/comment/store.go
internal/comment/store_pg.go
internal/comment/service.go
internal/handler/comment.go
internal/auth/magic.go               # magic-link 발급/검증
internal/auth/customer.go            # customer-token 미들웨어, ZITADEL 매핑
internal/notify/email.go             # SMTP, 템플릿 렌더
internal/router/auto.go              # cogito 위임, hermes 라우팅
internal/gardener/gate.go            # gardener score 게이트
internal/audit/log.go                # audit_log 헬퍼
templates/email/*.tmpl
```

## 7. 수정 파일 (예상)

```
internal/ticket/model.go             # channel, requester_*, public_token, locale, metadata
internal/ticket/store_pg.go          # 신규 컬럼, comment 조회시 caller-context 필터
internal/ticket/service.go           # OnCreated → 자동 라우팅 hook, gardener gate
internal/auth/apikey.go              # allowed_capabilities, rate limit, origin check
internal/queue/publisher.go          # ticket.comment.added, ticket.escalated 이벤트
internal/handler/sse.go              # ?filter=ticket:<id> | workspace:<wid>
internal/handler/ticket.go           # 신규 필드 응답 + caller-context
cmd/service/main.go                  # 신규 라우트, 미들웨어 등록
```

---

## 8. 단계별 우선순위

| Phase | 상태 | 항목 | 결과 | 머지 커밋 |
|-------|------|------|------|-----------|
| **H0** | ✅ 완료 | 003_helpdesk.sql + tickets 신규 컬럼 + ticket_comments | 데이터 모델 준비 | `2288b61` |
| **H1** | ✅ 완료 | comment CRUD + is_internal 필터 | 사람-사람 대화 가능 | `2288b61` |
| **H2** | ✅ 완료 | API Key scope/capability/origin/rate-limit 강화 + ticket_macros | public key 안전 노출 가능 | `5ff155f` |
| **H3** | ✅ 완료 | magic-link 발급/교환 + customer-token 미들웨어 + SSE filter | 비로그인 고객 상태 페이지 가능 | `02cc5d5` |
| **H4** | ✅ 완료 | SMTP notify + 템플릿 (magic-link/ticket-received/ticket-replied) | 이메일 발송 가능 | `02a40aa` |
| **H5** | ⚠ 부분 완료 | AI draft comments + gardener_score 컬럼 + publish 엔드포인트 | AI 1차 응대 데이터 레이어 준비 | `0d5084f` |
| **H6** | ⚠ 부분 완료 | audit_log table/handler + helpdesk KPI endpoint | 운영 가시성 | `3508da5` |
| **H7** | ✅ 완료 | PII 마스킹 daily cron (3y mask / 5y delete) | 컴플라이언스 | `831f0ec` |

### 8.1 Deferred — 외부 의존성 또는 후속 phase에서 별도 진행

| 항목 | 상태 | 사유 / 후속 작업 |
|------|------|-----------------|
| **H5: cogito 호출** | ⏸ 인터페이스만 | hermes inter-bot 통합은 cogito 운영 안정화 후. `ticket.OnCreated` hook은 비어 있음. |
| **H5: gardener 자동 평가** | ⏸ 컬럼만 | `gardener_score` 저장은 작동하나 실제 점수 계산은 gardener-gopedia 안정화 후 연결. |
| **H6: SLA escalation 규칙** | ⏸ 미구현 | 기존 SLA worker는 동작 중 (overdue 감지). 자동 reassign / Slack/Telegram alert는 후속. |
| **H6: handler-level audit Record() 호출** | ⏸ 인프라만 | `audit.Logger` 구현체는 준비됐고 `/audit` API도 동작. 워크스페이스 멤버 변경, API key CRUD 등 개별 핸들러에서 `Record()` 호출 추가는 follow-up. |
| **이메일 인바운드** | 🗓 2026-Q3 | 고객이 메일로 답글 → 티켓 자동 업데이트 (IMAP/webhook). |

---

## 9. 미해결 질문

- [x] ~~cogito 호출 인터페이스: HTTP(JWT) vs Hermes inter-bot (`/execute` capability).~~ → **결정(2026-05-11)**: hermes inter-bot, capability `plan.workflow` (auth: read), host `cogito.cogito.svc.cluster.local`. §4.1 참조.
- [x] ~~gardener threshold를 workspace 설정으로 둘지, project별로 둘지.~~ → **결정(2026-05-11)**: project 우선 + workspace fallback (`gardener_policy` 테이블). §4.2 + policy §5.2.1 참조.
- [x] ~~magic-link 토큰을 `tickets.public_token` 단일 칼럼에 둘지 vs 별도 `magic_tokens` 테이블.~~ → **결정(2026-05-11)**: 별도 `magic_tokens` 테이블. §1.5 참조.
- [ ] 이메일 인바운드 (고객이 메일로 답글) 지원 시점. (2026-Q3 검토)
