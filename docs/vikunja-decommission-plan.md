# Vikunja → Goquest 폐기 plan

> 작성일: 2026-05-11
> 목표: 이번 주 내 Vikunja 완전 폐기, Goquest로 대체
> 데이터 마이그레이션: **없음** (vikunja에 mock data만 있음, 모두 삭제)
> 작업 단위: 단일 작업자 (lyckabc) 기준

---

## 0. 결정사항 요약

| 항목 | 결정 |
|---|---|
| 데이터 마이그레이션 | ❌ 안 함. mock data만 있으므로 vikunja DB는 삭제 |
| 마이그레이션 방식 | Big bang (한 번에 폐기) |
| 일정 | 이번 주 (2026-W19) 완료 |
| Vikunja history 보존 | 코드/배포/DB 모두 제거하되 **이 plan 문서 + `geneso/.../vikunja-decommission-2026-05.md` 한 장**으로 "왜·언제 폐기했는지" 기록만 영구 보존. DB backup 없음 |
| Goquest 대체 기능 | Project 구조, due_date, 캘린더 sync (iOS EventKit), Comments, Tags, Priority — **모두 이미 동작** |
| 반복 작업 (recurring) | 폐기 후 필요시 별도 phase로 추가 |

---

## 1. 작업 인벤토리 (현재 vikunja 의존 객체)

### 1.1 K8s 리소스
- `metaviewer/vikunja-*` deployment + pod (현재 1 Running)
- `metaviewer/vikunja-sync-*` CronJob + 최근 실행 pod들
- ArgoCD applications: `vikunja`, `vikunja-sync` (metaflow ns)
- PostgreSQL DB: `vikunja` schema/database (taxon ns)

### 1.2 코드/IaC 파일
**삭제 대상 디렉토리:**
- `neunexus/deploy/vikunja-sync/`
- `neunexus/deploy/bots/vikunja/`
- `neunexus/services/bots/vikunja/`

**삭제 대상 파일:**
- `neunexus/deploy/argocd-apps/vikunja.yaml`
- `neunexus/deploy/argocd-apps/vikunja-sync.yaml`

**수정 대상:**
- `neunexus/ci/run.py`, `neunexus/ci/src/main.py` — vikunja build target 제거
- `neunexus/deploy/ai-assistant/bot-registry-cm.yaml` — vikunja bot entry 제거
- `neunexus/deploy/bots/nuclex/{deployment.yaml,vso-secrets.yaml}` — vikunja 환경변수 제거
- `neunexus/services/lymphhub/sso-apps.tf` — `zitadel_application_oidc.vikunja` 리소스 제거
- `neunexus/docs/runbooks/{resource-usage,zitadel-sso,services-install}.md` — vikunja 섹션 삭제
- `neunexus/docs/runbooks/troubleshooting/*.md` — vikunja 참조 삭제
- `neunexus/docs/services/overview.md` — vikunja → goquest

### 1.3 Goquest 안의 vikunja 잔재
- `internal/migrate/sql/001_init.sql:22` — `vikunja_task_id BIGINT` 컬럼
- `internal/integration/nuclex.go:44` — 이벤트 payload에 `vikunja_task_id` 포함
- `internal/ticket/model.go` — `VikunjaTaskID` 필드 (Ticket / CreateReq / UpdateReq 3개 구조체)
- `internal/ticket/store_pg.go` — INSERT/SELECT/UPDATE에 vikunja_task_id 포함

### 1.4 외부 문서
- `geneso/...` — vikunja 관련 문서 (별도 작업)
- `CLAUDE.md` global 지침 — bot 목록에서 vikunja 제거

---

## 2. Phase별 작업

### Phase V1 — Goquest 코드 cleanup (1 PR)
**소요:** ~1시간

목표: `vikunja_task_id` 잔재 제거.

작업:
1. 새 migration `009_drop_vikunja_task_id.sql` — `ALTER TABLE tickets DROP COLUMN vikunja_task_id`
2. `internal/ticket/model.go`: `VikunjaTaskID` 필드 제거 (3곳)
3. `internal/ticket/store_pg.go`: INSERT/SELECT/UPDATE에서 컬럼 제거
4. `internal/integration/nuclex.go`: 이벤트 payload에서 제거
5. 테스트 통과 확인

PR: `chore(vikunja): remove vikunja_task_id from goquest`

### Phase V2 — neunexus 인프라 cleanup (1 PR)
**소요:** ~2시간

목표: vikunja bot/cronjob/ArgoCD app 모두 삭제.

