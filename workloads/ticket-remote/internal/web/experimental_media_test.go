package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"ticketremote/internal/phone"
)

func TestExperimentalMediaBrowserBundleKeepsPreviewSeparateAndFailClosed(t *testing.T) {
	app, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	templateBody, err := staticFS.ReadFile("static/index.html.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"/api/v1/experimental-media/capability",
		"/api/v1/experimental-media/stream",
		"ticket.experimental-media.v1",
		"brightness(1.18) contrast(1.06) saturate(1.04)",
		"fallback-sdr",
		"iso-gainmap-keyframe-v1",
		"dynamic-range-limit",
		"probeExperimentalHDRFixture",
		"new ExperimentalHDRImageSwitcher",
		"experimentalMediaHDRSwitcher.present",
		"experimental_hdr_first_image_shown",
		"experimental_hdr_decode_sample",
	} {
		if !strings.Contains(string(app), expected) {
			t.Fatalf("generated browser bundle is missing %q", expected)
		}
	}
	for _, expected := range []string{
		`<canvas id="screen"`,
		`<canvas id="experimentalMediaCanvas"`,
		`aria-hidden="true" hidden`,
		`<div id="experimentalMediaMount" hidden>`,
		`<img id="experimentalMediaHDRImage"`,
		`<img id="experimentalMediaHDRImageBuffer"`,
		`dynamic-range-limit: no-limit`,
	} {
		if !strings.Contains(string(templateBody), expected) {
			t.Fatalf("viewer template is missing %q", expected)
		}
	}
}

func TestExperimentalMediaSerializesAllTransformerCalls(t *testing.T) {
	hub := newExperimentalMediaHub("", time.Second, nil, nil)
	defer hub.Close()
	var active atomic.Int32
	var maximum atomic.Int32
	hub.transform = func(_ context.Context, frame []byte, _ experimentalSourceConfig) ([]byte, error) {
		current := active.Add(1)
		for {
			previous := maximum.Load()
			if current <= previous || maximum.CompareAndSwap(previous, current) {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
		active.Add(-1)
		return frame, nil
	}
	frame := testTSF2FrameWithTimestamp(1, 1, true, 1)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := hub.apply(context.Background(), frame); err != nil {
				t.Errorf("apply: %v", err)
			}
		}()
	}
	group.Wait()
	if got := maximum.Load(); got != 1 {
		t.Fatalf("maximum concurrent transforms = %d, want 1", got)
	}
}

func TestExperimentalWarmStartCannotRegressBehindNewerLiveKeyframe(t *testing.T) {
	original := testTSF2FrameWithTimestamp(7, 20, true, 100)
	if !experimentalWarmStartStillCurrent(original, append([]byte(nil), original...)) {
		t.Fatal("exact current cached keyframe should remain eligible")
	}
	for name, latest := range map[string][]byte{
		"newer sequence": testTSF2FrameWithTimestamp(7, 21, true, 101),
		"newer epoch":    testTSF2FrameWithTimestamp(8, 1, true, 102),
		"delta":          testTSF2FrameWithTimestamp(7, 20, false, 100),
	} {
		if experimentalWarmStartStillCurrent(original, latest) {
			t.Fatalf("stale warm keyframe remained eligible after %s", name)
		}
	}
}

