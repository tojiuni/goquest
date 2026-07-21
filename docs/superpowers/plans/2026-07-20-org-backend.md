# goquest org backend (goq-org-backend) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 티켓에 org 참조 축 2개(requester/assignee)를 추가하고, lymphhub JWT의 `org_id`/`org_role` claim으로 외부 회사(발주·수주) 사용자의 열람·재배정을 인가한다.

**Architecture:** org의 정본은 lymphhub — goquest는 JWT claim만 소비한다(org CRUD 없음). 인가는 **claim-at-request-time** 방식: 요청 시 JWT에서 읽은 `OrgCtx`(org_id, org_role)로 핸들러/미들웨어에서 직접 판정한다. SpiceDB에 org 멤버십을 동기화하지 않는다(정본이 외부라 동기화 채널이 없고, claim이 항상 더 신선함 — 스펙 §C의 "SpiceDB org 관계"를 이 방식으로 구현하는 것으로 조정, Epic 티켓에 기록). 기존 staff 경로(SpiceDB VisibleTicketIDs)는 불변.

**Tech Stack:** Go 1.25, chi, pgx, golang-jwt/v5, 기존 goquest 내부 패키지 (`internal/auth`, `internal/ticket`, `internal/handler`).

## Global Constraints

- 스펙: geneso `goquest/spec/org-work-requests.md` (GQT-SPC-20260720-01).
- 브랜치/커밋/PR에 `goq-org-backend` 포함 (goquest VCS auto-link).
- claim 이름은 스펙 확정값 그대로: `org_id`(UUID 문자열), `org_role`(`admin`|`member`). 그 외 값·파싱 실패 → org ctx 없음(기존 동작).
- `assignee_type`은 PG ENUM(001_init.sql) — `ALTER TYPE ... ADD VALUE`는 트랜잭션 안에서 못 도는 버전이 있으므로 **TEXT + CHECK로 전환**한다(003_helpdesk.sql의 channel과 동일 스타일).
- org 컬럼은 UUID NULL, **FK 없음** (정본이 lymphhub 외부).
- 클라이언트가 `requester_org_id`를 body로 위조할 수 없어야 한다 — 서버 스탬프 전용(`json:"-"`).
- 검증: `go build ./... && go test ./...` (pod에서 실행 가능). 각 태스크 끝마다 커밋.
- 평문 시크릿 금지.

## File Structure

- `internal/auth/jwt.go` — Claims 확장 + `OrgCtx` 파싱/헬퍼 (인증 축)
- `internal/migrate/sql/022_org.sql` — 스키마 (org 컬럼 2개 + assignee_type TEXT 전환)
- `internal/ticket/model.go` — Ticket/CreateTicketReq/UpdateTicketReq/ListTicketsFilter 필드
- `internal/ticket/store_pg.go` — insert/select/scan/ticketWhere
- `internal/ticket/org_where_test.go` — ticketWhere org scope 단위 테스트 (신규)
- `internal/ticket/service.go` — org 할당 검증
- `internal/auth/ticket_view.go` — per-ticket org 열람 게이트
- `internal/handler/ticket.go` — Create 스탬프, List 파라미터, OrgReassign 핸들러
- `internal/handler/comment.go` — org ctx는 internal 댓글 불가
- `cmd/service/main.go` — 라우트 등록

---

### Task 1: auth — org claim 파싱 + OrgCtx

**Files:**
- Modify: `internal/auth/jwt.go`
- Test: `internal/auth/jwt_test.go`

**Interfaces:**
- Consumes: 기존 `Claims`, `Middleware`, ctx 패턴 (`customer.go`의 `CustomerCtx` 스타일).
- Produces (이후 태스크가 사용):
  - `type OrgCtx struct { ID uuid.UUID; Role string }` — Role은 `"admin"` 또는 `"member"`
  - `func OrgFrom(r *http.Request) *OrgCtx` — org claim 없으면 nil
  - `func WithOrgCtx(ctx context.Context, o *OrgCtx) context.Context` — 테스트 훅
  - `func orgFromClaims(c *Claims) *OrgCtx` (내부)

- [ ] **Step 1: 실패하는 테스트 작성** — `internal/auth/jwt_test.go`에 추가:

