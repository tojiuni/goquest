FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG CMD=service
RUN go build -o /bin/goquest-${CMD} ./cmd/${CMD}/

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
ARG CMD=service
COPY --from=builder /bin/goquest-${CMD} /bin/app
EXPOSE 8080
CMD ["/bin/app"]
