package web

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"ticketremote/internal/auth"
	"ticketremote/internal/config"
	"ticketremote/internal/phone"
	"ticketremote/internal/state"
)

type streamDesiredRecordingStore struct {
	state.Store
	desired      chan<- state.StreamDesiredStateInput
	phone        chan<- state.PhoneCurrentReportInput
	relayReports chan<- state.RelayCurrentReportInput
	commands     chan<- state.StreamCommandInput
	logs         chan<- state.SafeOperationalLogInput
	desiredMu    sync.Mutex
	desiredCalls int
	desiredStart chan<- struct{}
	desiredBlock <-chan struct{}
}

func (s *streamDesiredRecordingStore) AppendStreamCommand(ctx context.Context, input state.StreamCommandInput) error {
	if err := s.Store.AppendStreamCommand(ctx, input); err != nil {
		return err
	}
	if s.commands != nil {
		select {
		case s.commands <- input:
		default:
		}
	}
	return nil
}

func (s *streamDesiredRecordingStore) UpdateRelayCurrentReport(ctx context.Context, input state.RelayCurrentReportInput) error {
	if err := s.Store.UpdateRelayCurrentReport(ctx, input); err != nil {
		return err
	}
	if s.relayReports != nil {
		select {
		case s.relayReports <- input:
		default:
		}
	}
	return nil
}

func (s *streamDesiredRecordingStore) SetStreamDesiredState(ctx context.Context, input state.StreamDesiredStateInput) error {
	s.desiredMu.Lock()
	s.desiredCalls++
	call := s.desiredCalls
	s.desiredMu.Unlock()
	if call == 1 && s.desiredBlock != nil {
		if s.desiredStart != nil {
			select {
			case s.desiredStart <- struct{}{}:
			default:
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.desiredBlock:
		}
	}
	if err := s.Store.SetStreamDesiredState(ctx, input); err != nil {
		return err
	}
	if s.desired != nil {
		select {
		case s.desired <- input:
		default:
		}
	}
	return nil
}

func (s *streamDesiredRecordingStore) UpdatePhoneCurrentReport(ctx context.Context, input state.PhoneCurrentReportInput) error {
	if err := s.Store.UpdatePhoneCurrentReport(ctx, input); err != nil {
		return err
	}
	if s.phone != nil {
		select {
		case s.phone <- input:
		default:
		}
	}
	return nil
}

func (s *streamDesiredRecordingStore) AppendSafeOperationalLog(ctx context.Context, input state.SafeOperationalLogInput) error {
	if err := s.Store.AppendSafeOperationalLog(ctx, input); err != nil {
		return err
	}
	if s.logs != nil {
		select {
		case s.logs <- input:
		default:
		}
	}
	return nil
}

func newStreamControlTestServer(t *testing.T, recorder *streamDesiredRecordingStore) *Server {
	t.Helper()
	store := NewMemoryStore()
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		DisplayName:     "ViVi timed ticket",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "pixel",
		PhoneBaseURL:    "http://127.0.0.1:1",
		PhoneAttachName: "Pixel",
	}); err != nil {
		t.Fatal(err)
	}
	if recorder == nil {
		recorder = &streamDesiredRecordingStore{}
	}
	recorder.Store = store
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID:         "pixel",
		AttachName:        "Pixel",
		BaseURL:           "http://127.0.0.1:1",
		ReconnectMinDelay: time.Hour,
		ReconnectMaxDelay: time.Hour,
		NoViewerStopDelay: time.Hour,
	})
	server, err := NewServer(config.Config{
		PublicBaseURL: "http://ticket.test",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     time.Hour,
		Access: auth.AccessConfig{
			Mode:     "dev",
			DevEmail: "ticket@jolkins.id.lv",
		},
		Phone: config.PhoneConfig{
			BackendID:  "pixel",
			AttachName: "Pixel",
			BaseURL:    "http://127.0.0.1:1",
			Backends:   []config.PhoneBackend{{ID: "pixel", AttachName: "Pixel", BaseURL: "http://127.0.0.1:1"}},
		},
	}, recorder, relay)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	return server
}

func waitForSafeLog(t *testing.T, logs <-chan state.SafeOperationalLogInput, event string) state.SafeOperationalLogInput {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case got := <-logs:
			if got.Event == event {
				return got
			}
		case <-deadline:
			t.Fatalf("timed out waiting for safe operational log %q", event)
		}
	}
}