```go
func TestOrgFromClaims(t *testing.T) {
	orgID := uuid.New()
	cases := []struct {
		name  string
		c     Claims
		want  *OrgCtx
	}{
		{"admin", Claims{OrgID: orgID.String(), OrgRole: "admin"}, &OrgCtx{ID: orgID, Role: "admin"}},
		{"member", Claims{OrgID: orgID.String(), OrgRole: "member"}, &OrgCtx{ID: orgID, Role: "member"}},
		// 알 수 없는 role은 최소 권한(member)으로 강등 — fail-closed.
		{"unknown role downgraded", Claims{OrgID: orgID.String(), OrgRole: "owner"}, &OrgCtx{ID: orgID, Role: "member"}},
		{"no org claim", Claims{}, nil},
		{"bad uuid", Claims{OrgID: "not-a-uuid", OrgRole: "admin"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := orgFromClaims(&tc.c)
			if (got == nil) != (tc.want == nil) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			if got != nil && (got.ID != tc.want.ID || got.Role != tc.want.Role) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestOrgFromRequest_RoundTrip(t *testing.T) {
	o := &OrgCtx{ID: uuid.New(), Role: "admin"}
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(WithOrgCtx(r.Context(), o))
	if got := OrgFrom(r); got == nil || got.ID != o.ID {
		t.Fatalf("OrgFrom = %v, want %v", got, o)
	}
}
```

- [ ] **Step 2: 실패 확인** — Run: `go test ./internal/auth/ -run TestOrg -v` / Expected: FAIL (`undefined: OrgCtx`)

- [ ] **Step 3: 구현** — `internal/auth/jwt.go`:

```go
// Claims에 필드 추가 (기존 struct에):
	OrgID   string `json:"org_id,omitempty"`
	OrgRole string `json:"org_role,omitempty"`

// 파일 하단에 추가:
const orgCtxKey contextKey = "org_ctx"

// OrgCtx carries the caller's lymphhub organization claim. Present only for
// external-org users; staff tokens have no org claim. Role is "admin" or
// "member" — anything else is downgraded to member (fail-closed).
type OrgCtx struct {
	ID   uuid.UUID
	Role string
}

func (o *OrgCtx) IsAdmin() bool { return o != nil && o.Role == "admin" }

func orgFromClaims(c *Claims) *OrgCtx {
	if c.OrgID == "" {
		return nil
	}
	id, err := uuid.Parse(c.OrgID)
	if err != nil {
		return nil
	}
	role := c.OrgRole
	if role != "admin" {
		role = "member"
	}
	return &OrgCtx{ID: id, Role: role}
}

func WithOrgCtx(ctx context.Context, o *OrgCtx) context.Context {
	return context.WithValue(ctx, orgCtxKey, o)
}

func OrgFrom(r *http.Request) *OrgCtx {
	v, _ := r.Context().Value(orgCtxKey).(*OrgCtx)
	return v
}
```

`Middleware` 내 JWT 파싱 성공 지점(65행 부근) 수정:

```go
			ctx := context.WithValue(r.Context(), requesterKey, requesterFromClaims(claims))
			if o := orgFromClaims(claims); o != nil {
				ctx = context.WithValue(ctx, orgCtxKey, o)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
```

import에 `"github.com/google/uuid"` 추가.

- [ ] **Step 4: 통과 확인** — Run: `go test ./internal/auth/ -v` / Expected: 전체 PASS (기존 테스트 회귀 포함)

- [ ] **Step 5: Commit**

```bash
git add internal/auth/jwt.go internal/auth/jwt_test.go
git commit -m "feat(auth): parse lymphhub org_id/org_role claims into OrgCtx (goq-org-backend)"
```

---

### Task 2: 스키마 + 모델 + store 컬럼

**Files:**
- Create: `internal/migrate/sql/022_org.sql`
- Modify: `internal/ticket/model.go`, `internal/ticket/store_pg.go`

**Interfaces:**
- Produces:
  - `Ticket.RequesterOrgID *uuid.UUID` / `Ticket.AssigneeOrgID *uuid.UUID` (json `requester_org_id`/`assignee_org_id`)
  - `CreateTicketReq.RequesterOrgID *uuid.UUID` (json:"-" — 서버 스탬프 전용)
  - `UpdateTicketReq.AssigneeOrgID *uuid.UUID` (json `assignee_org_id`)
  - `AssigneeOrg AssigneeType = "org"`
  - DB: `tickets.requester_org_id`, `tickets.assignee_org_id` (UUID NULL) + 부분 인덱스

