package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ticketRemoteSourceFile(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{"..", ".."}, parts...)...)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

func TestSpacetimeModuleUsesFocusedPublicTablesAndRetentionPolicy(t *testing.T) {
	source := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")

	for _, required := range []string{
		"const HISTORY_TTL_MS: i64 = 6 * 60 * 60 * 1000;",
		"const CLEANUP_INTERVAL_SECS: u64 = 30 * 60;",
		"const CLEANUP_BATCH_SIZE: u32 = 0;",
		"const SAFE_LOG_DETAIL_MAX_BYTES: usize = 8192;",
		"ticketremote_ticket_summary",
		"ticketremote_viewer_public",
		"ticketremote_phone_status",
		"ticketremote_phone_status_history",
		"ticketremote_stream_desired_state",
		"ticketremote_stream_command",
		"ticketremote_phone_current_report",
		"ticketremote_safe_operational_log",
		"ticketremote_dev_perf_metrics_config",
		"ticketremote_dev_perf_metric",
		"ticketremote_cleanup_schedule",
		"pub expiresAt: String",
		"ticketBackendExpiresAt",
		"ticketBackendStatusExpiresAt",
		"ticketExpiresAt",
		"ticketCreatedAt",
		"eventCreatedAt",
		"correlationCreatedAt",
		"sourceCreatedAt",
		"next_audit_ordinal(",
		"cleanup_expired(",
		"ScheduleAt::Interval(",
		"ticketBackendStatus",
		"ticketremote_phone_status_history()\n        .ticketId()\n        .filter(&ticket.id)",
		"ticketremote_safe_operational_log()\n        .ticketId()\n        .filter(&ticket.id)",
		"#[spacetimedb::table(accessor = ticketremote_stream_command_signal, public",
		"ticketremote_stream_command_signal",
		"upsert_stream_command_signal(",
		"sampled_safe_log_event(",
		"require_service(ctx)?;",
		"pub fn ticketremote_update_phone_status(",
		"pub fn ticketremote_set_stream_desired_state(",
		"pub fn ticketremote_append_stream_command(",
		"pub fn ticketremote_ack_stream_command(",
		"pub fn ticketremote_update_phone_current_report(",
		"pub fn ticketremote_append_safe_operational_log(",
		"pub fn ticketremote_snapshot_runtime_tables_to_logs(",
		"pub fn ticketremote_set_dev_perf_metrics(",
		"pub fn ticketremote_member_append_dev_perf_metric(",
		"pub fn ticketremote_purge_expired_stream_commands(",
		"#[spacetimedb::view(accessor = ticketremote_service_stream_command, public, primary_key = id)]",
		"#[spacetimedb::view(accessor = ticketremote_service_safe_operational_log, public, primary_key = id)]",
		"service_ticket_id_for_viewer(",
		"status == \"acknowledged\" || status == \"dispatched\"",
		"ticketremote_stream_command()\n            .status()\n            .filter(status)",
		"coalesced_safe_log_detail(",
		"stream_desired_state_set",
		"stream_command_appended",
		"stream_command_acknowledged",
		"dev_perf_metrics_enabled(",
		"insert_table_event_log(",
		"cleanup_limit_reached(",
		"cleanup_expired_completed",
		"runtime_table_snapshot_completed",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("SpacetimeDB module is missing clean-sheet architecture marker %q", required)
		}
	}
	for _, forbidden := range []string{
		"ticketremote_live_state",
		"stateJson",
		"rowsFrom(tx.db.ticketremote_audit_event.ticketId.filter(ticketId)).length",
		"writeLiveState(",
		"const HISTORY_TTL_MS: i64 = 24 * 60 * 60 * 1000;",
		"const HISTORY_TTL_MS: i64 = 72 * 60 * 60 * 1000;",
		"const CLEANUP_INTERVAL_SECS: u64 = 5 * 60;",
		"const CLEANUP_BATCH_SIZE: u32 = 5000;",
		"const CLEANUP_BATCH_SIZE: u32 = 200;",
		"const SAFE_LOG_DETAIL_MAX_BYTES: usize = 2048;",
		"#[spacetimedb::table(accessor = ticketremote_service_stream_command, public",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("SpacetimeDB module still contains old scan-heavy marker %q", forbidden)
		}
	}

	commandChunk := rustItemChunk(t, source, "#[spacetimedb::table(accessor = ticketremote_stream_command,")
	if strings.Contains(commandChunk, "public") {
		t.Fatalf("phone command table must not be public because payloads can contain short-lived control data: %s", commandChunk)
	}
	signalChunk := rustItemChunk(t, source, "#[spacetimedb::table(accessor = ticketremote_stream_command_signal, public")
	if !strings.Contains(signalChunk, "public") {
		t.Fatalf("stream command signal table must be public so the bridge can cheaply avoid private reads: %s", signalChunk)
	}
	for _, forbidden := range []string{"payloadJson", "reason", "commandType"} {
		if strings.Contains(signalChunk, forbidden) {
			t.Fatalf("stream command signal table must not expose command details, found %q in %s", forbidden, signalChunk)
		}
	}
}

