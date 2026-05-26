# 중앙 인가 Core 슬라이스 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** goquest 티켓 읽기 경로에 generic SpiceDB 인가 primitive(`Check`/`VisibleResourceIDs`)와 단일 그룹 미들웨어(`RequireTicketView`)를 적용해 누출 벡터(Get/subtasks/attempts/comments)·List 페이지네이션·principal(customer-token) 문제를 한 번에 닫는다.

**Architecture:** `internal/spicedb`에 도메인 무관 primitive 2개를 추가하고, `internal/auth`에 `/tickets/{id}` 그룹용 chi 미들웨어를 둔다. 미들웨어가 principal(OIDC=SpiceDB 체크 / customer-token=ticket-바운드 capability)을 통일하고 티켓을 ctx에 주입한다. List/ListSubtasks는 per-ticket 루프를 `VisibleResourceIDs`(LookupResources) 기반 가시성 필터로 교체한다. shadow(NoopChecker)에서는 전부 무변경.

**Tech Stack:** Go, chi v5, authzed-go v1.9.0 (SpiceDB), pgx/v5, uuid.

**Spec:** `docs/superpowers/specs/2026-05-27-authz-core-slice-design.md`

---

## 핵심 설계 결정 (구현 전 반드시 숙지)

- **가시성 2-모드 문제:** 티켓은 `visibility='project'`(→ `can_view_project`)와 `visibility='restricted'`(→ `can_view_restricted`)로 나뉜다. 단일 LookupResources(`can_view_project`)만 쓰면 프로젝트 멤버에게 restricted 티켓이 새어나간다. 따라서 List는 **두 번** LookupResources를 호출(project 집합 + restricted 집합)하고, SQL에서 `(visibility='project' AND id=ANY(proj)) OR (visibility='restricted' AND id=ANY(restr))`로 결합한다 → `total`·페이지네이션 정확.
- **shadow 무변경 규약:** NoopChecker의 `VisibleResourceIDs`는 `(nil, unrestricted=true, nil)`을 반환한다. 호출부는 `unrestricted`면 필터를 적용하지 않는다(=오늘과 동일).
- **fail-closed:** SpiceDB 체크 에러는 거부(미들웨어 403 흐름) 또는 500. 기존 Get의 fail-open은 미들웨어로 흡수되며 fail-closed로 통일.
- **resource id는 평문 UUID:** LookupResources가 돌려주는 ticket id는 인코딩되지 않은 UUID 문자열이다. 인코딩되는 것은 **subject(user)** 뿐(`subjectID`).
- **legacy 티켓(project 없음):** `ProjectID == nil`이면 SpiceDB 게이트를 건너뛴다(기존 Get 동작 보존).
- **mutating 라우트:** 본 슬라이스는 **view 게이트**만 추가한다. Update/Cancel/Approve에 manage 강제는 비범위(오늘도 게이트 없음 → 회귀 아님). 후속.

---

## Task 1: generic 인가 primitive (`Check` + `VisibleResourceIDs`)

**Files:**
- Modify: `internal/spicedb/checker.go`
- Modify: `internal/spicedb/noop.go`
- Modify: `internal/spicedb/interfaces.go`
- Test: `internal/spicedb/primitive_test.go` (create), `internal/spicedb/noop_test.go` (modify)

- [ ] **Step 1: Noop primitive 테스트 작성 (실패)**

`internal/spicedb/noop_test.go`의 `TestNoopChecker_AlwaysPermits` 함수 끝(닫는 `}` 직전)에 추가:

```go
	ok, err = ch.Check(ctx, spicedb.TypeTicket, id.String(), "can_view_project", "user1")
	if err != nil || !ok {
		t.Errorf("Check: want (true, nil), got (%v, %v)", ok, err)
	}

	ids, unrestricted, err := ch.VisibleResourceIDs(ctx, spicedb.TypeTicket, "can_view_project", "user1")
	if err != nil {
		t.Errorf("VisibleResourceIDs: unexpected err %v", err)
	}
	if !unrestricted {
		t.Error("VisibleResourceIDs: NoopChecker must report unrestricted=true (shadow no-op)")
	}
	if ids != nil {
		t.Errorf("VisibleResourceIDs: want nil ids, got %v", ids)
	}
```

