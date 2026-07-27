# syntax=docker/dockerfile:1.7

FROM golang:1.25.12-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY web ./web

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/promtact ./cmd/promtact && \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/promtactl ./cmd/promtactl

FROM alpine:3.23
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S -g 10001 promtact && \
    adduser -S -D -H -u 10001 -G promtact promtact

WORKDIR /app
COPY --from=build /out/promtact /usr/local/bin/promtact
COPY --from=build /out/promtactl /usr/local/bin/promtactl
COPY --chown=promtact:promtact web ./web
COPY --chown=promtact:promtact configs ./configs

USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/promtact"]
CMD ["--addr", "0.0.0.0:8080", "--web", "/app/web"]
