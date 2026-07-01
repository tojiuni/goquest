# goquest ↔ VCS 연동 (branch/commit/PR 링크 + 자동 상태전이) 설계

- 날짜: 2026-07-01
- 대상: goquest (신규 모듈 `internal/vcs/`) + goquest-mcp (도구/프롬프트)
- 작성자: lyckabc
- 선행/연관: ticket `ref_ids`(문서 링크, 별개), goquest 모듈 패턴(`internal/<module>/` = service+store+model, `cmd/service/main.go` 배선), 기존 아웃바운드 `internal/webhook`(deliver)·`internal/integration/nuclex`(emitter)

## 배경 / 동기

goquest 티켓으로 모든 작업을 관리하지만, 실제 코드 산출물(branch·commit·PR)과 티켓이 분리돼 있어 진행 상황을 사람이 수동 연결해야 한다. 목표는 **git 이벤트를 받아 티켓에 branch/commit/PR을 자동 링크하고 상태를 전이**하는 것이다. 여러 git 제공자(GitHub/GitLab/Forgejo)가 **같은 pipeline**을 쓰도록 provider를 **얇은 어댑터 모듈**로 분리한다.

## 핵심 결정 (확정)

1. **방향**: inbound webhook. goquest가 provider webhook을 수신·정규화·링크. (아웃바운드 goquest→git은 비목표.)
2. **매칭**: `title_name`이 branch명 / commit 메시지 / PR 제목·본문 어디든 포함되면 그 티켓에 링크. **1 티켓 : N git객체**(여러 branch·commit·PR 누적).
3. **자동화**: 링크 + **자동 상태 전이**(전진만, 수동/terminal 보호).
4. **branch = 1급 앵커**: branch 생성(title_name 매칭) 시 branch-link 생성 + 작업시작 전이. 그 branch의 commit은 메시지에 title_name이 없어도 상속. shared branch(main 등)는 commit-메시지 경로로 링크.
5. **아키텍처**: goquest 내부 모듈 `internal/vcs/` + `Provider` 인터페이스. **GitHub 어댑터 우선**, GitLab/Forgejo는 인터페이스만(후속).
6. **등록**: `vcs_repos`를 **API로 등록**. vcs-links는 **`GET /tickets/{id}` 응답에 embed**.

## 목표

1. GitHub webhook(push/PR/create/delete)을 받아 title_name 매칭으로 branch/commit/PR을 티켓에 링크.
2. 링크 이벤트에 따라 티켓 상태 자동 전이(규칙 기반, 상태머신 준수).
3. provider 무관 공유 linker pipeline + provider별 얇은 어댑터.
4. goquest-mcp에 repo 등록 도구 + 규약 프롬프트. 링크는 티켓 응답 embed로 노출.

## 비목표 (범위 밖)

- GitLab/Forgejo 어댑터 구현 (인터페이스만 열어둠).
- 양방향(goquest→git: PR 코멘트/상태 push).
- branch/PR에서 티켓 **자동 생성** (기존 티켓 링크만; 미해석 토큰은 무시).
- 1 repo → 다중 project 매핑.
- 비동기 워커(Redis 스트림) — v1은 요청 내 동기 처리 + 멱등.
- MCP 전용 vcs-link 읽기 도구(embed로 충분).

## 설계

### 1. 모듈 레이아웃 (`internal/vcs/`)

```
provider.go   Provider 인터페이스 + 정규화 VcsEvent + registry(name→Provider)
github.go     GitHub 어댑터 (HMAC 검증, push/pull_request/create/delete → []VcsEvent)
linker.go     공유 pipeline (repo→project 해석 → title_name 추출 → 링크 upsert → 자동전이)
model.go      VcsRepo, VcsLink, VcsEvent, EventKind
store.go      인터페이스 (Store)
store_pg.go   Postgres 구현
```
- 핸들러: `internal/handler/vcs.go` — `POST /api/v1/vcs/webhook/{provider}`, `POST /api/v1/vcs/repos`(등록), `GET /api/v1/vcs/repos`(목록).
- `cmd/service/main.go`에 배선. 티켓 조회 시 `vcs_links` embed는 ticket 서비스/스토어가 `vcs` Store를 통해 로드(또는 ticket 핸들러가 vcs Store 주입받아 합성).

