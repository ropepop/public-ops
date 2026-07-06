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

func TestSpacetimeBareBonesSchemaKeepsOnlyCurrentProductSurfaces(t *testing.T) {
	source := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")

	for _, required := range []string{
		"const HISTORY_TTL_MS: i64 = 6 * 60 * 60 * 1000;",
		"const CLEANUP_BATCH_SIZE: u32 = 500;",
		"const SAFE_LOG_DETAIL_MAX_BYTES: usize = 1024;",
		"ticketremote_ticket",
		"ticketremote_ticket_member",
		"ticketremote_phone_backend",
		"ticketremote_stream_desired_state",
		"ticketremote_stream_command",
		"ticketremote_stream_command_signal",
		"ticketremote_phone_current_report",
		"ticketremote_relay_current_report",
		"ticketremote_control_code_request",
		"ticketremote_control_code_owner",
		"ticketremote_safe_operational_log",
		"ticketremote_cleanup_schedule",
		"ticketremote_service_ticket",
		"ticketremote_service_ticket_member",
		"ticketremote_service_phone_backend",
		"ticketremote_service_stream_command",
		"ticketremote_service_bootstrap",
		"ticketremote_update_phone_current_report",
		"ticketremote_update_relay_current_report",
		"ticketremote_member_request_control_code",
		"ticketremote_member_append_safe_operational_log",
		"ticketremote_append_safe_operational_log",
		"ticketremote_cleanup_expired",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("SpacetimeDB module missing current product marker %q", required)
		}
	}

	for _, forbidden := range removedSpacetimeMarkers() {
		if strings.Contains(source, forbidden) {
			t.Fatalf("SpacetimeDB module still contains removed marker %q", forbidden)
		}
	}
}

func TestSafeOperationalLogsAreImmediateCheapRows(t *testing.T) {
	source := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")

	for _, required := range []string{
		"pub fn ticketremote_append_safe_operational_log(\n    ctx: &ReducerContext,\n    id: String,",
		"pub fn ticketremote_member_append_safe_operational_log(\n    ctx: &ReducerContext,\n    id: String,",
		"detailJson: safe_json_string(detail_json, SAFE_LOG_DETAIL_MAX_BYTES),",
		"id: safe_log_row_id(",
		"ctx.db\n        .ticketremote_safe_operational_log()\n        .insert(TicketremoteSafeOperationalLog",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("safe log path missing cheap-row marker %q", required)
		}
	}

	logBody := sourceBetween(t, source, "fn insert_safe_operational_log(", "fn safe_log_row_id(")
	for _, forbidden := range []string{
		"next_audit_ordinal",
		"sampled_safe_log_event",
		"coalesced_safe_log_detail",
		".find(&id)",
		".update(",
	} {
		if strings.Contains(logBody, forbidden) {
			t.Fatalf("safe log insert path must not read/coalesce/update rows: %q", forbidden)
		}
	}

	safeLogChunk := rustItemChunk(t, source, "#[spacetimedb::table(accessor = ticketremote_safe_operational_log,")
	for _, required := range []string{
		"index(accessor = ticketExpiresAt, btree(columns = [ticketId, expiresAt]))",
		"index(accessor = ticketCreatedAt, btree(columns = [ticketId, createdAt]))",
	} {
		if !strings.Contains(safeLogChunk, required) {
			t.Fatalf("safe log table missing retained index %q", required)
		}
	}
	for _, forbidden := range []string{
		"eventCreatedAt",
		"correlationCreatedAt",
		"sourceCreatedAt",
		"levelCreatedAt",
	} {
		if strings.Contains(safeLogChunk, forbidden) {
			t.Fatalf("safe log table still has removed broad browsing index %q", forbidden)
		}
	}

	cleanupBody := sourceBetween(t, source, "fn cleanup_expired(", "fn purge_expired_stream_commands_for_ticket(")
	for _, forbidden := range []string{
		"insert_safe_operational_log",
		"cleanup_expired_completed",
		"nothing",
	} {
		if strings.Contains(cleanupBody, forbidden) {
			t.Fatalf("cleanup must not write routine log rows: %q", forbidden)
		}
	}
	for _, required := range []string{"CLEANUP_BATCH_SIZE", "cleanup_limit_reached"} {
		if !strings.Contains(cleanupBody, required) {
			t.Fatalf("cleanup must keep bounded deletes, missing %q", required)
		}
	}
}

