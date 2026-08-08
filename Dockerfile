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

# Somewhere writable for the local snapshot and, on a demonstration image, for
# the example documents. A demo directory owned by root once took a live
# deployment down for twenty minutes; the image should not be able to repeat it.
RUN mkdir -p /var/lib/promtact && chown 10001:10001 /var/lib/promtact
VOLUME /var/lib/promtact

USER 10001:10001
EXPOSE 8080

# Readiness rather than liveness. A process that answers is not the same as a
# store that can be written to, and a gateway unable to record its decisions
# should stop receiving traffic rather than keep taking it.
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/readyz >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/promtact"]
CMD ["--addr", "0.0.0.0:8080", "--web", "/app/web"]
