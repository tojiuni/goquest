# goquest

AI-agent dedicated ticket system for the neunexus ecosystem.

## Overview

goquest is a lightweight, fast ticket management service designed for AI agent workloads. It provides:

- **REST API** (`goquest-service`, port 8080): ticket CRUD, state machine, Redis event publishing
- **Conventional Bot** (`goquest-bot`, port 8081): OpenClaw-compatible bot interface for agent-driven ticket operations

## Architecture

```
nuclex (PM) → POST /api/v1/tickets → goquest-service
                                          ↓
                               Redis Streams (goquest.tickets.*)
                                          ↓
                               OpenClaw → hermes-{domain} K8s Job
                                          ↓
                               PATCH /api/v1/tickets/{id} (status update)
                                          ↓
                               nuclex.events stream → Vikunja task closed
```

## Ticket Domains

| Domain | Hermes Assignee |
|--------|-----------------|
| infra | hermes-infra |
| data | hermes-data |
| code | hermes-code |
| monitor | hermes-monitor |
| knowledge | hermes-knowledge |
| pm | hermes-pm |

## Development

```bash
# Build service binary
go build ./cmd/service/

# Build bot binary
go build ./cmd/bot/

# Run all builds and vet
go build ./...
go vet ./...
```

## CI/CD

Woodpecker CI with Kaniko builds:
- **push/main**: builds and pushes `goquest-service` + `goquest-bot` images
- **pull_request**: validates both builds (no push)

Images: `artifacts.toji.homes/neunexus/goquest-{service,bot}:latest`