func TestSpacetimeBrowserClientSubscribesOnlyCurrentProductTables(t *testing.T) {
	source := ticketRemoteSourceFile(t, "web-client", "src", "index.ts")

	for _, required := range []string{
		"SELECT * FROM ticketremote_stream_desired_state WHERE id =",
		"SELECT * FROM ticketremote_phone_current_report WHERE id =",
		"SELECT * FROM ticketremote_relay_current_report WHERE id =",
		"SELECT * FROM ticketremote_control_code_request WHERE ticketId =",
		"AND ownerPublicId =",
		"memberAppendSafeOperationalLog",
		"id: this.logRowId(\"browser\", event, correlationId)",
		"publishFocusedState(",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("browser Spacetime client missing current product marker %q", required)
		}
	}
	for _, forbidden := range []string{
		"ticketremote_ticket_summary",
		"ticketremote_viewer_public",
		"ticketremote_phone_status",
		"ticketremote_dev_perf",
		"ticketremote_audit",
		"ticketremote_safe_operational_log",
		"ticketremote_service_",
		"appendDevMetric",
		"memberAppendDevPerfMetric",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("browser Spacetime client still references removed surface %q", forbidden)
		}
	}
}

func TestSidecarAndAdminLogViewerRemoved(t *testing.T) {
	sidecar := ticketRemoteSourceFile(t, "spacetime-sidecar", "src", "main.rs")
	browser := ticketRemoteSourceFile(t, "web-client", "ticket-app-source.js")
	server := ticketRemoteSourceFile(t, "internal", "web", "server.go")
	admin := ticketRemoteSourceFile(t, "internal", "web", "static", "admin.html.tmpl")
	events := ticketRemoteSourceFile(t, "internal", "web", "operational_events.go")

	for _, required := range []string{
		"SELECT * FROM ticketremote_service_ticket",
		"SELECT * FROM ticketremote_service_ticket_member",
		"SELECT * FROM ticketremote_service_phone_backend",
		"SELECT * FROM ticketremote_stream_desired_state WHERE id =",
		"SELECT * FROM ticketremote_phone_current_report WHERE id =",
		"SELECT * FROM ticketremote_relay_current_report WHERE id =",
		"SELECT * FROM ticketremote_control_code_request WHERE ticketId =",
	} {
		if !strings.Contains(sidecar, required) {
			t.Fatalf("sidecar missing current subscription %q", required)
		}
	}
	for _, forbidden := range []string{
		"/logs",
		"LogsResponse",
		"safe_operational_logs",
		"install_command_watchers(ctx, Arc::clone",
		"\"/commands\"",
		"\"/signal\"",
		"SELECT * FROM ticketremote_service_stream_command",
		"SELECT * FROM ticketremote_stream_command_signal WHERE ticketId =",
		"ticketremote_purge_expired_stream_commands",
		"ticketremote_service_stream_command().iter()",
		"ticketremote_service_safe_operational_log",
		"ticketremote_service_viewer_presence",
		"ticketremote_service_control_session",
	} {
		if strings.Contains(sidecar, forbidden) {
			t.Fatalf("sidecar still contains removed log/presence surface %q", forbidden)
		}
	}

	for _, forbidden := range []string{
		"/api/v1/admin/operational-events",
		"adminEvents",
		"adminEventsURL",
		"renderOperationalEvents",
	} {
		if strings.Contains(browser, forbidden) || strings.Contains(server, forbidden) || strings.Contains(admin, forbidden) {
			t.Fatalf("admin/browser log viewer still contains removed marker %q", forbidden)
		}
	}
	for _, required := range []string{
		"/api/v1/internal/service-events",
		"handleServiceEvent",
		"recordProductEvent",
		"AppendSafeOperationalLog",
	} {
		if !strings.Contains(server+events, required) {
			t.Fatalf("service event ingestion must remain, missing %q", required)
		}
	}
}

