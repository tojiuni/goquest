# goquest 기능 목록

AI 에이전트 워크플로우 연동을 위한 goquest 서비스 기능 레퍼런스.  
Base URL: `https://goquest.toji.homes/api/v1`

---

## 인증

| 방식 | 헤더 | 용도 |
|------|------|------|
| ZITADEL JWT | `Authorization: Bearer <jwt>` | 사용자/서비스 계정 |
| API Key | `X-API-Key: gqk_<key>` | 에이전트 → 프로젝트 범위 접근 |

> API Key는 프로젝트 단위로 발급된다. JWT보다 먼저 처리되며, 인증 성공 시 JWT 검증은 생략된다.

---

## 티켓 (Ticket)

핵심 작업 단위. 에이전트 워크플로우에서 가장 빈번하게 호출된다.

### 엔드포인트

| 메서드 | 경로 | 설명 |
|--------|------|------|
| `POST` | `/tickets` | 티켓 생성 |
| `GET` | `/tickets` | 목록 조회 (필터 지원) |
| `GET` | `/tickets/{id}` | 단건 조회 |
| `PATCH` | `/tickets/{id}` | 상태·필드 업데이트 |
| `DELETE` | `/tickets/{id}` | 취소 (cancelled) |
| `GET` | `/tickets/{id}/subtasks` | 서브태스크 목록 |
| `POST` | `/tickets/{id}/attempts` | 답변 제출 (answer 모드) |
| `GET` | `/tickets/{id}/attempts` | 제출 이력 조회 |

### 티켓 필드

| 필드 | 타입 | 값 |
|------|------|-----|
| `type` | string | `task` `bug` `feature` `incident` `request` |
| `status` | string | `pending` `assigned` `in_progress` `completed` `failed` `cancelled` |
| `priority` | string | `urgent` `high` `normal` `low` |
| `assignee_type` | string | `human` `agent` |
| `domain` | string | `infra` `data` `code` `monitor` `knowledge` `pm` |
| `completion_mode` | string | `manual` `answer` `agent` `subtask` |
| `visibility` | string | `project` `restricted` |

### 주요 생성 파라미터

```json
{
  "title": "요약",
  "description": "상세 내용",
  "type": "task",
  "priority": "normal",
  "assignee_type": "agent",
  "assignee_id": "hermes-infra",
  "domain": "infra",
  "project_id": "<uuid>",
  "completion_mode": "manual",
  "reward_xp": 50,
  "due_at": "2026-05-15T09:00:00Z",
  "callback_url": "https://my-agent.internal/callback",
  "tag_ids": ["<uuid>"],
  "completion_spec": {}
}
```

### 상태 전이

```
pending → assigned → in_progress → completed
                                 → failed
                   → cancelled
```

`completed` / `cancelled` / `failed` 은 terminal 상태 (이후 전이 불가).

### domain → hermes bot 매핑

| domain | 담당 bot |
|--------|---------|
| `infra` | hermes-infra |
| `data` | hermes-data |
| `code` | hermes-code |
| `monitor` | hermes-monitor |
| `knowledge` | hermes-knowledge |
| `pm` | hermes-pm |

### completion_mode 별 동작

| 모드 | 동작 |
|------|------|
| `manual` | 담당자가 직접 상태를 `completed`로 PATCH |
| `answer` | `/attempts`에 답변 제출 시 자동 평가; 정답이면 자동 완료 |
| `agent` | 에이전트가 PATCH로 완료 처리 |
| `subtask` | 모든 서브태스크가 완료되면 자동 완료 |

### answer 모드 completion_spec

```json
{
  "question": "프랑스의 수도는?",
  "answer_type": "exact",
  "correct_answer": "Paris",
  "max_attempts": 3,
  "hint": "유럽 서쪽에 위치합니다"
}
```

`answer_type`: `exact` (정확 일치) | `choice` (선택지) | `ai_eval` (AI 평가)

### 목록 필터 쿼리 파라미터

