# 중앙 인가 — Core 슬라이스 (goquest 검증)

- 날짜: 2026-05-27
- 대상 repo: goquest (+ neunexus 배포는 기존 SPICEDB env 유지)
- 상위: 플랫폼 인가 ADR(neunexus). 본 문서는 합의된 "중앙 인가" 프레임의 **첫 수직 슬라이스**.

## 배경 / 동기

P3에서 티켓 가시성을 핸들러마다 수동 `CanViewTicket`로 강제했더니 누락이 반복됐다(List·subtasks·attempts·comment). 또 post-filter는 페이지네이션을 깨고 N+1을 유발하며, magic-link(customer 토큰) 주체가 SpiceDB subject가 아니라 comment 게이트를 막았다.

합의된 방향(brainstorming): **core(엔진) + 재사용 template + project 바인딩**, **org→fine** 빌드. 단 실행은 **core 수직 슬라이스부터**(즉시 가치 + 리스크 감소), org 축/ZITADEL 동기와 template codegen은 후속(2번째 소비자가 패턴 입증 후 추출).

본 슬라이스의 목표: **generic 인가 primitive를 goquest의 실제 ticket 경로 1개에 적용**해 패턴을 검증하고, 그 과정에서 enforce 누출 블로커(subtask/attempt/comment 미강제)와 List 페이지네이션/N+1, principal(customer-token) 문제를 함께 해소한다.

### 확인된 사실
- chi 미들웨어 RBAC 패턴이 이미 존재(`auth.RequireWorkspaceRole/RequireProjectRole`: param 파싱→체크→403/통과+ctx 주입). 단 `/tickets/{id}` 그룹엔 미들웨어 없음.
- `spicedb.Checker`에 저수준 `check(resType,resID,perm,subType,subID)` 존재. user subject는 `subjectID()` base64url 인코딩(중앙).
- customer-token: `auth.CustomerFrom(r) *CustomerCtx{Email, TicketID *uuid.UUID}` — 토큰이 특정 ticket에 바운드. `auth.IsCustomerRequest(r)`.
- ticket 가시성: `can_view_project`(project 멤버+requester+assignee) vs `can_view_restricted`(requester+assignee+restricted_viewer), 핸들러가 `ticket.Visibility`로 선택.
- 현재 SPICEDB_ENFORCE=false(shadow): checker는 NoopChecker(전부 허용).

## 목표 (슬라이스 범위)
1. **generic 인가 primitive** 2개를 `spicedb`에 추가: 단건 `Check`, 컬렉션 `VisibleResourceIDs`(LookupResources).
2. **`/tickets/{id}` 그룹 미들웨어** `RequireTicketView` — 한 곳에서 게이트(Get/attempts/comments/subtasks 접근), principal(OIDC vs customer-token) 통일, 티켓 1회 조회 후 ctx 주입.
3. **List/ListSubtasks 리팩터** — post-filter → `VisibleResourceIDs` 기반(페이지네이션·N+1 해소; shadow=무필터).
4. **단일 fail-mode**(fail-closed) + 중복 per-handler 체크 정리.

## 비목표 (후속)
- org 축 + ZITADEL org 동기(P2 재개) — 상속 필요 확정 후.
- template codegen 추출 — 2번째 서비스 통합 시.
- shared 라이브러리로 추출 — 본 슬라이스는 goquest 내부에 두고 패턴 검증.

## 설계

### A. generic primitive (`internal/spicedb`)
```go
// 단건: objType:objID 에 대해 subject가 permission을 가지는가.
func (ch *Checker) Check(ctx, objType, objID, permission, userID string) (bool, error)
// 컬렉션: subject가 permission을 가지는 objType id 집합. unrestricted=true면 "제한 없음"(shadow).
func (ch *Checker) VisibleResourceIDs(ctx, objType, permission, userID string) (ids []string, unrestricted bool, err error)
```
- `Check`는 기존 저수준 `check`를 일반화(주체 user는 `subjectID` 인코딩, 기존과 동일).
- `VisibleResourceIDs`는 SpiceDB `LookupResources(objType, permission, subject)` 사용.
- **NoopChecker**(shadow): `Check`→`(true,nil)`; `VisibleResourceIDs`→`(nil, true, nil)`(=무제한 → 호출부가 필터 skip). → shadow에서 모든 동작이 무변경.

