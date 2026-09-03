package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminViviAuthControlsAreOwnerOnlyAndCarryNoCredentials(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")

	ownerRequest := httptest.NewRequest(http.MethodGet, "/admin", nil)
	ownerRequest.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	ownerResponse := httptest.NewRecorder()
	server.ServeHTTP(ownerResponse, ownerRequest)
	if ownerResponse.Code != http.StatusOK {
		t.Fatalf("owner admin status = %d", ownerResponse.Code)
	}
	ownerBody := ownerResponse.Body.String()
	for _, required := range []string{
		`id="ticketAdminViviAuth"`,
		`/static/admin-vivi-auth.js?v=`,
		`"isOwner":true`,
	} {
		if !strings.Contains(ownerBody, required) {
			t.Fatalf("owner ViVi account shell missing %q", required)
		}
	}
	for _, forbidden := range []string{
		`name="viviLoginEmail"`,
		`name="viviLoginPassword"`,
		`VIVI_LOGIN_PASSWORD`,
		`VIVI_LOGIN_EMAIL`,
	} {
		if strings.Contains(ownerBody, forbidden) {
			t.Fatalf("server-rendered owner page contains credential field or value marker %q", forbidden)
		}
	}

	adminRequest := httptest.NewRequest(http.MethodGet, "/admin", nil)
	adminRequest.Header.Set("X-Ticket-Remote-Email", "admin@example.com")
	adminResponse := httptest.NewRecorder()
	server.ServeHTTP(adminResponse, adminRequest)
	if adminResponse.Code != http.StatusOK {
		t.Fatalf("administrator admin status = %d", adminResponse.Code)
	}
	adminBody := adminResponse.Body.String()
	if strings.Contains(adminBody, `id="ticketAdminViviAuth"`) || strings.Contains(adminBody, `/static/admin-vivi-auth.js`) {
		t.Fatal("non-owner administrator received the ViVi credential controls")
	}
	if !strings.Contains(adminBody, `"isOwner":false`) {
		t.Fatal("administrator config did not preserve the owner capability boundary")
	}
}