- [ ] **Step 2: 테스트 실패 확인**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go test ./internal/spicedb/ -run TestNoopChecker_AlwaysPermits 2>&1 | head`
Expected: 컴파일 실패 — `ch.Check undefined` / `ch.VisibleResourceIDs undefined`.

- [ ] **Step 3: 인터페이스에 메서드 추가**

`internal/spicedb/interfaces.go`의 `PermissionChecker` 인터페이스에 추가:

```go
// PermissionChecker checks fine-grained access.
type PermissionChecker interface {
	CanViewTicket(ctx context.Context, ticketID uuid.UUID, userID string, visibility VisibilityMode) (bool, error)
	CanManageTicket(ctx context.Context, ticketID uuid.UUID, userID string) (bool, error)
	CanViewProject(ctx context.Context, projectID uuid.UUID, userID string) (bool, error)
	// Check is the generic single-resource primitive: does userID hold `permission`
	// on objType:objID? (user subject is encoded centrally.)
	Check(ctx context.Context, objType, objID, permission, userID string) (bool, error)
	// VisibleResourceIDs returns the objType ids on which userID holds `permission`,
	// via LookupResources. unrestricted=true signals "no restriction" (NoopChecker /
	// shadow) so callers skip filtering.
	VisibleResourceIDs(ctx context.Context, objType, permission, userID string) (ids []string, unrestricted bool, err error)
}
```

- [ ] **Step 4: NoopChecker 구현**

`internal/spicedb/noop.go`의 `NoopChecker` 메서드들(`CanViewProject` 뒤) 다음에 추가:

```go
func (n *NoopChecker) Check(_ context.Context, _, _, _, _ string) (bool, error) {
	return true, nil
}
func (n *NoopChecker) VisibleResourceIDs(_ context.Context, _, _, _ string) ([]string, bool, error) {
	return nil, true, nil
}
```

- [ ] **Step 5: Checker 구현**

`internal/spicedb/checker.go` 상단 import에 `"errors"`, `"io"` 추가(기존 `"context"`, `"fmt"` 옆). 그리고 파일 끝(마지막 `check` 함수 뒤)에 추가:

```go
// Check is the generic single-resource primitive. user subject is encoded by check().
func (ch *Checker) Check(ctx context.Context, objType, objID, permission, userID string) (bool, error) {
	return ch.check(ctx, objType, objID, permission, TypeUser, userID)
}

// VisibleResourceIDs streams LookupResources and collects the ids on which userID
// holds permission. A live Checker is always restricted (unrestricted=false).
func (ch *Checker) VisibleResourceIDs(ctx context.Context, objType, permission, userID string) ([]string, bool, error) {
	stream, err := ch.c.inner.LookupResources(ctx, &v1.LookupResourcesRequest{
		ResourceObjectType: objType,
		Permission:         permission,
		Subject: &v1.SubjectReference{
			Object: &v1.ObjectReference{ObjectType: TypeUser, ObjectId: subjectID(userID)},
		},
	})
	if err != nil {
		return nil, false, fmt.Errorf("spicedb lookup %s %s: %w", objType, permission, err)
	}
	var ids []string
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, false, fmt.Errorf("spicedb lookup recv %s %s: %w", objType, permission, err)
		}
		ids = append(ids, resp.ResourceObjectId)
	}
	return ids, false, nil
}
```

- [ ] **Step 6: 테스트 통과 + 전체 빌드 확인**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go test ./internal/spicedb/ 2>&1 | tail -5 && go build ./...`
Expected: spicedb 테스트 PASS, 빌드 성공.

- [ ] **Step 7: Commit**

```bash
cd /Users/dong-hoshin/Documents/dev/goquest
git add internal/spicedb/
git commit -m "feat(spicedb): add generic Check + VisibleResourceIDs primitives"
```

---

## Task 2: ListTicketsFilter 가시성 필터 + store SQL

**Files:**
- Modify: `internal/ticket/model.go:177-192` (ListTicketsFilter)
- Modify: `internal/ticket/store_pg.go:303-361` (List WHERE 절)
- Test: `internal/ticket/store_visible_test.go` (create)

- [ ] **Step 1: 필터 타입 정의 (테스트보다 먼저 — 타입 필요)**

`internal/ticket/model.go`의 `ListTicketsFilter` 바로 위에 새 타입 추가:

```go
// VisibleTicketIDs carries the caller's SpiceDB-visible ticket ids, split by the
// permission that granted them. Set only under enforce; nil means "no visibility
// restriction" (shadow). Honors per-ticket visibility: project-visibility tickets
// must be in Project, restricted ones in Restricted.
type VisibleTicketIDs struct {
	Project    []uuid.UUID
	Restricted []uuid.UUID
}
```

그리고 `ListTicketsFilter` 구조체에 필드 추가(`Offset int` 다음 줄):

```go
	VisibleFilter *VisibleTicketIDs
```

- [ ] **Step 2: store 필터 테스트 작성 (실패)**

