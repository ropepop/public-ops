#!/usr/bin/env bash
set -euo pipefail

server="${TICKET_REMOTE_SPACETIME_HOST:-${SPACETIME_HOST:-https://maincloud.spacetimedb.com}}"
database="${TICKET_REMOTE_SPACETIME_DATABASE:-${SPACETIME_DATABASE:-ticket-remote-prod-v3}}"
logging_server="${TICKET_REMOTE_OPERATIONAL_LOGGING_HOST:-${OPERATIONAL_LOGGING_HOST:-https://maincloud.spacetimedb.com}}"
logging_database="${TICKET_REMOTE_OPERATIONAL_LOGGING_DATABASE:-${OPERATIONAL_LOGGING_DATABASE:-operational-logging-prod}}"
ticket="${TICKET_REMOTE_TICKET_ID:-vivi-default}"
since="${TRACE_SINCE:-}"
limit="${TRACE_LIMIT:-80}"

if [[ -z "$since" ]]; then
  since="$(date -u -v-30M '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -d '30 minutes ago' '+%Y-%m-%dT%H:%M:%SZ')"
fi

escape_sql() {
  printf "%s" "$1" | sed "s/'/''/g"
}

run_sql() {
  local title="$1"
  local sql="$2"
  printf '\n## %s\n' "$title"
  spacetime sql -s "$server" "$database" "$sql"
}

run_logging_sql_recent() {
  local title="$1"
  local sql="$2"
  local row_limit="$3"
  local output
  printf '\n## %s\n' "$title"
  if ! output="$(spacetime sql -s "$logging_server" "$logging_database" "$sql")"; then
    return 1
  fi
  # Maincloud SQL does not support ORDER BY. Keep the complete time-bounded
  # result private in memory, then sort the timestamp-first rows locally and
  # print only the requested number. No temporary file or broader projection
  # is created.
  printf '%s\n' "$output" | sed -n '1,2p'
  printf '%s\n' "$output" | sed -n '3,$p' | LC_ALL=C sort -r | awk -v limit="$row_limit" 'NR <= limit'
}

ticket_sql="$(escape_sql "$ticket")"
since_sql="$(escape_sql "$since")"
limit_sql="$(printf "%s" "$limit" | tr -cd '0-9')"
if [[ -z "$limit_sql" ]]; then
  limit_sql=80
fi

echo "server=$server"
echo "database=$database"
echo "logging_server=$logging_server"
echo "logging_database=$logging_database"
echo "ticket=$ticket"
echo "since=$since"

run_sql "desired state" \
  "SELECT ticketId, backendId, desiredActive, viewerCount, reason, revision, updatedBy, updatedAt FROM ticketremote_stream_desired_state WHERE ticketId = '$ticket_sql';"

run_sql "relay report" \
  "SELECT ticketId, backendId, videoClients, streamVerdict, lastFrameAgoMillis, framesForwarded, updatedAt FROM ticketremote_relay_current_report WHERE ticketId = '$ticket_sql';"

run_sql "phone report" \
  "SELECT ticketId, backendId, streamState, desiredActive, lastCommandId, lastCommandRevision, updatedAt FROM ticketremote_phone_current_report WHERE ticketId = '$ticket_sql';"

run_sql "pending commands" \
  "SELECT id, backendId, commandType, status, reason, revision, createdAt, updatedAt, expiresAt FROM ticketremote_stream_command WHERE ticketId = '$ticket_sql' LIMIT $limit_sql;"

run_logging_sql_recent "recent trace rows" \
  "SELECT occurredAt, recordedAt, source, level, event, correlationId, detailJson FROM operationallog_event WHERE domain = 'ticket' AND scopeId = '$ticket_sql' AND occurredAt >= '$since_sql';" \
  "$limit_sql"