func TestAdminViviAuthSourceUsesDirectAuthoritativeStateWithoutBrowserPersistence(t *testing.T) {
	source := ticketRemoteSourceFile(t, "web-client", "admin-vivi-auth-source.js")
	built := ticketRemoteSourceFile(t, "internal", "web", "static", "admin-vivi-auth.js")
	for _, required := range []string{
		"import { html, reactive } from '@arrow-js/core'",
		"ownerViviAuth: true",
		"client.saveViviCredentials(email, password, model.operationBaselineRevision, revision)",
		"client.clearViviCredentials(model.operationBaselineRevision, revision)",
		"client.requestViviReauthLogoutLogin(requestId, model.revision)",
		"client.requestViviReauthFullReset(requestId, model.revision)",
		"newViviOperationId(fullReset ? 'vivi-full-reset' : 'vivi-logout-login'",
		"Non-destructive ViVi account switch",
		"signs out inside the app",
		"selectViviReauthAttempt(attempts, model.operationRequestId)",
		"viviReauthAttemptsBusy(model.attempts)",
		"Deletes all local ViVi app data and its linked-device identity",
		"external device-link reset",
		"state && state.ownerViviCredentials",
		"state && state.viviCredentialState",
		"state && state.viviReauthAttempt",
		"vivi_credential_revision_stale",
		"authoritativeEmail",
		"Owner access changed",
		"createOwnerViviPrivateViewFence",
		"ownerViviPrivateViewGapDisposition",
		"ownerViviStateUpdateAllowed",
		"ownerViviStatusRequiresHardRevoke",
		"resetOwnerViviConnectionAuthority",
		"resetOwnerViviCredentialCopies",
		"resetOwnerViviSensitiveModel",
		"ownerPrivateViewFence.arm()",
		"let ownerSnapshotApplied = false",
		"onSnapshotApplied: () =>",
		"snapshotApplied: ownerSnapshotApplied",
		"snapshotReady: snapshot.ready",
		"if (!snapshot.ready)",
		"resetConnectionAuthority()",
		"disabled=\"${controlsDisabled}\"",
		"revokeOwnerCredentialAccess('The saved ViVi account is unavailable",
		"if (disposed || accessClosed) return",
		"connectedClient.disconnect(false)",
		"view.addEventListener('pagehide', cleanup, { once: true })",
		"view.addEventListener('pageshow'",
		"ownerViviPageRestoreRequiresReload",
		"view.location.reload()",
		"ticketAdminViviAuthUi = 'arrow'",
		`type="text" autocomplete="off"`,
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("owner ViVi account source missing %q", required)
		}
	}
	core := ticketRemoteSourceFile(t, "web-client", "owner-vivi-access-core.js")
	for _, required := range []string{
		"return snapshotReady === true ? 'none' : 'grace'",
		"snapshotApplied === true",
		"eventGeneration === currentGeneration && eventConnection === currentConnection",
		"status === 'owner_vivi_access_failed'",
		"resetOwnerViviCredentialCopies(model)",
		"ownerViewObserved: false",
		"confirm: ''",
	} {
		if !strings.Contains(core, required) {
			t.Fatalf("owner ViVi access policy missing %q", required)
		}
	}
	if strings.Contains(core, "if (credentials) return 'none'") {
		t.Fatal("owner ViVi access policy still treats a stale private row as authoritative readiness")
	}
	clientSource := ticketRemoteSourceFile(t, "web-client", "src", "index.ts")
	for _, required := range []string{
		"this.subscribeState(connection, generation)",
		"this.attachStateListeners(connection, generation)",
		"if (!connectionIsCurrent()) return",
		"ownerViviConnectionEventAllowed",
	} {
		if !strings.Contains(clientSource, required) {
			t.Fatalf("Spacetime client connection-generation fence missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"localStorage", "sessionStorage", "indexedDB", "type=\"password\"",
		"fetch('/api/v1/admin", "window.confirm(",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("owner ViVi account source retained forbidden persistence or transport %q", forbidden)
		}
	}
	if strings.Contains(source, "if (ownerPrivateViewUnready) {\n      revokeOwnerCredentialAccess()") {
		t.Fatal("normal credential projection propagation must not cause immediate revocation")
	}
	for _, required := range []string{
		"Generated from web-client/admin-vivi-auth-source.js",
		"saveViviCredentials", "clearViviCredentials", "requestViviReauthLogoutLogin", "requestViviReauthFullReset",
		"ticketAdminViviAuthUi", "ownerViviCredentials", "viviCredentialState",
		"Owner access changed", "Restoring the owner ViVi account connection", "pageshow", "location.reload",
	} {
		if !strings.Contains(built, required) {
			t.Fatalf("built owner ViVi account asset missing %q; run make web-client-build", required)
		}
	}
	for _, forbidden := range []string{"localStorage", "sessionStorage", "indexedDB"} {
		if strings.Contains(built, forbidden) {
			t.Fatalf("built owner ViVi account asset contains browser persistence %q", forbidden)
		}
	}

	spacetimeBuilt := ticketRemoteSourceFile(t, "internal", "web", "static", "spacetime-client.js")
	for _, required := range []string{
		`name: "ticketremote_vivi_credential_state"`,
		`name: "ticketremote_vivi_reauth_attempt"`,
		`name: "ticketremote_owner_vivi_credentials"`,
		`reducerSchema("ticketremote_owner_save_vivi_credentials"`,
		`reducerSchema("ticketremote_owner_clear_vivi_credentials"`,
		`reducerSchema("ticketremote_owner_request_vivi_reauth"`,
		`reducerSchema("ticketremote_owner_request_vivi_reauth_logout_login"`,
		`reducerSchema("ticketremote_owner_request_vivi_reauth_full_reset"`,
	} {
		if !strings.Contains(spacetimeBuilt, required) {
			t.Fatalf("built Spacetime client is missing ViVi runtime binding %q; run make web-client-build", required)
		}
	}
}
