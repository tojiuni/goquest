# goquest QA 시나리오

**대상 버전**: Phase 1–6 (PR #1–#8)  
**베이스 URL**: `https://goquest.toji.homes/api/v1`  
**인증**: ZITADEL OIDC Bearer 토큰 또는 `X-API-Key` 헤더

---

## 0. 공통 전제

```bash
BASE="https://goquest.toji.homes/api/v1"
AUTH="Authorization: Bearer <token>"  # 또는 X-API-Key: gqk_...
```

---

## 1. 헬스 체크

| # | 시나리오 | 커맨드 | 기대 결과 |
|---|---------|-------|---------|
| 1-1 | 서비스 헬스 | `GET /healthz` | `{"status":"ok"}` |
| 1-2 | 의존성 헬스 | `GET /api/v1/health/deps` | `{"postgres":"ok","redis":"ok"}` |

---

## 2. 인증

| # | 시나리오 | 기대 결과 |
|---|---------|---------|
| 2-1 | 토큰 없이 보호 엔드포인트 호출 | `401 Unauthorized` |
| 2-2 | 만료 토큰으로 호출 | `401 invalid token` |
| 2-3 | 유효 Bearer 토큰으로 호출 | `200` |
| 2-4 | 유효 `X-API-Key` 헤더로 호출 | `200` (API Key 생성 선행 필요) |

---

## 3. Workspace

| # | 시나리오 | 커맨드 | 기대 결과 |
|---|---------|-------|---------|
| 3-1 | Workspace 생성 | `POST /workspaces {"name":"acme","display_name":"Acme"}` | `201` + `id` 반환 |
| 3-2 | Workspace 목록 | `GET /workspaces` | 내가 속한 워크스페이스 목록 |
| 3-3 | Workspace 상세 | `GET /workspaces/{wid}` | `200` + 상세 정보 |
| 3-4 | Workspace 수정 (admin) | `PATCH /workspaces/{wid} {"display_name":"Acme Corp"}` | `200` |
| 3-5 | Workspace 수정 (viewer 권한) | `PATCH /workspaces/{wid}` | `403 Forbidden` |
| 3-6 | 멤버 목록 | `GET /workspaces/{wid}/members` | 멤버 배열 |
| 3-7 | 멤버 역할 변경 (owner) | `PATCH /workspaces/{wid}/members/{uid}/role {"role":"admin"}` | `200` |
| 3-8 | 초대 생성 | `POST /workspaces/{wid}/invitations {"email":"...","role":"viewer"}` | `201` + `token` |
| 3-9 | 초대 수락 | `POST /invitations/{token}/accept` | `200` |
| 3-10 | Workspace 삭제 (owner) | `DELETE /workspaces/{wid}` | `204` |

---

## 4. Project

| # | 시나리오 | 기대 결과 |
|---|---------|---------|
| 4-1 | 프로젝트 생성 (ws_admin) | `POST /workspaces/{wid}/projects {"name":"backend"}` → `201` |
| 4-2 | 프로젝트 생성 (ws_viewer 권한) | `403 Forbidden` |
| 4-3 | 프로젝트 목록 | `GET /workspaces/{wid}/projects` → `200` |
| 4-4 | 프로젝트 멤버 추가 | `POST /workspaces/{wid}/projects/{pid}/members {"user_id":"...","role":"admin"}` → `201` |
| 4-5 | 프로젝트 멤버 역할 변경 | `PATCH .../members/{uid}/role {"role":"viewer"}` → `200` |
| 4-6 | 프로젝트 삭제 (ws_admin) | `DELETE .../projects/{pid}` → `204` |

---

## 5. Tag

| # | 시나리오 | 기대 결과 |
|---|---------|---------|
| 5-1 | 태그 생성 | `POST /workspaces/{wid}/tags {"name":"bug","color":"#ff0000"}` → `201` |
| 5-2 | 태그 목록 | `GET /workspaces/{wid}/tags` → `200` |
| 5-3 | 태그 수정 | `PATCH /workspaces/{wid}/tags/{tid} {"color":"#00ff00"}` → `200` |
| 5-4 | 태그 삭제 | `DELETE /workspaces/{wid}/tags/{tid}` → `204` |

---

## 6. Ticket (기본)

| # | 시나리오 | 기대 결과 |
|---|---------|---------|
| 6-1 | 티켓 생성 (기본) | `POST /tickets {"title":"fix bug"}` → `201`, `status=pending`, `type=task` |
| 6-2 | 빈 제목으로 생성 | → `400 title is required` |
| 6-3 | 잘못된 domain | `{"title":"x","domain":"invalid"}` → `400 invalid domain` |
| 6-4 | 에이전트 티켓 생성 | `{"assignee_type":"agent","assignee_id":"hermes"}` → `201`, PublishAssigned 이벤트 발행 |
| 6-5 | 티켓 목록 | `GET /tickets` → `200` + 배열 |
| 6-6 | 티켓 상세 | `GET /tickets/{id}` → `200` |
| 6-7 | 티켓 상태 변경 | `PATCH /tickets/{id} {"status":"in_progress"}` → `200` |
| 6-8 | 티켓 취소 | `DELETE /tickets/{id}` → `200`, `status=cancelled` |
| 6-9 | 서브태스크 조회 | `GET /tickets/{id}/subtasks` → `200` + 배열 |

---

## 7. Ticket — 완료 모드 & 답안 채점

| # | 시나리오 | 기대 결과 |
|---|---------|---------|
| 7-1 | answer-mode 티켓 생성 | `{"title":"Q","completion_mode":"answer","completion_spec":{"question":"2+2?","answer_type":"exact","correct_answer":"4","max_attempts":3}}` → `201` |
| 7-2 | 정답 제출 | `POST /tickets/{id}/attempts {"answer":"4"}` → `201`, `is_correct=true`, 티켓 `status=completed` |
| 7-3 | 오답 제출 | `{"answer":"5"}` → `201`, `is_correct=false` |
| 7-4 | 최대 시도 초과 | 3회 이후 → `400 max attempts reached` |
| 7-5 | 완료된 티켓에 제출 | → `400 ticket is already closed` |
| 7-6 | manual-mode 티켓에 제출 | → `400 ticket is not in answer mode` |
| 7-7 | 답안 목록 | `GET /tickets/{id}/attempts` → `200` + 배열 |

---

## 8. XP / 레벨

| # | 시나리오 | 기대 결과 |
|---|---------|---------|
| 8-1 | 레벨 목록 | `GET /xp/levels` → `200`, 14개 레벨 |
| 8-2 | 내 XP 조회 | `GET /workspaces/{wid}/xp/me` → `200`, `xp`, `level` 필드 |
| 8-3 | `reward_xp` 있는 티켓 완료 | `PATCH /tickets/{id} {"status":"completed"}` → XP 자동 증가 (8-2 재조회로 확인) |
| 8-4 | 레벨업 확인 | XP가 임계치 초과 시 `level` 증가 |

---

## 9. API Key

| # | 시나리오 | 기대 결과 |
|---|---------|---------|
| 9-1 | API Key 생성 | `POST /workspaces/{wid}/projects/{pid}/api-keys {"name":"ci-bot"}` → `201`, `key: "gqk_..."` (once) |
| 9-2 | API Key 목록 | `GET .../api-keys` → `200`, `key` 필드 없음 (hash만 저장) |
| 9-3 | X-API-Key로 인증 | `GET /api/v1/xp/levels -H "X-API-Key: gqk_..."` → `200` |
| 9-4 | 잘못된 키 | `X-API-Key: gqk_bogus` → `401 invalid api key` |
| 9-5 | API Key 삭제 | `DELETE .../api-keys/{kid}` → `204` |
| 9-6 | 삭제된 키로 인증 | → `401` |

---

## 10. Ticket Templates

| # | 시나리오 | 기대 결과 |
|---|---------|---------|
| 10-1 | 템플릿 생성 | `POST .../templates {"name":"Bug Report","title":"Bug: fix {{desc}}","reward_xp":10}` → `201` |
| 10-2 | 이름 없이 생성 | → `400 name is required` |
| 10-3 | 템플릿 목록 | `GET .../templates` → `200` + 배열 |
| 10-4 | 템플릿 상세 | `GET .../templates/{tid}` → `200` |
| 10-5 | 템플릿으로 티켓 생성 | `POST .../templates/{tid}/instantiate {}` → `201` 티켓, 템플릿 필드 복사 |
| 10-6 | 오버라이드로 인스턴스화 | `{"title":"custom title"}` → 제목 오버라이드 적용 |
| 10-7 | 템플릿 삭제 | `DELETE .../templates/{tid}` → `204` |

---

## 11. SLA Timer

| # | 시나리오 | 기대 결과 |
|---|---------|---------|
| 11-1 | due_at 과거 티켓 생성 | `POST /tickets {"title":"overdue","due_at":"2020-01-01T00:00:00Z"}` → `201` |
| 11-2 | 1분 대기 후 상태 확인 | `GET /tickets/{id}` → `status=failed` |
| 11-3 | 이미 완료된 티켓 | due_at 과거여도 `completed`/`cancelled` 상태 유지 |

---

## 12. Webhook 콜백

| # | 시나리오 | 기대 결과 |
|---|---------|---------|
| 12-1 | callback_url 설정 티켓 생성 | `POST /tickets {"title":"cb","callback_url":"https://webhook.site/..."}` → `201` |
| 12-2 | 티켓 완료 | `PATCH /tickets/{id} {"status":"completed"}` → callback URL에 POST 도착 |
| 12-3 | 티켓 실패 | `{"status":"failed"}` → callback URL에 POST 도착 |
| 12-4 | callback_url 없는 티켓 | 완료 시 오류 없이 정상 처리 |

---

## 13. SSE 이벤트 스트림

| # | 시나리오 | 기대 결과 |
|---|---------|---------|
| 13-1 | SSE 연결 | `GET /api/v1/events` (Auth 필요) → `text/event-stream`, 연결 유지 |
| 13-2 | 티켓 생성 이벤트 | 연결 후 다른 터미널에서 티켓 생성 → `data: {"stream":"goquest.tickets.created",...}` 수신 |
| 13-3 | 티켓 완료 이벤트 | 티켓 완료 시 `goquest.tickets.completed` 수신 |
| 13-4 | Heartbeat | 25초 간격으로 `: heartbeat` 수신 |

---

## 14. SpiceDB 권한 (Phase 4)

| # | 시나리오 | 기대 결과 |
|---|---------|---------|
| 14-1 | `visibility=restricted` 티켓 조회 (권한 있음) | `200` |
| 14-2 | `visibility=restricted` 티켓 조회 (권한 없음) | `403 Forbidden` |
| 14-3 | SpiceDB 미설정 환경 | NoopChecker로 폴백, 모든 요청 허용 |

---

## QA 결과 기록표

| 시나리오 | 결과 | 비고 |
|---------|------|------|
| 1-1 | | |
| 1-2 | | |
| ... | | |
