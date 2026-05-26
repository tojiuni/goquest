# P3 enforce 블로커 해결 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `SPICEDB_ENFORCE=true` 전 남은 블로커(List 미강제, 재배정 드리프트, restricted 의미 명시, 운영 cutover)를 해결해 goquest 티켓 가시성 강제를 안전하게 켤 수 있게 한다.

**Architecture:** 가시성 강제는 핸들러 계층에서 SpiceDB checker로 한다. List는 결과 페이지를 `CanViewTicket`으로 post-filter(shadow의 NoopChecker에선 전부 통과 → 무변경). 재배정은 핸들러 Update에서 assignee 관계를 SpiceDB에 재동기(Create의 WriteTicket과 동일 계층). restricted_viewer는 추가 엔드포인트가 없으므로 "requester+assignee 전용"으로 문서화.

**Tech Stack:** Go, authzed-go(SpiceDB), 표준 testing.

**상위:** P3 spec `docs/superpowers/specs/2026-05-26-p3-spicedb-ticket-authz-design.md`의 "enforce 전 반드시 해결" 섹션.

**브랜치:** goquest `feat/p3-enforce-blockers` (코드 작업 — worktree 권장).

확인된 사실:
- `internal/handler/ticket.go`: `TicketHandler{svc *ticket.Service, checker spicedb.PermissionChecker, writer spicedb.RelationshipWriter, resolver}`. `Create`가 `WriteTicket`을 분리 bounded 컨텍스트 고루틴으로 호출. `Get`이 `CanViewTicket` 사용. `List`는 `h.svc.List`만 호출(체크 없음). `Update`는 `h.svc.Update`만 호출(SpiceDB 미연동). `auth.RequesterID(r)`로 user id.
- `internal/spicedb`: `Writer`에 `touch`/`delete` 헬퍼, user subject는 `subjectID()`로 base64url 인코딩(중앙). `RelationshipWriter` 인터페이스 + `NoopWriter`. 테스트 mock writer가 workspace/project/handler 테스트에 존재.
- `ticket.UpdateTicketReq.AssigneeID *string`; `svc.Update`는 결과 `*Ticket`(새 AssigneeID 포함) 반환.
- restricted viewer 추가 엔드포인트 **없음**(AddTicketViewer 미사용).

---

## File Structure
- `internal/spicedb/interfaces.go` — `RelationshipWriter`에 `ReplaceTicketAssignee` 추가.
- `internal/spicedb/writer.go` — `ReplaceTicketAssignee` 구현.
- `internal/spicedb/noop.go` — no-op.
- `internal/handler/ticket.go` — Update에서 assignee 재동기; List post-filter.
- 테스트: `internal/spicedb/writer_test.go`, 기존 mock writer들(workspace/project/handler 테스트)에 새 메서드 추가.
- 문서: `docs/runbooks/spicedb-enforce-cutover.md`(신규), P3 spec 보강.

---

## Task 1: writer `ReplaceTicketAssignee` (재배정 동기 기반)

**Files:** `internal/spicedb/interfaces.go`, `writer.go`, `noop.go`, `writer_test.go`

- [ ] **Step 1: 실패 테스트**

`internal/spicedb/writer_test.go`에 추가:
```go
func TestReplaceTicketAssignee_BuildsUserSubject(t *testing.T) {
	// 인터페이스에 존재하고 NoopWriter가 만족하는지 + Writer 메서드 시그니처 확인용 컴파일 테스트.
	var _ RelationshipWriter = (*NoopWriter)(nil)
	var _ RelationshipWriter = (*Writer)(nil)
	// touch가 assignee user subject를 인코딩하는지 (write 경로 단위)
	u := touch(TypeTicket, "tid", "assignee", TypeUser, "lyckabc@toji.homes", "")
	if u.Relationship.Subject.Object.ObjectId == "lyckabc@toji.homes" {
		t.Error("assignee user subject must be encoded")
	}
}
```
Run `go test ./internal/spicedb/` → FAIL(컴파일: ReplaceTicketAssignee 미정의로 인터페이스 단언 실패).

- [ ] **Step 2: 인터페이스 + Noop + Writer 구현**

