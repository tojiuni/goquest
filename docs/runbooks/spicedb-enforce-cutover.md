# goquest SpiceDB enforce cutover 런북

`SPICEDB_ENFORCE=true` 전환 절차. shadow(=false)에서 writer는 dual-write, checker는 permissive(NoopChecker).

## 전제 (사전 확인)
- goquest-service 이미지에 토큰 수정(#33) + 인코딩 수정(#34) + enforce-블로커(List 필터/assignee 재동기) 포함. 배포 로그에 `spicedb connected (shadow, checks permissive)` 확인.
- 백필 완료: `cmd/backfill-spicedb` errors=0. `zed relationship read goquest/*`로 표본 확인.

## restricted 가시성 (결정)
restricted 티켓은 **requester + assignee 에게만** 보인다. 명시적 추가 열람자(restricted_viewer) 기능은 **없음**(추가 엔드포인트 미구현; `spicedb.Writer.AddTicketViewer`는 미사용 훅). 추가 열람자가 필요하면 별도 기능으로 구현 후 백필.

## cutover 절차
1. `zed permission check`로 표본 검증(멤버 true, 비멤버 false). E2E 통과 확인.
2. deployment `SPICEDB_ENFORCE=true` → 재배포.
3. **즉시 확인(중요): 로그에 `spicedb connected + ENFORCING` 가 떠야 한다.** `spicedb unavailable` 이 뜨면 dial 실패로 **조용히 permissive 강등**된 것 — enforce 안 됨. 연결 복구 후 재시도.
4. 스모크: requester/멤버는 `GET /tickets/{id}`·`GET /tickets` 정상, 비멤버는 단건 403 / 목록 제외.

## 롤백
deployment `SPICEDB_ENFORCE=false` → 재배포. shadow로 복귀(즉시 허용).

## PR #37 (core 슬라이스) 이후 갱신
중앙 인가 core 슬라이스(`RequireTicketView` 그룹 미들웨어 + `VisibleResourceIDs`)로 아래가 바뀌었다:
- **List `total` 근사치 → 정확:** 가시성을 store SQL(`VisibleTicketIDs` 2-bucket)로 필터 → count 쿼리도 동일 술어 → 크로스페이지 합계 정확. per-ticket N RPC 제거.
- **댓글/attempts/subtasks 누출 → 해소:** `/tickets/{id}` 그룹 단일 미들웨어가 게이트. customer-token(magic-link)은 SpiceDB 우회하되 **토큰이 바운드된 ticket id에만** 허용 → 기존 A토큰→B티켓 누출도 차단.
- **에러 정책 통일:** 미들웨어·List·ListSubtasks 모두 **fail-closed**(SpiceDB 에러 → 403/500). 기존 Get fail-open 제거.
- ⚠️ **배선 시점 즉시 발동(enforce 플래그와 무관):** ① customer-token의 ticket 바인딩 강제, ② 존재하지 않는 티켓에 대한 미들웨어 404(이전엔 댓글 경로에서 FK 위반 400). 클라이언트가 400을 단언하면 영향. enforce 전환과 별개로 **배포 즉시** 적용됨에 유의.
- ✅ **subtask-without-project — 해소:** ① 생성 시 `Service.Create`가 부모 ProjectID 상속(`ParentID` 있고 `ProjectID` 없을 때) → 신규 subtask는 tuple 생성됨. ② migration `013_subtask_inherit_project.sql`이 기존 orphan subtask의 `project_id`를 부모에서 채움 → `cmd/backfill-spicedb`(WHERE project_id IS NOT NULL)가 픽업. ⚠️ migration은 **단일 레벨**(orphan subtask의 orphan subtask는 재실행 시 다음 패스에 보정; 재실행 idempotent). enforce 전 migration 적용 + 백필 재실행 확인.

## GA 전 정리(추적) — 파일럿엔 무방, GA 전 결정
- **에러 정책 불일치:** `Get`은 SpiceDB 체크 에러 시 **fail-open**(티켓 반환, 로깅), `List`는 **fail-closed**(500). SpiceDB 일시 장애 시 목록은 깨지고 단건은 열리는 비대칭. GA 전 한쪽으로 정렬(보안상 양쪽 fail-closed 권장, 또는 양쪽 fail-open으로 가용성 우선) 결정 필요.
- **List 페이지네이션 근사치:** 가시성 post-filter라 `total`이 필터된 페이지 수 = 크로스페이지 합계 부정확. GA 전 SpiceDB `LookupResources`/`BulkCheckPermission`로 정밀화(성능: 페이지당 N CheckPermission RPC도 함께 해소).
- **List 핸들러 테스트 갭:** enforce 필터 경로(거부/통과/에러 500) 단위 테스트 부재(ticket.Service 구성 부담). 스텁 checker로 핸들러 테스트 추가 권장.
- **미강제 누출 벡터:**
  - `ListSubtasks` (`GET /tickets/{id}/subtasks`) — ✅ **해소**: 자식 티켓을 CanViewTicket post-filter.
  - `ListAttempts` (`GET /tickets/{id}/attempts`) — ✅ **해소**: 부모 티켓 visibility 게이트(거부 시 403).
  - 댓글 `List` (`GET .../tickets/{id}/comments`) — ⛔ **미해소(설계 필요)**: ticket 가시성 게이트 없음. **주의: 댓글은 magic-link(customer 토큰)로도 접근**되는데 그 사용자는 SpiceDB 멤버가 아니므로 단순 CanViewTicket 게이트를 걸면 정당한 magic-link 열람이 깨진다. customer-token 경로(`canSeeInternal`와 동일 분기)를 우회하는 게이트 설계가 필요 → 별도 사이클. enforce 전 처리 또는 "프로젝트 멤버까지 누출 수용" 명시 결정.
- **재배정 외 드리프트:** 워크스페이스/프로젝트 삭제 시 tuple 고아 → 주기 reconcile(백필 재실행)로 보정.
- **정체성 부채:** 레거시 numeric sub ↔ 현재 이메일 sub 불일치(현재 활성 사용자 데이터는 전부 이메일이라 enforce 무관). 워크스페이스 병합과 동류.