func TestExperimentalHDRTransformerReturnsIndependentGainMapEnvelope(t *testing.T) {
	fixture, err := staticFS.ReadFile("static/hdr-capability-fixture.jpg")
	if err != nil {
		t.Fatal(err)
	}
	requestCount := 0
	transformer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "video/h264" || r.Header.Get("X-Ticket-Width") != "540" || r.Header.Get("X-Ticket-Height") != "1212" {
			t.Errorf("transform request = %s headers=%v", r.Method, r.Header)
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("X-HDR-Format", "jpeg-iso-21496-gainmap")
		_, _ = w.Write(fixture)
	}))
	defer transformer.Close()
	hub := newExperimentalMediaHub(transformer.URL, time.Second, nil, nil)
	defer hub.Close()
	config, ok := hub.configure([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"hardware-h264-annexb","width":540,"height":1212,"streamEpoch":17}`))
	if !ok || !strings.Contains(string(config), `"experimentalPipeline":"iso-gainmap-keyframe-v1"`) || !strings.Contains(string(config), `"transport":"independent-image"`) {
		t.Fatalf("HDR config = %s", config)
	}
	keyframe := testTSF2FrameWithTimestamp(17, 41, true, 10000)
	output, err := hub.apply(context.Background(), keyframe)
	if err != nil {
		t.Fatal(err)
	}
	meta := parseTSF2(output)
	if !meta.ok || !meta.keyFrame || meta.epoch != 17 || meta.sequence != 41 {
		t.Fatalf("HDR envelope metadata = %#v", meta)
	}
	if got := output[tsf2HeaderBytes:]; len(got) != len(fixture) || got[0] != 0xff || got[1] != 0xd8 {
		t.Fatalf("HDR envelope payload bytes = %d", len(got))
	}
	if requestCount != 1 {
		t.Fatalf("transform requests = %d, want 1", requestCount)
	}
	if _, err := hub.apply(context.Background(), testTSF2FrameWithTimestamp(17, 42, false, 10001)); !errors.Is(err, errExperimentalFrameSkipped) {
		t.Fatalf("delta frame error = %v, want skip", err)
	}
	if requestCount != 1 {
		t.Fatalf("delta frame reached transformer; requests=%d", requestCount)
	}
}

func TestExperimentalHDRTransportAllowlistIsExact(t *testing.T) {
	for _, test := range []struct {
		transport string
		want      bool
	}{
		{transport: "h264-annexb", want: true},
		{transport: "hardware-h264-annexb", want: true},
		{transport: "hardware-h264-avcc", want: false},
		{transport: "hardware-h264-annexb-extra", want: false},
		{transport: "HARDWARE-H264-ANNEXB", want: false},
		{transport: " hardware-h264-annexb", want: false},
		{transport: "", want: false},
	} {
		if got := isExperimentalHDRTransport(test.transport); got != test.want {
			t.Fatalf("isExperimentalHDRTransport(%q) = %v, want %v", test.transport, got, test.want)
		}
	}
}

func TestExperimentalWarmConfigCannotRegressNewerSharedSource(t *testing.T) {
	hub := newExperimentalMediaHub("http://transformer.invalid", time.Second, nil, nil)
	defer hub.Close()
	hub.transform = func(_ context.Context, frame []byte, source experimentalSourceConfig) ([]byte, error) {
		if source.epoch != 8 || source.transport != "hardware-h264-annexb" {
			return nil, fmt.Errorf("shared source regressed: %#v", source)
		}
		return append([]byte(nil), frame...), nil
	}
	newer := []byte(`{"type":"config","codec":"avc1.42C028","transport":"hardware-h264-annexb","width":720,"height":1482,"streamEpoch":8}`)
	older := []byte(`{"type":"config","codec":"avc1.42C028","transport":"hardware-h264-annexb","width":720,"height":1482,"streamEpoch":7}`)
	if _, ok := hub.configure(newer); !ok {
		t.Fatal("configure newer source failed")
	}
	if rendered, ok := hub.clientConfig(older); !ok || !strings.Contains(string(rendered), `"streamEpoch":7`) {
		t.Fatalf("render stale client config = %s, ok=%v", rendered, ok)
	}
	if _, err := hub.apply(context.Background(), testTSF2FrameWithTimestamp(8, 1, true, 1000)); err != nil {
		t.Fatal(err)
	}
}

func TestExperimentalHDRRelayRejectsOrdinaryJPEGDespiteFormatHeader(t *testing.T) {
	ordinaryJPEG := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x02, 0xff, 0xd9}
	transformer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("X-HDR-Format", "jpeg-iso-21496-gainmap")
		_, _ = w.Write(ordinaryJPEG)
	}))
	defer transformer.Close()
	hub := newExperimentalMediaHub(transformer.URL, time.Second, nil, nil)
	defer hub.Close()
	if _, ok := hub.configure([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"streamEpoch":17}`)); !ok {
		t.Fatal("configure failed")
	}
	if _, err := hub.apply(context.Background(), testTSF2FrameWithTimestamp(17, 41, true, 10000)); err == nil || !strings.Contains(err.Error(), "invalid image") {
		t.Fatalf("ordinary JPEG error = %v, want invalid image", err)
	}
}

