package web

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"ticketremote/internal/auth"
	"ticketremote/internal/state"
)

func TestExperimentalMediaCapabilityIsBrowserOnly(t *testing.T) {
	server := &Server{}
	snapshot := state.Snapshot{Members: []state.Member{{Email: "member@example.com", Role: state.RoleMember, Active: true}}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/experimental-media/capability", nil)
	rec := httptest.NewRecorder()
	server.handleExperimentalMediaCapability(rec, req, auth.Identity{Email: "member@example.com"}, "", snapshot)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	engines, _ := payload["allowedEngines"].([]any)
	if len(engines) != 1 || engines[0] != "client_webgpu_v2" || payload["selectedEngine"] != "client_webgpu_v2" {
		t.Fatalf("engine payload = %#v", payload)
	}
	if payload["requiresHDR"] != false || payload["clientPipeline"] != "webgpu-mainthread-edr-v2" {
		t.Fatalf("browser-only payload = %#v", payload)
	}
	if payload["targetDisplayBoost"] != float64(4) || payload["selectedDisplayBoost"] != float64(4) {
		t.Fatalf("default HDR boost payload = %#v, want 4x", payload)
	}
	if got, want := payload["allowedDisplayBoosts"], []any{float64(2), float64(3), float64(4), float64(5), float64(6)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("allowed HDR display boosts = %#v, want %#v", got, want)
	}
}

func TestExperimentalMediaCapabilityUsesEveryActiveMemberAccountBoost(t *testing.T) {
	for _, test := range []struct {
		name   string
		role   string
		active bool
		saved  uint32
		want   uint32
	}{
		{name: "member", role: state.RoleMember, active: true, saved: 2, want: 2},
		{name: "admin", role: state.RoleAdmin, active: true, saved: 5, want: 5},
		{name: "owner", role: state.RoleOwner, active: true, saved: 6, want: 6},
		{name: "inactive member", role: state.RoleMember, active: false, saved: 2, want: 4},
		{name: "retired value", role: state.RoleMember, active: true, saved: 16, want: 4},
		{name: "missing value", role: state.RoleMember, active: true, want: 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			const email = "person@example.com"
			snapshot := state.Snapshot{Members: []state.Member{{
				Email:  email,
				Role:   test.role,
				Active: test.active,
			}}}
			if test.saved != 0 {
				snapshot.MemberHDRBoosts = []state.MemberHDRBoost{{
					AccountScopeID:       ticketAccountScopeID(email),
					SelectedDisplayBoost: test.saved,
				}}
			}

			req := httptest.NewRequest(http.MethodGet, "/api/v1/experimental-media/capability", nil)
			rec := httptest.NewRecorder()
			(&Server{}).handleExperimentalMediaCapability(rec, req, auth.Identity{Email: email}, "", snapshot)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			var payload struct {
				SelectedDisplayBoost uint32 `json:"selectedDisplayBoost"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.SelectedDisplayBoost != test.want {
				t.Fatalf("selected HDR display boost = %d, want %d", payload.SelectedDisplayBoost, test.want)
			}
		})
	}
}

func TestServerHDRRuntimeAndBrowserSocketAreRemoved(t *testing.T) {
	server := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/experimental-media/stream", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("retired HDR stream status = %d, want 404", rec.Code)
	}

	repoRoot := filepath.Clean(filepath.Join("..", "..", "..", ".."))
	checks := map[string][]string{
		filepath.Join(repoRoot, "infra", "arbuzas", "docker", "compose.yml"): {
			"ticket_hdr_transformer:",
			"TICKET_REMOTE_HDR_TRANSFORMER_URL",
		},
		filepath.Join(repoRoot, "workloads", "ticket-remote", "internal", "web", "static", "app.js"): {
			"/api/v1/experimental-media/stream",
			"ticket.experimental-media.v1",
			"iso-gainmap-keyframe-v1",
		},
	}
	for path, forbiddenValues := range checks {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range forbiddenValues {
			if strings.Contains(string(body), forbidden) {
				t.Fatalf("%s retained retired server HDR contract %q", path, forbidden)
			}
		}
	}
}

func TestBuiltClientHDRUsesFullColorSurfaceContract(t *testing.T) {
	bundle, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	built := string(bundle)
	for _, required := range []string{
		"black_anchored_hue_expansion_v3",
		"sdr_identity_request_patch_v1",
		"dynamic_range_capability_available",
		"dynamic_range_capability_unavailable",
		"foreground_recovery",
		"settlement_deadline_exceeded",
		"compositor_settlement_result",
		"controlCodeCapturePriorityActive",
		"controlCodeHDRFreezeTarget",
		"exact-hdr",
		"edrRequestPatchIntended",
		"intendedRequestPatchPeak",
		"intendedRequestPatchEdge",
		"continuousSurface",
		"edr_activation_presented",
		"experimental_hdr_activation_presented",
	} {
		if !strings.Contains(built, required) {
			t.Fatalf("built browser HDR client is stale or incomplete: missing %q; run make web-client-build", required)
		}
	}
	for _, forbidden := range []string{
		"sdr_preserving_chromatic_highlight_shoulder_v2",
		"HIGHLIGHT_KNEE_LINEAR",
		"highlightKneeEncoded",
		"applyNeutralGrayContrast",
		"applyEDRActivation",
		"onActivationSurface",
		"hdr-edr-activation",
		"headroomReady",
		"experimentalMediaHeadroom",
	} {
		if strings.Contains(built, forbidden) {
			t.Fatalf("built browser HDR client retained obsolete handshake %q; run make web-client-build", forbidden)
		}
	}
}

func TestIndexBootstrapsOnlyActiveAccountHDRPreference(t *testing.T) {
	const email = "person@example.com"
	scope := ticketAccountScopeID(email)
	for _, tc := range []struct {
		name                   string
		active, known, enabled bool
	}{
		{"saved on", true, true, true}, {"saved off", true, true, false},
		{"not yet loaded", true, false, false}, {"inactive", false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := newTicketSetupTestServer(t, "pixel")
			t.Cleanup(server.Close)
			// Exercise the real index handler with an inspectable template boundary.
			server.indexTmpl = template.Must(template.New("config").Parse("<script>{{.ConfigJSON}}</script>"))
			snapshot := state.Snapshot{
				Members:              []state.Member{{Email: email, Role: state.RoleMember, Active: tc.active}},
				MemberHDRPreferences: []state.MemberHDRPreference{{AccountScopeID: "other-account", Enabled: true}},
				MemberHDRBoosts:      []state.MemberHDRBoost{{AccountScopeID: scope, SelectedDisplayBoost: 5}},
			}
			if tc.known {
				snapshot.MemberHDRPreferences = append(snapshot.MemberHDRPreferences, state.MemberHDRPreference{AccountScopeID: scope, Enabled: tc.enabled})
			}
			rec := httptest.NewRecorder()
			id := auth.Identity{Email: email}
			server.handleIndex(rec, httptest.NewRequest(http.MethodGet, "/", nil), id, "test-session", snapshot, "")
			var cfg map[string]json.RawMessage
			if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(rec.Body.String(), "<script>"), "</script>")), &cfg); err != nil {
				t.Fatal(err)
			}
			bootstrap, present := cfg["experimentalMediaBootstrap"]
			if present != tc.active {
				t.Fatalf("bootstrap presence = %v", present)
			}
			if !tc.active {
				return
			}
			var payload struct {
				AccountScopeID string          `json:"accountScopeId"`
				Enabled        bool            `json:"enabled"`
				Known          bool            `json:"preferenceKnown"`
				Capability     json.RawMessage `json:"capability"`
			}
			if err := json.Unmarshal(bootstrap, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.AccountScopeID != scope || payload.Enabled != tc.enabled || payload.Known != tc.known {
				t.Fatalf("wrong account preference: %s", bootstrap)
			}
			capability := httptest.NewRecorder()
			server.handleExperimentalMediaCapability(capability, httptest.NewRequest(http.MethodGet, "/api/v1/experimental-media/capability", nil), id, "", snapshot)
			if strings.TrimSpace(string(payload.Capability)) != strings.TrimSpace(capability.Body.String()) {
				t.Fatal("bootstrap display contract differs from capability route")
			}
			if strings.Contains(rec.Body.String(), "other-account") {
				t.Fatal("another account leaked into page config")
			}
			health := redactSnapshotForHealth(snapshot)
			if len(health.MemberHDRPreferences) != 0 {
				t.Fatal("health exposed account preferences")
			}
			if len(snapshot.MemberHDRPreferences) == 0 {
				t.Fatal("health redaction mutated the original snapshot")
			}
		})
	}
}
