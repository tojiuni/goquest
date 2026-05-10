# goquest — ZITADEL OIDC Bearer 토큰 발급 가이드

**목적**: CLI / 스크립트에서 goquest API를 호출할 때 사용할 JWT Bearer 토큰 발급  
**최종 업데이트**: 2026-05-10

---

## 배경

goquest 서비스는 `JWKS_URL = https://auth.toji.homes/oauth/v2/keys` 를 통해 JWT 서명을 검증한다.  
ZITADEL PAT(Personal Access Token) 또는 기계 사용자(machine user)의 JWT-bearer 응답 토큰은  
**opaque 토큰**이어서 goquest 미들웨어가 파싱하지 못한다.  
반드시 **JWT 형식**(header.payload.signature) 토큰을 발급받아야 한다.

---

## 1. 사전 준비 — ZITADEL 세팅

### 1-1. goquest-cli Native App 생성

goquest-ui 앱은 SPA(`OIDC_APP_TYPE_USER_AGENT`)이고, device_code flow를 위한 secret이 없어  
별도 native app을 생성해야 한다.

```bash
ZITADEL_TOKEN="<admin PAT>"
PROJECT_ID="368698100572810481"   # neunexus 프로젝트 ID

curl -X POST "https://auth.toji.homes/management/v1/projects/${PROJECT_ID}/apps/oidc" \
  -H "Authorization: Bearer ${ZITADEL_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "goquest-cli",
    "redirectUris": ["http://localhost/callback"],
    "responseTypes": ["OIDC_RESPONSE_TYPE_CODE"],
    "grantTypes": [
      "OIDC_GRANT_TYPE_AUTHORIZATION_CODE",
      "OIDC_GRANT_TYPE_DEVICE_CODE"
    ],
    "appType": "OIDC_APP_TYPE_NATIVE",
    "authMethodType": "OIDC_AUTH_METHOD_TYPE_NONE",
    "accessTokenType": "OIDC_TOKEN_TYPE_JWT",
    "idTokenUserinfoAssertion": true
  }'
# → 응답에서 clientId 확인 (예: 372322743665945815)
```

현재 발급된 client ID: `372322743665945815`

---

## 2. Device Authorization Flow (권장)

브라우저 한 번 승인으로 JWT 발급. 이후 토큰 만료 전까지 재사용 가능.

### Step 1 — Device Code 요청

```bash
CLI_CLIENT_ID="372322743665945815"

DEVICE_RESP=$(curl -s -X POST "https://auth.toji.homes/oauth/v2/device_authorization" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "client_id=${CLI_CLIENT_ID}&scope=openid profile email")

USER_CODE=$(echo "$DEVICE_RESP" | python3 -c "import json,sys; print(json.load(sys.stdin)['user_code'])")
DEVICE_CODE=$(echo "$DEVICE_RESP" | python3 -c "import json,sys; print(json.load(sys.stdin)['device_code'])")

echo "브라우저에서 https://auth.toji.homes/device 로 이동 후 코드 입력: $USER_CODE"
```

### Step 2 — 브라우저 승인

1. `https://auth.toji.homes/device` 접속
2. 출력된 user_code 입력 (예: `KHZV-ZJNP`)
3. `lyckabc` 계정으로 로그인 후 승인

### Step 3 — Access Token 교환

```bash
TOKEN_RESP=$(curl -s -X POST "https://auth.toji.homes/oauth/v2/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "grant_type=urn:ietf:params:oauth:grant-type:device_code&device_code=${DEVICE_CODE}&client_id=${CLI_CLIENT_ID}")

ACCESS_TOKEN=$(echo "$TOKEN_RESP" | python3 -c "import json,sys; print(json.load(sys.stdin)['access_token'])")
echo "TOKEN: $ACCESS_TOKEN"
```

### Step 4 — 검증

```bash
BASE="https://goquest.toji.homes/api/v1"
curl -s "$BASE/workspaces" -H "Authorization: Bearer $ACCESS_TOKEN" | python3 -m json.tool
```

---

## 3. 토큰 유효 기간

| 항목 | 값 |
|------|-----|
| access_token 유효기간 | 12시간 (`exp - iat` 기준) |
| 갱신 방법 | Step 1~3 반복 (재승인 필요) |

---

## 4. 트러블슈팅 이력

### 문제 1: `"unsupported_grant_type"` — goquest-ui client

**원인**: goquest-ui(`372199926945344727`)는 SPA 앱으로 `password`, `device_code` grant 미허용  
**해결**: `goquest-cli` native app(`372322743665945815`) 별도 생성

### 문제 2: `"unauthorized_client: empty client secret"` — goquest-ui에 device_code 추가 시도

**원인**: SPA 앱은 client secret 없는 공개 클라이언트이며, ZITADEL은 SPA에 device_code 허용 시 secret 요구  
**해결**: native 타입(`OIDC_APP_TYPE_NATIVE`) 앱은 secret 없이 device_code 허용됨

### 문제 3: machine user jwt-bearer → opaque 토큰

**원인**: ZITADEL 기계 사용자 jwt-bearer grant는 기본적으로 opaque access token 반환  
goquest는 JWKS 기반 JWT 파싱 → opaque 토큰 인식 불가  
**결론**: 기계 사용자 방식은 goquest와 호환되지 않음; device_code flow 사용

### 문제 4: ArgoCD repo 인증 실패

**원인**: `tojiuni/neunexus` repo credential K8s Secret의 GitHub 토큰이 만료됨  
VSO가 Vault의 구 토큰값으로 계속 덮어써서 API PUT으로 갱신해도 반영 안 됨  
**해결**:
1. `secret/neunexus/argocd` Vault 시크릿의 `github-token` 값을 최신 토큰으로 갱신
2. ArgoCD Credential Template(`POST /api/v1/repocreds`) 생성 — `https://github.com/tojiuni` 전체에 적용
3. 기존 repo 삭제 후 `GET /repositories/{repo}?forceRefresh=true` 로 강제 재연결 → Successful

---

## 5. 관련 Vault 경로

| 목적 | Vault 경로 | 필드 |
|------|-----------|------|
| ZITADEL admin PAT | `secret/neunexus/zitadel` | `zitadel-token` |
| GitHub PAT (goquest push/ArgoCD) | `secret/neunexus/tojismith` | `github_token` |
| ArgoCD admin 패스워드 | `secret/neunexus/argocd` | `admin-password-mnemonic` |
| lyckabc ZITADEL 계정 | `secret/neunexus/lyckabc` | `zitadel_password` |

---

## 6. ZITADEL 리소스 ID 참고

| 리소스 | ID |
|--------|-----|
| neunexus 프로젝트 | `368698100572810481` |
| goquest-ui clientId | `372199926945344727` |
| goquest-cli clientId | `372322743665945815` |
| neunexus_admin 기계사용자 | `368030505284726001` |
| lyckabc 사용자 | `370549762107174099` |