func TestExperimentalHDRBrowserChecksEligibilityBeforeMountingToggle(t *testing.T) {
	source := ticketAppSource(t)
	probe := substringBetween(t, source,
		"async function probeExperimentalHDRFixture(url) {",
		"  async function discoverExperimentalMediaCapability() {")
	for _, required := range []string{
		"matchMedia('(dynamic-range: high)').matches",
		"CSS.supports('dynamic-range-limit', 'no-limit')",
		"await probe.decode()",
		"URL.revokeObjectURL(objectURL)",
	} {
		if !strings.Contains(probe, required) {
			t.Fatalf("HDR eligibility probe is missing %q", required)
		}
	}
	discovery := substringBetween(t, source,
		"async function discoverExperimentalMediaCapability() {",
		"  if (cfg.experimentalMediaCandidate === true)")
	if strings.Index(discovery, "await probeExperimentalHDRFixture") > strings.Index(discovery, "mountExperimentalMediaControl()") {
		t.Fatal("HDR toggle mounted before fixture eligibility completed")
	}
}

func TestExperimentalHDRBrowserUsesDurableAccountPreferenceWithoutBrowserStorage(t *testing.T) {
	source := ticketAppSource(t)
	client := ticketRemoteSourceFile(t, "web-client", "src", "index.ts")
	preference := ticketRemoteSourceFile(t, "web-client", "experimental-hdr-preference.mjs")
	for _, required := range []string{
		"new ExperimentalHDRPreferenceController({",
		"experimentalMediaPreferenceController.observe(state && state.memberHDR ? state.memberHDR.enabled : null)",
		"experimentalMediaPreferenceController.choose(Boolean(toggle.checked))",
		"client.setHDRPreference(Boolean(enabled))",
		"hdr_preference_write_failed",
		"clientLog('state_failed', 'hdr_preference_refresh_failed')",
		"HDR izvēle darbojas šajā sesijā, bet kontā netika saglabāta.",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("HDR account preference integration is missing %q", required)
		}
	}
	for _, required := range []string{
		"setHDRPreference(enabled: boolean)",
		"refreshHDRState(): Promise<void>",
		"validAccountScopeId(this.cfg.accountScopeId)",
		"ticketremote_member_hdr_state WHERE ticketId = ${ticket} AND accountScopeId = ${accountScopeId}",
		"memberHDR: memberHDRState ?",
	} {
		if !strings.Contains(client, required) {
			t.Fatalf("direct client HDR account isolation is missing %q", required)
		}
	}
	for _, forbidden := range []string{"localStorage", "sessionStorage", "indexedDB", "document.cookie"} {
		if strings.Contains(preference, forbidden) {
			t.Fatalf("HDR preference controller must not persist through browser storage %q", forbidden)
		}
	}
}

func TestExperimentalHDRAccountScopeDoesNotReuseShortDisplayIDs(t *testing.T) {
	const expected = "801e6852dd9ec833c6627a23f54faa19d507556ff3de6378756503a1b6bb627b"
	if got := ticketAccountScopeID(" Scope-Vector@Example.Invalid "); got != expected {
		t.Fatalf("normalized account scope = %q, want stable SHA-256 vector", got)
	}
	// This synthetic pair collides under the existing four-character viewer
	// display ID and therefore guards against accidentally reusing that label
	// as durable account authority.
	first := ticketAccountScopeID("synthetic-review-247@example.invalid")
	second := ticketAccountScopeID("synthetic-review-259@example.invalid")
	if first == second || len(first) != 64 || len(second) != 64 {
		t.Fatalf("durable account scopes are not isolated: first=%q second=%q", first, second)
	}
}

