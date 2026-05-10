# 테스트 체크리스트 — Phase 0 & 1

> 브랜치: `test/phase-0-1-unit-tests`  
> 전략: DB 없이 mock store 사용 (unit test)

---

## internal/ticket — 모델 확장 검증

- [x] `TestCompletionSpec_MarshalJSON_Nil` — nil spec → `{}`
- [x] `TestCompletionSpec_MarshalJSON_Value` — 값 있는 spec 정상 직렬화
- [x] `TestTicket_NewFields_Defaults` — Visibility/CompletionMode 기본값 확인
- [x] `TestVisibility_Constants` — 상수 문자열 값 확인
- [x] `TestCompletionMode_Constants` — 상수 문자열 값 확인

## internal/workspace — 서비스 로직

- [x] `TestHasRole_Ordering` — owner > admin > member > viewer 순서
- [x] `TestService_Create_Success` — 워크스페이스 생성 + owner 자동 멤버 등록
- [x] `TestService_Create_EmptyName` — 빈 이름 → 에러
- [x] `TestService_Create_PersonalType` — personal 타입 보존
- [x] `TestService_EnsurePersonal_CreatesNew` — personal ws 없을 때 생성
- [x] `TestService_EnsurePersonal_ReturnsExisting` — 이미 있으면 그대로 반환
- [x] `TestService_AcceptInvitation_Success` — 유효 토큰 → 멤버 추가
- [x] `TestService_AcceptInvitation_Expired` — 만료 토큰 → 에러
- [x] `TestService_AcceptInvitation_AlreadyUsed` — 이미 사용된 토큰 → 에러
- [x] `TestService_SetMemberRole_OnlyOwner` — owner만 역할 변경 가능
- [x] `TestService_RemoveMember_RequiresAdmin` — admin 미만 → 에러

## internal/project — 서비스 로직

- [x] `TestHasRole_Ordering` — admin > member > viewer 순서
- [x] `TestService_Create_Success` — 프로젝트 생성 + creator admin 자동 등록
- [x] `TestService_Create_EmptyName` — 빈 이름 → 에러

## internal/auth — RBAC 미들웨어

- [x] `TestRequireWorkspaceRole_Authorized` — 충분한 역할 → 200 통과
- [x] `TestRequireWorkspaceRole_Forbidden` — 부족한 역할 → 403
- [x] `TestRequireWorkspaceRole_NotMember` — 멤버 아님 → 403
- [x] `TestRequireWorkspaceRole_InvalidWID` — 잘못된 UUID → 400
- [x] `TestRequireProjectRole_ExplicitRole` — 명시 프로젝트 역할 → 통과
- [x] `TestRequireProjectRole_FallbackFromWorkspace` — ws 역할 폴백 동작

## internal/handler — HTTP 핸들러

- [x] `TestWorkspaceHandler_Create_201` — POST /workspaces → 201 + body
- [x] `TestWorkspaceHandler_Create_BadBody` — 잘못된 바디 → 400
- [x] `TestWorkspaceHandler_List_200` — GET /workspaces → 200 + list
- [x] `TestWorkspaceHandler_List_Empty` — 빈 목록 → 200
- [x] `TestWorkspaceHandler_AcceptInvitation_Success` — 유효 토큰 → 200
- [x] `TestWorkspaceHandler_AcceptInvitation_Error` — 에러 토큰 → 400
- [x] `TestProjectHandler_Create_201` — POST /workspaces/:wid/projects → 201
- [x] `TestProjectHandler_Create_BadBody` — 잘못된 바디 → 400
- [x] `TestProjectHandler_List_200` — GET /workspaces/:wid/projects → 200

---

## 합격 기준

| 기준 | 내용 |
|------|------|
| `go test ./...` | 모든 패키지 PASS |
| 커버리지 | 서비스/핸들러 핵심 경로 70% 이상 |
| mock only | DB 연결 없이 실행 가능 |
