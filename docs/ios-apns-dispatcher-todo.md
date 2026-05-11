# APNs Dispatcher — TODO

> Phase 7 iOS app PR landed the **device registration** API (POST /devices/register
> + device_tokens table). The actual APNs push delivery — fanning ticket events
> out to subscribed devices — is **not yet implemented**.
>
> This document tracks the remaining work to close the loop.

---

## Status snapshot

| Component | Status |
|-----------|--------|
| `device_tokens` table (migration 008) | ✅ deployed |
| `POST /api/v1/devices/register` | ✅ deployed |
| `GET /api/v1/devices` | ✅ deployed |
| `DELETE /api/v1/devices/:id` | ✅ deployed |
| iOS app: token capture + register call | ✅ planned in `goquest-ios` |
| **APNs dispatcher (this doc)** | ❌ pending |
| **Push payload templates** | ❌ pending |
| **APNs auth key in Vault** | ❌ pending |

---

## Required work

### 1. APNs credentials

- Generate an APNs Auth Key (`.p8`) in Apple Developer portal
  (Certificates → Keys → "+", check **Apple Push Notifications service (APNs)**)
- Store in Vault: `secret/neunexus/goquest/apns` with fields:
  - `key_id` (10-char string from Apple)
  - `team_id` (10-char Apple developer team id)
  - `key_p8` (PEM contents)
  - `bundle_id` = `home.toji.goquest`
  - `environment` = `production` | `sandbox` (use sandbox during dev)

### 2. New package: `internal/push`

```go
package push

type Notification struct {
    UserID    string
    Title     string
    Body      string
    TicketID  string
    Category  string // "ticket.assigned" | "ticket.commented" | "ticket.overdue"
}

type Sender interface {
    Send(ctx context.Context, n Notification) error
}
```

Implementation options (pick one):
- **`github.com/sideshow/apns2`** — widely used, simple JWT auth. Recommended.
- **Direct HTTP/2 to `api.push.apple.com`** — no deps, more code.

### 3. Event subscriber

Hook into existing Redis streams (`goquest.tickets.assigned`,
`goquest.tickets.commented`, etc.). For each event:
1. Look up assignee_id / requester_id → query `device_tokens WHERE user_id = ?`
2. For each registered token, call `Sender.Send(...)`
3. On APNs `Unregistered` / `BadDeviceToken` response → delete the token row

### 4. main.go wiring

```go
apnsSender := push.NewAPNs(cfg.APNs)
pushWorker := push.NewWorker(rdb, deviceStore, apnsSender)
go pushWorker.Run(ctx)
```

### 5. iOS app changes (after dispatcher is live)

- Verify push permission flow in Settings
- Test against sandbox APNs first (debug builds)
- Display badge count from `aps.badge` field on Inbox tab

---

## Why deferred

- Requires Apple Developer Program enrolment (paid). Currently unconfirmed.
- Requires production-grade Vault secret rotation policy.
- Better to ship the iOS app's read-only MVP + Calendar Sync first; APNs is
  pure enhancement.

When ready, this becomes the next PR after the iOS app ships.