func TestSpacetimeScheduledCleanupIsUnbounded(t *testing.T) {
	source := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")
	scheduledBody := sourceBetween(t, source,
		"pub fn ticketremote_scheduled_cleanup_expired(",
		"#[spacetimedb::reducer]\npub fn ticketremote_upsert_member(")
	ensureBody := sourceBetween(t, source,
		"fn ensure_cleanup_schedule(",
		"fn clear_phone_backends(")
	cleanupBody := sourceBetween(t, source,
		"fn history_expired(created_at: &str, expires_at: &str, now_ms: i64) -> bool",
		"fn purge_expired_stream_commands_for_ticket(")
	purgeBody := sourceBetween(t, source,
		"fn purge_expired_stream_commands_for_ticket(",
		"fn refresh_touched_signals(")

	for _, required := range []string{
		"cleanup_expired(ctx, &arg.ticketId, &now, 0);",
		"batchSize: CLEANUP_BATCH_SIZE",
		"let limit = batch_size;",
		"cleanup_limit_reached(deleted, limit)",
		"cleanup_remaining(limit, deleted)",
		"fn history_expired(created_at: &str, expires_at: &str, now_ms: i64) -> bool",
		"parse_time_ms(created_at).saturating_add(HISTORY_TTL_MS) <= now_ms",
		"history_expired(&row.createdAt, &row.expiresAt, now_ms)",
		`"limit": if limit == 0 { serde_json::Value::String("unbounded".into()) }`,
		`"deletedCounts"`,
		"cleanup_expired_completed",
	} {
		body := scheduledBody + ensureBody + cleanupBody
		if !strings.Contains(body, required) {
			t.Fatalf("unbounded cleanup marker missing %q", required)
		}
	}
	if !strings.Contains(purgeBody, "let limit = batch_size;") ||
		!strings.Contains(purgeBody, "cleanup_limit_reached(deleted, limit)") {
		t.Fatalf("stream command purge must use the same unbounded zero semantics: %s", purgeBody)
	}
}

func TestSpacetimePendingCommandLookupUsesPendingIndex(t *testing.T) {
	source := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")
	start := strings.Index(source, "fn upsert_stream_command_signal(")
	if start < 0 {
		t.Fatal("upsert_stream_command_signal missing")
	}
	end := strings.Index(source[start:], "fn insert_stream_command(")
	if end < 0 {
		t.Fatal("insert_stream_command missing after signal helper")
	}
	body := source[start : start+end]
	if !strings.Contains(body, "ticketBackendStatus()") ||
		!strings.Contains(body, ".filter((ticket_id, backend_id, \"pending\"))") ||
		!strings.Contains(body, "parse_time_ms(&row.expiresAt) > now_ms") {
		t.Fatalf("pending command lookup must use bounded pending index: %s", body)
	}
	if strings.Contains(body, "ticketremote_stream_command().ticketId().filter(ticket_id)") {
		t.Fatalf("pending command lookup must not scan all stream commands for a ticket: %s", body)
	}

	for _, marker := range []string{
		"#[spacetimedb::view(accessor = ticketremote_service_stream_command, public, primary_key = id)]",
		".ticketBackendStatus()",
		".filter((&ticket_id, \"pixel\", \"pending\"))",
		"fn pending_commands(",
	} {
		sourceToSearch := source
		if marker == "fn pending_commands(" {
			sourceToSearch = ticketRemoteSourceFile(t, "spacetime-sidecar", "src", "main.rs")
		}
		if !strings.Contains(sourceToSearch, marker) {
			t.Fatalf("pending command reducer marker missing %q", marker)
		}
	}
}