func TestRelayViewerPublishesDesiredActiveAndTrace(t *testing.T) {
	desired := make(chan state.StreamDesiredStateInput, 4)
	logs := make(chan state.SafeOperationalLogInput, 8)
	server := newStreamControlTestServer(t, &streamDesiredRecordingStore{desired: desired, logs: logs})

	server.addRelayViewer("session-a")

	select {
	case got := <-desired:
		if !got.DesiredActive {
			t.Fatalf("desired active = false, want true: %#v", got)
		}
		if got.ViewerCount != 1 {
			t.Fatalf("viewer count = %d, want 1", got.ViewerCount)
		}
		if got.Reason != "relay_viewer_added" {
			t.Fatalf("reason = %q, want relay_viewer_added", got.Reason)
		}
		if got.UpdatedBy != "ticket_remote_relay" {
			t.Fatalf("updated by = %q, want ticket_remote_relay", got.UpdatedBy)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for active desired-state update")
	}

	got := waitForSafeLog(t, logs, "stream_opened")
	if got.Source != "ticket_remote_relay" {
		t.Fatalf("log source = %q, want ticket_remote_relay", got.Source)
	}
	if !strings.Contains(got.DetailJSON, `"viewerCount":1`) {
		t.Fatalf("viewer trace missing viewer count: %s", got.DetailJSON)
	}
}

func TestStreamDesiredWriterCannotFinishStaleIdleAfterNewActiveState(t *testing.T) {
	desired := make(chan state.StreamDesiredStateInput, 4)
	firstStarted := make(chan struct{}, 1)
	releaseFirst := make(chan struct{})
	server := newStreamControlTestServer(t, &streamDesiredRecordingStore{
		desired:      desired,
		desiredStart: firstStarted,
		desiredBlock: releaseFirst,
	})

	idleResult := make(chan bool, 1)
	go func() {
		idleResult <- server.releaseStreamDesiredIfNoVideoClients("blocked_idle_release")
	}()
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("idle desired-state write did not reach the store")
	}

	server.addRelayViewer("new-active-session")
	server.addRelayViewer("newest-active-session")
	select {
	case got := <-desired:
		t.Fatalf("new active state bypassed the blocked sole writer: %#v", got)
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseFirst)
	var first state.StreamDesiredStateInput
	select {
	case first = <-desired:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the unblocked idle write")
	}
	var second state.StreamDesiredStateInput
	select {
	case second = <-desired:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the newer active write")
	}
	if first.DesiredActive || first.ViewerCount != 0 {
		t.Fatalf("first write = %#v, want the already-started idle transition", first)
	}
	if !second.DesiredActive || second.ViewerCount != 2 {
		t.Fatalf("final write = %#v, want the newer active transition", second)
	}
	select {
	case got := <-desired:
		t.Fatalf("superseded intermediate viewer count was not coalesced: %#v", got)
	case <-time.After(50 * time.Millisecond):
	}
	select {
	case ok := <-idleResult:
		if !ok {
			t.Fatal("blocked idle write did not complete successfully")
		}
	case <-time.After(time.Second):
		t.Fatal("idle release did not finish after the store was unblocked")
	}
}

