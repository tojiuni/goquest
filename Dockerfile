# syntax=docker/dockerfile:1
# CACHE: RUN --mount=type=cache 로 의존성 재사용. --no-cache/--pull 로 빌드하지 말 것
# (둘 다 cache mount 를 폐기해 매 빌드 전부 재다운로드함).
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG CMD=service
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -o /bin/goquest-${CMD} ./cmd/${CMD}/

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
ARG CMD=service
COPY --from=builder /bin/goquest-${CMD} /bin/app
EXPOSE 8080
CMD ["/bin/app"]