작업:
1. 디렉토리 3개 삭제 (`deploy/vikunja-sync/`, `deploy/bots/vikunja/`, `services/bots/vikunja/`)
2. ArgoCD app YAML 2개 삭제
3. `ci/run.py`, `ci/src/main.py`에서 vikunja 빌드 target 제거
4. `bot-registry-cm.yaml`에서 vikunja entry 제거
5. nuclex deployment의 vikunja env/secret 제거
6. lymphhub `sso-apps.tf`에서 `zitadel_application_oidc.vikunja` 제거 + 해당 vault secret 제거
7. 모든 runbook 문서에서 vikunja 섹션 삭제 (또는 "Decommissioned 2026-05" 한 줄로 대체)

PR: `chore(vikunja): decommission vikunja deployment + IaC`

### Phase V3 — K8s 리소스 실 제거
**소요:** ~30분

목표: 클러스터에서 vikunja 흔적 제거.

작업:
1. ArgoCD apps 삭제 → vikunja/vikunja-sync pods 자동 폐기
2. Vikunja DB drop:
   ```sql
   DROP DATABASE vikunja;
   DROP USER vikunja;
   ```
3. ZITADEL `vikunja` OIDC app 삭제 (Terraform이 다음 apply에서 자동 처리)
4. Vault secret `secret/neunexus/sso/vikunja` 삭제
5. 폐기 직전 health check: goquest-ui + goquest-service + goquest-ios 모두 정상 동작 확인

### Phase V4 — 외부 문서 cleanup
**소요:** ~30분

작업:
1. `CLAUDE.md` global instructions에서 vikunja 항목 제거
2. `geneso/universitas/operations/vikunja-decommission-2026-05.md` 추가 — 폐기 기록
3. Gopedia 인제스트로 새 문서 반영

> **기록 보존 전략 (History retention)**
>
> Phase V1~V3가 끝나면 vikunja 관련 **코드, K8s 배포, ArgoCD 앱, ZITADEL OIDC 앱, Vault secret, DB**가 모두 영구 삭제됩니다. 따라서 "왜·언제·무엇이 폐기됐는지"를 추적할 수 있는 유일한 흔적은 다음 세 가지뿐입니다:
>
> 1. 이 plan 문서 (`goquest/docs/vikunja-decommission-plan.md`) — 폐기 의사결정 + 단계 기록
> 2. 각 phase의 git commit message (`chore(vikunja): …`)
> 3. **`geneso/.../vikunja-decommission-2026-05.md`** — 운영팀이 검색 가능한 위치(Gopedia 인제스트 대상)에 남기는 한 장의 회고/공지
>
> 위 셋이 사라지면 "이 시스템에 vikunja가 있었다"는 사실 자체를 복원할 수 없습니다. V4에서 geneso 문서를 빠뜨리지 말 것.

---

## 3. Rollback 전략

Big bang이지만 단계가 분리되어 있어 각 phase별 rollback 가능:

| Phase | Rollback |
|---|---|
| V1 (Goquest code) | git revert + migration rollback (`ALTER TABLE tickets ADD COLUMN vikunja_task_id BIGINT`) |
| V2 (IaC) | git revert. 단, V3가 실행됐다면 ArgoCD가 다시 vikunja를 띄우려 시도 — 수동으로 image 확보 필요 |
| V3 (K8s 실 제거) | 사실상 어려움. mock data라 복구 불필요 |
| V4 (docs) | git revert 즉시 |

데이터가 없으므로 V3까지는 사실상 "코드/설정 cleanup"이고, V4까지 완료된 이후에도 vikunja 인스턴스 자체를 재배포하면 새 vikunja DB로 부팅됨.

---

## 4. 완료 기준 (Definition of Done)

- [ ] V1 PR 머지 + 마이그레이션 클러스터 적용
- [ ] V2 PR 머지
- [ ] ArgoCD에 vikunja/vikunja-sync app 없음
- [ ] `kubectl get pods -A | grep vikunja` 결과 empty
- [ ] `psql -c "\l" | grep vikunja` 결과 empty
- [ ] ZITADEL Console에 vikunja 앱 없음
- [ ] CLAUDE.md/geneso/runbook에서 vikunja 참조 없음 (또는 "Decommissioned 2026-05" 한 줄로만 남음)
- [ ] Goquest로 일상 task 관리 (5일 무사고 운영) — 정성적

---

## 5. Out of scope (이 plan 안 다룸)

- Recurring tasks 기능 (필요시 별도 phase로)
- 첨부파일 (attachment) — vikunja에 있었지만 mock data, 사용 안 했음
- Plane (vikunja 후속으로 잠시 검토됐던 옵션) — 도입 안 함
- 다중 사용자 협업 기능 — 현재 단일 사용자라 deferred