func TestSpacetimeSidecarSnapshotUsesCurrentPhoneReportHealth(t *testing.T) {
	source := ticketRemoteSourceFile(t, "spacetime-sidecar", "src", "main.rs")
	body := sourceBetween(t, source,
		"fn build_snapshot(app: &Arc<AppState>, ticket_id: &str) -> Result<SnapshotResponse, HttpError> {",
		"    Ok(SnapshotResponse {")

	for _, required := range []string{
		"ticketremote_phone_current_report()",
		"report.status_json.clone()",
		"unwrap_or(row.health_json)",
		"report.updated_at.clone()",
		"unwrap_or(row.last_seen_at)",
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("sidecar snapshot must merge current phone report health, missing %q in %s", required, body)
		}
	}
}

func TestSpacetimeSidecarReadsOperationalLogsThroughServiceView(t *testing.T) {
	source := ticketRemoteSourceFile(t, "spacetime-sidecar", "src", "main.rs")
	for _, required := range []string{
		"SELECT * FROM ticketremote_service_safe_operational_log",
		".ticketremote_service_safe_operational_log()",
		"fn safe_operational_log_json(row: TicketremoteServiceSafeOperationalLog)",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("sidecar operational log service-view marker missing %q", required)
		}
	}
	if strings.Contains(source, "SELECT * FROM ticketremote_safe_operational_log") ||
		strings.Contains(source, ".ticketremote_safe_operational_log()") {
		t.Fatalf("sidecar must not subscribe to the private operational log table directly")
	}
}

func TestSpacetimePreservesCapturedControlCodeSuccessDuringCleanup(t *testing.T) {
	source := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")
	closeBody := sourceBetween(t, source,
		"pub fn ticketremote_member_close_control_code(",
		"#[spacetimedb::reducer]\npub fn ticketremote_member_append_safe_operational_log(")
	updateBody := sourceBetween(t, source,
		"pub fn ticketremote_update_control_code_request(",
		"#[spacetimedb::reducer]\npub fn ticketremote_append_safe_operational_log(")
	for _, required := range []string{
		"let capture_acknowledged = current_request",
		"if capture_acknowledged {",
		"captureRequired: Some(false)",
		"cleanupPending: Some(false)",
		"expiresAt: Some(control_code_result_expires_at(&now))",
	} {
		if !strings.Contains(closeBody, required) {
			t.Fatalf("captured close must preserve successful result, missing %q in %s", required, closeBody)
		}
	}
	for _, required := range []string{
		"let preserve_captured_success = existing.status == \"succeeded\"",
		"&& existing.captureAcknowledged",
		"&& control_code_cleanup_reason(&incoming_reason)",
		"clean_status = existing.status.clone();",
		"existing.reason.clone()",
		"fn control_code_cleanup_reason(reason: &str) -> bool",
		"\"ticket_detail\"",
	} {
		if !strings.Contains(updateBody, required) && !strings.Contains(source, required) {
			t.Fatalf("cleanup updates must not downgrade captured success, missing %q", required)
		}
	}
}

