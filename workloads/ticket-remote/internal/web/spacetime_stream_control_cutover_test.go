package web

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"nhooyr.io/websocket"
)

func TestStreamControlCutoverDoesNotUseRemovedPhoneControlAuthority(t *testing.T) {
	productionFiles := []string{
		"server.go",
		"stream_control.go",
		"../phone/relay.go",
	}
	forbidden := []string{
		"relay.SendJSON(",
		"relay.SendText(",
		"relay.SendControlExit(",
		"StartPhoneSession(",
		"websocketURL(\"/api/v1/session\")",
		"/api/v1/session/start",
		"/api/v1/session/stop",
		"waitForPhoneControlCodeAccepted",
		"fetchPhoneControlCodeRequest",
	}
	for _, file := range productionFiles {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		source := string(body)
		for _, snippet := range forbidden {
			if strings.Contains(source, snippet) {
				t.Fatalf("%s reintroduced removed phone-control authority %q", file, snippet)
			}
		}
	}
}

func TestStreamControlCutoverUsesSpacetimeCommandsAndVideoOnlyRelay(t *testing.T) {
	checks := map[string][]string{
		"stream_control.go": {
			"SetStreamDesiredState",
			"AppendStreamCommand",
			"UpdateRelayCurrentReport",
			"relayReportLoop",
			"relayReportHeartbeat",
			"backgroundStreamCommandRequiresDemand",
		},
		"server.go": {
			"publishRelayCurrentReportAsync",
			"handleVideoStreamMessage",
			"identifyMember(w, r)",
		},
		"static/spacetime-client.js": {
			"this.callReducer(\"memberRequestControlCode\"",
			"this.callReducer(\"memberConfirmControlCodeBrowserCapture\"",
			"this.callReducer(\"memberCloseControlCode\"",
		},
		"../phone/relay.go": {
			"websocketURL(\"/api/v1/stream\")",
		},
	}
	for file, snippets := range checks {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		source := string(body)
		for _, snippet := range snippets {
			if !strings.Contains(source, snippet) {
				t.Fatalf("%s missing Spacetime/video cutover snippet %q", file, snippet)
			}
		}
	}
}

func TestPhoneDisconnectDoesNotLogNormalViewerCloseCancellation(t *testing.T) {
	body, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	disconnectBody := substringBetween(t, string(body),
		"func (s *Server) handlePhoneDisconnect(err error) {",
		"func expectedPhoneDisconnect(err error) bool {")
	if !strings.Contains(disconnectBody, "err != nil && !expectedPhoneDisconnect(err)") {
		t.Fatalf("normal page-close phone disconnect cancellation must not be logged as a production oddity")
	}
	if !strings.Contains(string(body), "errors.Is(err, context.Canceled)") ||
		!strings.Contains(string(body), "websocket.CloseStatus(err) == websocket.StatusNormalClosure") {
		t.Fatalf("expected disconnect helper must treat context cancellation and normal close frames as quiet shutdown")
	}
	if !strings.Contains(disconnectBody, "s.publishRelayCurrentReportAsync(\"phone_stream_disconnected\")") ||
		!strings.Contains(disconnectBody, "releaseStreamDesiredIfNoVideoClients") {
		t.Fatalf("normal disconnect still needs to update relay state and release stream demand")
	}
}

func TestExpectedPhoneDisconnectIncludesNormalCloseFrames(t *testing.T) {
	normalClose := fmt.Errorf("failed to get reader: received close frame: %w", websocket.CloseError{
		Code:   websocket.StatusNormalClosure,
		Reason: "no viewers",
	})
	if !expectedPhoneDisconnect(normalClose) {
		t.Fatalf("normal no-viewer phone close must be treated as an expected shutdown")
	}
	internalClose := fmt.Errorf("failed to get reader: received close frame: %w", websocket.CloseError{
		Code:   websocket.StatusInternalError,
		Reason: "spacetime_command_recover_stream",
	})
	if expectedPhoneDisconnect(internalClose) {
		t.Fatalf("internal phone close must still be reported")
	}
}