- [ ] **Step 1: 마이그레이션 작성** — `internal/migrate/sql/022_org.sql`:

```sql
-- 022_org.sql — goq-org-backend: org reference axes + assignee_type 'org'.
-- Idempotent (IF NOT EXISTS / catalog guards), spec GQT-SPC-20260720-01.

-- assignee_type ENUM('human','agent') → TEXT + CHECK so 'org' can be added
-- transactionally (channel in 003_helpdesk.sql uses the same style).
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_type WHERE typname = 'assignee_type') THEN
    ALTER TABLE tickets ALTER COLUMN assignee_type TYPE TEXT
      USING assignee_type::TEXT;
    ALTER TABLE tickets ALTER COLUMN assignee_type SET DEFAULT 'human';
    DROP TYPE assignee_type;
  END IF;
END $$;

ALTER TABLE tickets DROP CONSTRAINT IF EXISTS tickets_assignee_type_check;
ALTER TABLE tickets ADD CONSTRAINT tickets_assignee_type_check
  CHECK (assignee_type IN ('human','agent','org'));

-- Org reference axes. UUIDs refer to lymphhub organizations — no FK, the
-- source of truth is external.
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS requester_org_id UUID;
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS assignee_org_id  UUID;

CREATE INDEX IF NOT EXISTS idx_tickets_requester_org
  ON tickets(requester_org_id) WHERE requester_org_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_tickets_assignee_org
  ON tickets(assignee_org_id) WHERE assignee_org_id IS NOT NULL;
```

- [ ] **Step 2: 모델 필드 추가** — `internal/ticket/model.go`:

```go
// AssigneeType 상수에 추가:
	// AssigneeOrg assigns the ticket to an external lymphhub organization; the
	// org's admin re-assigns internally (assignee_id then holds the member).
	AssigneeOrg AssigneeType = "org"

// Ticket struct — RequesterID 아래에 추가:
	// Org reference axes (lymphhub org UUIDs; source of truth is external).
	RequesterOrgID *uuid.UUID `json:"requester_org_id,omitempty"`
	AssigneeOrgID  *uuid.UUID `json:"assignee_org_id,omitempty"`

// CreateTicketReq — 서버 스탬프 전용 (클라이언트 body로 위조 불가):
	RequesterOrgID *uuid.UUID `json:"-"`

// UpdateTicketReq — AssigneeType 아래에 추가:
	AssigneeOrgID *uuid.UUID `json:"assignee_org_id,omitempty"`
```

- [ ] **Step 3: store 반영** — `internal/ticket/store_pg.go`:

Create INSERT: 컬럼 목록에 `requester_org_id, assignee_org_id` 추가, VALUES에 `$26,$27` 추가, 인자 끝에 `t.RequesterOrgID, t.AssigneeOrgID` 추가.

`ticketSelectSQL`의 t.* 프로젝션에 `t.requester_org_id, t.assignee_org_id` 추가 (기존 `t.completed_at, t.cancellation_note` 뒤, 파생 필드 앞 — scanTicket 순서와 일치시킬 것).

`scanTicket`: Scan 인자에서 `&t.CompletedAt, &cancellationNote,` 뒤에 `&t.RequesterOrgID, &t.AssigneeOrgID,` 추가 (둘 다 `*uuid.UUID`라 nullable 직접 스캔 가능).

`Update`(store): `req.AssigneeOrgID != nil`이면 `assignee_org_id=$n` SET 절 추가 — 기존 AssigneeID/AssigneeType 처리와 같은 동적 SET 패턴을 따른다.

- [ ] **Step 4: 빌드·회귀 확인** — Run: `go build ./... && go test ./internal/ticket/ ./internal/migrate/`
Expected: PASS (scanTicket 순서 불일치 시 여기서 즉시 깨짐)

- [ ] **Step 5: Commit**

```bash
git add internal/migrate/sql/022_org.sql internal/ticket/model.go internal/ticket/store_pg.go
git commit -m "feat(ticket): org reference columns + assignee_type 'org' (goq-org-backend)"
```

---

### Task 3: ticketWhere org scope + List 파라미터