func TestSpacetimeControlCodeBrowserCaptureCommandMatchesPixelEnvelope(t *testing.T) {
	source := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")
	prepareBody := sourceBetween(t, source,
		"pub fn ticketremote_member_prepare_control_code(",
		"pub fn ticketremote_member_request_control_code(")
	confirmBody := sourceBetween(t, source,
		"pub fn ticketremote_member_confirm_control_code_browser_capture(",
		"pub fn ticketremote_member_close_control_code(")
	closeBody := sourceBetween(t, source,
		"pub fn ticketremote_member_close_control_code(",
		"pub fn ticketremote_member_append_safe_operational_log(")

	for _, required := range []string{
		`"type": "prepare_control_code"`,
		`"owner": "ticket"`,
		`"app": "vivi"`,
		`"flow": "control_code"`,
		`"source": "browser_spacetime"`,
	} {
		if !strings.Contains(prepareBody, required) {
			t.Fatalf("prepare command must match Pixel envelope, missing %q in %s", required, prepareBody)
		}
	}
	for _, required := range []string{
		`"owner": "ticket"`,
		`"app": "vivi"`,
		`"flow": "control_code"`,
		`"candidateFrameEpoch": frame_epoch_number`,
		`"candidateFrameSequence": frame_sequence_number`,
		"let frame_epoch_number = frame_ordinal_number(&frame_epoch);",
		"let frame_sequence_number = frame_ordinal_number(&frame_sequence);",
	} {
		if !strings.Contains(confirmBody, required) {
			t.Fatalf("browser-capture ack command must match Pixel envelope/metadata, missing %q in %s", required, confirmBody)
		}
	}
	for _, required := range []string{
		`"owner": "ticket"`,
		`"app": "vivi"`,
		`"flow": "control_code"`,
		`"candidateFrameEpoch": 0`,
		`"candidateFrameSequence": 0`,
		"cleanupPending: Some(!capture_acknowledged)",
	} {
		if !strings.Contains(closeBody, required) {
			t.Fatalf("browser-capture close command must match Pixel envelope/metadata, missing %q in %s", required, closeBody)
		}
	}
	for _, forbidden := range []string{
		`"frameEpoch": frame_epoch`,
		`"frameSequence": frame_sequence`,
	} {
		if strings.Contains(confirmBody, forbidden) {
			t.Fatalf("browser-capture command must send numeric candidate frame metadata, found %q in %s", forbidden, confirmBody)
		}
	}
}

func TestSpacetimePrivateTableEventLogsCaptureRawControlCodeData(t *testing.T) {
	source := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")
	requestBody := sourceBetween(t, source,
		"pub fn ticketremote_member_request_control_code(",
		"pub fn ticketremote_member_confirm_control_code_browser_capture(")
	ownerChunk := rustItemChunk(t, source, "#[spacetimedb::table(accessor = ticketremote_control_code_owner,")
	publicRequestChunk := rustItemChunk(t, source, "#[spacetimedb::table(accessor = ticketremote_control_code_request, public")
	signalChunk := rustItemChunk(t, source, "#[spacetimedb::table(accessor = ticketremote_stream_command_signal, public")

	for _, required := range []string{
		"digits: clean_digits.clone()",
		`"digits": clean_digits`,
		`"digits": &row.digits`,
		`"ticketremote_control_code_owner"`,
		"control_code_owner_log_row(&owner)",
		"insert_table_event_log(",
		"true,",
	} {
		if !strings.Contains(requestBody+source, required) {
			t.Fatalf("private raw control-code log marker missing %q", required)
		}
	}
	if !strings.Contains(ownerChunk, "pub digits: String") {
		t.Fatalf("private owner table must retain raw digits until expiry: %s", ownerChunk)
	}
	for _, forbidden := range []string{"digits", "payloadJson", "email"} {
		if strings.Contains(publicRequestChunk, forbidden) {
			t.Fatalf("public control-code request table must not expose raw/private field %q: %s", forbidden, publicRequestChunk)
		}
		if strings.Contains(signalChunk, forbidden) {
			t.Fatalf("public command signal table must not expose raw/private field %q: %s", forbidden, signalChunk)
		}
	}
}