`internal/ticket/store_visible_test.go` 생성. 이 테스트는 fake가 아니라 실제 pg가 필요하므로, 기존 store 테스트의 셋업 헬퍼를 따른다. 먼저 기존 패턴 확인:

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && ls internal/ticket/*_test.go && grep -l "pgStore\|newTestStore\|testPool" internal/ticket/*_test.go`

기존 store 통합 테스트 헬퍼가 **있으면** 그것을 재사용해 다음 케이스를 작성:
- project 티켓 P1, restricted 티켓 R1을 삽입.
- `VisibleFilter{Project:[P1], Restricted:[]}` → 결과에 P1만, total=1.
- `VisibleFilter{Project:[P1], Restricted:[R1]}` → P1,R1 모두, total=2.
- `VisibleFilter{Project:[], Restricted:[]}` → 빈 결과, total=0.

기존 store 통합 테스트 헬퍼가 **없으면**(전부 service-level fake store만 존재) 이 SQL 분기는 Task 5의 핸들러 테스트(stub checker)로 커버되므로 본 step의 단위 테스트는 생략하고 Step 3으로 진행한다. (그 경우 이 파일은 생성하지 않는다.)

- [ ] **Step 3: store_pg.List에 WHERE 절 추가**

`internal/ticket/store_pg.go`의 `List` 함수에서 `if f.TopLevelOnly {` 블록 바로 다음(`countQuery :=` 직전)에 추가:

```go
	if f.VisibleFilter != nil {
		// enforce mode: restrict to SpiceDB-visible tickets, honoring per-ticket
		// visibility. Empty arrays → ANY(empty) is false → ticket excluded.
		where += fmt.Sprintf(
			" AND ((t.visibility='project' AND t.id = ANY($%d)) OR (t.visibility='restricted' AND t.id = ANY($%d)))",
			i, i+1)
		args = append(args, f.VisibleFilter.Project, f.VisibleFilter.Restricted)
		i += 2
	}
```

`f.VisibleFilter.Project`/`Restricted`가 nil일 때 `ANY(NULL)`이 되지 않도록, 호출부(Task 5)에서 **항상 non-nil 슬라이스**(빈 슬라이스 허용)를 넣는다. 본 step에서는 코드만 추가.

- [ ] **Step 4: 빌드 + (테스트 작성했으면) 통과 확인**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go build ./... && go test ./internal/ticket/ 2>&1 | tail -5`
Expected: 빌드 성공. (Step 2에서 통합 테스트 작성 시 PASS; pg 미가용 환경이면 해당 테스트는 skip 처리하고 그 사실을 보고.)

- [ ] **Step 5: Commit**

```bash
cd /Users/dong-hoshin/Documents/dev/goquest
git add internal/ticket/
git commit -m "feat(ticket): visibility-aware id filter in List store query"
```

---

## Task 3: `RequireTicketView` 미들웨어 (auth 패키지)

**Files:**
- Create: `internal/auth/ticket_view.go`
- Test: `internal/auth/ticket_view_test.go`

- [ ] **Step 1: 미들웨어 테스트 작성 (실패)**

`internal/auth/ticket_view_test.go` 생성:

```go
package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/auth"
	"github.com/tojiuni/goquest/internal/spicedb"
	"github.com/tojiuni/goquest/internal/ticket"
)

type stubGetter struct {
	t   *ticket.Ticket
	err error
}

func (s stubGetter) Get(_ context.Context, _ uuid.UUID) (*ticket.Ticket, error) {
	return s.t, s.err
}

// stubChecker only implements what the middleware uses; embed Noop for the rest.
// Embed the POINTER (*NoopChecker) — its methods have pointer receivers, so a value
// embed would not satisfy PermissionChecker. A nil embedded pointer is fine because
// the Noop methods never dereference the receiver.
type stubChecker struct {
	*spicedb.NoopChecker
	allow bool
	err   error
}

func (s stubChecker) CanViewTicket(_ context.Context, _ uuid.UUID, _ string, _ spicedb.VisibilityMode) (bool, error) {
	return s.allow, s.err
}

func serve(t *testing.T, mw func(http.Handler) http.Handler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/tickets/{id}", func(r chi.Router) {
		r.Use(mw)
		r.Get("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func reqWithUser(id uuid.UUID, userID string) *http.Request {
	r := httptest.NewRequest("GET", "/tickets/"+id.String()+"/", nil)
	return r.WithContext(auth.WithRequesterID(r.Context(), userID))
}

func TestRequireTicketView_OIDC(t *testing.T) {
	id := uuid.New()
	pid := uuid.New()
	tk := &ticket.Ticket{ID: id, ProjectID: &pid, Visibility: ticket.VisibilityProject}

	// member allowed
	mw := auth.RequireTicketView(stubGetter{t: tk}, stubChecker{allow: true})
	if rec := serve(t, mw, reqWithUser(id, "member")); rec.Code != http.StatusOK {
		t.Errorf("member: want 200, got %d", rec.Code)
	}
	// non-member denied
	mw = auth.RequireTicketView(stubGetter{t: tk}, stubChecker{allow: false})
	if rec := serve(t, mw, reqWithUser(id, "stranger")); rec.Code != http.StatusForbidden {
		t.Errorf("non-member: want 403, got %d", rec.Code)
	}
	// ticket not found
	mw = auth.RequireTicketView(stubGetter{err: ticket.ErrNotFound}, stubChecker{allow: true})
	if rec := serve(t, mw, reqWithUser(id, "member")); rec.Code != http.StatusNotFound {
		t.Errorf("not found: want 404, got %d", rec.Code)
	}
	// checker error → fail-closed (500)
	mw = auth.RequireTicketView(stubGetter{t: tk}, stubChecker{err: context.DeadlineExceeded})
	if rec := serve(t, mw, reqWithUser(id, "member")); rec.Code != http.StatusInternalServerError {
		t.Errorf("checker err: want 500, got %d", rec.Code)
	}
}

func TestRequireTicketView_Shadow(t *testing.T) {
	id := uuid.New()
	pid := uuid.New()
	tk := &ticket.Ticket{ID: id, ProjectID: &pid, Visibility: ticket.VisibilityRestricted}
	mw := auth.RequireTicketView(stubGetter{t: tk}, &spicedb.NoopChecker{})
	if rec := serve(t, mw, reqWithUser(id, "anyone")); rec.Code != http.StatusOK {
		t.Errorf("shadow: want 200, got %d", rec.Code)
	}
}

func TestRequireTicketView_LegacyNoProject(t *testing.T) {
	id := uuid.New()
	tk := &ticket.Ticket{ID: id, ProjectID: nil, Visibility: ticket.VisibilityProject}
	// no project → skip SpiceDB → allowed even with deny checker
	mw := auth.RequireTicketView(stubGetter{t: tk}, stubChecker{allow: false})
	if rec := serve(t, mw, reqWithUser(id, "anyone")); rec.Code != http.StatusOK {
		t.Errorf("legacy: want 200, got %d", rec.Code)
	}
}

func TestRequireTicketView_CustomerToken(t *testing.T) {
	id := uuid.New()
	other := uuid.New()
	pid := uuid.New()
	tk := &ticket.Ticket{ID: id, ProjectID: &pid, Visibility: ticket.VisibilityProject}

	withCust := func(reqID uuid.UUID, bound *uuid.UUID) *http.Request {
		r := httptest.NewRequest("GET", "/tickets/"+reqID.String()+"/", nil)
		return r.WithContext(auth.WithCustomerCtx(r.Context(), &auth.CustomerCtx{Email: "c@x.com", TicketID: bound}))
	}

	// deny checker proves customer branch bypasses SpiceDB
	mw := auth.RequireTicketView(stubGetter{t: tk}, stubChecker{allow: false})

	// bound to this ticket → allowed
	if rec := serve(t, mw, withCust(id, &id)); rec.Code != http.StatusOK {
		t.Errorf("bound customer: want 200, got %d", rec.Code)
	}
	// bound to another ticket → 403
	if rec := serve(t, mw, withCust(id, &other)); rec.Code != http.StatusForbidden {
		t.Errorf("mismatched customer: want 403, got %d", rec.Code)
	}
	// no binding → 403
	if rec := serve(t, mw, withCust(id, nil)); rec.Code != http.StatusForbidden {
		t.Errorf("unbound customer: want 403, got %d", rec.Code)
	}
}
```

- [ ] **Step 2: 테스트 실패 확인**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go test ./internal/auth/ -run TestRequireTicketView 2>&1 | head`
Expected: 컴파일 실패 — `auth.RequireTicketView undefined`, `auth.WithCustomerCtx undefined`, `auth.TicketFrom undefined`.

- [ ] **Step 3: `WithCustomerCtx` 테스트 훅 추가**

`internal/auth/customer.go`의 `CustomerFrom` 함수 바로 위에 추가:

```go
// WithCustomerCtx attaches a CustomerCtx (test/helper hook; production path is
// CustomerTokenMiddleware).
func WithCustomerCtx(ctx context.Context, c *CustomerCtx) context.Context {
	return context.WithValue(ctx, customerCtxKey, c)
}
```

- [ ] **Step 4: 미들웨어 구현**

`internal/auth/ticket_view.go` 생성:

```go
package auth

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/spicedb"
	"github.com/tojiuni/goquest/internal/ticket"
)

// ticketGetter is the minimal ticket lookup the middleware needs (satisfied by
// *ticket.Service).
type ticketGetter interface {
	Get(ctx context.Context, id uuid.UUID) (*ticket.Ticket, error)
}

type ticketCtxKeyT string

const ticketCtxKey ticketCtxKeyT = "viewed_ticket"

// TicketFrom returns the ticket stashed by RequireTicketView, or nil.
func TicketFrom(r *http.Request) *ticket.Ticket {
	t, _ := r.Context().Value(ticketCtxKey).(*ticket.Ticket)
	return t
}

// RequireTicketView gates every /tickets/{id}/* route on view access and stashes
// the ticket in context. Principal handling:
//   - customer-token (magic-link): allowed iff the token is bound to this ticket id.
//   - OIDC user: SpiceDB CanViewTicket (visibility-aware). Legacy tickets with no
//     project skip the SpiceDB gate. fail-closed on checker error.
func RequireTicketView(getter ticketGetter, checker spicedb.PermissionChecker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := uuid.Parse(chi.URLParam(r, "id"))
			if err != nil {
				http.Error(w, "invalid ticket id", http.StatusBadRequest)
				return
			}

			t, err := getter.Get(r.Context(), id)
			if err != nil {
				if err == ticket.ErrNotFound {
					http.Error(w, "ticket not found", http.StatusNotFound)
					return
				}
				http.Error(w, "lookup ticket", http.StatusInternalServerError)
				return
			}

			if cust := CustomerFrom(r); cust != nil {
				// magic-link: the token is the capability. It must be bound to this ticket.
				if cust.TicketID == nil || *cust.TicketID != id {
					http.Error(w, "forbidden", http.StatusForbidden)
					return
				}
			} else if t.ProjectID != nil {
				// OIDC user, project-scoped ticket → SpiceDB gate.
				ok, cErr := checker.CanViewTicket(r.Context(), t.ID, RequesterID(r), spicedb.VisibilityMode(t.Visibility))
				if cErr != nil {
					http.Error(w, "visibility check", http.StatusInternalServerError) // fail-closed
					return
				}
				if !ok {
					http.Error(w, "access denied", http.StatusForbidden)
					return
				}
			}
			// legacy ticket (ProjectID==nil) for OIDC: no fine gate.

			ctx := context.WithValue(r.Context(), ticketCtxKey, t)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
```

`ticket.ErrNotFound` 비교는 기존 핸들러가 `errors.Is`를 쓰므로 일치시키려면: 위 `err == ticket.ErrNotFound`를 `errors.Is(err, ticket.ErrNotFound)`로 바꾸고 import에 `"errors"` 추가. (sentinel이 wrap될 수 있으므로 `errors.Is` 사용.)

- [ ] **Step 5: 테스트 통과 확인**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go test ./internal/auth/ -run TestRequireTicketView 2>&1 | tail -10`
Expected: 모든 서브테스트 PASS.

- [ ] **Step 6: Commit**

```bash
cd /Users/dong-hoshin/Documents/dev/goquest
git add internal/auth/ticket_view.go internal/auth/ticket_view_test.go internal/auth/customer.go
git commit -m "feat(auth): RequireTicketView middleware unifying OIDC + customer-token principals"
```

---

## Task 4: 미들웨어 배선 + Get/ListAttempts 단순화

**Files:**
- Modify: `cmd/service/main.go:236-250` (route group)
- Modify: `internal/handler/ticket.go` (Get, ListAttempts)

- [ ] **Step 1: 미들웨어 인스턴스 생성 + 그룹 적용**

`cmd/service/main.go`의 `ticketH := handler.NewTicketHandler(...)`(line ~131) 다음에 추가:

```go
	requireTicketView := auth.RequireTicketView(svc, sdbChecker)
```

(`svc`는 `*ticket.Service` 변수명. 만약 다른 이름이면 그에 맞춘다 — line 131의 `NewTicketHandler(svc, ...)` 첫 인자와 동일.)

그리고 `r.Route("/tickets/{id}", func(r chi.Router) {`(line 236) 바로 다음 줄에 추가:

```go
			r.Use(requireTicketView)
```

- [ ] **Step 2: 빌드 확인 (배선만)**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go build ./...`
Expected: 성공.

- [ ] **Step 3: Get 핸들러 단순화 (중복 체크 제거, ctx 티켓 사용)**

`internal/handler/ticket.go`의 `Get` 함수(line 179-210) 전체를 교체:

```go
func (h *TicketHandler) Get(w http.ResponseWriter, r *http.Request) {
	// Access already gated by RequireTicketView, which also stashed the ticket.
	if t := auth.TicketFrom(r); t != nil {
		writeJSON(w, http.StatusOK, t)
		return
	}
	// Fallback (middleware not applied / direct call): fetch by id.
	id, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ticket id")
		return
	}
	t, err := h.svc.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, ticket.ErrNotFound) {
			writeError(w, http.StatusNotFound, "ticket not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, t)
}
```

- [ ] **Step 4: ListAttempts 핸들러 단순화 (중복 visibility 체크 제거)**

`internal/handler/ticket.go`의 `ListAttempts` 함수(line 381-415) 전체를 교체:

```go
func (h *TicketHandler) ListAttempts(w http.ResponseWriter, r *http.Request) {
	// Parent-ticket access already gated by RequireTicketView.
	id, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ticket id")
		return
	}
	attempts, err := h.svc.ListAttempts(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"attempts": attempts})
}
```

- [ ] **Step 5: 빌드 + 기존 핸들러 테스트 회귀 확인**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go build ./... && go test ./internal/handler/ 2>&1 | tail -10`
Expected: 빌드 성공. 기존 handler 테스트 PASS(또는 Get/ListAttempts 관련 테스트가 미들웨어 전제로 바뀌어야 하면 그 테스트를 미들웨어 경유로 갱신 — 실패 시 보고).

- [ ] **Step 6: Commit**

```bash
cd /Users/dong-hoshin/Documents/dev/goquest
git add cmd/service/main.go internal/handler/ticket.go
git commit -m "feat: wire RequireTicketView; drop redundant per-handler view checks"
```

---

## Task 5: List 핸들러를 VisibleResourceIDs로 리팩터

**Files:**
- Modify: `internal/handler/ticket.go` (List, line 95-177; helper 추가)
- Test: `internal/handler/ticket_list_test.go` (create)

- [ ] **Step 1: List 가시성 필터 테스트 작성 (실패)**

`internal/handler/ticket_list_test.go` 생성. List는 `*ticket.Service`를 받으므로 직접 단위 테스트가 무거우면, 가시성 결합 로직을 별도 순수 함수로 추출해 그 함수를 테스트한다. 먼저 헬퍼 함수의 테스트:

```go
package handler

import (
	"testing"

	"github.com/google/uuid"
)

func TestParseTicketUUIDs_SkipsInvalid(t *testing.T) {
	good := uuid.New()
	ids := parseTicketUUIDs([]string{good.String(), "not-a-uuid", ""})
	if len(ids) != 1 || ids[0] != good {
		t.Fatalf("want [%s], got %v", good, ids)
	}
	// always non-nil (empty slice, not nil) so ANY($) is well-formed
	empty := parseTicketUUIDs(nil)
	if empty == nil {
		t.Error("parseTicketUUIDs(nil) must return non-nil empty slice")
	}
}
```

- [ ] **Step 2: 테스트 실패 확인**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go test ./internal/handler/ -run TestParseTicketUUIDs 2>&1 | head`
Expected: 컴파일 실패 — `parseTicketUUIDs undefined`.

- [ ] **Step 3: 헬퍼 추가**

`internal/handler/ticket.go`의 `parseUUID` 함수(line 417) 위에 추가:

```go
// parseTicketUUIDs parses LookupResources ids into uuids, skipping any that don't
// parse. Always returns a non-nil slice so it is safe as a SQL ANY($) argument.
func parseTicketUUIDs(ids []string) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ids))
	for _, s := range ids {
		if u, err := uuid.Parse(s); err == nil {
			out = append(out, u)
		}
	}
	return out
}
```

- [ ] **Step 4: 헬퍼 테스트 통과 확인**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go test ./internal/handler/ -run TestParseTicketUUIDs 2>&1 | tail`
Expected: PASS.

- [ ] **Step 5: List 핸들러의 per-ticket 루프를 교체**

`internal/handler/ticket.go`의 `List` 함수에서 `tickets, total, err := h.svc.List(r.Context(), f)` **이전**(즉 `f`를 채운 직후, line ~141)에 가시성 필터 주입을 추가하고, 기존 post-filter 루프(line 148-169, 주석 "Visibility enforcement" ~ `tickets = filtered`)를 **삭제**한다.

`f` 구성 직후(`tickets, total, err := h.svc.List` 호출 직전)에 삽입:

```go
	// Visibility: restrict to SpiceDB-visible tickets. Two LookupResources calls —
	// project-visibility tickets need can_view_project, restricted need
	// can_view_restricted; combined visibility-aware in the store query. In shadow
	// (NoopChecker) both report unrestricted → no filter (identical to pre-enforce).
	uid := auth.RequesterID(r)
	projIDs, unrP, err := h.checker.VisibleResourceIDs(r.Context(), spicedb.TypeTicket, "can_view_project", uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "visibility lookup: "+err.Error())
		return
	}
	restrIDs, unrR, err := h.checker.VisibleResourceIDs(r.Context(), spicedb.TypeTicket, "can_view_restricted", uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "visibility lookup: "+err.Error())
		return
	}
	if !unrP || !unrR {
		f.VisibleFilter = &ticket.VisibleTicketIDs{
			Project:    parseTicketUUIDs(projIDs),
			Restricted: parseTicketUUIDs(restrIDs),
		}
	}
```

그리고 기존 post-filter 블록(주석 `// Visibility enforcement: ...`부터 `tickets = filtered`까지, line 148-169)을 완전히 삭제. 그 결과 `tickets, total, err := h.svc.List(...)` 다음은 바로 `writeJSON(... "tickets": tickets, "total": total ...)`로 이어진다.

- [ ] **Step 6: 빌드 + 회귀 확인**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go build ./... && go test ./internal/handler/ 2>&1 | tail -10`
Expected: 빌드 성공, 테스트 PASS. (기존 List 테스트가 post-filter 동작을 가정했다면, shadow에서는 동일 결과이므로 통과해야 한다. 실패 시 그 테스트의 기대값을 점검·보고.)

- [ ] **Step 7: Commit**

```bash
cd /Users/dong-hoshin/Documents/dev/goquest
git add internal/handler/ticket.go internal/handler/ticket_list_test.go
git commit -m "refactor(handler): List visibility via VisibleResourceIDs (paginate-correct, no N+1)"
```

---

## Task 6: ListSubtasks를 VisibleResourceIDs 멤버십으로 리팩터

**Files:**
- Modify: `internal/handler/ticket.go` (ListSubtasks, line 323-352; helper 추가)
- Test: `internal/handler/ticket_subtask_test.go` (create)

- [ ] **Step 1: 멤버십 헬퍼 테스트 작성 (실패)**

`internal/handler/ticket_subtask_test.go` 생성:

```go
package handler

import (
	"testing"

	"github.com/google/uuid"
	"github.com/tojiuni/goquest/internal/ticket"
)

func TestVisibleSubtask(t *testing.T) {
	p := uuid.New() // project-visibility, in proj set
	r := uuid.New() // restricted-visibility, in restr set
	x := uuid.New() // project-visibility, NOT in proj set

	projSet := map[uuid.UUID]bool{p: true}
	restrSet := map[uuid.UUID]bool{r: true}

	cases := []struct {
		id   uuid.UUID
		vis  ticket.Visibility
		want bool
	}{
		{p, ticket.VisibilityProject, true},
		{r, ticket.VisibilityRestricted, true},
		{x, ticket.VisibilityProject, false},
		{r, ticket.VisibilityProject, false}, // restricted ticket not in proj set
	}
	for _, c := range cases {
		if got := visibleSubtask(c.id, c.vis, projSet, restrSet); got != c.want {
			t.Errorf("visibleSubtask(%s,%s)=%v want %v", c.id, c.vis, got, c.want)
		}
	}
}
```

- [ ] **Step 2: 테스트 실패 확인**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go test ./internal/handler/ -run TestVisibleSubtask 2>&1 | head`
Expected: 컴파일 실패 — `visibleSubtask undefined`.

- [ ] **Step 3: 헬퍼 추가**

`internal/handler/ticket.go`의 `parseTicketUUIDs` 함수 다음에 추가:

```go
// visibleSubtask reports whether a subtask is visible given the caller's two
// LookupResources sets, honoring the subtask's own visibility mode.
func visibleSubtask(id uuid.UUID, vis ticket.Visibility, projSet, restrSet map[uuid.UUID]bool) bool {
	if vis == ticket.VisibilityRestricted {
		return restrSet[id]
	}
	return projSet[id]
}

func uuidSet(ids []uuid.UUID) map[uuid.UUID]bool {
	m := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}
```

- [ ] **Step 4: 헬퍼 테스트 통과 확인**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go test ./internal/handler/ -run TestVisibleSubtask 2>&1 | tail`
Expected: PASS.

- [ ] **Step 5: ListSubtasks 루프 교체**

`internal/handler/ticket.go`의 `ListSubtasks`에서 기존 per-child 루프(주석 `// Visibility: drop subtasks...`부터 `subtasks = visible`까지, line 336-349)를 다음으로 교체:

```go
	// Visibility: keep only subtasks the caller can view. Two LookupResources calls
	// (project + restricted), then membership-test each child by its own visibility.
	// shadow (NoopChecker) → unrestricted → keep all.
	uid := auth.RequesterID(r)
	projIDs, unrP, err := h.checker.VisibleResourceIDs(r.Context(), spicedb.TypeTicket, "can_view_project", uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "visibility lookup: "+err.Error())
		return
	}
	restrIDs, unrR, err := h.checker.VisibleResourceIDs(r.Context(), spicedb.TypeTicket, "can_view_restricted", uid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "visibility lookup: "+err.Error())
		return
	}
	if !unrP || !unrR {
		projSet := uuidSet(parseTicketUUIDs(projIDs))
		restrSet := uuidSet(parseTicketUUIDs(restrIDs))
		visible := subtasks[:0]
		for _, st := range subtasks {
			if visibleSubtask(st.ID, st.Visibility, projSet, restrSet) {
				visible = append(visible, st)
			}
		}
		subtasks = visible
	}
```

- [ ] **Step 6: 빌드 + 회귀 확인**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go build ./... && go test ./internal/handler/ 2>&1 | tail -10`
Expected: 빌드 성공, 테스트 PASS.

- [ ] **Step 7: Commit**

```bash
cd /Users/dong-hoshin/Documents/dev/goquest
git add internal/handler/ticket.go internal/handler/ticket_subtask_test.go
git commit -m "refactor(handler): ListSubtasks visibility via VisibleResourceIDs membership"
```

---

## Task 7: 커버리지 가드 테스트

`/tickets/{id}` 하위 라우트가 모두 그룹 미들웨어를 거치는지(미들웨어 미적용 라우트 = 누출) 보장.

**Files:**
- Test: `cmd/service/route_guard_test.go` (create)

- [ ] **Step 1: 가드 테스트 작성 (실패)**

`cmd/service/route_guard_test.go` 생성. chi 라우터를 빌드하는 헬퍼가 main에 없으면, 작은 구성 함수로 라우트 트리를 분리하는 대신, 본 가드는 **소스 검사**로 단순화한다: `cmd/service/main.go`가 `/tickets/{id}` Route 블록에서 `r.Use(requireTicketView)`를 호출하는지 정적으로 확인.

```go
package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestTicketIDGroupHasViewGuard ensures the /tickets/{id} route group applies
// RequireTicketView. A new sub-route under that group is automatically gated;
// removing the guard fails this test.
func TestTicketIDGroupHasViewGuard(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	// locate the /tickets/{id} Route block and assert r.Use(requireTicketView) appears
	// before the block's first sub-route registration.
	re := regexp.MustCompile(`r\.Route\("/tickets/\{id\}",\s*func\(r chi\.Router\)\s*\{`)
	loc := re.FindIndex(src)
	if loc == nil {
		t.Fatal(`/tickets/{id} route block not found`)
	}
	rest := string(src[loc[1]:])
	// within the next 400 chars (block header region) the guard must appear
	head := rest
	if len(head) > 400 {
		head = head[:400]
	}
	if !strings.Contains(head, "r.Use(requireTicketView)") {
		t.Error("/tickets/{id} group is missing r.Use(requireTicketView) — view gate leak")
	}
}
```

- [ ] **Step 2: 테스트 통과 확인 (Task 4가 가드를 추가했으므로 PASS여야 함)**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go test ./cmd/service/ -run TestTicketIDGroupHasViewGuard 2>&1 | tail`
Expected: PASS. (만약 main.go에 라우트 트리를 독립 함수로 추출하는 편이 더 견고하다고 판단되면 그렇게 리팩터 후 런타임 테스트로 대체 가능 — 단 본 슬라이스 범위에서는 정적 가드로 충분.)

- [ ] **Step 3: 전체 테스트 + 빌드**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go build ./... && go test ./... 2>&1 | tail -20`
Expected: 전체 PASS(또는 pg-필요 통합 테스트만 skip).

- [ ] **Step 4: Commit**

```bash
cd /Users/dong-hoshin/Documents/dev/goquest
git add cmd/service/route_guard_test.go
git commit -m "test: guard that /tickets/{id} group keeps the view middleware"
```

---

## 완료 후

- `go test ./...` 전체 통과 확인.
- enforce 전환은 별도(cutover 런북 `docs/runbooks/spicedb-enforce-cutover.md`). 본 슬라이스는 shadow에서 무변경이므로 머지·배포 안전.
- superpowers:finishing-a-development-branch로 PR 생성. PR 본문에 "enforce 블로커(List total/subtask/attempt/comment 누출 + principal) 해소" 명시.
- 후속(별도 plan): org 축 + ZITADEL org 동기, template codegen, shared lib 추출, mutating 라우트 manage 게이트.
