# syntax=docker/dockerfile:1
# CACHE: RUN --mount=type=cache 로 의존성 재사용. --no-cache/--pull 로 빌드하지 말 것
# (둘 다 cache mount 를 폐기해 매 빌드 전부 재다운로드함).
FROM golang:1.25-alpine AS builder
WORKDIR /app
# git is required to fetch private github.com/tojiuni/* modules (GOPRIVATE forces
# a direct VCS fetch, bypassing the proxy); golang:*-alpine ships without it.
RUN apk add --no-cache git
# Private modules are fetched via a BuildKit secret (id=github_creds) that toji-ci
# injects (a repo-scoped git credential); GOPRIVATE skips the proxy/sumdb for
# tojiuni modules. The secret is optional — a public build without it still works.
ENV GOPRIVATE=github.com/tojiuni/*
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=secret,id=github_creds \
    sh -euc 'if [ -f /run/secrets/github_creds ]; then \
      git config --global credential.helper "store --file=/run/secrets/github_creds"; \
    fi; go mod download'
COPY . .
ARG CMD=service
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=secret,id=github_creds \
    sh -euc 'if [ -f /run/secrets/github_creds ]; then \
      git config --global credential.helper "store --file=/run/secrets/github_creds"; \
    fi; go build -o /bin/goquest-${CMD} ./cmd/${CMD}/'

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
ARG CMD=service
COPY --from=builder /bin/goquest-${CMD} /bin/app
EXPOSE 8080
CMD ["/bin/app"]
