# P3: goquest SpiceDB 통합 — 티켓 가시성 fine-grained 강제 (파일럿)

- 날짜: 2026-05-26
- 대상 repo: goquest (코드) + neunexus (배포 config)
- 상위: 플랫폼 인가 ADR(`neunexus:docs/superpowers/specs/2026-05-26-platform-authz-architecture-design.md`) Phase 3
- 선행: P1(SpiceDB 플랫폼 기반 — 통합 스키마에 `goquest/*` 정의 라이브) 완료. **P2(ZITADEL→SpiceDB 브리지)는 보류** (coarse 역할은 JWT `groups`로, fine 멤버십은 서비스 dual-write로 흐르므로 즉시 필요 낮음 — P3로 실제 필요 검증 후 재결정).

## 배경 / 동기

플랫폼 인가의 파일럿으로 goquest를 SpiceDB(공유 lymphhub 인스턴스)에 통합한다. 현재 상태:
- goquest 배포에 `SPICEDB_ENDPOINT` 미설정 → 기본 `localhost:50051` 연결 실패 → `NoopChecker`(항상 허용) + `NoopWriter`(쓰기 폐기). **fine-grained 인가가 사실상 꺼져 있음.**
- goquest spicedb writer/checker가 **bare 타입명**(`"workspace"`/`"project"`/`"ticket"`)을 사용 → P1에서 스키마는 `goquest/*` prefix로 들어갔으므로 **불일치**(연결 시 lymphhub의 unprefixed 타입과 충돌·오작동).
- 핸들러에서 SpiceDB 사용처는 `CanViewTicket`(ticket.go) **한 곳**. `WriteTicket`만 배선됨. `WriteWorkspaceMember`/`WriteProjectWorkspace`/`WriteProjectMember`는 인터페이스에만 존재(미호출).
- 워크스페이스/프로젝트 역할 인가(`RequireWorkspaceRole`/`RequireProjectRole`)는 DB 테이블(`workspace_members`/`project_members`)로 독립 동작 중.

## 목표 (파일럿 범위 A)

티켓 **가시성**(`CanViewTicket`)을 공유 SpiceDB(`goquest/*`)로 실제 강제한다. 안전 롤아웃(백필 전 거부 방지)을 보장한다.

## 비목표 (후속)
- 역할 미들웨어(`RequireWorkspaceRole`/`RequireProjectRole`)의 SpiceDB 이전 — DB 유지(후속 P3-next/P4).
- ZITADEL→SpiceDB 브리지(P2, 보류).

## 설계

### (a) prefix 정합 — goquest object type
`internal/spicedb`의 writer/checker가 `goquest/` prefix를 쓰도록 변경(subject `user`는 공유 — unprefixed 유지).
- 패키지 상수: `const objPrefix = "goquest/"` (또는 타입별 상수 `wsType="goquest/workspace"`, `pjType="goquest/project"`, `tkType="goquest/ticket"`).
- writer.go의 `touch(...)`/`delete(...)` 첫 인자(resType)와 project→workspace의 subType을 prefixed로:
  - `WriteTicket`: `goquest/ticket`(res), project subType `goquest/project`, requester/assignee subType `user`.
  - `WriteWorkspaceMember`/`DeleteWorkspaceMember`: `goquest/workspace`, subType `user`.
  - `WriteProjectMember`: `goquest/project`, subType `user`.
  - `WriteProjectWorkspace`: res `goquest/project`, subType `goquest/workspace`.
  - `WriteTicketRestrictedViewer`: `goquest/ticket`, subType `user`.
- checker.go의 `check(...)` resType: `goquest/ticket`, `goquest/project`; subType `user`.
- 단위 테스트: writer/checker가 `goquest/*` 타입의 RelationshipUpdate/CheckRequest를 생성하는지 검증(grpc 연결 없이 구조 검사).

### (b) dual-write 배선 (멤버/구조 관계 유지)
티켓 가시성 체인 `ticket->can_view_project = project->can_view + requester + assignee`, `project->can_view = (project)admin/member/viewer + workspace->can_view`가 동작하려면 멤버·구조 tuple이 SpiceDB에 있어야 한다. `RelationshipWriter`를 `workspace.Service`/`project.Service`에 주입하고 각 변경점에서 호출(best-effort, 실패는 로깅 — DB가 source-of-truth).

- `workspace.Service`(NewService에 writer 추가):
  - `create()` 오너 멤버 추가 후 → `WriteWorkspaceMember(wsID, ownerID, "owner")`.
  - `SetMemberRole` → 기존 역할 delete + 새 역할 `WriteWorkspaceMember`(역할 변경).
  - `RemoveMember` → `DeleteWorkspaceMember(wsID, userID)`.
  - 멤버 추가 경로(`AddMember`/초대 수락 시 멤버 생성) → `WriteWorkspaceMember`.
- `project.Service`(NewService에 writer 추가):
  - `Create()` → `WriteProjectWorkspace(projectID, workspaceID)` + 생성자 멤버(`WriteProjectMember(projectID, creatorID, "admin")` — 생성자 역할에 맞춤).
  - `AddMember`/`SetMemberRole`/`RemoveMember` → `WriteProjectMember`/(역할변경 시 delete+write)/`DeleteProjectMember`.
- `ticket` create는 기존 `WriteTicket`(핸들러) 유지(prefix만 정합).
- 쓰기는 TOUCH(idempotent). 역할 변경은 이전 역할 relation delete 후 신규 write.
- ⚠️ 신규 `DeleteProjectMember`가 writer 인터페이스에 없으면 추가(스키마상 project member relation 삭제).