func TestExperimentalMediaAsyncConfigureCannotResurrectAfterFallback(t *testing.T) {
	source := ticketAppSource(t)
	body := substringBetween(t, source,
		"async function configureExperimentalMedia(config) {",
		"  async function renderExperimentalHDRFrame(frame) {")
	for _, expected := range []string{
		"const configureGeneration = ++experimentalMediaConfigureGeneration;",
		"experimentalMediaHDRRenderGeneration += 1;",
		"hideExperimentalMediaCanvas();",
		"const support = await VideoDecoder.isConfigSupported(decoderConfig);",
		"if (configureGeneration !== experimentalMediaConfigureGeneration || !experimentalMediaState.enabled) return;",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("experimental async configure guard is missing %q", expected)
		}
	}
	if strings.Index(body, "configureGeneration !== experimentalMediaConfigureGeneration") < strings.Index(body, "await VideoDecoder.isConfigSupported") {
		t.Fatal("experimental async configure guard must run after capability detection resolves")
	}
	if !strings.Contains(source, "if (experimentalMediaSocket === socket && experimentalMediaState.enabled)") {
		t.Fatal("stale experimental socket failures must not close a newer preview")
	}
	if strings.Count(source, "experimentalMediaSocket === socket && experimentalMediaState.enabled") < 3 {
		t.Fatal("experimental open, message failure, and socket error handlers must all ignore stale sockets")
	}
}

func TestExperimentalHDRBrowserRetainsProvenFrameUntilReplacementIsReady(t *testing.T) {
	source := ticketAppSource(t)
	body := substringBetween(t, source,
		"async function renderExperimentalHDRFrame(frame) {",
		"  async function decodeExperimentalMediaFrame(raw) {")
	for _, expected := range []string{
		"experimentalMediaHDRSwitcher.present(",
		"isCurrent: () => generation === experimentalMediaHDRRenderGeneration",
		"if (result.status === 'stale') return;",
		"if (result.status === 'failed')",
		"advanceExperimentalHDRReplacementFailure(",
		"experimentalMediaHDRSwitcher.hasActive()",
		"experimentalMediaHDRMaxConsecutiveReplacementFailures",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("HDR replacement handoff is missing %q", expected)
		}
	}
}

func TestExperimentalHDRBrowserFallbackBranchesClearBothSurfaces(t *testing.T) {
	source := ticketAppSource(t)
	hide := substringBetween(t, source,
		"function hideExperimentalMediaCanvas() {",
		"  function closeExperimentalMedia(options) {")
	if !strings.Contains(hide, "experimentalMediaHDRSwitcher.clear()") {
		t.Fatal("SDR fallback does not clear both HDR surfaces")
	}
	closeBody := substringBetween(t, source,
		"function closeExperimentalMedia(options) {",
		"  function failExperimentalMedia(reason) {")
	if !strings.Contains(closeBody, "hideExperimentalMediaCanvas();") {
		t.Fatal("socket fallback close does not reach the tested HDR clear path")
	}
	decodeBody := substringBetween(t, source,
		"async function decodeExperimentalMediaFrame(raw) {",
		"  function connectExperimentalMedia() {")
	epochMismatch := substringBetween(t, decodeBody,
		"if (experimentalMediaEpoch && frame.epoch !== experimentalMediaEpoch) {",
		"    if (frame.sequence <= experimentalMediaSequence) return;")
	if !strings.Contains(epochMismatch, "hideExperimentalMediaCanvas();") {
		t.Fatal("stale HDR epoch does not reach the tested HDR clear path")
	}
	connectBody := substringBetween(t, source,
		"function connectExperimentalMedia() {",
		"  function mountExperimentalMediaControl() {")
	if !strings.Contains(connectBody, "socket.onclose") || !strings.Contains(connectBody, "failExperimentalMedia('experimental socket closed')") {
		t.Fatal("experimental socket close does not enter terminal SDR fallback")
	}
}

func TestExperimentalMediaCapabilityAllowsEveryActiveMemberAndRejectsOutsiders(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")
	defer server.Close()

	for _, test := range []struct {
		email string
		want  int
	}{
		{email: "ticket@jolkins.id.lv", want: http.StatusOK},
		{email: "admin@example.com", want: http.StatusOK},
		{email: "member@example.com", want: http.StatusOK},
		{email: "outsider@example.com", want: http.StatusForbidden},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/experimental-media/capability", nil)
		req.Header.Set("X-Ticket-Remote-Email", test.email)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != test.want {
			t.Fatalf("capability status for %s = %d, want %d; body=%s", test.email, rec.Code, test.want, rec.Body.String())
		}
	}
}