### 2. Provider 인터페이스 (provider 차이는 여기만)

```go
type EventKind string
const (
    BranchCreated EventKind = "branch_created"
    BranchDeleted EventKind = "branch_deleted"
    CommitPushed  EventKind = "commit_pushed"
    PROpened      EventKind = "pr_opened"
    PRUpdated     EventKind = "pr_updated"
    PRMerged      EventKind = "pr_merged"
    PRClosed      EventKind = "pr_closed"
)

type VcsEvent struct {
    Kind      EventKind
    Repo      string // full name, e.g. "tojiuni/goquest"
    Branch    string // ref short name (branch/commit branch/pr head)
    CommitSHA string
    CommitMsg string
    PRNumber  int
    PRTitle   string
    PRBody    string
    PRState   string
    PRMerged  bool
    URL       string // 링크 대상 URL
    Actor     string
}

type Provider interface {
    Name() string
    Verify(r *http.Request, body []byte, secret string) error   // 서명/HMAC
    Parse(eventType string, body []byte) ([]VcsEvent, error)     // 정규화 (1 payload → N event)
}
```
- GitHub 어댑터: `X-Hub-Signature-256` HMAC-SHA256 검증. `X-GitHub-Event`로 분기(`push`→branch/commit, `pull_request`→PR, `create`/`delete`→branch). push payload는 여러 commit → 여러 `CommitPushed`.

### 3. 데이터 모델

`vcs_repos` (등록):
| 컬럼 | 설명 |
|---|---|
| id | uuid |
| provider | `github`\|`gitlab`\|`forgejo` |
| repo_full_name | 예 `tojiuni/goquest` (provider당 unique) |
| project_id | 매핑 대상 project (title_name 해석 범위) |
| auto_transition | bool, default true (false면 링크만) |
| merge_target | `completed`(default) \| `pending_approval` |
| active | bool |
| created_at | |

`vcs_links`:
| 컬럼 | 설명 |
|---|---|
| id | uuid |
| ticket_id | fk |
| provider | |
| repo_full_name | |
| kind | `branch`\|`commit`\|`pull_request` |
| external_ref | branch명 \| commit sha \| PR 번호(문자열) |
| url | git 객체 URL |
| state | `open`\|`merged`\|`closed`\|`deleted` (commit은 `open` 고정/불변) |
| title | PR 제목 \| commit 제목줄 |
| actor | |
| created_at, updated_at | |

- **unique(ticket_id, provider, repo_full_name, kind, external_ref)** → 멱등 upsert.
- `GET /tickets/{id}` 응답에 `vcs_links: []VcsLink` embed.

### 4. 공유 linker pipeline

정규화된 `VcsEvent`마다:
1. `repo → project` 해석(`vcs_repos` active). 미등록/비활성 → 무시(202 + log).
2. **title_name 후보 추출**:
   - `BranchCreated`/`BranchDeleted`: `Branch`에서.
   - `CommitPushed`: `CommitMsg`에서. **추가로** `Branch`가 이 repo에서 링크된 branch(`vcs_links` kind=branch)면 그 티켓 **상속**(메시지에 없어도).
   - PR 계열: `PRTitle` + `PRBody` + `Branch`(head)에서.
   - 토큰 스캔 정규식 `[a-z][a-z0-9]*(-[a-z0-9]+)+`. 각 토큰을 project 내 `title_name`으로 조회 → **해석되는 것만** 링크(미해석 무시 → dangling 없음).
3. 해석된 티켓마다 `vcs_link` **멱등 upsert**, state 갱신(`PRMerged`→merged, `BranchDeleted`→deleted, `PRClosed`→closed).
4. 자동 전이(§5). 전이·링크 결과를 티켓 코멘트로 기록(예: `linked PR #12 (merged) → completed by vcs:github`).

### 5. 자동 전이 규칙

전진만, terminal/수동 보호. `vcs_repos.auto_transition=true`일 때만.
| 이벤트 | 조건 | 전이 |
|---|---|---|
| BranchCreated (매칭) | ticket ∈ {pending, assigned} | → in_progress (pending이면 assigned 경유) |
| PROpened | ticket ∈ {pending, assigned} | → in_progress |
| PRMerged | terminal 아님 | → `merge_target` (기본 completed; 설정 시 pending_approval) |
| PRClosed(미merge), BranchDeleted | — | 전이 없음 (코멘트만) |

