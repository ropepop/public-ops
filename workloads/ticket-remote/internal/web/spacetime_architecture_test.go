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

func TestGoRuntimeUsesDirectPhoneBridgeWithoutRetiredBrokerLayer(t *testing.T) {
	sources := []string{
		ticketRemoteSourceFile(t, "internal", "config", "config.go"),
		ticketRemoteSourceFile(t, "internal", "web", "server.go"),
		ticketRemoteSourceFile(t, "internal", "web", "relay_viewers.go"),
	}
	for _, source := range sources {
		for _, retired := range []string{
			"Broker" + "BaseURL",
			"TICKET_REMOTE_PHONE_" + "BROKER_URL",
			"phone" + "BrokerHTTPClient",
			"publishTicket" + "Presence",
			"acquireTicketPhone" + "LeaseAsync",
			"releaseTicketPhone" + "LeaseAsync",
		} {
			if strings.Contains(source, retired) {
				t.Fatalf("Go runtime still contains retired phone-broker marker %q", retired)
			}
		}
	}
	leasePath := filepath.Join("..", "..", "internal", "web", "ticket_phone_"+"lease.go")
	if _, err := os.Stat(leasePath); !os.IsNotExist(err) {
		t.Fatalf("retired phone lease source still exists or could not be checked: %v", err)
	}
	coordinatorPath := filepath.Join("..", "..", "internal", "web", "control_code.go")
	if _, err := os.Stat(coordinatorPath); !os.IsNotExist(err) {
		t.Fatalf("retired Go control-code coordinator still exists or could not be checked: %v", err)
	}
}