func TestExperimentalMediaCapabilityProbeIsSuggestedToEveryActiveMemberPage(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")
	defer server.Close()
	for _, test := range []struct {
		email string
		want  string
	}{
		{email: "ticket@jolkins.id.lv", want: `"experimentalMediaCandidate":true`},
		{email: "admin@example.com", want: `"experimentalMediaCandidate":true`},
		{email: "member@example.com", want: `"experimentalMediaCandidate":true`},
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Ticket-Remote-Email", test.email)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), test.want) {
			t.Fatalf("viewer config for %s status=%d missing %q", test.email, rec.Code, test.want)
		}
	}
}

func TestExperimentalMediaSocketRunsBesideAuthoritativeStreamWithoutOwningPhone(t *testing.T) {
	server, ticketServer, relay := newStreamSharingTestServer(t)
	defer server.Close()
	defer ticketServer.Close()
	defer relay.Close()
	server.handlePhoneText([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"streamEpoch":7,"phoneUptimeMillis":10000}`))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	experimental := dialExperimentalMediaTestClient(t, ctx, ticketServer.URL, "owner-experimental")
	defer experimental.Close(websocket.StatusNormalClosure, "test complete")
	config := readNextTextMessageOfType(t, ctx, experimental, "config")
	if config["experimentalPipeline"] != experimentalMediaPipelineVersion || config["experimentalVisualMode"] != experimentalMediaVisualMode {
		t.Fatalf("experimental config = %#v", config)
	}
	if got := server.direct.activeVideoClientCount(); got != 0 {
		t.Fatalf("experimental socket counted as authoritative video client: %d", got)
	}
	server.mu.Lock()
	viewerRefs := len(server.relayViewerRefs)
	server.mu.Unlock()
	if viewerRefs != 0 {
		t.Fatalf("experimental socket retained phone relay ownership: %d", viewerRefs)
	}

	normal := dialStreamTestClient(t, ctx, ticketServer.URL, "owner-normal")
	defer normal.Close(websocket.StatusNormalClosure, "test complete")
	_ = readNextTextMessageOfType(t, ctx, normal, "config")

	keyFrame := testTSF2FrameWithTimestamp(7, 1, true, 10000)
	server.handlePhoneMessage(phone.Message{Binary: keyFrame})
	if got := readNextBinaryFrame(t, ctx, normal); parseTSF2(got).sequence != 1 {
		t.Fatalf("normal stream frame = %#v", parseTSF2(got))
	}
	if got := readNextBinaryFrame(t, ctx, experimental); parseTSF2(got).sequence != 1 {
		t.Fatalf("experimental stream frame = %#v", parseTSF2(got))
	}

	_ = experimental.Close(websocket.StatusNormalClosure, "preview off")
	deltaFrame := testTSF2FrameWithTimestamp(7, 2, false, 10001)
	server.handlePhoneMessage(phone.Message{Binary: deltaFrame})
	if got := readNextTSF2Sequence(t, ctx, normal, 2); parseTSF2(got).keyFrame {
		t.Fatal("normal stream stopped after experimental socket closed")
	}
}

func TestExperimentalTransformFailureClosesOnlyExperimentalSocket(t *testing.T) {
	server, ticketServer, relay := newStreamSharingTestServer(t)
	defer server.Close()
	defer ticketServer.Close()
	defer relay.Close()
	server.handlePhoneText([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"streamEpoch":9,"phoneUptimeMillis":10000}`))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	normal := dialStreamTestClient(t, ctx, ticketServer.URL, "owner-normal-failure")
	defer normal.Close(websocket.StatusNormalClosure, "test complete")
	_ = readNextTextMessageOfType(t, ctx, normal, "config")
	experimental := dialExperimentalMediaTestClient(t, ctx, ticketServer.URL, "owner-experimental-failure")
	_ = readNextTextMessageOfType(t, ctx, experimental, "config")

	server.experimental.mu.Lock()
	server.experimental.transform = func(context.Context, []byte, experimentalSourceConfig) ([]byte, error) {
		return nil, errors.New("test transform failure")
	}
	server.experimental.mu.Unlock()
	server.handlePhoneMessage(phone.Message{Binary: testTSF2FrameWithTimestamp(9, 1, true, 10000)})
	if got := readNextBinaryFrame(t, ctx, normal); parseTSF2(got).sequence != 1 {
		t.Fatalf("normal stream did not receive frame during experimental failure: %#v", parseTSF2(got))
	}
	if _, _, err := experimental.Read(ctx); err == nil {
		t.Fatal("experimental socket remained open after transform failure")
	}
}