func TestLowCostHotPathsUseSingleSignalAndOneRowLookups(t *testing.T) {
	module := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")
	server := ticketRemoteSourceFile(t, "internal", "web", "server.go")
	browser := ticketRemoteSourceFile(t, "web-client", "src", "index.ts")

	commandChunk := rustItemChunk(t, module, "#[spacetimedb::table(accessor = ticketremote_stream_command,")
	for _, required := range []string{
		"index(accessor = ticketExpiresAt, btree(columns = [ticketId, expiresAt]))",
		"index(accessor = ticketBackendStatus, btree(columns = [ticketId, backendId, status]))",
	} {
		if !strings.Contains(commandChunk, required) {
			t.Fatalf("stream command table missing retained lookup %q", required)
		}
	}
	for _, forbidden := range []string{
		"ticketBackendExpiresAt",
		"ticketBackendStatusExpiresAt",
		"ticketBackendRevision",
	} {
		if strings.Contains(commandChunk, forbidden) {
			t.Fatalf("stream command table still has extra hot index marker %q", forbidden)
		}
	}

	for _, marker := range []string{
		"#[spacetimedb::table(accessor = ticketremote_stream_command_signal, public)]",
		"#[spacetimedb::table(accessor = ticketremote_phone_current_report, public)]",
		"#[spacetimedb::table(accessor = ticketremote_relay_current_report, public)]",
	} {
		if !strings.Contains(module, marker) {
			t.Fatalf("hot current table should use primary-key shape, missing %q", marker)
		}
	}
	if !strings.Contains(module, "upsert_stream_command_signal(ctx, &row.ticketId, &row.backendId, &row.revision, now);") {
		t.Fatalf("desired-state changes must wake the Pixel signal row")
	}
	if strings.Contains(module, "lastFrameAgoMillis: last_frame_ago_millis") {
		t.Fatalf("relay current report must not write a constantly changing frame age")
	}
	if !strings.Contains(module, "lastFrameAgoMillis: 0") ||
		!strings.Contains(module, "#[default(None::<String>)]") ||
		!strings.Contains(module, "pub lastFrameAt: Option<String>") ||
		!strings.Contains(module, "lastFrameAt: Some(bounded_text(last_frame_at.trim(), 80))") {
		t.Fatalf("relay current report must store a stable lastFrameAt timestamp")
	}
	if _, err := os.Stat("stream_command_bridge.go"); !os.IsNotExist(err) {
		t.Fatalf("old server-to-phone command bridge file must be deleted, stat error=%v", err)
	}
	if strings.Contains(server, "s.startStreamCommandBridge()") {
		t.Fatalf("server startup must not run the phone command bridge")
	}
	for _, required := range []string{
		"const backendRow = sqlString(`${this.cfg.ticketId}:${this.backendId()}`);",
		"SELECT * FROM ticketremote_stream_desired_state WHERE id = ${backendRow}",
		"SELECT * FROM ticketremote_phone_current_report WHERE id = ${backendRow}",
		"SELECT * FROM ticketremote_relay_current_report WHERE id = ${backendRow}",
	} {
		if !strings.Contains(browser, required) {
			t.Fatalf("browser missing one-row subscription marker %q", required)
		}
	}
}

func removedSpacetimeMarkers() []string {
	return []string{
		"ticketremote_viewer_presence",
		"ticketremote_viewer_public",
		"ticketremote_control_session",
		"ticketremote_phone_status",
		"ticketremote_phone_status_history",
		"ticketremote_dev_perf_metrics_config",
		"ticketremote_dev_perf_metric",
		"ticketremote_audit_event",
		"ticketremote_audit_counter",
		"ticketremote_ticket_summary",
		"ticketremote_service_viewer_presence",
		"ticketremote_service_control_session",
		"ticketremote_service_safe_operational_log",
		"ticketremote_member_append_dev_perf_metric",
		"ticketremote_append_dev_perf_metric",
		"ticketremote_set_dev_perf_metrics",
		"ticketremote_snapshot_runtime_tables_to_logs",
		"insert_table_event_log",
		"next_audit_ordinal",
		"phone_history_log_row",
		"control_code_request_log_row",
		"stream_command_log_row",
		"audit_event_log_row",
		"runtime_table_snapshot_completed",
		"cleanup_expired_completed",
		"stream_command_appended",
		"stream_command_acknowledged",
		"browser_stream_focus_set",
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