**Files:**
- Modify: `internal/ticket/model.go`, `internal/ticket/store_pg.go:396` (ticketWhere), `internal/handler/ticket.go:194` (List)
- Test: `internal/ticket/org_where_test.go` (신규)

**Interfaces:**
- Produces:
  - `type OrgScope struct { OrgID uuid.UUID; Role string; CallerID string; Side string }` — Side는 `""`(양쪽)|`"requester"`|`"assignee"`
  - `ListTicketsFilter.OrgScope *OrgScope`
  - REST: `GET /tickets?org_side=requester|assignee` (org claim 보유 호출자에게만 의미)

- [ ] **Step 1: 실패하는 테스트 작성** — `internal/ticket/org_where_test.go`:

```go
package ticket

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestTicketWhereOrgScope(t *testing.T) {
	org := uuid.New()

	t.Run("admin sees both sides of own org", func(t *testing.T) {
		where, args, _ := ticketWhere(ListTicketsFilter{
			OrgScope: &OrgScope{OrgID: org, Role: "admin", CallerID: "a@corp.com"},
		})
		if !strings.Contains(where, "t.requester_org_id=$1 OR t.assignee_org_id=$1") {
			t.Fatalf("admin clause missing: %s", where)
		}
		if len(args) != 1 || args[0] != org {
			t.Fatalf("args = %v", args)
		}
	})

	t.Run("member sees only own tickets within org", func(t *testing.T) {
		where, args, _ := ticketWhere(ListTicketsFilter{
			OrgScope: &OrgScope{OrgID: org, Role: "member", CallerID: "m@corp.com"},
		})
		if !strings.Contains(where, "t.requester_org_id=$1 AND t.requester_id=$2") ||
			!strings.Contains(where, "t.assignee_org_id=$1 AND t.assignee_id=$2") {
			t.Fatalf("member clause missing: %s", where)
		}
		if len(args) != 2 || args[1] != "m@corp.com" {
			t.Fatalf("args = %v", args)
		}
	})

	t.Run("side narrows admin to requester axis", func(t *testing.T) {
		where, _, _ := ticketWhere(ListTicketsFilter{
			OrgScope: &OrgScope{OrgID: org, Role: "admin", CallerID: "a@corp.com", Side: "requester"},
		})
		if strings.Contains(where, "assignee_org_id") {
			t.Fatalf("side=requester must not touch assignee axis: %s", where)
		}
	})
}
```

- [ ] **Step 2: 실패 확인** — Run: `go test ./internal/ticket/ -run TestTicketWhereOrgScope -v` / Expected: FAIL (`undefined: OrgScope`)

- [ ] **Step 3: 구현** — `model.go`의 ListTicketsFilter 위에:

```go
// OrgScope restricts a listing to what an external-org caller may see,
// derived from JWT org claims (never from client input). Role semantics:
// admin → every ticket where the org is requester or assignee side;
// member → only tickets the caller personally requested / is assigned.
type OrgScope struct {
	OrgID    uuid.UUID
	Role     string // "admin" | "member"
	CallerID string
	Side     string // "" | "requester" | "assignee"
}
```

ListTicketsFilter에 `OrgScope *OrgScope` 필드 추가. `ticketWhere`의 Query 처리 뒤에:

```go
	if f.OrgScope != nil {
		o := f.OrgScope
		orgArg := i
		args = append(args, o.OrgID)
		i++
		if o.Role == "admin" {
			switch o.Side {
			case "requester":
				where += fmt.Sprintf(" AND t.requester_org_id=$%d", orgArg)
			case "assignee":
				where += fmt.Sprintf(" AND t.assignee_org_id=$%d", orgArg)
			default:
				where += fmt.Sprintf(" AND (t.requester_org_id=$%d OR t.assignee_org_id=$%d)", orgArg, orgArg)
			}
		} else {
			callerArg := i
			args = append(args, o.CallerID)
			i++
			switch o.Side {
			case "requester":
				where += fmt.Sprintf(" AND (t.requester_org_id=$%d AND t.requester_id=$%d)", orgArg, callerArg)
			case "assignee":
				where += fmt.Sprintf(" AND (t.assignee_org_id=$%d AND t.assignee_id=$%d)", orgArg, callerArg)
			default:
				where += fmt.Sprintf(" AND ((t.requester_org_id=$%d AND t.requester_id=$%d) OR (t.assignee_org_id=$%d AND t.assignee_id=$%d))",
					orgArg, callerArg, orgArg, callerArg)
			}
		}
	}
```

