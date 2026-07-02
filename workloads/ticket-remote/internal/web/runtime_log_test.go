package web

import (
	"strings"
	"testing"
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