func TestSpacetimeTableEventLogsActionTransitions(t *testing.T) {
	source := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")
	insertCommandBody := sourceBetween(t, source,
		"fn insert_stream_command(",
		"fn update_stream_command_status(")
	updateCommandBody := sourceBetween(t, source,
		"fn update_stream_command_status(",
		"fn upsert_phone_current_report(")
	updateControlBody := sourceBetween(t, source,
		"fn update_control_code_public_request(",
		"fn table_event_token(")
	deleteControlBody := sourceBetween(t, source,
		"fn delete_control_code_request(",
		"fn active_control_code_owner_rows(")
	auditBody := sourceBetween(t, source,
		"fn audit(",
		"fn ensure_cleanup_schedule(")
	phoneHistoryBody := sourceBetween(t, source,
		"fn append_phone_history(",
		"fn apply_phone_update(")
	snapshotBody := sourceBetween(t, source,
		"pub fn ticketremote_snapshot_runtime_tables_to_logs(",
		"#[spacetimedb::reducer]\npub fn ticketremote_audit(")

	checks := []struct {
		label   string
		body    string
		markers []string
	}{
		{"stream command insert", insertCommandBody, []string{"ticketremote_stream_command", "insert", "stream_command_log_row(&row)"}},
		{"stream command ack delete", updateCommandBody, []string{"acknowledged_delete", "dispatched_delete", `"previous"`, `"nextStatus"`}},
		{"control code request update", updateControlBody, []string{"ticketremote_control_code_request", "update", "control_code_request_log_row(&row)"}},
		{"control code request delete", deleteControlBody, []string{"ticketremote_control_code_request", "delete", "control_code_request_log_row(&row)"}},
		{"control code owner delete", deleteControlBody, []string{"ticketremote_control_code_owner", "delete", "control_code_owner_log_row(&row)"}},
		{"audit event insert", auditBody, []string{"ticketremote_audit_event", "insert", "audit_event_log_row(&row)"}},
		{"phone history insert", phoneHistoryBody, []string{"ticketremote_phone_status_history", "insert", "phone_history_log_row(&row)"}},
		{"snapshot reducer", snapshotBody, []string{"runtime_table_snapshot_completed", "snapshot", `"loggedCounts"`}},
	}
	for _, check := range checks {
		for _, marker := range check.markers {
			if !strings.Contains(check.body, marker) {
				t.Fatalf("%s missing marker %q in %s", check.label, marker, check.body)
			}
		}
	}
}

func TestSpacetimeOperationalLogWritesDoNotRunCleanup(t *testing.T) {
	source := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")
	start := strings.Index(source, "pub fn ticketremote_append_safe_operational_log(")
	if start < 0 {
		t.Fatal("appendSafeOperationalLog reducer missing")
	}
	end := strings.Index(source[start:], "pub fn ticketremote_cleanup_expired(")
	if end < 0 {
		t.Fatal("cleanup reducer missing after appendSafeOperationalLog")
	}
	body := source[start : start+end]
	if strings.Contains(body, "cleanup_expired(") {
		t.Fatalf("safe operational log writes must not run cleanup inline: %s", body)
	}

	auditStart := strings.Index(source, "pub fn ticketremote_audit(")
	if auditStart < 0 {
		t.Fatal("audit reducer missing")
	}
	auditEnd := strings.Index(source[auditStart:], "fn now(")
	if auditEnd < 0 {
		t.Fatal("now helper missing after audit reducer")
	}
	auditBody := source[auditStart : auditStart+auditEnd]
	if strings.Contains(auditBody, "cleanup_expired(") {
		t.Fatalf("audit writes must not run cleanup inline: %s", auditBody)
	}
}

func TestSpacetimeStreamCommandCleanupRefreshesSignal(t *testing.T) {
	source := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")
	start := strings.Index(source, "fn purge_expired_stream_commands_for_ticket(")
	if start < 0 {
		t.Fatal("purgeExpiredStreamCommandsForTicket missing")
	}
	end := strings.Index(source[start:], "fn refresh_touched_signals(")
	if end < 0 {
		t.Fatal("refresh_touched_signals marker missing after purgeExpiredStreamCommandsForTicket")
	}
	body := source[start : start+end]
	for _, marker := range []string{
		"let mut touched: Vec<String> = Vec::new();",
		"touched.push(",
		"refresh_touched_signals(ctx, ticket_id, &touched, now);",
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("stream command cleanup must refresh command signal marker %q in %s", marker, body)
		}
	}
}

