# goquest: brain gRPC 클라이언트 + ref doc REST 엔드포인트 설계

- 작성일: 2026-07-22
- 작성자: lyckabc (with Claude Sonnet 5)
- 상태: Design (구현 전)
- 관련 모듈: `internal/brainclient/`(신규), `internal/handler/`, `cmd/service/main.go`
- 선행 의존: brain repo `2026-07-22-brain-ticket-docs-rpc-design.md` (머지·배포 완료 후 착수)
- 후속 의존: goquest-ui repo `2026-07-22-goquest-ui-refdocviewer-brain-design.md`

## 1. 배경

`2026-05-30-goquest-gopedia-ref-doc-design.md`(기존 설계)는 "goquest → 지식저장소 런타임 호출 없음, ref 해석은 전적으로 caller(bot) 책임"을 원칙으로 삼았다. 그런데 실제 구현은 이 원칙을 벗어나 **goquest-ui가 브라우저에서 gopedia를 직접 호출**하는 형태로 흘러갔다 — goquest 백엔드가 중재하지 않는 세 번째 신뢰 축이 생긴 셈이다.

이번 설계는 이 원칙을 다시 세우되 대상을 gopedia→brain으로 바꾼다: **goquest 백엔드가 ref 해석을 중재**한다(브라우저가 brain에 직접 붙지 않음 — brain은 애초에 gRPC 전용이라 브라우저가 붙을 수도 없다). 순수 무의존 원칙 대신 "단일 중재 지점" 원칙으로 전환하는 것이며, 이유는: (1) brain은 HTTP 표면이 전혀 없어 브라우저가 direct 호출 불가, (2) 인증서/토큰을 브라우저에 노출하지 않고 백엔드가 다루는 게 안전, (3) goquest가 이미 자체 API 요청의 Bearer JWT를 검증하고 있어 그 신원을 그대로 재사용할 수 있다.

## 2. 결정사항

### 2.1 brain gRPC 클라이언트

- `brain/proto/brain.proto`를 goquest 레포에 vendoring(`internal/brainpb/brain.proto`로 복사) 후 `protoc-gen-go`+`protoc-gen-go-grpc`로 스텁 생성. brain의 proto가 바뀌면 수동 재생성 필요(두 레포가 별도 git repo — 다른 toji 서비스 간 결합과 동일 수준, 자동 동기화는 후속 과제).
- `internal/brainclient/client.go`:
  ```go
  type Client struct {
      conn   *grpc.ClientConn
      graph  brainpb.GraphServiceClient
  }

  func New(addr string) (*Client, error)

  func (c *Client) TicketDocs(ctx context.Context, ticketID string, callerJWT string, callerUser string) ([]RelatedDoc, error)

  type RelatedDoc struct {
      Title      string
      SourcePath string
  }
  ```
  `TicketDocs`는 brain의 `GraphService.TicketDocs` RPC를 호출하며, gRPC 메타데이터에 `authorization: Bearer <callerJWT>`를 부착하고 요청의 `user` 필드에 `callerUser`(예: `user:lyckabc`)를 세팅한다.

### 2.2 Caller-auth 패턴 (brain-mcp-caller-auth 선례 재사용)

goquest는 이미 자체 API 요청의 Authorization 헤더(LymphHub JWT)를 검증하고 있다. 그 원본 JWT를 디코드(서명 검증은 이미 goquest 미들웨어가 끝낸 뒤이므로 여기선 클레임만 추출)해 `sub`/`preferred_username`을 얻고, `user:<preferred_username>` 형식으로 brain 호출의 `user` 필드에 세팅한 뒤 원본 JWT를 그대로 brain에 forward한다.

무토큰(X-API-Key 정적 클라이언트 등) 요청에 대한 서비스 계정 폴백은 이번 범위에서 생략한다(YAGNI) — ticket_docs·doc content는 항상 로그인 사용자 컨텍스트(goquest-ui)에서만 호출되며, 배치/내부 호출 사례가 아직 없다. 필요해지면 후속 티켓으로 `goquest-brain-reader` LymphHub client를 추가한다.

### 2.3 신규 REST 엔드포인트

```
GET /api/v1/tickets/{id}/docs
  → brain TicketDocs 호출, {"docs": [{"title": "...", "source_path": "..."}]}

GET /api/v1/docs/content?source_path=<path>
  → GitHub(tojiuni/geneso)에서 raw 콘텐츠 fetch, {"content": "...", "source_path": "..."}
```

`/docs/content`는 brain과 무관 — GitHub App 설치 토큰(Vault `neunexus/workspace/github-app-tojiuni`, 이 세션에서 이미 쓴 패턴과 동일: tokenhub `github-tojiuni` provider 경유)으로 `tojiuni/geneso`의 raw 파일 콘텐츠를 서버사이드에서 fetch한다. 브라우저에 GitHub 토큰을 노출하지 않기 위해 반드시 백엔드 경유 — geneso는 private repo이므로 브라우저 직접 fetch는 애초에 불가능하다.

캐싱은 v1에서 생략한다(파일당 저빈도 읽기, GitHub rate limit 리스크 낮음) — 문제되면 후속으로 짧은 TTL 인메모리 캐시 추가.

## 3. 에러 처리

- brain 연결 실패/타임아웃 → `/tickets/{id}/docs`는 502, 목록 자체가 없다는 뜻은 아니므로 UI에서 "일시적으로 불러올 수 없음"과 "참조 없음"을 구분해야 함(빈 배열 vs 에러).
- git fetch 404(파일 이동/삭제) → `/docs/content`는 404 반환, UI가 "문서를 찾을 수 없음"으로 표시.
- Authorization 헤더 없음 → 두 엔드포인트 모두 401(goquest 기존 인증 미들웨어가 처리).

## 4. 테스트

- `internal/brainclient` 목 테스트: TicketDocs 호출 시 메타데이터(Bearer, user 필드)가 올바르게 세팅되는지, brain 에러 시 에러 전파.
- `internal/handler` 핸들러 테스트: `/tickets/{id}/docs`(brain 목 응답 → 200/502), `/docs/content`(GitHub 목 응답 → 200/404).
- e2e(가능하면): 실제 프로덕션 brain 대상 스모크 — 알려진 ticket_id로 실제 참조 문서 목록이 돌아오는지(이번 세션에서 이미 검증한 `related_docs`/`ticket_docs` 결과와 대조).

## 5. 롤아웃

brain의 `TicketDocs` RPC가 프로덕션에 배포된 뒤 착수한다(선행 의존). 이 PR이 머지·배포되면 goquest-ui 쪽 작업(`2026-07-22-goquest-ui-refdocviewer-brain-design.md`)이 시작 가능해진다 — 그 전까지는 goquest-ui가 기존 gopedia 경로를 계속 사용하므로 하위호환 창은 안전하다.