### B. `RequireTicketView` 미들웨어 (`/tickets/{id}` 그룹)
chi 미들웨어. 흐름:
1. `{id}` 파싱.
2. **principal 분기**:
   - `auth.CustomerFrom(r)`가 있으면(magic-link): `CustomerCtx.TicketID != nil && *TicketID == id` 이면 통과(토큰-바운드 capability), 아니면 403. (SpiceDB 우회 — customer는 subject가 아님.)
   - 아니면(OIDC): 티켓 조회(`svc.Get(id)` — 없음 404)로 `Visibility` 얻어 permission 선택(`can_view_project`/`can_view_restricted`) → `checker.Check(TypeTicket, id, perm, RequesterID)` → 거부 403.
3. 조회한 ticket을 **ctx에 주입**(핸들러가 재조회 안 하도록).
4. `next`.

main.go: `r.Route("/tickets/{id}", ...)`에 `r.Use(requireTicketView)` 추가 → Get/subtasks/attempts/comments(+mutating) 자동 게이트.

### C. List / ListSubtasks 리팩터
- **List**(`/tickets`): `ids, unrestricted, err := checker.VisibleResourceIDs(ctx, TypeTicket, "can_view_project", uid)`.
  - `unrestricted`면(shadow) 기존 그대로(무필터, store total 유지).
  - 아니면(enforce) `ids` 집합을 store 필터에 전달(또는 결과를 ids로 교집합)하고 `total`도 그에 맞춤. (post-filter 제거 → 페이지네이션 정확.)
  - ⚠️ store.List에 `ids []uuid.UUID` 필터 파라미터 추가 필요(쿼리 `WHERE id = ANY($ids)`); 또는 슬라이스가 작으면 결과 교집합. 구현 시 결정(쿼리 필터 권장).
- **ListSubtasks**: 부모 접근은 그룹 미들웨어가 게이트. 자식은 독립 가시성 → 자식 id를 `VisibleResourceIDs`와 교집합(shadow=무필터). (#36의 per-child 필터를 primitive 기반으로 대체.)

### D. fail-mode + 정리
- 단일 정책: **fail-closed**(SpiceDB 에러 → 거부/500). 미들웨어·List·primitive 호출부 일관. (기존 Get의 fail-open도 미들웨어로 흡수되며 fail-closed로 통일 — 런북 항목 해소.)
- 미들웨어가 게이트하므로 Get/ListAttempts 등의 **중복 per-handler CanViewTicket 제거**, 핸들러는 ctx의 ticket 사용.
- attempts/comments는 부모-스코프라 미들웨어 게이트로 충분(별도 필터 불필요).

## 데이터 흐름 (enforce 시)
```
GET /tickets/{id}/comments
 → requireTicketView: customer? (token==id→ok) : Check(goquest/ticket:id, can_view_*, user) → 403 or ctx+next
 → commentH.List (게이트 통과 후 실행)
GET /tickets
 → VisibleResourceIDs(can_view_project) → store.List(filter ids) → 정확한 total
```

## 테스트 계획
- `Check`/`VisibleResourceIDs`: 인코딩된 subject·objType 구성 단위 테스트; NoopChecker의 unrestricted=true.
- `RequireTicketView` 미들웨어(httptest + stub checker + stub ticket getter):
  - OIDC 멤버 통과 / 비멤버 403 / 티켓 없음 404.
  - customer-token: 바운드 id 통과 / 다른 id 403 / TicketID nil 403.
  - shadow(NoopChecker) 전부 통과.
- List: shadow=무필터(total 보존), enforce=ids 필터 + total 정확.
- ListSubtasks: 자식 가시성 교집합.

## 커버리지 가드
- `/tickets/{id}` 하위 라우트가 모두 그룹 미들웨어를 거치는지(미들웨어 미적용 라우트 = 누출) 테스트/리뷰 체크리스트.

## 롤아웃
- shadow(enforce=false)에서 NoopChecker가 unrestricted/allow → 전 동작 무변경. 머지·배포 안전.
- enforce 켤 때 cutover 런북 따름(이 슬라이스가 List/subtask/attempt/comment 누출 + 페이지네이션 + principal을 닫으므로 enforce 블로커 대부분 해소).

## 미해결 / 후속
- org 축 + ZITADEL org 동기(상속) — 별도 spec(Phase A 재정의).
- template codegen + shared 라이브러리 추출 — 2번째 서비스 시.
- 정체성 부채(레거시 numeric↔이메일) — 별도.
