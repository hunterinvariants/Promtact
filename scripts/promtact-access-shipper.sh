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
# Postgres may write its log to a file or, as the Debian and Ubuntu packages do
# by default, to stderr which systemd captures into the journal. Both are
# supported: reading the journal avoids restarting the database to turn the file
# collector on, which is the better trade for a running deployment.
#
# Configure in /etc/promtact/access-shipper.env:
#   PROMTACT_URL=http://127.0.0.1:8080
#   PROMTACT_REPORTER_TOKEN=...
#   PROMTACT_PG_UNIT=postgresql@18-main      # journal source, or
#   PROMTACT_PG_LOG=/var/log/postgresql/...  # file source

set -euo pipefail

URL="${PROMTACT_URL:-http://127.0.0.1:8080}"
TOKEN="${PROMTACT_REPORTER_TOKEN:-}"
PGLOG="${PROMTACT_PG_LOG:-}"
PGUNIT="${PROMTACT_PG_UNIT:-}"
HEARTBEAT_SECONDS="${PROMTACT_HEARTBEAT_SECONDS:-300}"

if [ -z "$TOKEN" ]; then
  echo "access-shipper: PROMTACT_REPORTER_TOKEN is required" >&2
  exit 1
fi
if [ -n "$PGLOG" ]; then
  if [ ! -r "$PGLOG" ]; then
    echo "access-shipper: PROMTACT_PG_LOG is set but not readable: $PGLOG" >&2
    exit 1
  fi
elif [ -z "$PGUNIT" ]; then
  echo "access-shipper: set PROMTACT_PG_UNIT (journal) or PROMTACT_PG_LOG (file)" >&2
  exit 1
fi

# One reader for either source. -n0 and -f start at the end: history is not
# replayed, because resubmitting sessions from before the shipper started would
# raise alarms about access that was already dealt with.
read_log() {
  if [ -n "$PGLOG" ]; then
    tail -F -n0 "$PGLOG"
  else
    journalctl -u "$PGUNIT" -f -n0 --output=cat
  fi
}

submit() {
  # Delivery failure must not kill the shipper: the service notices silence by
  # itself, and a crash loop would only make the gap longer.
  curl -sS -m 10 -o /dev/null \
    -X POST "$URL/api/access-log" \
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
read_log | while IFS= read -r line; do
  case "$line" in
    *"connection authorized"*) event="connect" ;;
    *"disconnection"*)         event="disconnect" ;;
    *)                         continue ;;
  esac

  # The prefix has a fixed shape, so all four fields come out of one pass.
  # sed rather than grep -oP: PCRE is missing on busybox and BSD, and a control
  # that silently extracts nothing there is worse than one that is absent.
  fields="$(printf '%s' "$line" | sed -n     's/.*user=\([^,]*\),db=\([^,]*\),app=\([^,]*\),client=\([^ ]*\).*/|||/p')"
  [ -z "$fields" ] && continue

  user="${fields%%|*}";  rest="${fields#*|}"
  db="${rest%%|*}";      rest="${rest#*|}"
  app="${rest%%|*}"
  client="${rest#*|}"

  # The application name is taken from the message body when it is there. At
  # authorization time Postgres does not yet know it, so the prefix carries
  # "[unknown]" while the body already states it. Reading only the prefix would
  # make every session look anonymous — including the service's own, which would
  # then be reported as unannounced by the thousand until someone switched the
  # alarm off.
  body_app="$(printf '%s' "$line" | sed -n 's/.*application_name=\([^ ,]*\).*//p')"
  if [ -n "$body_app" ]; then
    app="$body_app"
  fi

  now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  submit "$(printf '{"sessions":[{"at":"%s","user":"%s","application":"%s","source":"%s","database":"%s","event":"%s"}]}' \
    "$now" "${user:-unknown}" "${app:-unknown}" "${client:-unknown}" "${db:-unknown}" "$event")"
done