- [ ] **Step 4: 통과 확인** — Run: `go test ./internal/ticket/ -run TestTicketWhereOrgScope -v` / Expected: PASS

- [ ] **Step 5: List 핸들러 배선** — `internal/handler/ticket.go` List, 필터 파싱 뒤·VisibleTicketIDs 계산 앞에:

```go
	// External-org callers are scoped by their JWT org claim — never by query
	// params. Staff tokens carry no org claim and keep the SpiceDB path.
	if o := auth.OrgFrom(r); o != nil {
		f.OrgScope = &ticket.OrgScope{
			OrgID:    o.ID,
			Role:     o.Role,
			CallerID: auth.RequesterID(r),
			Side:     q.Get("org_side"),
		}
	}
```

org 호출자는 SpiceDB VisibleFilter 계산을 건너뛴다(있다면 `f.OrgScope == nil`일 때만 기존 경로 실행하도록 조건 감싸기).

- [ ] **Step 6: 전체 확인 + Commit** — Run: `go build ./... && go test ./...` / Expected: PASS

```bash
git add internal/ticket/ internal/handler/ticket.go
git commit -m "feat(ticket): org-scoped listing — OrgScope filter + org_side param (goq-org-backend)"
```

---

### Task 4: Create 스탬프 + per-ticket 열람 게이트

**Files:**
- Modify: `internal/handler/ticket.go:69` (Create), `internal/auth/ticket_view.go` (RequireTicketView), `internal/ticket/service.go` (Create에서 RequesterOrgID 전달)
- Test: `internal/auth/ticket_view_test.go`

**Interfaces:**
- Consumes: Task 1 `OrgFrom`/`WithOrgCtx`, Task 2 `Ticket.RequesterOrgID`/`AssigneeOrgID`, `CreateTicketReq.RequesterOrgID`.
- Produces: org 호출자가 만든 티켓은 `requester_org_id`가 자동 스탬프됨; `/tickets/{id}/*`는 org 규칙으로 통과/403.

- [ ] **Step 1: 실패하는 게이트 테스트 작성** — `internal/auth/ticket_view_test.go`에 추가 (기존 테스트의 fake getter/checker 헬퍼 재사용):

```go
func TestRequireTicketView_OrgRules(t *testing.T) {
	org, otherOrg := uuid.New(), uuid.New()
	pid := uuid.New()
	tk := &ticket.Ticket{ID: uuid.New(), ProjectID: &pid,
		RequesterID: "m@corp.com", RequesterOrgID: &org}

	cases := []struct {
		name     string
		orgCtx   *OrgCtx
		caller   string
		wantCode int
	}{
		{"org admin views org ticket", &OrgCtx{ID: org, Role: "admin"}, "boss@corp.com", http.StatusOK},
		{"org member views own ticket", &OrgCtx{ID: org, Role: "member"}, "m@corp.com", http.StatusOK},
		{"org member blocked from colleague ticket", &OrgCtx{ID: org, Role: "member"}, "other@corp.com", http.StatusForbidden},
		{"foreign org blocked", &OrgCtx{ID: otherOrg, Role: "admin"}, "x@rival.com", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// getter returns tk; checker must NOT be consulted for org callers —
			// pass a checker that fails the test if called.
			rec := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/tickets/"+tk.ID.String(), nil)
			ctx := WithRequesterID(r.Context(), tc.caller)
			ctx = WithOrgCtx(ctx, tc.orgCtx)
			r = r.WithContext(ctx)
			// chi URL param 주입은 기존 테스트 파일의 헬퍼 패턴을 그대로 사용.
			handlerUnderTest(tk).ServeHTTP(rec, withChiID(r, tk.ID.String()))
			if rec.Code != tc.wantCode {
				t.Fatalf("%s: code=%d want %d", tc.name, rec.Code, tc.wantCode)
			}
		})
	}
}
```

(`handlerUnderTest`/`withChiID`는 기존 `ticket_view_test.go`가 이미 쓰는 구성 방식에 맞춰 — 파일 열어 기존 헬퍼명 그대로 재사용. 없으면 그 파일의 기존 테스트가 만드는 방식대로 인라인 구성.)