가드:
- terminal(`completed`/`failed`/`cancelled`) 티켓은 전이 skip(링크는 기록).
- 상태머신 준수: `pending → in_progress` 직행 불가 → linker가 `assigned` 중간 전이 수행.
- 멱등: 재전송 webhook → 링크 upsert no-op, 이미 목표 상태면 전이 no-op.

### 6. 인그레스 & 보안

- `POST /api/v1/vcs/webhook/{provider}` — **API 키 없이 공개**, provider별 **HMAC 검증**.
- 시크릿: **provider-level 단일 시크릿**(Vault, 예 `secret/neunexus/goquest#github_webhook_secret`; toji-ci `github-webhook-secret` 패턴과 동일)으로 모든 goquest repo 검증. repo 등록에 per-repo 시크릿 불요. env `VCS_GITHUB_WEBHOOK_SECRET`로 주입.
- 검증 순서: 원문 body로 HMAC 검증 → 통과 후에만 payload 신뢰·parse.
- **멱등**: `X-GitHub-Delivery`(또는 provider delivery id) 최근 처리 집합 + upsert 멱등. v1은 upsert 멱등에 의존(delivery 저장은 후속 최적화).
- v1 동기 처리(빠른 링크·전이) 후 200/202 응답. 비동기 워커는 후속.

### 7. 에러 처리

| 상황 | 처리 |
|---|---|
| 서명 불일치 | 401 |
| 미등록/비활성 repo | 202 무시 + log |
| 미해석 title_name 토큰 | 무시(에러 아님) |
| 티켓 terminal | 전이 skip, 링크 기록 |
| provider parse 실패 | 400 + log |
| delivery 재전송 | 멱등 no-op |

### 8. MCP (goquest-mcp)

- 도구: `register_vcs_repo(provider, repo_full_name, project|project_id, auto_transition?, merge_target?)`, `list_vcs_repos`. (선택) `link_vcs(ticket, kind, external_ref, url?)` 수동 첨부(webhook 누락 보완).
- 읽기: `vcs_links[]`가 `GET /tickets/{id}` embed → 기존 `get_ticket`/`get_ticket_by_name`가 자동 노출(새 읽기 도구 불요).
- 프롬프트: `ticket_guide`/`create_ticket` description에 규약 추가 — **branch명에 title_name 포함(예 `goq-foo` 또는 `feature/goq-foo`), PR/commit 메시지에 title_name 포함 → goquest가 자동 링크·전이**. LLM이 작업 시작 시 title_name 기반 branch를 만들고 PR 제목/본문에 title_name을 넣도록 유도.

### 9. 데이터 흐름 (PR merged 예)

```
GitHub PR merged → webhook POST /api/v1/vcs/webhook/github (X-Hub-Signature-256)
  → github.Verify(body, secret) → github.Parse → VcsEvent{Kind:pr_merged, Repo, PRNumber, Branch, PRTitle...}
  → linker: repo→project 해석 → title_name 추출(PR 제목/본문/head branch)
      → 티켓 해석 → vcs_link upsert(kind=pull_request, state=merged)
      → auto_transition: ticket → merge_target(completed) [terminal/상태머신 가드]
      → 티켓 코멘트 기록
  → 200
GET /tickets/{id} → vcs_links[] embed
```

## 테스트

- 단위: GitHub 어댑터 parse(push/pull_request/create/delete 픽스처 → VcsEvent), HMAC Verify(정상/변조), linker(title_name 추출, branch 상속, upsert 멱등, 전이 매트릭스 incl. terminal 가드·assigned 경유, auto_transition=false), provider registry.
- 통합: 서명된 webhook 요청 → DB 링크 생성 + 티켓 전이 + 코멘트. 전이는 table-driven.
- 회귀: 재전송(동일 delivery/payload) → 링크·상태 불변.

## 미해결 / 추후

- GitLab/Forgejo 어댑터(인터페이스 구현) — 필요 시.
- 비동기 처리(Redis 스트림 + 워커) — 처리량/복원력 필요 시.
- delivery id 영속 dedup — 중복 폭주 시.
- 양방향(goquest→git 상태/코멘트 push) — 별도 spec.
- MCP `link_vcs` 수동 첨부 범위 — 구현 단계에서 확정.