func TestSpacetimeBareBonesSchemaKeepsOnlyCurrentProductSurfaces(t *testing.T) {
	source := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")

	for _, required := range []string{
		"const HISTORY_TTL_MS: i64 = 6 * 60 * 60 * 1000;",
		"const CLEANUP_BATCH_SIZE: u32 = 500;",
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
		"legacy_operational_log_writer_inactive",
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

func TestLegacySafeOperationalLogSurfaceCannotWriteAndStillDrains(t *testing.T) {
	source := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")
	sidecar := ticketRemoteSourceFile(t, "spacetime-sidecar", "src", "main.rs")

	for _, required := range []string{
		"pub fn ticketremote_append_safe_operational_log(",
		"pub fn ticketremote_member_append_safe_operational_log(",
		"legacy_operational_log_writer_inactive",
		"Legacy compatibility state.",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("legacy safe-log migration surface missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"fn insert_safe_operational_log(",
		"table.insert(TicketremoteSafeOperationalLog",
		"fn safe_log_sample_interval_ms(",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("Ticket module must not retain a legacy safe-log writer %q", forbidden)
		}
	}
	for _, required := range []string{
		"const MAX_SAFE_OPERATIONAL_LOG_DETAIL_BYTES: usize = 1024;",
		"TICKET_REMOTE_OPERATIONAL_LOGGING_HOST",
		"TICKET_REMOTE_OPERATIONAL_LOGGING_DATABASE",
		"operationallog_append_ticket_event",
		"append_safe_operational_log(app, request)",
	} {
		if !strings.Contains(sidecar, required) {
			t.Fatalf("central Ticket logging path missing %q", required)
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

	cleanupBody := sourceBetween(t, source, "fn cleanup_expired(", "ticket_expiry_purgers! {")
	for _, forbidden := range []string{
		"insert_safe_operational_log",
		"cleanup_expired_completed",
		"nothing",
	} {
		if strings.Contains(cleanupBody, forbidden) {
			t.Fatalf("cleanup must not write routine log rows: %q", forbidden)
		}
	}
	for _, required := range []string{"batch_size.min(CLEANUP_BATCH_SIZE)", "deleted < limit"} {
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
		"memberAppendSafeOperationalLog",
		"appendSafeLog(",
		"logRowId(",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("browser Spacetime client still references removed surface %q", forbidden)
		}
	}
}

func TestSpacetimeBrowserInstallsCSPSafeCodecsBeforeBuildingConnection(t *testing.T) {
	client := ticketRemoteSourceFile(t, "web-client", "src", "index.ts")
	codecs := ticketRemoteSourceFile(t, "web-client", "src", "csp-safe-codecs.ts")
	bundle := ticketRemoteSourceFile(t, "internal", "web", "static", "spacetime-client.js")

	for _, required := range []string{
		"const productSerializers = new WeakMap",
		"const productDeserializers = new WeakMap",
		"const sumSerializers = new WeakMap",
		"const sumDeserializers = new WeakMap",
		"ProductType.makeSerializer = productSerializer;",
		"ProductType.makeDeserializer = productDeserializer;",
		"SumType.makeSerializer = sumSerializer;",
		"SumType.makeDeserializer = sumDeserializer;",
		"__timestamp_micros_since_unix_epoch__",
		"ty.variants[1].algebraicType",
	} {
		if !strings.Contains(codecs, required) {
			t.Fatalf("CSP-safe Spacetime codec installer missing %q", required)
		}
	}
	if strings.Contains(codecs, "Function(") {
		t.Fatalf("CSP-safe Spacetime codec installer must not use the Function constructor")
	}

	for name, body := range map[string]string{"source": client, "bundle": bundle} {
		installAt := strings.Index(body, "installCspSafeSpacetimeCodecs();")
		connectAt := strings.Index(body, "const builder = DbConnection.builder()")
		if installAt < 0 || connectAt < 0 {
			t.Fatalf("%s missing codec installation or connection construction marker", name)
		}
		if installAt > connectAt {
			t.Fatalf("%s must install CSP-safe codecs before constructing a Spacetime connection", name)
		}
	}

	connectBody := sourceBetween(t, client, "  connect(): void {", "  disconnect(markDisconnected")
	for _, required := range []string{
		"try {",
		"const builder = DbConnection.builder()",
		"} catch (error) {",
		"this.connected = false;",
		"this.conn = null;",
		"this.handlers.onStatus?.(\"offline\", connectionError.message);",
		"this.rejectLive(connectionError);",
		"if (!this.manuallyDisconnected) this.scheduleReconnect();",
	} {
		if !strings.Contains(connectBody, required) {
			t.Fatalf("Spacetime connection must recover from synchronous construction failures, missing %q", required)
		}
	}
}

func TestTicketTraceReadsProductStateAndCentralOperationalRowsSeparately(t *testing.T) {
	trace := ticketRemoteSourceFile(t, "scripts", "trace-spacetime.sh")
	for _, required := range []string{
		"TICKET_REMOTE_SPACETIME_DATABASE",
		"TICKET_REMOTE_OPERATIONAL_LOGGING_HOST",
		"TICKET_REMOTE_OPERATIONAL_LOGGING_DATABASE",
		"operationallog_event",
		"domain = 'ticket'",
		"scopeId = '$ticket_sql'",
		"LC_ALL=C sort -r",
		`awk -v limit="$row_limit" 'NR <= limit'`,
	} {
		if !strings.Contains(trace, required) {
			t.Fatalf("Ticket trace script missing central-log marker %q", required)
		}
	}
	if strings.Contains(trace, "FROM ticketremote_safe_operational_log") {
		t.Fatal("Ticket trace script still queries the legacy Ticket log table")
	}
	if strings.Contains(trace, "ORDER BY occurredAt") {
		t.Fatal("Ticket trace script uses unsupported Maincloud ORDER BY instead of bounded client-side sorting")
	}
}

func TestSidecarAndAdminLogViewerRemoved(t *testing.T) {
	sidecar := ticketRemoteSourceFile(t, "spacetime-sidecar", "src", "main.rs")
	browser := ticketRemoteSourceFile(t, "web-client", "ticket-app-source.js")
	server := ticketRemoteSourceFile(t, "internal", "web", "server.go")
	admin := ticketRemoteSourceFile(t, "internal", "web", "static", "admin.html.tmpl")
	events := ticketRemoteSourceFile(t, "internal", "web", "event_sink.go")

	for _, required := range []string{
		"SELECT * FROM ticketremote_service_ticket",
		"SELECT * FROM ticketremote_service_ticket_member",
		"SELECT * FROM ticketremote_service_phone_backend",
		"SELECT * FROM ticketremote_phone_current_report WHERE id =",
		"APPEND_TICKET_EVENT_REDUCER",
		"operationallog_append_ticket_event",
		"TICKET_REMOTE_OPERATIONAL_LOGGING_HOST",
		"TICKET_REMOTE_OPERATIONAL_LOGGING_DATABASE",
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
		"SELECT * FROM ticketremote_stream_desired_state WHERE id =",
		"SELECT * FROM ticketremote_relay_current_report WHERE id =",
		"SELECT * FROM ticketremote_control_code_request WHERE ticketId =",
		"ticketremote_purge_expired_stream_commands",
		"ticketremote_service_stream_command().iter()",
		"ticketremote_service_safe_operational_log",
		"ticketremote_service_viewer_presence",
		"ticketremote_service_control_session",
		"ticketremote_append_safe_operational_log_then",
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
		"#[spacetimedb::table(accessor = ticketremote_stream_viewer_focus, public,",
	} {
		if !strings.Contains(module, marker) {
			t.Fatalf("hot current table should use primary-key shape, missing %q", marker)
		}
	}
	if !strings.Contains(module, "upsert_stream_command_signal(ctx, &row.ticketId, &row.backendId, &row.revision, now);") {
		t.Fatalf("desired-state changes must wake the Pixel signal row")
	}
	if !strings.Contains(module, `"start" | "keyframe" | "recover_stream"`) || strings.Contains(module, "prepare_control_code") {
		t.Fatalf("only the retained background stream commands may be deduped")
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
		"SELECT * FROM ticketremote_stream_viewer_focus WHERE ticketId = ${ticket} AND backendId = ${backendId}",
	} {
		if !strings.Contains(browser, required) {
			t.Fatalf("browser missing one-row subscription marker %q", required)
		}
	}
}

func TestStreamViewerFocusUsesSafePublicIDs(t *testing.T) {
	module := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")
	tableChunk := substringBetween(t, module,
		"#[spacetimedb::table(accessor = ticketremote_stream_viewer_focus, public,",
		"#[spacetimedb::table(accessor = ticketremote_stream_command")
	reducerChunk := substringBetween(t, module,
		"ticketremote_member_set_stream_focus(ctx;",
		"macro_rules! member_stream_reducers")
	browser := readTicketWebClientSource(t, "src/index.ts")

	for _, required := range []string{
		"pub publicId: String",
		"pub active: bool",
		"pub lastSeenAt: String",
		"pub expiresAt: String",
		"index(accessor = ticketBackend, btree(columns = [ticketId, backendId]))",
		"index(accessor = ticketExpiresAt, btree(columns = [ticketId, expiresAt]))",
	} {
		if !strings.Contains(tableChunk, required) {
			t.Fatalf("viewer focus table missing safe presence marker %q", required)
		}
	}
	for _, forbidden := range []string{
		"pub email:",
		"pub sessionId:",
		"pub connectionId:",
	} {
		if strings.Contains(tableChunk, forbidden) {
			t.Fatalf("viewer focus table must not expose private marker %q", forbidden)
		}
	}
	for _, required := range []string{
		"upsert_stream_viewer_focus(",
		"active_stream_viewer_focus_count(ctx, &ticket.id, &backend_id, &now)",
		"viewers > 0",
		"purge_expired_stream_viewer_focus_for_ticket_backend(",
	} {
		if !strings.Contains(reducerChunk, required) {
			t.Fatalf("stream focus reducer missing presence marker %q", required)
		}
	}
	for _, required := range []string{
		"const STREAM_FOCUS_REFRESH_MS = 30000;",
		"activeViewerFocusRows(",
		"viewerPresence = viewerFocusRows.map",
		"Math.max(Number.isFinite(reportedViewerCount) ? reportedViewerCount : 0, viewerPresence.length)",
		"scheduleViewerPresenceExpiry(viewerFocusRows)",
	} {
		if !strings.Contains(browser, required) {
			t.Fatalf("browser client missing viewer focus marker %q", required)
		}
	}
	if strings.Contains(browser, "viewerPresence: []") {
		t.Fatalf("browser client must not publish an always-empty viewerPresence list")
	}
}

func TestSpacetimeSuppressesServiceBackgroundCommandsButHonorsRequesterRecovery(t *testing.T) {
	module := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")
	insertRequest := substringBetween(t, module,
		"fn insert_control_code_public_request(",
		"#[derive(Default)]\nstruct ControlCodeChanges")
	appendReducer := substringBetween(t, module,
		"pub fn ticketremote_append_stream_command(",
		"service_reducers! {\n    ticketremote_ack_stream_command")
	memberStreamReducers := substringBetween(t, module,
		"macro_rules! member_stream_reducers",
		"member_reducers! {")
	if strings.Contains(module, "prepare_control_code") ||
		strings.Contains(module, "control_code_fast_state_current_ready") {
		t.Fatalf("the single-path contract must not retain a preparation reducer or fast-state suppression helper")
	}

	if strings.Contains(memberStreamReducers, "live_relay_suppresses_background_stream_command(") {
		t.Fatalf("member stream reducers must not use relay-wide liveness to suppress one stale requester")
	}
	for _, required := range []string{
		"let email = client_email_from_auth(ctx, &ticket.id)?;",
		"insert_stream_command(",
		`("source", "browser")`,
		`ticketremote_member_request_keyframe => "keyframe"`,
		`ticketremote_member_recover_stream => "recover_stream"`,
	} {
		if !strings.Contains(memberStreamReducers, required) {
			t.Fatalf("member stream reducers missing requester-scoped marker %q", required)
		}
	}
	for _, required := range []string{
		`status: "queued".into(),`,
		`reason: "requested".into(),`,
		"requestedAt: now.into(),",
		"updatedAt: now.into(),",
	} {
		if !strings.Contains(insertRequest, required) {
			t.Fatalf("accepted control-code request must become active immediately, missing %q", required)
		}
	}
	for _, required := range []string{
		"relay_current_report_suppresses_background_stream_command(ctx, &id, now)",
		"clean_reason.contains(\"control_code\")",
		"report.videoClients == 0 || report.streamVerdict != \"live\"",
		"STREAM_BACKGROUND_REPORT_MAX_AGE_MS",
		"lastFrameVisualAgeMillis",
		"liveFrameMaxAgeMillis",
	} {
		if !strings.Contains(module, required) {
			t.Fatalf("live relay suppression helper missing %q", required)
		}
	}
	for _, required := range []string{
		"suppressible_background_stream_command(&command_type)",
		"live_relay_suppresses_background_stream_command(",
		"return Ok(());",
	} {
		if !strings.Contains(appendReducer, required) {
			t.Fatalf("service stream command append reducer missing live suppression marker %q", required)
		}
	}
	for _, required := range []string{
		"ticketremote_phone_current_report().id().find(id)",
		"report.desiredActive",
		"streamActive",
		"sessionState",
		"relayStreamState",
		"hardwareH264Active",
		"hardwareH264Visibility",
		"streamWatchdogStage",
		"activeVideoClients",
		"videoClients",
		"relayViewers",
	} {
		if !strings.Contains(module, required) {
			t.Fatalf("live phone suppression fallback missing %q", required)
		}
	}
	if !strings.Contains(module, `"fastRevision": bounded_text(&expectedFastRevision, 160)`) {
		t.Fatalf("the durable request must carry only the browser's fast-state handoff revision")
	}
}

func TestSpacetimeControlCodeQueuesColdRequestsAndSerializesPhoneWork(t *testing.T) {
	module := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")
	reducer := substringBetween(t, module,
		"ticketremote_member_request_control_code(ctx;",
		"ticketremote_member_confirm_control_code_browser_capture(ctx;")
	for _, required := range []string{
		"let session_id = non_empty(&sessionId, &connection_session_id(ctx));",
		"valid_control_code_digits(&clean_digits)",
		"ticket_has_control_code_request_in_progress(ctx, &ticket.id, &now)",
		`return Err("request_in_progress".into());`,
		"CONTROL_CODE_RATE_LIMIT",
		"insert_control_code_public_request(",
		`"generate_control_code"`,
		`"fastRevision": bounded_text(&expectedFastRevision, 160)`,
	} {
		if !strings.Contains(reducer, required) {
			t.Fatalf("queue-first control-code reducer missing %q", required)
		}
	}
	if strings.Contains(reducer, `return Err("fast_not_ready".into());`) {
		t.Fatalf("cold fast state must be telemetry, not a request admission gate")
	}
	if strings.Contains(reducer, "unwrap_or_else(|| expectedFastRevision.clone())") {
		t.Fatalf("browser fast revision must not overwrite the authoritative server state")
	}
	if strings.Contains(reducer, "fastStateReadyAtSubmit") || strings.Contains(reducer, "submitMode") {
		t.Fatalf("request payload must not duplicate fast-state readiness fields")
	}
	for _, required := range []string{
		"let $email = client_email_from_auth($ctx, &$ticket.id)?;",
		`"fast_ready"`,
		`matches!(row.status.as_str(), "queued" | "running")`,
		`row.status == "succeeded" && (row.cleanupPending`,
		`row.captureRequired && !row.captureAcknowledged`,
		`matches!(row.status.as_str(), "closed" | "expired" | "failed")`,
		".ticketremote_control_code_request()",
		".ticketId()",
	} {
		if !strings.Contains(module, required) {
			t.Fatalf("control-code phone occupancy contract missing %q", required)
		}
	}
}

func TestTerminalControlCodeUpdateAtomicallyAcknowledgesGenerateCommand(t *testing.T) {
	module := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")
	updateReducer := substringBetween(t, module,
		"pub fn ticketremote_update_control_code_request(",
		"fn control_code_cleanup_reason(")

	for _, required := range []string{
		"if succeeded || terminal_failure",
		`&format!("{}:generate_control_code", requestId.trim())`,
		`"acknowledged"`,
		`"terminal_request_published"`,
	} {
		if !strings.Contains(updateReducer, required) {
			t.Fatalf("terminal request reducer missing atomic command acknowledgement marker %q", required)
		}
	}
}

func TestSensitiveControlCodeOperationalLogsHaveNarrowServicePurge(t *testing.T) {
	source := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")
	body := sourceBetween(
		t,
		source,
		"ticketremote_purge_sensitive_operational_logs(ctx;",
		"ticketremote_cleanup_expired(ctx;",
	)

	for _, required := range []string{
		".ticketremote_safe_operational_log()",
		".ticketId()",
		`"pixel_ticket_control_code_result"`,
		`"pixel_ticket_control_code_request_result_detected"`,
		".delete(&row.id)",
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("sensitive-log purge missing %q", required)
		}
	}
	if !strings.Contains(source, "require_service($ctx)?;") {
		t.Fatalf("service reducer macro must preserve fail-closed service authentication")
	}
	if strings.Contains(body, "detailJson") {
		t.Fatal("sensitive-log purge must identify rows only by bounded event name")
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
