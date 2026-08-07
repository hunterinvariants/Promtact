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
# Postgres may write its log to a file or to the journal. The Debian and Ubuntu
# packages redirect the server's stderr into /var/log/postgresql/ even with
# logging_collector off, which is why pg_current_logfile() can return nothing
# while the file exists.
#
# Configure in /etc/promtact/access-shipper.env:
#   PROMTACT_URL=http://127.0.0.1:8080
#   PROMTACT_REPORTER_TOKEN=...        a token whose only role is reporter
#   PROMTACT_PG_LOG=/var/log/postgresql/postgresql-18-main.log
#   PROMTACT_PG_UNIT=postgresql@18-main   (alternative to PG_LOG)

# -e is deliberately absent. This loop has to outlive an unexpected exit status:
# a monitor that stops monitoring because one command returned non-zero is worse
# than no monitor at all, because systemd goes on reporting the unit as active.
set -uo pipefail

URL="${PROMTACT_URL:-http://127.0.0.1:8080}"
TOKEN="${PROMTACT_REPORTER_TOKEN:-}"
PGLOG="${PROMTACT_PG_LOG:-}"
PGUNIT="${PROMTACT_PG_UNIT:-}"
HEARTBEAT_SECONDS="${PROMTACT_HEARTBEAT_SECONDS:-300}"
SERVICE_APP="${PROMTACT_APPLICATION_NAME:-promtact}"

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
  echo "access-shipper: set PROMTACT_PG_LOG (file) or PROMTACT_PG_UNIT (journal)" >&2
  exit 1
fi

submit() {
  # Delivery failure must not kill the shipper: the service notices silence by
  # itself, and a crash loop would only make the gap longer.
  curl -sS -m 10 -o /dev/null \
    -X POST "$URL/api/access-log" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d "$1" || echo "access-shipper: submission failed" >&2
}

# One reader for either source. Both start at the end: replaying history would
# raise alarms about access that was already dealt with.
read_log() {
  if [ -n "$PGLOG" ]; then
    tail -F -n0 "$PGLOG"
  else
    journalctl -u "$PGUNIT" -f -n0 --output=cat
  fi
}

# One heartbeat immediately, then on its own interval. Waiting the full interval
# for the first would leave a broken configuration looking healthy for five
# minutes after every start, which is long enough to walk away believing it works.
submit '{"heartbeat":true,"sessions":[]}'
(
  while true; do
    sleep "$HEARTBEAT_SECONDS"
    submit '{"heartbeat":true,"sessions":[]}'
  done
) &
trap 'kill 0' EXIT

reported_malformed=0

read_log | while IFS= read -r line; do
  case "$line" in
    *"connection authorized"*) event="connect" ;;
    *"disconnection:"*)        event="disconnect" ;;
    *)                         continue ;;
  esac

  # The prefix has a fixed shape, so all four fields come out of one pass, space
  # separated. sed rather than grep -oP: PCRE is missing on busybox and BSD,
  # where a control that silently extracts nothing is worse than one that is
  # plainly absent.
  fields=$(printf '%s' "$line" | sed -n 's/.*user=\([^,]*\),db=\([^,]*\),app=\([^,]*\),client=\([^ ]*\).*/\1 \2 \3 \4/p')
  if [ -z "$fields" ]; then
    continue
  fi

  # shellcheck disable=SC2086
  set -- $fields
  user="${1:-}"
  db="${2:-}"
  app="${3:-}"
  client="${4:-}"

  # The application name comes from the message body when it is there. At
  # authorization time Postgres does not yet know it, so the prefix carries
  # "[unknown]" while the body already states it. Reading only the prefix would
  # make every session look anonymous, including the service's own, which would
  # then be reported as unannounced by the thousand until someone switched the
  # alarm off.
  body_app=$(printf '%s' "$line" | sed -n 's/.*application_name=\([^ ,]*\).*/\1/p')
  if [ -n "$body_app" ]; then
    app="$body_app"
  fi

  # Empty fields mean the log is not shaped the way this expects: a changed
  # log_line_prefix, a different Postgres version, or a broken pattern. Sending
  # them anyway would fill the findings with blank records that read as genuine
  # unannounced sessions. Report once, then skip — a flood of identical
  # complaints is its own denial of attention.
  if [ -z "$user" ] || [ -z "$app" ]; then
    if [ "$reported_malformed" -eq 0 ]; then
      echo "access-shipper: cannot parse the log line; check log_line_prefix" >&2
      echo "access-shipper: offending line: $(printf '%.160s' "$line")" >&2
      reported_malformed=1
    fi
    continue
  fi

  # The service's own connections are the expected traffic, filtered here as
  # well as server-side. Doing it in both places keeps the ordinary volume off
  # the wire entirely, and it is thousands of times the volume of the sessions
  # this exists to catch.
  if [ "$app" = "$SERVICE_APP" ]; then
    continue
  fi

  now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  submit "$(printf '{"sessions":[{"at":"%s","user":"%s","application":"%s","source":"%s","database":"%s","event":"%s"}]}' \
    "$now" "$user" "$app" "$client" "$db" "$event")"
done