func TestRelayReportEventsAreCoalescedByOneServerReporter(t *testing.T) {
	if relayReportHeartbeat <= time.Second || relayReportHeartbeat > 5*time.Second {
		t.Fatalf("relay heartbeat = %s, want a bounded cadence above one write per second", relayReportHeartbeat)
	}
	reports := make(chan state.RelayCurrentReportInput, 8)
	server := newStreamControlTestServer(t, &streamDesiredRecordingStore{relayReports: reports})
	for i := 0; i < 20; i++ {
		server.publishRelayCurrentReportAsync("viewer_state_changed")
	}
	select {
	case <-reports:
	case <-time.After(time.Second):
		t.Fatal("coalesced relay report was not published")
	}
	select {
	case report := <-reports:
		t.Fatalf("burst produced a duplicate relay report: %#v", report)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestRelayReportPersistsConservativeTSF3SourceAgeAndSeparateReceiptTime(t *testing.T) {
	reports := make(chan state.RelayCurrentReportInput, 1)
	server := newStreamControlTestServer(t, &streamDesiredRecordingStore{relayReports: reports})
	if !server.direct.setConfig(testAllIntraConfig([]byte(`{"type":"config","streamEpoch":7,"phoneUptimeMillis":10000,"frameEnvelope":"tsf3"}`))) {
		t.Fatal("strict TSF3 config was rejected")
	}
	recordTestBoundedPhoneClock(t, server.direct, 10_000_000)
	if !server.direct.recordFrame(testTSF3Frame(7, 1, true, 10_000_000)) {
		t.Fatal("bounded TSF3 frame was rejected")
	}
	if err := server.publishRelayCurrentReport(context.Background(), time.Now(), "test_source_age"); err != nil {
		t.Fatal(err)
	}
	report := <-reports
	conservative, err := time.Parse(time.RFC3339Nano, report.LastFrameAt)
	if err != nil {
		t.Fatalf("parse durable lastFrameAt %q: %v", report.LastFrameAt, err)
	}
	var status map[string]any
	if err := json.Unmarshal([]byte(report.StatusJSON), &status); err != nil {
		t.Fatal(err)
	}
	received, err := time.Parse(time.RFC3339Nano, stringFromAny(status["lastFrameReceivedAt"]))
	if err != nil {
		t.Fatalf("parse receipt time: %v (%#v)", err, status)
	}
	estimate, err := time.Parse(time.RFC3339Nano, stringFromAny(status["lastFrameSourceEstimateAt"]))
	if err != nil {
		t.Fatalf("parse source estimate: %v (%#v)", err, status)
	}
	uncertaintyMillis, ok := status["lastFrameUncertaintyMillis"].(float64)
	if !ok || uncertaintyMillis < 0 {
		t.Fatalf("source uncertainty missing: %#v", status)
	}
	if conservative.After(estimate) || !received.After(conservative) {
		t.Fatalf("durable age is not conservative/source-based: conservative=%s estimate=%s received=%s uncertainty_ms=%v", conservative, estimate, received, uncertaintyMillis)
	}
}

func TestRelayReportIncludesBoundedPageWarmthWithoutSessionIdentity(t *testing.T) {
	reports := make(chan state.RelayCurrentReportInput, 2)
	server := newStreamControlTestServer(t, &streamDesiredRecordingStore{relayReports: reports})
	now := time.Now()
	server.mu.Lock()
	server.streamPageOpenWarmUntil = map[string]time.Time{"private-session": now.Add(streamPageOpenWarmHold)}
	server.mu.Unlock()
	for _, at := range []time.Time{now, now.Add(streamPageOpenWarmHold)} {
		if err := server.publishRelayCurrentReport(context.Background(), at, "warm_observation"); err != nil {
			t.Fatal(err)
		}
		report := <-reports
		if strings.Contains(report.StatusJSON, "private-session") {
			t.Fatal("warm report leaked session identity")
		}
		var status struct {
			PageOpenWarm struct {
				RetainedSessions int    `json:"retainedSessions"`
				ExpiresAt        string `json:"expiresAt"`
			} `json:"pageOpenWarm"`
		}
		if err := json.Unmarshal([]byte(report.StatusJSON), &status); err != nil {
			t.Fatal(err)
		}
		wantCount, wantExpiry := 1, now.Add(streamPageOpenWarmHold).UTC().Format(time.RFC3339Nano)
		if !at.Equal(now) {
			wantCount, wantExpiry = 0, ""
		}
		if status.PageOpenWarm.RetainedSessions != wantCount || status.PageOpenWarm.ExpiresAt != wantExpiry {
			t.Fatalf("warm projection = %#v, want count=%d expiry=%s", status.PageOpenWarm, wantCount, wantExpiry)
		}
	}
}

func TestCompactRelayReportPreservesBrowserContinuityContract(t *testing.T) {
	status := map[string]any{
		"streamVerdict":            "stale_recovering",
		"live":                     false,
		"continuity":               true,
		"freshnessState":           freshnessLiveOK,
		"fps":                      1,
		"sourceFps":                1,
		"keyframeIntervalFrames":   1,
		"frameEnvelope":            frameEnvelopeTSF3,
		"allIntraConfigValid":      true,
		"streamEpoch":              uint64(7),
		"lastFrameSequence":        uint64(42),
		"lastFrameVisualAgeKnown":  true,
		"lastFrameVisualAgeMillis": int64(1_700),
		"liveOKMaxAgeMillis":       durationMillis(liveOKMaxAge),
		"phoneConnected":           true,
		"phoneDesired":             true,
		"phoneStreamState":         "streaming",
		"lastFrameAgoMillis":       int64(20),
		"lastKeyFrameAgoMillis":    int64(20),
		"startupTrace":             []any{"large internal trace"},
	}

	encoded, err := json.Marshal(compactRelayCurrentReportStatus(status))
	if err != nil {
		t.Fatal(err)
	}
	var browserDetail map[string]any
	if err := json.Unmarshal(encoded, &browserDetail); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"streamVerdict":            "stale_recovering",
		"live":                     false,
		"continuity":               true,
		"freshnessState":           freshnessLiveOK,
		"fps":                      float64(1),
		"sourceFps":                float64(1),
		"keyframeIntervalFrames":   float64(1),
		"frameEnvelope":            frameEnvelopeTSF3,
		"allIntraConfigValid":      true,
		"streamEpoch":              float64(7),
		"lastFrameSequence":        float64(42),
		"lastFrameVisualAgeKnown":  true,
		"lastFrameVisualAgeMillis": float64(1_700),
		"liveOKMaxAgeMillis":       float64(durationMillis(liveOKMaxAge)),
		"phoneConnected":           true,
		"phoneDesired":             true,
		"phoneStreamState":         "streaming",
	} {
		if got := browserDetail[key]; got != want {
			t.Fatalf("browser continuity field %q = %#v, want %#v; detail=%#v", key, got, want, browserDetail)
		}
	}
	for _, omitted := range []string{"startupTrace", "lastFrameAgoMillis", "lastKeyFrameAgoMillis"} {
		if _, ok := browserDetail[omitted]; ok {
			t.Fatalf("compact relay report retained %q: %#v", omitted, browserDetail)
		}
	}
}

func TestIdleDesiredStateBlocksLateStartAndRecoveryCommands(t *testing.T) {
	desired := make(chan state.StreamDesiredStateInput, 8)
	commands := make(chan state.StreamCommandInput, 8)
	server := newStreamControlTestServer(t, &streamDesiredRecordingStore{desired: desired, commands: commands})

	server.addRelayViewer("viewer-one")
	server.removeRelayViewer("viewer-one")
	server.cancelIdleStreamDesiredRelease()
	if !server.releaseStreamDesiredIfNoVideoClients("test_idle_authority") {
		t.Fatal("expected authoritative idle state to be published")
	}

	deadline := time.After(time.Second)
	for {
		select {
		case update := <-desired:
			if !update.DesiredActive && update.ViewerCount == 0 {
				goto idlePublished
			}
		case <-deadline:
			t.Fatal("idle desired state was not observed")
		}
	}

idlePublished:
	server.appendStreamCommandAsync("start", "late_prewarm", map[string]any{"source": "test"}, streamCommandTTL)
	server.appendStreamRecoveryCommandAsync("late_viewer_recovery")
	select {
	case command := <-commands:
		t.Fatalf("late background command escaped idle guard: %#v", command)
	case <-time.After(350 * time.Millisecond):
	}
}

func TestKeyframeWhileDisconnectedWritesWarningTrace(t *testing.T) {
	logs := make(chan state.SafeOperationalLogInput, 16)
	server := newStreamControlTestServer(t, &streamDesiredRecordingStore{logs: logs})

	server.addRelayViewer("session-b")
	if err := server.requestPhoneKeyframeNow("test_keyframe"); err != nil {
		t.Fatal(err)
	}

	got := waitForSafeLog(t, logs, "keyframe_requested")
	if got.Level != "warn" {
		t.Fatalf("log level = %q, want warn", got.Level)
	}
	if !strings.Contains(got.DetailJSON, `"streamState"`) {
		t.Fatalf("keyframe warning trace missing stream state: %s", got.DetailJSON)
	}
}

func TestReleaseStreamDesiredIfNoVideoClientsWritesIdleState(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		DisplayName:     "ViVi timed ticket",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "pixel",
		PhoneBaseURL:    "http://127.0.0.1:1",
		PhoneAttachName: "Pixel",
	}); err != nil {
		t.Fatal(err)
	}
	desired := make(chan state.StreamDesiredStateInput, 2)
	phoneReports := make(chan state.PhoneCurrentReportInput, 2)
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID:         "pixel",
		AttachName:        "Pixel",
		BaseURL:           "http://127.0.0.1:1",
		ReconnectMinDelay: time.Hour,
		ReconnectMaxDelay: time.Hour,
		NoViewerStopDelay: time.Hour,
	})
	server, err := NewServer(config.Config{
		PublicBaseURL: "http://ticket.test",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     time.Hour,
		Access: auth.AccessConfig{
			Mode:     "dev",
			DevEmail: "ticket@jolkins.id.lv",
		},
		Phone: config.PhoneConfig{
			BackendID:  "pixel",
			AttachName: "Pixel",
			BaseURL:    "http://127.0.0.1:1",
			Backends:   []config.PhoneBackend{{ID: "pixel", AttachName: "Pixel", BaseURL: "http://127.0.0.1:1"}},
		},
	}, &streamDesiredRecordingStore{Store: store, desired: desired, phone: phoneReports}, relay)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)

	server.direct.addVideoClient()
	if server.releaseStreamDesiredIfNoVideoClients("test_active") {
		t.Fatal("active video client should prevent idle desired-state release")
	}
	select {
	case got := <-desired:
		t.Fatalf("unexpected desired-state release while video client active: %#v", got)
	case <-time.After(50 * time.Millisecond):
	}

	server.direct.removeVideoClient()
	if !server.releaseStreamDesiredIfNoVideoClients("test_idle") {
		t.Fatal("idle release did not publish desired-state update")
	}
	select {
	case got := <-desired:
		if got.DesiredActive {
			t.Fatalf("desired active = true, want false: %#v", got)
		}
		if got.ViewerCount != 0 {
			t.Fatalf("viewer count = %d, want 0", got.ViewerCount)
		}
		if got.Reason != "test_idle" {
			t.Fatalf("reason = %q, want test_idle", got.Reason)
		}
		if got.UpdatedBy != "ticket_remote_relay" {
			t.Fatalf("updated by = %q, want ticket_remote_relay", got.UpdatedBy)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for idle desired-state update")
	}
	select {
	case got := <-phoneReports:
		if got.StreamState != "idle" {
			t.Fatalf("phone stream state = %q, want idle", got.StreamState)
		}
		if got.DesiredActive {
			t.Fatalf("phone desired active = true, want false: %#v", got)
		}
		if !strings.Contains(got.StatusJSON, `"reason":"test_idle"`) {
			t.Fatalf("phone status json missing idle reason: %s", got.StatusJSON)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for idle phone-current report")
	}
}

func TestReleaseStreamDesiredIfRelayViewerRetainedDoesNotWriteIdleState(t *testing.T) {
	desired := make(chan state.StreamDesiredStateInput, 4)
	server := newStreamControlTestServer(t, &streamDesiredRecordingStore{desired: desired})

	server.addRelayViewer("session-a")
	select {
	case got := <-desired:
		if !got.DesiredActive || got.ViewerCount != 1 {
			t.Fatalf("viewer should publish active desired state, got %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for viewer desired-state update")
	}
	if server.releaseStreamDesiredIfNoVideoClients("test_retained") {
		t.Fatal("retained relay viewer should prevent idle desired-state release")
	}
	select {
	case got := <-desired:
		t.Fatalf("unexpected idle desired-state release while relay viewer retained: %#v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestPhoneCurrentReportIncludesStreamRecoveryStatus(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		DisplayName:     "ViVi timed ticket",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "pixel",
		PhoneBaseURL:    "http://127.0.0.1:1",
		PhoneAttachName: "Pixel",
	}); err != nil {
		t.Fatal(err)
	}
	phoneReports := make(chan state.PhoneCurrentReportInput, 1)
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID:         "pixel",
		AttachName:        "Pixel",
		BaseURL:           "http://127.0.0.1:1",
		ReconnectMinDelay: time.Hour,
		ReconnectMaxDelay: time.Hour,
		NoViewerStopDelay: time.Hour,
	})
	server, err := NewServer(config.Config{
		PublicBaseURL: "http://ticket.test",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     time.Hour,
		Access: auth.AccessConfig{
			Mode:     "dev",
			DevEmail: "ticket@jolkins.id.lv",
		},
		Phone: config.PhoneConfig{
			BackendID:  "pixel",
			AttachName: "Pixel",
			BaseURL:    "http://127.0.0.1:1",
			Backends:   []config.PhoneBackend{{ID: "pixel", AttachName: "Pixel", BaseURL: "http://127.0.0.1:1"}},
		},
	}, &streamDesiredRecordingStore{Store: store, phone: phoneReports}, relay)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)

	if !server.beginStreamAutoRecovery("stale_frame", time.Now()) {
		t.Fatal("expected recovery status to start")
	}
	if err := server.publishPhoneCurrentReport(context.Background(), time.Now(), "test_recovery_status"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-phoneReports:
		for _, needle := range []string{
			`"currentRecoveryStage":"queued"`,
			`"lastWatchdogAction":"stream_recovery"`,
			`"lastRecoveryResult":"started"`,
			`"lastRecoveryReason":"stale_frame"`,
		} {
			if !strings.Contains(got.StatusJSON, needle) {
				t.Fatalf("phone status json missing %s: %s", needle, got.StatusJSON)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for phone-current report")
	}
}