### (c) 백필 command
신규 `cmd/backfill-spicedb`(또는 기존 CLI 서브커맨드): DB를 순회해 모든 관계를 SpiceDB에 TOUCH.
- workspaces → 각 멤버 `goquest/workspace:<id>#<role>@user:<uid>`.
- projects → `goquest/project:<id>#workspace@goquest/workspace:<wsid>` + 각 멤버.
- tickets → `goquest/ticket:<id>#project@goquest/project:<pid>` + requester/assignee(+restricted_viewer).
- idempotent(TOUCH) → 재실행 안전. 일회성 Job으로 실행.

### (d) config + 안전 롤아웃 (enforce 플래그 분리)
연결 시 writer/checker가 동시에 켜지면 백필 전 `CanViewTicket`이 거부할 수 있다. **writer는 항상 실제, checker는 `SPICEDB_ENFORCE`로 분리.**
- `internal/config`: `SpiceDBEnforce bool` 추가(`SPICEDB_ENFORCE`, 기본 false).
- `cmd/service/main.go`: 연결 성공 시
  - `sdbWriter = sdbClient.Writer()` (항상 — dual-write 활성).
  - `sdbChecker = sdbClient.Checker()` **단, `cfg.SpiceDBEnforce`일 때만**; 아니면 `NoopChecker` 유지(가시성 체크 허용 = shadow).
- **롤아웃 순서:**
  1. (a)(b)(d) 코드 배포 + 배포 config에 `SPICEDB_ENDPOINT=spicedb.lymphhub.svc:50051`, `SPICEDB_TOKEN`, `SPICEDB_INSECURE=true`, `SPICEDB_ENFORCE=false` → **dual-write 활성, 체크는 허용(shadow)**.
  2. 백필 Job 실행 → 기존 데이터 tuple 채움.
  3. `zed relationship read`로 표본 검증.
  4. `SPICEDB_ENFORCE=true`로 전환 → `CanViewTicket` 실제 강제.

### 배포 (neunexus)
goquest-service deployment에 env 추가(VSO/Vault):
- `SPICEDB_ENDPOINT=spicedb.lymphhub.svc:50051`
- `SPICEDB_TOKEN` ← Vault `secret/neunexus/spicedb#preshared-key`
- `SPICEDB_INSECURE=true`
- `SPICEDB_ENFORCE` (1→false, 4→true 단계 전환)

## 데이터 흐름 (강제 후)
```
GET /tickets/{id}  (project 가시성 티켓)
 → CanViewTicket(ticketID, user, mode)
 → SpiceDB Check goquest/ticket:<id> can_view_project user:<sub>
     = project->can_view + requester + assignee
     = (goquest/project member/admin/viewer) + (goquest/workspace member... via project->workspace->can_view) + requester/assignee
 → 백필+dual-write로 채워진 tuple로 판정
```

## 테스트 계획
단위(grpc 불필요):
- writer/checker가 `goquest/*` 타입을 생성하는지(RelationshipUpdate/CheckPermissionRequest 구조 검사).
- 멤버 변경 시 service가 writer를 올바른 인자로 호출(mock writer): create→owner write, SetMemberRole→delete+write, RemoveMember→delete, project Create→project→workspace + creator member.
- 백필: fake store들 + mock writer로 모든 tuple 생성 검증, idempotent.
- enforce 플래그: 연결 시 writer는 실제·checker는 enforce=false면 Noop / true면 실제(main wiring 단위 또는 통합).

롤아웃 검증:
- shadow(enforce=false) 단계: 기능 회귀 없음(체크 허용).
- 백필 후 `zed relationship read goquest/workspace|project|ticket` 표본 수 확인.
- enforce=true 후: requester/assignee/멤버는 view 가능, 비멤버는 거부.

## enforce 전환 전 반드시 해결 (블로커) — 상태
`SPICEDB_ENFORCE=true` 전에 처리/결정해야 하는 항목 (holistic 리뷰 식별). 해소 상태(2026-05-27, PR enforce-blockers):
1. **restricted_viewer 미배선** — ✅ **결정**: restricted = requester+assignee 전용(추가 viewer 엔드포인트 없음). cutover 런북에 문서화, 코드 변경 없음.
2. **재배정 드리프트** — ✅ **해소**: ticket `Update` 핸들러가 `ReplaceTicketAssignee`로 assignee를 SpiceDB에 재동기. (visibility는 tuple이 아니라 체크 시점에 반영되므로 별도 불필요.)
3. **`List` 엔드포인트 미강제** — ✅ **해소**: List를 `CanViewTicket` post-filter(shadow에선 무변경). 단 `total`은 페이지 근사치(LookupResources로 정밀화는 후속).
4. (decision) 워크스페이스/프로젝트 삭제 시 tuple 고아, 백필 role 화이트리스트 미검증 — 런북 "GA 전 정리"에 추적.
5. (운영) dial 실패 시 조용한 permissive 강등 — ✅ cutover 런북에 `ENFORCING` 로그 확인 단계 추가.
6. (추적) `Get`(fail-open) vs `List`(fail-closed) 에러 정책 불일치 + List 핸들러 테스트 갭 — 런북 "GA 전 정리"에 기록.

상세 절차: `docs/runbooks/spicedb-enforce-cutover.md`.

## 미해결 / 후속
- 역할 미들웨어 SpiceDB 이전(전면 인가) — 후속.
- dual-write가 best-effort라 DB↔SpiceDB 드리프트 가능 → 주기 reconcile(백필 재실행)로 보정.
- goquest.zed(서비스 로컬, unprefixed)와 플랫폼 스키마(`goquest/*`)의 단일 출처화 — 본 P3에서 코드가 `goquest/*`를 쓰므로, goquest.zed는 문서/참조용으로 남기고 플랫폼 스키마를 런타임 출처로 한다(중복 정의 주의).