func TestSpacetimeCurrentStateAndHistoryRetentionBoundaries(t *testing.T) {
	source := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")

	currentTables := []string{
		"#[spacetimedb::table(accessor = ticketremote_stream_desired_state, public",
		"#[spacetimedb::table(accessor = ticketremote_phone_current_report, public",
		"#[spacetimedb::table(accessor = ticketremote_ticket_summary, public",
	}
	for _, marker := range currentTables {
		chunk := rustItemChunk(t, source, marker)
		if strings.Contains(chunk, "expiresAt") {
			t.Fatalf("current state table %q must not expire: %s", marker, chunk)
		}
	}

	expiringTables := []string{
		"#[spacetimedb::table(accessor = ticketremote_stream_command,",
		"#[spacetimedb::table(accessor = ticketremote_safe_operational_log,",
		"#[spacetimedb::table(accessor = ticketremote_dev_perf_metric,",
		"#[spacetimedb::table(accessor = ticketremote_phone_status_history,",
		"#[spacetimedb::table(accessor = ticketremote_audit_event,",
	}
	for _, marker := range expiringTables {
		chunk := rustItemChunk(t, source, marker)
		if !strings.Contains(chunk, "pub expiresAt: String") {
			t.Fatalf("history/log table %q must have indexed expiry: %s", marker, chunk)
		}
	}

	safeLogChunk := rustItemChunk(t, source, "#[spacetimedb::table(accessor = ticketremote_safe_operational_log,")
	for _, marker := range []string{
		"index(accessor = ticketExpiresAt, btree(columns = [ticketId, expiresAt]))",
		"index(accessor = ticketCreatedAt, btree(columns = [ticketId, createdAt]))",
		"index(accessor = eventCreatedAt, btree(columns = [event, createdAt]))",
		"index(accessor = correlationCreatedAt, btree(columns = [correlationId, createdAt]))",
		"index(accessor = sourceCreatedAt, btree(columns = [source, createdAt]))",
	} {
		if !strings.Contains(safeLogChunk, marker) {
			t.Fatalf("safe operational log table missing trace lookup index %q in %s", marker, safeLogChunk)
		}
	}
}

func rustItemChunk(t *testing.T, source string, marker string) string {
	t.Helper()
	start := strings.Index(source, marker)
	if start < 0 {
		t.Fatalf("Rust item marker missing: %s", marker)
	}
	rest := source[start:]
	end := strings.Index(rest[1:], "\n\n#[")
	if end < 0 {
		end = strings.Index(rest[1:], "\n\nfn ")
	}
	if end < 0 {
		t.Fatalf("Rust item marker has no end: %s", marker)
	}
	return rest[:end+1]
}

func sourceBetween(t *testing.T, source string, startMarker string, endMarker string) string {
	t.Helper()
	start := strings.Index(source, startMarker)
	if start < 0 {
		t.Fatalf("source marker missing: %s", startMarker)
	}
	end := strings.Index(source[start:], endMarker)
	if end < 0 {
		t.Fatalf("source end marker missing after %s: %s", startMarker, endMarker)
	}
	return source[start : start+end]
}

func TestSpacetimeBrowserClientSubscribesToTicketScopedFocusedTables(t *testing.T) {
	source := ticketRemoteSourceFile(t, "web-client", "src", "index.ts")

	for _, required := range []string{
		"SELECT * FROM ticketremote_ticket_summary WHERE ticketId =",
		"SELECT * FROM ticketremote_viewer_public WHERE ticketId =",
		"SELECT * FROM ticketremote_phone_status WHERE ticketId =",
		"SELECT * FROM ticketremote_control_code_request WHERE ticketId =",
		"AND ownerPublicId =",
		"appendDevMetric(",
		"memberAppendDevPerfMetric",
		"publishFocusedState(",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("Spacetime browser client is missing focused subscription marker %q", required)
		}
	}
	for _, forbidden := range []string{
		"ticketremoteLiveState",
		"ticketremote_live_state",
		"ticketremote_service_",
		"SELECT * FROM ticketremote_dev_perf_metric",
		"SELECT * FROM ticketremote_safe_operational_log",
		"stateJson",
		"asRowState(",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("Spacetime browser client still contains old broad live-state marker %q", forbidden)
		}
	}
}
