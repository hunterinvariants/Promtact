#!/usr/bin/env bash
# Ships observed database sessions to Promtact for reconciliation against
# announced break-glass windows.
#
# It reads Postgres's own connection log rather than asking the database who is
# connected: a poll can be timed around, a log entry is written when the session
# opens. The service is told about every session that is not its own; it decides
# which of them nobody announced.
#
# This runs on the host, so an operator can stop it. That is deliberate and
# accounted for: it sends a heartbeat even when nothing happened, and the service
# treats silence after it has been heard from as its own signal.
#
# Configure in /etc/promtact/access-shipper.env:
#   PROMTACT_URL=http://127.0.0.1:8080
#   PROMTACT_ADMIN_TOKEN=...
#   PROMTACT_PG_LOG=/var/log/postgresql/postgresql-16-main.log

set -euo pipefail

URL="${PROMTACT_URL:-http://127.0.0.1:8080}"
TOKEN="${PROMTACT_ADMIN_TOKEN:-}"
PGLOG="${PROMTACT_PG_LOG:-}"
HEARTBEAT_SECONDS="${PROMTACT_HEARTBEAT_SECONDS:-300}"

if [ -z "$TOKEN" ]; then
  echo "access-shipper: PROMTACT_ADMIN_TOKEN is required" >&2
  exit 1
fi
if [ -z "$PGLOG" ] || [ ! -r "$PGLOG" ]; then
  echo "access-shipper: PROMTACT_PG_LOG must point at a readable Postgres log" >&2
  exit 1
fi

submit() {
  # Delivery failure must not kill the shipper: the service notices silence by
  # itself, and a crash loop would only make the gap longer.
  curl -sS -m 10 -o /dev/null \
    -X POST "$URL/api/admin/access-log" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d "$1" || echo "access-shipper: submission failed" >&2
}

# A heartbeat on its own interval, so a quiet database still proves the shipper
# is alive.
(
  while true; do
    sleep "$HEARTBEAT_SECONDS"
    submit '{"heartbeat":true,"sessions":[]}'
  done
) &
trap 'kill 0' EXIT

# Postgres logs connections as:
#   ... user=NAME,db=NAME,app=NAME,client=ADDR ... connection authorized: ...
# The prefix is configured in postgresql.conf; see docs/operations.md.
tail -F -n0 "$PGLOG" | while IFS= read -r line; do
  case "$line" in
    *"connection authorized"*) event="connect" ;;
    *"disconnection"*)         event="disconnect" ;;
    *)                         continue ;;
  esac

  user="$(printf '%s' "$line"   | grep -oP 'user=\K[^,]*'   | head -1)"
  db="$(printf '%s' "$line"     | grep -oP 'db=\K[^,]*'     | head -1)"
  app="$(printf '%s' "$line"    | grep -oP 'app=\K[^,]*'    | head -1)"
  client="$(printf '%s' "$line" | grep -oP 'client=\K[^ ,]*' | head -1)"

  # The service's own connections are filtered here as well as server-side.
  # Doing it in both places keeps the ordinary traffic off the wire entirely,
  # which matters because it is thousands of times the volume of the sessions
  # this exists to catch.
  if [ "$app" = "promtact" ]; then
    continue
  fi

  now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  submit "$(printf '{"sessions":[{"at":"%s","user":"%s","application":"%s","source":"%s","database":"%s","event":"%s"}]}' \
    "$now" "${user:-unknown}" "${app:-unknown}" "${client:-unknown}" "${db:-unknown}" "$event")"
done