```
?status=pending&domain=infra&assignee_type=agent&assignee_id=hermes-infra
&requester_id=<uid>&project_id=<uuid>&limit=50&offset=0
```

### Webhook callback

티켓 생성 시 `callback_url` 설정 → terminal 상태 진입 시 POST 전송.

```json
{
  "event": "ticket.completed",
  "ticket": { ... }
}
```

---

## XP / 레벨 시스템

게임 퀘스트 엔진. 티켓 완료 시 `reward_xp`만큼 자동 적립.

| 메서드 | 경로 | 설명 |
|--------|------|------|
| `GET` | `/workspaces/{wid}/xp/me` | 내 XP/레벨 조회 |
| `GET` | `/xp/levels` | 전체 레벨 테이블 조회 |

### XP 응답 예시

```json
{
  "workspace_id": "<uuid>",
  "user_id": "lyckabc@toji.homes",
  "xp": 350,
  "level": 3
}
```

---

## 워크스페이스 (Workspace)

멀티테넌시 최상위 컨테이너.

| 메서드 | 경로 | 권한 | 설명 |
|--------|------|------|------|
| `POST` | `/workspaces` | 인증됨 | 생성 |
| `GET` | `/workspaces` | 인증됨 | 목록 |
| `GET` | `/workspaces/{wid}` | viewer | 단건 조회 |
| `PATCH` | `/workspaces/{wid}` | admin | 수정 |
| `DELETE` | `/workspaces/{wid}` | owner | 삭제 |
| `GET` | `/workspaces/{wid}/members` | viewer | 멤버 목록 |
| `PATCH` | `/workspaces/{wid}/members/{uid}/role` | owner | 역할 변경 |
| `DELETE` | `/workspaces/{wid}/members/{uid}` | admin | 멤버 제거 |
| `POST` | `/workspaces/{wid}/invitations` | admin | 초대 생성 |
| `GET` | `/workspaces/{wid}/invitations` | admin | 초대 목록 |
| `DELETE` | `/workspaces/{wid}/invitations/{iid}` | admin | 초대 취소 |
| `POST` | `/invitations/{token}/accept` | 인증됨 | 초대 수락 |

### 워크스페이스 역할

`owner` > `admin` > `member` > `viewer`

---

## 프로젝트 (Project)

워크스페이스 내 팀/캠페인 단위.

| 메서드 | 경로 | 권한 | 설명 |
|--------|------|------|------|
| `POST` | `/workspaces/{wid}/projects` | ws:admin | 생성 |
| `GET` | `/workspaces/{wid}/projects` | ws:viewer | 목록 |
| `GET` | `/workspaces/{wid}/projects/{pid}` | pj:viewer | 단건 |
| `PATCH` | `/workspaces/{wid}/projects/{pid}` | pj:admin | 수정 |
| `DELETE` | `/workspaces/{wid}/projects/{pid}` | ws:admin | 삭제 |
| `GET` | `/workspaces/{wid}/projects/{pid}/members` | pj:viewer | 멤버 목록 |
| `POST` | `/workspaces/{wid}/projects/{pid}/members` | pj:admin | 멤버 추가 |
| `PATCH` | `/workspaces/{wid}/projects/{pid}/members/{uid}/role` | pj:admin | 역할 변경 |
| `DELETE` | `/workspaces/{wid}/projects/{pid}/members/{uid}` | pj:admin | 멤버 제거 |

---

## 태그 (Tag)

워크스페이스 범위 레이블. 티켓에 복수 첨부 가능.

| 메서드 | 경로 | 설명 |
|--------|------|------|
| `POST` | `/workspaces/{wid}/tags` | 생성 |
| `GET` | `/workspaces/{wid}/tags` | 목록 |
| `GET` | `/workspaces/{wid}/tags/{tid}` | 단건 |
| `PATCH` | `/workspaces/{wid}/tags/{tid}` | 수정 |
| `DELETE` | `/workspaces/{wid}/tags/{tid}` | 삭제 |

