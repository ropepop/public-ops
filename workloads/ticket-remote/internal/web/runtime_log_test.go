package web

import (
	"encoding/json"
	"strings"
	"testing"

	"ticketremote/internal/state"
)

func TestRuntimeLoggingStaysSpacetimeOnly(t *testing.T) {
	files := []string{
		ticketRemoteSourceFile(t, "internal", "web", "server.go"),
		ticketRemoteSourceFile(t, "internal", "web", "stream_control.go"),
		ticketRemoteSourceFile(t, "internal", "web", "stream_command_bridge.go"),
		ticketRemoteSourceFile(t, "internal", "web", "ticket_phone_lease.go"),
		ticketRemoteSourceFile(t, "internal", "web", "control_code.go"),
		ticketRemoteSourceFile(t, "internal", "web", "runtime_log.go"),
		ticketRemoteSourceFile(t, "internal", "phone", "relay.go"),
	}
	for _, source := range files {
		for _, forbidden := range []string{`"log"`, "log.Print(", "log.Printf(", "fmt.Print("} {
			if strings.Contains(source, forbidden) {
				t.Fatalf("Ticket runtime must not write local process logs, found %q", forbidden)
			}
		}
	}

	runtimeLog := ticketRemoteSourceFile(t, "internal", "web", "runtime_log.go")
	for _, required := range []string{
		"AppendSafeOperationalLog",
		"safeRuntimeLogDetail",
		`body = ` + "`" + `{"truncated":true}` + "`",
	} {
		if !strings.Contains(runtimeLog, required) {
			t.Fatalf("runtime log path must keep safe Spacetime logging, missing %q", required)
		}
	}
}

func TestRuntimeLogDetailIsBoundedAndSanitized(t *testing.T) {
	detail := safeRuntimeLogDetail(map[string]any{
		"raw key with spaces": strings.Repeat("x", 300),
	})
	value, ok := detail["raw_key_with_spaces"].(string)
	if !ok {
		t.Fatalf("sanitized detail value = %#v, want string", detail["raw_key_with_spaces"])
	}
	if len(value) != 240 {
		t.Fatalf("sanitized detail length = %d, want 240", len(value))
	}
}

func TestPixelTraceEventKeepsRecoveryBoundaryDetail(t *testing.T) {
	logs := make(chan state.SafeOperationalLogInput, 4)
	server := newStreamControlTestServer(t, &streamDesiredRecordingStore{logs: logs})

	if !server.handlePixelTraceEvent(map[string]any{
		"type":                            "ticket_trace_event",
		"event":                           "stream_client_opened",
		"level":                           "info",
		"traceId":                         "trace-1",
		"streamState":                     "starting",
		"sessionState":                    "starting",
		"streamActive":                    true,
		"captureMode":                     "root_hardware_h264",
		"videoClients":                    "1",
		"frameSequence":                   "42",
		"sentFrames":                      "42",
		"lastFreshFrameAgeMillis":         "13000",
		"phoneUptimeMillis":               "123456",
		"hardwareH264State":               "idle",
		"hardwareH264Active":              "false",
		"hardwareH264Available":           "true",
		"hardwareH264Restarts":            "1",
		"hardwareH264LastExitReason":      "requested_restart:test",
		"hardwareH264LastFrameAgeMillis":  "12000",
		"hardwareH264HelperState":         "ready",
		"hardwareH264Visibility":          "visible",
		"lastStreamRecoveryResult":        "started",
		"lastStreamRecoveryFailureReason": "",
		"lastStreamRecoveryAgeMillis":     "4000",
		"streamWatchdogStage":             "recovering",
		"lastStreamWatchdogAction":        "restart_capture_engine",
		"lastStreamWatchdogReason":        "watchdog_stale_visible_frame",
		"lastVideoClientAgeMillis":        "250",
		"timestampMillis":                 "1783150000000",
	}) {
		t.Fatal("pixel trace event was not accepted")
	}

	log := waitForSafeLog(t, logs, "stream_client_opened")
	var detail map[string]any
	if err := json.Unmarshal([]byte(log.DetailJSON), &detail); err != nil {
		t.Fatalf("decode detail JSON: %v", err)
	}
	for key, want := range map[string]any{
		"lastFreshFrameAgeMillis":        "13000",
		"hardwareH264State":              "idle",
		"hardwareH264Restarts":           "1",
		"hardwareH264LastFrameAgeMillis": "12000",
		"hardwareH264HelperState":        "ready",
		"hardwareH264Visibility":         "visible",
		"lastStreamRecoveryResult":       "started",
		"lastStreamRecoveryAgeMillis":    "4000",
		"streamWatchdogStage":            "recovering",
		"lastStreamWatchdogReason":       "watchdog_stale_visible_frame",
		"lastVideoClientAgeMillis":       "250",
	} {
		if detail[key] != want {
			t.Fatalf("%s = %#v, want %#v in %#v", key, detail[key], want, detail)
		}
	}
}
