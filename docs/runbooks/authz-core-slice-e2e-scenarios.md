# 중앙 인가 core 슬라이스 — E2E 시나리오

PR #37(`RequireTicketView` + `VisibleResourceIDs`)의 동작을 시나리오로 정리한다. 각 시나리오는 **principal**(누가) × **티켓 가시성**(무엇) × **모드**(shadow/enforce)로 기대 HTTP 결과를 정의한다.

자동화 위치: `internal/handler/authz_e2e_test.go` (인메모리 store + stub checker로 미들웨어→핸들러→store 체인을 HTTP 경계에서 구동). 실제 Postgres SQL / 실제 SpiceDB RPC 전송은 인메모리 하니스가 **동일 계약**(store의 visibility 2-bucket 술어, checker의 `CanViewTicket`/`VisibleResourceIDs` 일관성)으로 재현한다 — 라이브 전송 검증은 cutover 시 별도(아래 §라이브).

## 용어
- **OIDC 사용자**: 일반 JWT 주체(`requester_id`).
- **customer-token**: magic-link(`ct_*`), 특정 ticket id에 바운드. SpiceDB 주체 아님.
- **project 가시성** 티켓: `can_view_project`(프로젝트 멤버 + requester + assignee)로 열람.
- **restricted 가시성** 티켓: `can_view_restricted`(requester + assignee + restricted_viewer)로만 열람.
- **shadow**: `SPICEDB_ENFORCE=false`(NoopChecker, 현재 prod 기본).
- **enforce**: `SPICEDB_ENFORCE=true`(cutover 후).

---

## Group A — 배선 시점 발동(enforce 플래그와 무관, 배포 즉시)
미들웨어가 켜지는 순간 적용. shadow에서도 적용된다.

| ID | principal | 요청 | 기대 |
|----|-----------|------|------|
| A1 | customer-token bound to T1 | `GET /tickets/T1/` | 200 |
| A2 | customer-token bound to T1 | `GET /tickets/T2/`(다른 티켓) | 403 |
| A3 | customer-token, binding=nil | `GET /tickets/T1/` | 403 |
| A4 | 아무 principal | `GET /tickets/{없는-id}/` | 404 (미들웨어 Get) |
| A5 | customer-token bound to T1 | `GET /tickets/T1/comments` | 200 (그룹 게이트 통과) |
| A6 | customer-token bound to T1 | `GET /tickets/T2/comments` | 403 (기존 A토큰→B 누출 차단) |

> A4 주의: 이전엔 댓글 경로에서 FK 위반으로 400이었다. 이제 미들웨어가 선제 404. 400 단언 클라이언트는 영향.

---

## Group B — shadow (현재 prod 기본; pre-branch와 동일해야 함)
NoopChecker: 미들웨어 allow-all, List/Subtask 무필터.

| ID | principal | 요청 | 기대 |
|----|-----------|------|------|
| B1 | OIDC 비멤버 | `GET /tickets/T_restricted/` | 200 (allow-all) |
| B2 | OIDC 임의 | `GET /tickets`(목록) | 모든 티켓, `total`=전체 수(필터 없음) |
| B3 | OIDC 임의 | `GET /tickets/T/subtasks` | 모든 subtask |
| B4 | OIDC 임의 | `GET /tickets/T/attempts` | 200, attempts 반환 |

---

## Group C — enforce (cutover 후)
stub checker가 "world"(사용자별 열람 가능 ticket 집합, 권한별)로 enforce 시뮬레이션.

세팅 예: 프로젝트 P에 멤버 `alice`. 티켓:
- `Tp` = project 가시성, 프로젝트 P 소속.
- `Tr` = restricted 가시성, requester=`bob`, assignee=`carol`.
- `Ts` = `Tp`의 subtask, restricted 가시성, requester=`bob`.

| ID | principal | 요청 | 기대 |
|----|-----------|------|------|
| C1 | alice(프로젝트 멤버) | `GET /tickets/Tp/` | 200 |
| C2 | alice | `GET /tickets/Tr/`(restricted, 본인 무관) | 403 |
| C3 | bob(Tr requester) | `GET /tickets/Tr/` | 200 |
| C4 | stranger(비멤버) | `GET /tickets/Tp/` | 403 |
| C5 | alice | `GET /tickets`(목록) | `Tp` 포함, `Tr` 제외; `total`=정확(필터 후) |
| C6 | bob | `GET /tickets`(목록) | `Tr` 포함(requester) |
| C7 | alice | `GET /tickets/Tp/subtasks` | restricted 자식 `Ts` 제외(alice는 Ts requester/assignee 아님) |
| C8 | bob | `GET /tickets/Tp/subtasks` | `Ts` 포함(bob=Ts requester) — 단, 부모 Tp 접근 가능해야 도달(여기선 bob도 Tp 열람 가능 가정) |
| C9 | alice, SpiceDB 에러 | `GET /tickets`(목록) | 500 (fail-closed) |
| C10 | alice, SpiceDB 에러 | `GET /tickets/Tp/` | 500 (fail-closed, 미들웨어) |

핵심 불변식: **프로젝트 멤버라도 restricted 티켓은 못 본다**(C2, C5, C7). requester/assignee만 본다(C3, C6, C8). 단건/목록/subtask 세 경로에서 일관.

---

## Group D — 알려진 갭(문서화된 기대 동작)
| ID | 상황 | enforce 동작 | 비고 |
|----|------|-------------|------|
| D1 | `ProjectID` 없는 subtask | ListSubtasks에서 **전원에게 제외** | SpiceDB tuple 없음 → `can_view_*` 미포함. ✅ 해소: 생성 시 부모 project 상속(`Service.Create`) + migration `013`이 기존 orphan 보정. 본 시나리오는 보정 전(legacy/멀티레벨) orphan에 대한 fail-closed 동작을 고정한다. |

---

## §라이브 (cutover 시 수동, 본 자동화 범위 밖)
실제 Postgres + 공유 SpiceDB + `SPICEDB_ENFORCE=true` 배포에서:
1. 백필 완료(`cmd/backfill-spicedb` errors=0) 후 `zed relationship read goquest/*` 표본.
2. 멤버/비멤버 계정으로 C1~C8을 실제 HTTP로 재현.
3. `zed permission check`로 표본 일치 확인.
4. 로그에 `spicedb connected + ENFORCING` 확인(조용한 permissive 강등 아님).
상세: `docs/runbooks/spicedb-enforce-cutover.md`.
