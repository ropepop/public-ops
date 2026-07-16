#!/usr/bin/env bash
set -euo pipefail

server="${TICKET_REMOTE_SPACETIME_HOST:-${SPACETIME_HOST:-https://maincloud.spacetimedb.com}}"
database="${TICKET_REMOTE_SPACETIME_DATABASE:-${SPACETIME_DATABASE:-ticket-remote-prod-v3}}"
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

ticket_sql="$(escape_sql "$ticket")"
since_sql="$(escape_sql "$since")"
limit_sql="$(printf "%s" "$limit" | tr -cd '0-9')"
if [[ -z "$limit_sql" ]]; then
  limit_sql=80
fi

echo "server=$server"
echo "database=$database"
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

run_sql "recent trace rows" \
  "SELECT createdAt, source, level, event, correlationId, detailJson FROM ticketremote_safe_operational_log WHERE ticketId = '$ticket_sql' AND createdAt >= '$since_sql' LIMIT $limit_sql;"