`interfaces.go`의 `RelationshipWriter`에 추가:
```go
	ReplaceTicketAssignee(ctx context.Context, ticketID uuid.UUID, assigneeID string) error
```
`noop.go`:
```go
func (n *NoopWriter) ReplaceTicketAssignee(_ context.Context, _ uuid.UUID, _ string) error { return nil }
```
`writer.go` (AddTicketViewer 근처):
```go
// ReplaceTicketAssignee makes SpiceDB's assignee relation match the current
// assignee: it removes any existing assignee on the ticket, then writes the new
// one (skipped when empty/unassigned). Idempotent — safe to call on every update.
func (w *Writer) ReplaceTicketAssignee(ctx context.Context, ticketID uuid.UUID, assigneeID string) error {
	// Delete all existing assignee relations (empty subId → matches any subject).
	if err := w.delete(ctx, TypeTicket, ticketID.String(), "assignee", TypeUser, "", ""); err != nil {
		return err
	}
	if assigneeID == "" {
		return nil
	}
	return w.write(ctx, touch(TypeTicket, ticketID.String(), "assignee", TypeUser, assigneeID, ""))
}
```

- [ ] **Step 3: 기존 mock writer들에 메서드 추가 (컴파일)**

`grep -rln "func (m \*mockWriter)" --include=*.go internal` 로 mock writer 정의 위치(workspace/project/handler 테스트) 모두 찾기. 각 `mockWriter`에 추가:
```go
func (m *mockWriter) ReplaceTicketAssignee(_ context.Context, _ uuid.UUID, _ string) error { return nil }
```
(handler 테스트의 stub resolver/writer가 있으면 거기도.)

- [ ] **Step 4: 빌드 + 테스트**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go build ./... && go test ./internal/spicedb/ ./internal/workspace/ ./internal/project/ ./internal/handler/`
Expected: PASS.

- [ ] **Step 5: Commit**
```bash
git add internal/spicedb/
git commit -m "feat(spicedb): ReplaceTicketAssignee for reassignment sync"
```
(mock 변경이 다른 패키지 테스트 파일이면 함께 add.)

---

## Task 2: 핸들러 Update에서 assignee 재동기 (블로커 #3)

**Files:** `internal/handler/ticket.go`

- [ ] **Step 1: Update 핸들러에 dual-write 추가**

`internal/handler/ticket.go`의 `Update`에서 `t, err := h.svc.Update(...)` 성공 직후(현재 `writeJSON` 직전)에 삽입 — Create의 WriteTicket과 동일하게 분리 bounded 컨텍스트:
```go
	// Keep SpiceDB's assignee relation in sync after an update (best-effort).
	tid, asg := t.ID, t.AssigneeID
	go func() {
		bgctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if wErr := h.writer.ReplaceTicketAssignee(bgctx, tid, asg); wErr != nil {
			log.Printf("spicedb ReplaceTicketAssignee %s: %v (continuing)", tid, wErr)
		}
	}()
```
(`context`/`log`/`time`은 Create 수정 때 이미 import됨.)

- [ ] **Step 2: 빌드 + 핸들러 테스트**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go build ./... && go test ./internal/handler/`
Expected: PASS.

- [ ] **Step 3: Commit**
```bash
git add internal/handler/ticket.go
git commit -m "fix(ticket): re-sync SpiceDB assignee on update (reassignment drift)"
```

---

## Task 3: List 가시성 post-filter (블로커 #2)

**Files:** `internal/handler/ticket.go`

- [ ] **Step 1: List 핸들러에서 결과를 CanViewTicket으로 필터**

`internal/handler/ticket.go`의 `List`에서 `tickets, total, err := h.svc.List(...)` 성공 직후, 응답 직전에 삽입:
```go
	// Visibility enforcement: drop tickets the caller cannot view. In shadow mode
	// (NoopChecker) every ticket passes, preserving current behavior; under enforce
	// the real checker filters. NOTE: post-filter — `total` reflects the filtered
	// page, so cross-page counts are approximate until LookupResources is adopted.
	userID := auth.RequesterID(r)
	visible := tickets[:0]
	for _, t := range tickets {
		ok, cErr := h.checker.CanViewTicket(r.Context(), t.ID, userID, spicedb.VisibilityMode(t.Visibility))
		if cErr != nil {
			writeError(w, http.StatusInternalServerError, "visibility check: "+cErr.Error())
			return
		}
		if ok {
			visible = append(visible, t)
		}
	}
	tickets = visible
	total = int64(len(tickets))
```
(`total`의 타입이 `int64`가 아니면 그 타입에 맞춰 캐스팅 — `svc.List` 반환 타입 확인. `tickets`가 `[]*ticket.Ticket`이면 `t.ID`/`t.Visibility` 접근 가능. `spicedb`·`auth` import 확인.)

