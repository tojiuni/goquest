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

## GA 전 정리(추적) — 파일럿엔 무방, GA 전 결정
- **에러 정책 불일치:** `Get`은 SpiceDB 체크 에러 시 **fail-open**(티켓 반환, 로깅), `List`는 **fail-closed**(500). SpiceDB 일시 장애 시 목록은 깨지고 단건은 열리는 비대칭. GA 전 한쪽으로 정렬(보안상 양쪽 fail-closed 권장, 또는 양쪽 fail-open으로 가용성 우선) 결정 필요.
- **List 페이지네이션 근사치:** 가시성 post-filter라 `total`이 필터된 페이지 수 = 크로스페이지 합계 부정확. GA 전 SpiceDB `LookupResources`/`BulkCheckPermission`로 정밀화(성능: 페이지당 N CheckPermission RPC도 함께 해소).
- **List 핸들러 테스트 갭:** enforce 필터 경로(거부/통과/에러 500) 단위 테스트 부재(ticket.Service 구성 부담). 스텁 checker로 핸들러 테스트 추가 권장.
- **재배정 외 드리프트:** 워크스페이스/프로젝트 삭제 시 tuple 고아 → 주기 reconcile(백필 재실행)로 보정.
- **정체성 부채:** 레거시 numeric sub ↔ 현재 이메일 sub 불일치(현재 활성 사용자 데이터는 전부 이메일이라 enforce 무관). 워크스페이스 병합과 동류.