func TestExperimentalMediaSocketAllowsEveryActiveMemberAndRejectsOutsiders(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")
	defer server.Close()
	ticketServer := httptest.NewServer(server)
	defer ticketServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	for _, email := range []string{"ticket@jolkins.id.lv", "admin@example.com", "member@example.com"} {
		conn := dialExperimentalMediaTestClientForEmail(t, ctx, ticketServer.URL, "role-"+strings.Split(email, "@")[0], email)
		if err := conn.Close(websocket.StatusNormalClosure, "role test complete"); err != nil {
			t.Fatalf("close %s experimental socket: %v", email, err)
		}
	}

	for _, email := range []string{"outsider@example.com"} {
		header := http.Header{"X-Ticket-Remote-Email": []string{email}}
		_, response, err := websocket.Dial(ctx, "ws"+ticketServer.URL[len("http"):]+"/api/v1/experimental-media/stream", &websocket.DialOptions{
			HTTPHeader:   header,
			Subprotocols: []string{"ticket.experimental-media.v1"},
		})
		if err == nil {
			t.Fatalf("%s unexpectedly upgraded experimental media socket", email)
		}
		if response == nil || response.StatusCode != http.StatusForbidden {
			t.Fatalf("%s response = %#v, err=%v", email, response, err)
		}
	}
}

func TestExperimentalMediaKeepsBoundedClientAdmissionAcrossAccounts(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")
	defer server.Close()
	first := &client{sessionID: "hdr-owner", email: "ticket@jolkins.id.lv"}
	second := &client{sessionID: "hdr-member", email: "member@example.com"}
	third := &client{sessionID: "hdr-admin", email: "admin@example.com"}
	duplicate := &client{sessionID: first.sessionID, email: second.email}

	if !server.tryAddExperimentalClient(first) || !server.tryAddExperimentalClient(second) {
		t.Fatal("two authenticated account sessions should fit the bounded HDR client ceiling")
	}
	if server.tryAddExperimentalClient(third) {
		t.Fatal("a third HDR client bypassed the fixed resource ceiling")
	}
	if server.tryAddExperimentalClient(duplicate) {
		t.Fatal("a duplicate browser session bypassed the per-session HDR guard")
	}
	server.removeExperimentalClient(first)
	if !server.tryAddExperimentalClient(third) {
		t.Fatal("a released HDR slot was not immediately reusable")
	}
	server.removeExperimentalClient(second)
	server.removeExperimentalClient(third)
}

func dialExperimentalMediaTestClient(t *testing.T, ctx context.Context, serverURL string, sessionID string) *websocket.Conn {
	t.Helper()
	return dialExperimentalMediaTestClientForEmail(t, ctx, serverURL, sessionID, "ticket@jolkins.id.lv")
}

func dialExperimentalMediaTestClientForEmail(t *testing.T, ctx context.Context, serverURL string, sessionID string, email string) *websocket.Conn {
	t.Helper()
	header := http.Header{"X-Ticket-Remote-Email": []string{email}}
	header.Add("Cookie", "ticket_remote_session="+sessionID)
	conn, _, err := websocket.Dial(ctx, "ws"+serverURL[len("http"):]+"/api/v1/experimental-media/stream", &websocket.DialOptions{
		HTTPHeader:   header,
		Subprotocols: []string{"ticket.experimental-media.v1"},
	})
	if err != nil {
		t.Fatalf("dial experimental media websocket: %v", err)
	}
	if conn.Subprotocol() != "ticket.experimental-media.v1" {
		t.Fatalf("experimental subprotocol = %q", conn.Subprotocol())
	}
	return conn
}