- [ ] **Step 2: 실패 확인** — Run: `go test ./internal/auth/ -run TestRequireTicketView_Org -v` / Expected: FAIL (403 아님 200 등)

- [ ] **Step 3: 게이트 구현** — `internal/auth/ticket_view.go`의 customer 분기 뒤에 org 분기 추가:

```go
			} else if o := OrgFrom(r); o != nil {
				// External-org caller: claim-based rule, SpiceDB not consulted.
				// admin → any ticket on the org's requester/assignee axis;
				// member → only tickets they personally requested / are assigned.
				caller := RequesterID(r)
				sameOrg := func(x *uuid.UUID) bool { return x != nil && *x == o.ID }
				allowed := false
				if o.IsAdmin() {
					allowed = sameOrg(t.RequesterOrgID) || sameOrg(t.AssigneeOrgID)
				} else {
					allowed = (sameOrg(t.RequesterOrgID) && t.RequesterID == caller) ||
						(sameOrg(t.AssigneeOrgID) && t.AssigneeID == caller)
				}
				if !allowed {
					http.Error(w, "forbidden", http.StatusForbidden)
					return
				}
			} else if t.ProjectID != nil {
```

- [ ] **Step 4: Create 스탬프** — `internal/handler/ticket.go` Create, `requesterID := auth.RequesterID(r)` 뒤에:

```go
	// External-org callers get their org stamped server-side; the json:"-" tag
	// on the field makes client-supplied values unreachable.
	if o := auth.OrgFrom(r); o != nil {
		req.RequesterOrgID = &o.ID
	}
```

`internal/ticket/service.go` Create에서 Ticket 조립 시 `RequesterOrgID: req.RequesterOrgID` 전달 (조립 지점을 열어 기존 필드 나열에 한 줄 추가).

- [ ] **Step 5: 통과 확인 + Commit** — Run: `go test ./internal/auth/ ./internal/ticket/ ./internal/handler/ -v` → PASS, 이후 `go test ./...` → PASS

```bash
git add internal/auth/ internal/handler/ticket.go internal/ticket/service.go
git commit -m "feat(auth): org view gate + requester org stamp on create (goq-org-backend)"
```

---

### Task 5: org 할당 + OrgReassign 엔드포인트 (Stage 2)

**Files:**
- Modify: `internal/ticket/service.go` (Update 검증), `internal/handler/ticket.go` (OrgReassign), `cmd/service/main.go` (라우트)
- Test: `internal/ticket/service_test.go`

**Interfaces:**
- Consumes: Task 2 `UpdateTicketReq.AssigneeOrgID`, `AssigneeOrg`; Task 1 `OrgFrom`.
- Produces:
  - staff: `PATCH /tickets/{id}` body `{"assignee_type":"org","assignee_org_id":"<uuid>"}` → org 할당
  - org admin: `POST /tickets/{id}/org-reassign` body `{"assignee_id":"member@corp.com"}` → 자사 멤버 재배정 (assignee_type='org' 유지, 상태머신 불변)

- [ ] **Step 1: 실패하는 서비스 테스트 작성** — `internal/ticket/service_test.go`에 추가 (기존 fake store 패턴 재사용):

```go
func TestUpdateOrgAssignmentValidation(t *testing.T) {
	svc, _ := newTestService(t) // 기존 테스트 파일의 서비스 구성 헬퍼 재사용
	at := AssigneeOrg
	_, err := svc.Update(context.Background(), existingTicketID(t, svc),
		UpdateTicketReq{AssigneeType: &at}) // assignee_org_id 없음
	if err == nil || !strings.Contains(err.Error(), "assignee_org_id required") {
		t.Fatalf("want assignee_org_id required error, got %v", err)
	}
}
```

(헬퍼명은 기존 service_test.go의 실제 구성 방식에 맞춰 조정 — 서비스 생성·티켓 시드 방식을 그 파일에서 그대로 복사.)

- [ ] **Step 2: 실패 확인** — Run: `go test ./internal/ticket/ -run TestUpdateOrgAssignment -v` / Expected: FAIL

- [ ] **Step 3: 검증 구현** — `internal/ticket/service.go` Update 진입부에:

```go
	if req.AssigneeType != nil && *req.AssigneeType == AssigneeOrg && req.AssigneeOrgID == nil {
		return nil, errors.New("assignee_org_id required when assignee_type is org")
	}
```