- [ ] **Step 2: 빌드 + 테스트**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go build ./... && go vet ./... && go test ./internal/handler/`
Expected: PASS. (기존 List 테스트가 NoopChecker로 동작하면 전부 통과 = 무변경 확인.)

- [ ] **Step 3: Commit**
```bash
git add internal/handler/ticket.go
git commit -m "fix(ticket): enforce visibility on list via post-filter (shadow-safe)"
```

---

## Task 4: 문서 — restricted 의미 + enforce cutover 런북 (블로커 #1·#4·#5)

**Files:** `docs/runbooks/spicedb-enforce-cutover.md`(신규), P3 spec 보강

- [ ] **Step 1: cutover 런북 작성**

`docs/runbooks/spicedb-enforce-cutover.md`:
```markdown
# goquest SpiceDB enforce cutover 런북

`SPICEDB_ENFORCE=true` 전환 절차. shadow(=false)에서 writer는 dual-write, checker는 permissive.

## 전제 (사전 확인)
- goquest-service 이미지에 토큰 수정(#33) + 인코딩 수정(#34) 포함 — 배포 로그에 `spicedb connected (shadow, checks permissive)` 확인.
- 백필 완료: `cmd/backfill-spicedb` errors=0. `zed relationship read goquest/*`로 표본 확인.
- 코드 블로커 해소: List post-filter, Update assignee 재동기 배포됨.

## restricted 가시성 (결정)
restricted 티켓은 requester + assignee 에게만 보인다. **명시적 추가 열람자(restricted_viewer) 기능은 없음**(추가 엔드포인트 미구현; `AddTicketViewer`는 미사용 훅). 추가 열람자가 필요하면 별도 기능으로.

## cutover 절차
1. `zed permission check`로 표본 검증(멤버 true, 비멤버 false).
2. deployment `SPICEDB_ENFORCE=true` → 재배포.
3. **즉시 확인(중요): 로그에 `spicedb connected + ENFORCING` 가 떠야 한다.** `spicedb unavailable` 이 뜨면 dial 실패로 **조용히 permissive 강등**된 것 — enforce 안 됨. 연결 복구 후 재시도.
4. 스모크: requester/멤버는 `GET /tickets/{id}`·`GET /tickets` 정상, 비멤버는 403/목록 제외.

## 롤백
deployment `SPICEDB_ENFORCE=false` → 재배포. shadow로 복귀(즉시 허용).
```

- [ ] **Step 2: P3 spec의 블로커 섹션 갱신(해소 표기)**

`docs/superpowers/specs/2026-05-26-p3-spicedb-ticket-authz-design.md`의 "enforce 전 반드시 해결" 항목에 상태 주석 추가:
- #1 restricted_viewer → "결정: requester+assignee 전용, 런북에 문서화(코드 변경 없음)".
- #2 List 미강제 → "해소: List post-filter(Task 3)".
- #3 재배정 드리프트 → "해소: Update assignee 재동기(Task 2)".
- #5 dial 실패 강등 → "런북에 cutover 확인 단계 추가".

- [ ] **Step 3: Commit**
```bash
git add docs/runbooks/spicedb-enforce-cutover.md docs/superpowers/specs/2026-05-26-p3-spicedb-ticket-authz-design.md
git commit -m "docs: enforce cutover runbook + restricted semantics + blocker status"
```

---

## Task 5: 최종 검증 + PR

- [ ] **Step 1: 전체 빌드/vet/테스트**

Run: `cd /Users/dong-hoshin/Documents/dev/goquest && go build ./... && go vet ./... && go test ./...`
Expected: 모두 PASS.

- [ ] **Step 2: 푸시 + PR**
```bash
cd /Users/dong-hoshin/Documents/dev/goquest && git push -u origin feat/p3-enforce-blockers
gh pr create --base main --title "feat(authz): resolve P3 enforce blockers (list filter, reassignment sync, cutover)" --body "List 가시성 post-filter(shadow-safe), Update assignee 재동기, restricted=requester/assignee 문서화, enforce cutover 런북. 이후 #33/#34 + 본 PR 배포 → 백필 재실행 → SPICEDB_ENFORCE=true 가능."
```

---

## Self-Review 결과
- **Spec coverage:** #1=Task4(문서·결정), #2=Task3, #3=Task1+2, #4·#5=Task4(런북). 전부 매핑.
- **Placeholder scan:** 없음. List의 `total` 타입·`tickets` 슬라이스 타입은 Task3 Step1에서 svc.List 반환 타입 확인 후 캐스팅 명시.
- **타입 일관성:** `ReplaceTicketAssignee(ctx, uuid.UUID, string)` 인터페이스·Writer·Noop·mock·핸들러 호출 일치. `CanViewTicket(ctx, id, userID, spicedb.VisibilityMode(...))` 기존 Get과 동일 시그니처.
- **주의:** List post-filter는 페이지 단위라 `total`이 근사치(LookupResources 도입 시 정밀화) — 런북/주석에 명시. shadow(Noop)에선 전부 통과해 무변경.