```json
{ "name": "infra", "color": "#4F46E5" }
```

---

## 템플릿 (Template)

프로젝트 단위 재사용 티켓 형틀.

| 메서드 | 경로 | 설명 |
|--------|------|------|
| `POST` | `/workspaces/{wid}/projects/{pid}/templates` | 생성 |
| `GET` | `/workspaces/{wid}/projects/{pid}/templates` | 목록 |
| `GET` | `/workspaces/{wid}/projects/{pid}/templates/{tid}` | 단건 |
| `DELETE` | `/workspaces/{wid}/projects/{pid}/templates/{tid}` | 삭제 |
| `POST` | `/workspaces/{wid}/projects/{pid}/templates/{tid}/instantiate` | 티켓 즉시 생성 |

### instantiate

```json
{
  "title": "덮어쓸 제목 (선택)",
  "assignee_id": "hermes-infra"
}
```

응답: 생성된 `Ticket` 객체.

---

## API 키 (API Key)

에이전트가 프로젝트에 접근할 때 사용. 평문 키는 생성 시 1회만 반환.

| 메서드 | 경로 | 설명 |
|--------|------|------|
| `POST` | `/workspaces/{wid}/projects/{pid}/api-keys` | 키 생성 |
| `GET` | `/workspaces/{wid}/projects/{pid}/api-keys` | 목록 |
| `DELETE` | `/workspaces/{wid}/projects/{pid}/api-keys/{kid}` | 삭제 |

```json
// POST 응답 (1회만)
{ "id": "...", "name": "ci-pipeline", "key": "gqk_..." }
```

---

## SSE 이벤트 스트림

실시간 이벤트 구독. `EventSource`가 아닌 `fetch` 스트리밍 사용 (Bearer 헤더 필요).

```
GET /api/v1/events
Authorization: Bearer <jwt>
```

### 이벤트 스트림 형식

```
data: {"stream":"goquest.tickets.created","data":"{...}"}

data: {"stream":"goquest.tickets.completed","data":"{...}"}

data: {"stream":"goquest.tickets.assigned","data":"{...}"}
```

---

## Bot 인터페이스 (`/execute`)

goquest bot 서버에 직접 요청할 때 사용 (KEDA interceptor 경유).

```
POST http://keda-add-ons-http-interceptor-proxy.keda.svc:8080/execute
Host: goquest.goquest.svc.cluster.local
Content-Type: application/json
```

### 지원 capability

| capability | auth_level | 설명 |
|-----------|------------|------|
| `ticket.create` | `write-low` | 티켓 생성 |
| `ticket.get` | `read` | 단건 조회 |
| `ticket.list` | `read` | 목록 조회 |
| `ticket.update` | `write-low` | 상태/필드 업데이트 |
| `ticket.cancel` | `write-mid` | 티켓 취소 |
| `ticket.assign` | `write-low` | 담당자 지정 |
| `ticket.health` | `read` | 서비스 헬스 |

### 요청 예시

```json
{
  "task_id": "550e8400-e29b-41d4-a716-446655440000",
  "capability": "ticket.create",
  "payload": {
    "title": "인프라 점검",
    "type": "task",
    "priority": "high",
    "assignee_type": "agent",
    "domain": "infra",
    "project_id": "<uuid>",
    "reward_xp": 100,
    "callback_url": "https://my-agent.internal/done"
  },
  "caller": "openclaw",
  "auth_level": "write-low"
}
```

---

## SLA 워커

백그라운드 프로세스. `due_at`이 지난 `pending`/`assigned`/`in_progress` 티켓을 1분 주기로 스캔하여 `failed`로 전이.  
별도 API 없음 — `due_at` 설정으로 간접 제어.

---

## 헬스체크

| 경로 | 설명 |
|------|------|
| `GET /healthz` | 서비스 생존 확인 |
| `GET /api/v1/health` | 서비스 생존 확인 |
| `GET /api/v1/health/deps` | DB / Redis 연결 상태 |