- [ ] **Step 4: OrgReassign 핸들러** — `internal/handler/ticket.go`에 추가:

```go
// OrgReassign lets an admin of the ticket's assignee org route the work to one
// of their own members. Only assignee_id changes; assignee_type stays "org"
// and the status machine is untouched. POST /tickets/{id}/org-reassign
func (h *TicketHandler) OrgReassign(w http.ResponseWriter, r *http.Request) {
	t := auth.TicketFrom(r) // RequireTicketView가 스태시
	o := auth.OrgFrom(r)
	if t == nil || o == nil || !o.IsAdmin() ||
		t.AssigneeOrgID == nil || *t.AssigneeOrgID != o.ID {
		writeError(w, http.StatusForbidden, "org admin of the assignee org required")
		return
	}
	var body struct {
		AssigneeID string `json:"assignee_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.AssigneeID == "" {
		writeError(w, http.StatusBadRequest, "assignee_id required")
		return
	}
	updated, err := h.svc.Update(r.Context(), t.ID, ticket.UpdateTicketReq{
		AssigneeID: &body.AssigneeID,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}
```

- [ ] **Step 5: 라우트 등록** — `cmd/service/main.go`의 `/tickets/{id}` 그룹(RequireTicketView가 걸린 서브라우터)에:

```go
			r.Post("/org-reassign", ticketH.OrgReassign)
```

- [ ] **Step 6: 전체 확인 + Commit** — Run: `go build ./... && go test ./...` / Expected: PASS

```bash
git add internal/ticket/ internal/handler/ticket.go cmd/service/main.go
git commit -m "feat(ticket): org assignment validation + org admin reassign endpoint (goq-org-backend)"
```

---

### Task 6: org 컨텍스트 internal 댓글 차단 + 마무리 검증

**Files:**
- Modify: `internal/handler/comment.go` (CanSeeInternal)
- Test: `internal/handler/comment_test.go` (기존 파일에 케이스 추가)

**Interfaces:**
- Consumes: Task 1 `OrgFrom`.
- Produces: org claim 보유 호출자는 `is_internal=true` 댓글·draft를 못 본다 (customer 컨텍스트와 동급).

- [ ] **Step 1: 실패하는 테스트 작성** — `internal/handler/comment_test.go`에 추가:

```go
func TestCanSeeInternal_OrgContextDenied(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(auth.WithRequesterID(r.Context(), "m@corp.com"))
	r = r.WithContext(auth.WithOrgCtx(r.Context(), &auth.OrgCtx{ID: uuid.New(), Role: "admin"}))
	if CanSeeInternal(r) {
		t.Fatal("org-context caller must not see internal comments")
	}
}
```

- [ ] **Step 2: 실패 확인** — Run: `go test ./internal/handler/ -run TestCanSeeInternal_Org -v` / Expected: FAIL

- [ ] **Step 3: 구현** — `internal/handler/comment.go` CanSeeInternal에 org 분기 추가 (customer 분기와 나란히):

```go
	if auth.OrgFrom(r) != nil {
		// External-org callers are outsiders to staff conversation, same as
		// magic-link customers.
		return false
	}
```

- [ ] **Step 4: 통과 + 전체 회귀** — Run: `go test ./internal/handler/ -v` → PASS, `go build ./... && go test ./...` → 전체 PASS

- [ ] **Step 5: Commit + PR**

```bash
git add internal/handler/comment.go internal/handler/comment_test.go
git commit -m "feat(comment): org-context callers cannot see internal comments (goq-org-backend)"
```

PR 제목에 `goq-org-backend` 포함. PR 본문에 e2e 3종(타 org 403 / admin·member 차등 / is_internal 미노출)이 어느 테스트로 커버되는지 명시 (Task 4 게이트 테스트 / Task 3+4 / Task 6).

---

## 남기는 것 (이 플랜 범위 밖)

- SpiceDB `org` definition·관계 기록: 인가는 claim-at-request-time으로 충족 — 멤버십 동기화 채널이 생기면(lymphhub 이벤트) 별도 티켓.
- SSE org 필터(`?filter=org:<id>`): 포털 Stage 1은 목록 새로고침으로 충분 — goq-ui-org-portal에서 필요해지면 별도.
- 마이그레이션 라이브 적용·배포: 머지 후 기존 배포 파이프라인(마이그레이션 자동 실행) 경로.
