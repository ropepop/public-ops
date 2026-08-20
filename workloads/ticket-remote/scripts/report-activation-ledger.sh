#!/usr/bin/env bash
set -euo pipefail

# Read-only aggregate report. The SQL projection contains no attempt IDs,
# correlations, payloads, member data, or ticket content.
server="${TICKET_REMOTE_SPACETIME_HOST:-${SPACETIME_HOST:-https://maincloud.spacetimedb.com}}"
database="${TICKET_REMOTE_SPACETIME_DATABASE:-${SPACETIME_DATABASE:-ticket-remote-prod-v3}}"
ticket="${TICKET_REMOTE_TICKET_ID:-vivi-default}"

if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
  printf '%s\n' "Usage: $0" "Reports trailing 30-day activation aggregates for one logical Ticket lane." \
    "Set TICKET_REMOTE_SPACETIME_HOST, TICKET_REMOTE_SPACETIME_DATABASE, TICKET_REMOTE_TICKET_ID, or ACTIVATION_REPORT_SINCE to override defaults."
  exit 0
fi

if [[ -n "${ACTIVATION_REPORT_SINCE:-}" ]]; then
  since="$ACTIVATION_REPORT_SINCE"
else
  since="$(date -u -v-30d '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -d '30 days ago' '+%Y-%m-%dT%H:%M:%SZ')"
fi

escape_sql() {
  printf "%s" "$1" | sed "s/'/''/g"
}

ticket_sql="$(escape_sql "$ticket")"
since_sql="$(escape_sql "$since")"

# Maincloud SQL currently does not support GROUP BY. Fetch only bounded policy
# columns into memory, then print the local aggregate. The query result itself
# is never sent to stdout, so the command's output remains aggregate-only.
rows="$(spacetime sql -s "$server" "$database" \
  "SELECT occurrenceDay, flow, admission, outcome, reason, occurrenceCount FROM ticketremote_activation_history WHERE ticketId = '$ticket_sql' AND backendId = 'pixel' AND occurredAt >= '$since_sql';")"

printf '%s\n' "$rows" | awk -F'|' '
function clean(value) {
  gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
  sub(/^"/, "", value)
  sub(/"$/, "", value)
  return value
}
NR <= 2 { next }
index($0, "---") == 1 || NF < 6 { next }
{
  day = clean($1)
  flow = clean($2)
  admission = clean($3)
  outcome = clean($4)
  reason = clean($5)
  count = clean($6) + 0
  if (day == "" || flow == "" || admission == "" || outcome == "" || reason == "") next
  totals[day SUBSEP flow SUBSEP admission SUBSEP outcome SUBSEP reason] += count
}
END {
  print "occurrenceDay | flow | admission | outcome | reason | count"
  for (key in totals) {
    split(key, parts, SUBSEP)
    print parts[1] " | " parts[2] " | " parts[3] " | " parts[4] " | " parts[5] " | " totals[key]
  }
}' | {
  IFS= read -r header
  printf '%s\n' "$header"
  sort
}
