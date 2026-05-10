# Ticket Remote Spacetime Security Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the listed ticket-remote security findings and make the browser-facing SpacetimeDB path enforce identity, ownership, timing, and visibility rules from authenticated server state.

**Architecture:** Keep SpacetimeDB as the direct member state and reducer boundary, but remove trust in browser-supplied identity, session ids, and clocks. The Go service remains the public HTTPS, stream, auth-cookie, admin, and deployment-readiness boundary; member-facing state is redacted before it reaches browsers. Arbuzas deployment must fail closed when production auth, SpacetimeDB state, HTTPS, or safety headers are missing.

**Tech Stack:** Go `net/http`, SpacetimeDB TypeScript module, SpacetimeDB TypeScript generated browser client, esbuild, Docker Compose, Cloudflare Tunnel, Arbuzas deploy script.

---

## Done Criteria

- `http://ticket.jolkins.id.lv/` redirects to `https://ticket.jolkins.id.lv/`.
- HTTPS responses include HSTS, CSP, frame, content-type, referrer, and permissions safety headers.
- Direct SpacetimeDB member reducers do not accept `now`, `email`, or `sessionId` for member-owned actions; they use `ctx.sender`, verified JWT email, `ctx.connectionId`, and `ctx.timestamp`.
- Presence updates cannot affect another connection's presence row.
- Control claim, release, revoke, expiry, and audit timing are based on SpacetimeDB server time for member reducers.
- Public/member state no longer exposes full members, viewer session ids, phone backend URLs, raw phone health, or other admin-only details.
- Browser sign-in material is not persisted in `localStorage` or `sessionStorage`.
- Stream recovery and keyframe requests have server-side rate limits.
- The `ticket_remote` container no longer mounts broad `/etc/arbuzas/secrets` or Pixel ADB private key material.
- Deploy validation fails if ticket state is memory/auto fallback or if auth mode is `dev`, `development`, or `none`.
- Local tests, SpacetimeDB build, generated web client build, deploy validation, and live public checks pass.

## File Structure

- Modify `workloads/ticket-remote/spacetimedb/src/index.ts`: direct member authorization, server-time use, connection-owned presence, safe public live state.
- Modify `workloads/ticket-remote/web-client/src/index.ts`: generated-client reducer calls after removing member `now` and `sessionId` args.
- Modify `workloads/ticket-remote/internal/web/static/app.js`: remove browser storage for sign-in material, render redacted state, keep direct SpacetimeDB token in memory only.
- Modify `workloads/ticket-remote/internal/web/server.go`: HTTPS redirect, safety headers, CSP nonce, redacted member responses, server-side stream recovery throttling.
- Modify `workloads/ticket-remote/internal/state/types.go`: add redacted member/admin-safe view types or redaction methods.
- Modify `workloads/ticket-remote/internal/state/store.go`: avoid production auto fallback by requiring explicit memory only for local development.
- Modify `workloads/ticket-remote/internal/config/config.go`: load production guard settings and reject unsafe production auth/state combinations.
- Modify `workloads/ticket-remote/internal/config/config_test.go`: production guard tests.
- Modify `workloads/ticket-remote/internal/web/server_setup_test.go`: header, storage, state-redaction, and SpacetimeDB source-policy tests.
- Modify `workloads/ticket-remote/internal/web/server_stream_test.go`: stream keyframe/recovery throttling tests.
- Modify `workloads/ticket-remote/internal/web/server_state_test.go`: member/admin redaction tests.
- Modify `workloads/ticket-remote/Makefile`: keep local `make run` explicit about memory state and dev auth.
- Modify `workloads/ticket-remote/README.md`: update production requirements and validation checklist.
- Modify `workloads/ticket-remote/module.yaml`: make health checks strict enough for production readiness.
- Modify `infra/arbuzas/docker/compose.yml`: remove broad secrets/ADB mounts from `ticket_remote`; set explicit production state/auth defaults.
- Modify `infra/arbuzas/docker/images/ticket-remote.Dockerfile`: remove ADB if simulator setup is moved out of the public web container; otherwise keep ADB but no private key mounts.
- Modify `tools/arbuzas/deploy.sh`: production validation for state backend, auth mode, HTTP redirect, headers, static asset strings, and public served version.

---

### Task 1: Add Regression Tests for HTTPS Redirect and Browser Safety Headers

**Files:**
- Modify: `workloads/ticket-remote/internal/web/server_setup_test.go`
- Modify: `workloads/ticket-remote/internal/web/server.go`

- [ ] **Step 1: Write failing tests for public HTTP redirect and security headers**

Add these tests to `workloads/ticket-remote/internal/web/server_setup_test.go` near the existing server shell tests:

```go
func TestPublicHTTPRedirectsToHTTPS(t *testing.T) {
	store := state.NewMemoryStore()
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID: "vivi-default", AdminEmail: "ticket@jolkins.id.lv",
		PhoneBackendID: "pixel", PhoneBaseURL: "http://pixel.test", PhoneAttachName: "Pixel",
	}); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(config.Config{
		PublicBaseURL: "https://ticket.jolkins.id.lv",
		TicketID: "vivi-default",
		CookieName: "ticket_remote_session",
		CookieTTL: time.Hour,
		Access: auth.AccessConfig{Mode: "dev", DevEmail: "ticket@jolkins.id.lv"},
		Phone: config.PhoneConfig{BackendID: "pixel", AttachName: "Pixel", BaseURL: "http://pixel.test", DefaultBackendID: "pixel"},
	}, store, phone.NewRelay(phone.RelayConfig{BackendID: "pixel", AttachName: "Pixel", BaseURL: "http://pixel.test"}))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://ticket.jolkins.id.lv/", nil)
	req.Header.Set("X-Forwarded-Proto", "http")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusPermanentRedirect {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "https://ticket.jolkins.id.lv/" {
		t.Fatalf("Location = %q", got)
	}
}

func TestHTTPSResponsesIncludeSafetyHeaders(t *testing.T) {
	store := state.NewMemoryStore()
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID: "vivi-default", AdminEmail: "ticket@jolkins.id.lv",
		PhoneBackendID: "pixel", PhoneBaseURL: "http://pixel.test", PhoneAttachName: "Pixel",
	}); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(config.Config{
		PublicBaseURL: "https://ticket.jolkins.id.lv",
		TicketID: "vivi-default",
		CookieName: "ticket_remote_session",
		CookieTTL: time.Hour,
		Access: auth.AccessConfig{Mode: "dev", DevEmail: "ticket@jolkins.id.lv"},
		Phone: config.PhoneConfig{BackendID: "pixel", AttachName: "Pixel", BaseURL: "http://pixel.test", DefaultBackendID: "pixel"},
	}, store, phone.NewRelay(phone.RelayConfig{BackendID: "pixel", AttachName: "Pixel", BaseURL: "http://pixel.test"}))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "https://ticket.jolkins.id.lv/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	required := map[string]string{
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains; preload",
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options": "DENY",
		"Referrer-Policy": "no-referrer",
		"Permissions-Policy": "camera=(), microphone=(), geolocation=(), payment=(), usb=(), serial=()",
	}
	for header, want := range required {
		if got := rec.Header().Get(header); got != want {
			t.Fatalf("%s = %q want %q", header, got, want)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, snippet := range []string{"default-src 'self'", "object-src 'none'", "base-uri 'none'", "frame-ancestors 'none'", "connect-src 'self' https: wss:"} {
		if !strings.Contains(csp, snippet) {
			t.Fatalf("CSP missing %q: %s", snippet, csp)
		}
	}
	if !strings.Contains(rec.Body.String(), `nonce="`) {
		t.Fatalf("expected rendered scripts to carry CSP nonce")
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
cd workloads/ticket-remote
go test ./internal/web -run 'TestPublicHTTPRedirectsToHTTPS|TestHTTPSResponsesIncludeSafetyHeaders' -count=1
```

Expected: both tests fail because redirect, HSTS, CSP, and nonce support are not implemented yet.

- [ ] **Step 3: Implement redirect and safety headers**

In `workloads/ticket-remote/internal/web/server.go`, add helpers:

```go
func (s *Server) redirectHTTPToHTTPS(w http.ResponseWriter, r *http.Request) bool {
	publicURL, err := url.Parse(s.cfg.PublicBaseURL)
	if err != nil || publicURL.Scheme != "https" || publicURL.Host == "" {
		return false
	}
	if isLocalHost(r.Host) {
		return false
	}
	proto := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")))
	if proto == "" {
		proto = strings.ToLower(strings.TrimSpace(r.URL.Scheme))
	}
	if proto == "https" {
		return false
	}
	target := *r.URL
	target.Scheme = "https"
	target.Host = publicURL.Host
	http.Redirect(w, r, target.String(), http.StatusPermanentRedirect)
	return true
}

func isLocalHost(hostport string) bool {
	host := hostport
	if parsed, _, err := net.SplitHostPort(hostport); err == nil {
		host = parsed
	}
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}
```

Call `redirectHTTPToHTTPS` at the start of `ServeHTTP` before route dispatch. Add `writeSecurityHeaders(w, nonce)` and call it from `writeNoStoreHeaders`, `writeJSON`, and HTML render paths. Generate a per-response nonce for HTML pages and render script tags as:

```html
<script nonce="{{.Nonce}}">
  window.TICKET_REMOTE_CONFIG = {{.ConfigJSON}};
</script>
<script nonce="{{.Nonce}}" defer src="/static/spacetime-client.js?v={{.AssetVersion}}"></script>
<script nonce="{{.Nonce}}" defer src="/static/app.js?v={{.AssetVersion}}"></script>
```

- [ ] **Step 4: Run tests and verify they pass**

Run:

```bash
cd workloads/ticket-remote
go test ./internal/web -run 'TestPublicHTTPRedirectsToHTTPS|TestHTTPSResponsesIncludeSafetyHeaders' -count=1
```

Expected: PASS.

---

### Task 2: Fail Closed for Production State and Auth

**Files:**
- Modify: `workloads/ticket-remote/internal/state/store.go`
- Modify: `workloads/ticket-remote/internal/config/config.go`
- Modify: `workloads/ticket-remote/internal/config/config_test.go`
- Modify: `workloads/ticket-remote/Makefile`
- Modify: `infra/arbuzas/docker/compose.yml`
- Modify: `tools/arbuzas/deploy.sh`
- Modify: `workloads/ticket-remote/module.yaml`

- [ ] **Step 1: Write failing tests for production state/auth requirements**

Add to `workloads/ticket-remote/internal/config/config_test.go`:

```go
func TestProductionModeRejectsDevAuth(t *testing.T) {
	t.Setenv("TICKET_REMOTE_PRODUCTION", "true")
	t.Setenv("TICKET_REMOTE_AUTH_MODE", "dev")
	t.Setenv("TICKET_REMOTE_STATE_BACKEND", "spacetime")
	t.Setenv("TICKET_REMOTE_SPACETIME_DATABASE", "ticket_remote")
	t.Setenv("TICKET_REMOTE_SPACETIME_BEARER_TOKEN", "test-token")

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "production auth mode") {
		t.Fatalf("expected production auth rejection, got %v", err)
	}
}

func TestProductionModeRequiresSpacetimeState(t *testing.T) {
	t.Setenv("TICKET_REMOTE_PRODUCTION", "true")
	t.Setenv("TICKET_REMOTE_AUTH_MODE", "spacetime")
	t.Setenv("TICKET_REMOTE_SPACETIME_AUTH_CLIENT_ID", "client_test")
	t.Setenv("TICKET_REMOTE_SESSION_SIGNING_KEY", "test-signing-key")
	t.Setenv("TICKET_REMOTE_STATE_BACKEND", "memory")

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "production state backend") {
		t.Fatalf("expected production state rejection, got %v", err)
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
cd workloads/ticket-remote
go test ./internal/config -run 'TestProductionModeRejectsDevAuth|TestProductionModeRequiresSpacetimeState' -count=1
```

Expected: FAIL because production guard settings do not exist yet.

- [ ] **Step 3: Implement production guard**

In `config.Config`, add:

```go
Production bool
```

Load it from `TICKET_REMOTE_PRODUCTION`. Reject production if auth mode is `dev`, `development`, or `none`. Reject production if `TICKET_REMOTE_STATE_BACKEND` is not `spacetime` or `spacetimedb`. Also require `TICKET_REMOTE_SPACETIME_DATABASE` and either `TICKET_REMOTE_SPACETIME_BEARER_TOKEN` or `TICKET_REMOTE_SPACETIME_JWT_PRIVATE_KEY_FILE`.

Keep local development explicit by changing `workloads/ticket-remote/Makefile`:

```make
run:
	TICKET_REMOTE_AUTH_MODE=dev TICKET_REMOTE_STATE_BACKEND=memory go run ./cmd/ticket-remote
```

- [ ] **Step 4: Harden Arbuzas Compose defaults**

In `infra/arbuzas/docker/compose.yml`, set:

```yaml
TICKET_REMOTE_PRODUCTION: "true"
TICKET_REMOTE_STATE_BACKEND: spacetime
```

Inside the `ticket_remote` command block, export:

```sh
export TICKET_REMOTE_PRODUCTION="$${TICKET_REMOTE_PRODUCTION:-true}"
export TICKET_REMOTE_STATE_BACKEND="$${TICKET_REMOTE_STATE_BACKEND:-spacetime}"
```

- [ ] **Step 5: Harden deploy validation**

In `tools/arbuzas/deploy.sh`, change `auth_configured_ok` so `dev`, `development`, and `none` return failure. Add a `ticket_state_backend_ok` validation that checks `/etc/arbuzas/env/ticket-remote.env` and the running container environment both resolve to `spacetime`, then checks a strict internal readiness response contains `"stateBackend":"spacetime"` and `"stateBackendFresh":true`.

Add public checks:

```sh
curl -fsS "https://${ARBUZAS_TICKET_REMOTE_HOSTNAME}/api/v1/livez" >/dev/null
curl -sS -o /dev/null -w '%{http_code} %{redirect_url}' "http://${ARBUZAS_TICKET_REMOTE_HOSTNAME}/"
curl -fsSI "https://${ARBUZAS_TICKET_REMOTE_HOSTNAME}/" | grep -Fi 'strict-transport-security:'
curl -fsSI "https://${ARBUZAS_TICKET_REMOTE_HOSTNAME}/" | grep -Fi 'content-security-policy:'
```

Expected validation: HTTP status is `301` or `308`, redirect URL starts with `https://ticket.jolkins.id.lv/`, and HTTPS headers contain HSTS and CSP.

- [ ] **Step 6: Run tests and validation shell syntax checks**

Run:

```bash
cd workloads/ticket-remote
go test ./internal/config -count=1
cd ../..
bash -n tools/arbuzas/deploy.sh
```

Expected: PASS.

---

### Task 3: Remove Browser-Supplied Time and Session Trust from Direct SpacetimeDB Reducers

**Files:**
- Modify: `workloads/ticket-remote/spacetimedb/src/index.ts`
- Modify: `workloads/ticket-remote/web-client/src/index.ts`
- Modify: `workloads/ticket-remote/internal/web/server_setup_test.go`

- [ ] **Step 1: Write failing source-policy tests**

Add to `workloads/ticket-remote/internal/web/server_setup_test.go`:

```go
func TestTicketSpacetimeMemberReducersUseServerClockAndConnectionID(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "spacetimedb", "src", "index.ts"))
	if err != nil {
		t.Fatal(err)
	}
	module := string(body)
	for _, reducer := range []string{
		"member_heartbeat_presence",
		"member_disconnect_presence",
		"member_claim_control",
		"member_release_control",
		"member_revoke_control",
		"member_upsert_member",
		"member_remove_member",
	} {
		start := strings.Index(module, `name: named('`+reducer+`')`)
		if start < 0 {
			t.Fatalf("missing reducer %s", reducer)
		}
		chunk := module[start:min(len(module), start+700)]
		if strings.Contains(chunk, "now: t.string()") {
			t.Fatalf("%s must not accept browser-supplied now", reducer)
		}
		if strings.Contains(chunk, "sessionId: t.string()") {
			t.Fatalf("%s must not accept browser-supplied sessionId", reducer)
		}
	}
	for _, snippet := range []string{"ctx.timestamp", "ctx.connectionId", "serverNow(ctx)", "connectionSessionId(ctx)"} {
		if !strings.Contains(module, snippet) {
			t.Fatalf("module missing %q", snippet)
		}
	}
}
```

- [ ] **Step 2: Run test and verify it fails**

Run:

```bash
cd workloads/ticket-remote
go test ./internal/web -run TestTicketSpacetimeMemberReducersUseServerClockAndConnectionID -count=1
```

Expected: FAIL because member reducers still accept `now` and `sessionId`.

- [ ] **Step 3: Implement server-time and connection-id helpers**

In `workloads/ticket-remote/spacetimedb/src/index.ts`, add:

```ts
function serverNow(ctx: any): string {
  const stamp = ctx.timestamp;
  const text = stamp && typeof stamp.toISOString === 'function' ? stamp.toISOString() : String(stamp || '').trim();
  if (!text) throw new SenderError('server time required');
  return text;
}

function connectionSessionId(ctx: any): string {
  const id = String(ctx.connectionId || '').trim();
  if (!id) throw new SenderError('connection required');
  return id;
}
```

Change member reducers so:

- `memberHeartbeatPresence` args are `{ ticketId, displayName, page, connected }`.
- `memberDisconnectPresence` args are `{ ticketId }`.
- `memberClaimControl` args are `{ ticketId }`.
- `memberReleaseControl` args are `{ ticketId, reason }`.
- `memberRevokeControl` args are `{ ticketId, reason }`.
- `memberUpsertMember` args are `{ ticketId, email, role }`.
- `memberRemoveMember` args are `{ ticketId, email }`.

Inside each member reducer, set:

```ts
const now = serverNow(ctx);
const sessionId = connectionSessionId(ctx);
const email = clientEmailFromAuth(ctx, ticket.id);
```

Presence lookup, insert, disconnect, and control-session ownership must use `sessionId` from `ctx.connectionId`. Release must reject active sessions owned by a different connection:

```ts
if (active.sessionId !== sessionId) throw new SenderError('not_controller');
```

- [ ] **Step 4: Update direct browser reducer calls**

In `workloads/ticket-remote/web-client/src/index.ts`, remove `now` and `sessionId` from direct member reducer payloads. For example:

```ts
this.reducer("memberHeartbeatPresence")({
  ticketId: this.cfg.ticketId,
  displayName: this.cfg.email,
  page: "ticket",
  connected,
});
```

`claimControl()` becomes:

```ts
return this.callReducer("memberClaimControl", { ticketId: this.cfg.ticketId });
```

`releaseControl(reason)` becomes:

```ts
return this.callReducer("memberReleaseControl", { ticketId: this.cfg.ticketId, reason });
```

- [ ] **Step 5: Rebuild generated browser client**

Run:

```bash
cd workloads/ticket-remote
make web-client-build
```

Expected: `web-client/src/generated/*` and `internal/web/static/spacetime-client.js` are regenerated from the changed module; do not manually edit generated files.

- [ ] **Step 6: Run tests and module build**

Run:

```bash
cd workloads/ticket-remote
go test ./internal/web -run TestTicketSpacetimeMemberReducersUseServerClockAndConnectionID -count=1
make spacetime-build
make web-client-build
```

Expected: PASS.

---

### Task 4: Redact Member-Visible State

**Files:**
- Modify: `workloads/ticket-remote/internal/state/types.go`
- Modify: `workloads/ticket-remote/internal/web/server.go`
- Modify: `workloads/ticket-remote/internal/web/server_state_test.go`
- Modify: `workloads/ticket-remote/spacetimedb/src/index.ts`
- Modify: `workloads/ticket-remote/internal/web/static/app.js`

- [ ] **Step 1: Write failing redaction tests**

Add to `workloads/ticket-remote/internal/web/server_state_test.go`:

```go
func TestMemberStateRedactionHidesAdminOnlyDetails(t *testing.T) {
	snapshot := state.Snapshot{
		Ticket: state.Ticket{ID: "vivi-default", DisplayName: "ViVi timed ticket", UpdatedAt: "2026-05-08T10:00:00Z"},
		Members: []state.Member{{Email: "owner@example.test", Role: state.RoleOwner, Active: true}, {Email: "viewer@example.test", Role: state.RoleMember, Active: true}},
		Viewers: []state.Viewer{{SessionID: "secret-session", Email: "viewer@example.test", Connected: true}},
		ActiveControl: &state.ControlSession{ID: "secret-control", SessionID: "secret-session", Email: "viewer@example.test", ExpiresAt: "2026-05-08T10:01:30Z"},
		Phone: &state.PhoneBackend{ID: "pixel", AttachName: "Pixel", BaseURL: "http://ticket_phone_bridge:9388", HealthJSON: `{"secret":true}`, LastError: "internal"},
		ServerTime: "2026-05-08T10:00:00Z",
		StateBackend: "spacetime",
	}

	public := snapshot.PublicForMember("viewer@example.test")
	body, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, forbidden := range []string{"secret-session", "secret-control", "owner@example.test", "ticket_phone_bridge", "healthJson", "lastError", `"members"`, `"viewers"`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("public member state leaked %q in %s", forbidden, text)
		}
	}
	for _, required := range []string{`"viewerCount":1`, `"activeControl"`, `"ownerEmail":"viewer@example.test"`, `"stateBackend":"spacetime"`} {
		if !strings.Contains(text, required) {
			t.Fatalf("public member state missing %q in %s", required, text)
		}
	}
}
```

Add a SpacetimeDB source-policy test in `server_setup_test.go` that fails if the public live-state payload includes `members`, `viewers`, `sessionId`, `baseUrl`, `healthJson`, or `lastError`.

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
cd workloads/ticket-remote
go test ./internal/web -run 'TestMemberStateRedactionHidesAdminOnlyDetails|TestTicketSpacetimeLiveStateIsRedacted' -count=1
```

Expected: FAIL because current member state exposes those fields.

- [ ] **Step 3: Add redacted state types**

In `workloads/ticket-remote/internal/state/types.go`, add public view types:

```go
type PublicSnapshot struct {
	Ticket Ticket `json:"ticket"`
	ViewerCount int `json:"viewerCount"`
	ActiveControl *PublicControlSession `json:"activeControl,omitempty"`
	Phone *PublicPhoneBackend `json:"phone,omitempty"`
	ServerTime string `json:"serverTime"`
	StateBackend string `json:"stateBackend"`
}

type PublicControlSession struct {
	OwnerEmail string `json:"ownerEmail"`
	ClaimedAt string `json:"claimedAt"`
	ExpiresAt string `json:"expiresAt"`
	RemainingMS int64 `json:"remainingMs"`
}

type PublicPhoneBackend struct {
	ID string `json:"id"`
	AttachName string `json:"attachName"`
	DesiredState string `json:"desiredState"`
	LastSeenAt string `json:"lastSeenAt,omitempty"`
}
```

Add `func (s Snapshot) PublicForMember(email string) PublicSnapshot` that only copies those fields.

- [ ] **Step 4: Use redacted state in member responses and socket broadcasts**

In `server.go`, keep full `state.Snapshot` for server-side decisions, but serialize `snapshot.PublicForMember(id.Email)` for:

- `/api/v1/me`
- `/api/v1/state`
- initial browser socket state messages
- state broadcasts to member sockets
- cached-state fallback messages

Keep full state only for admin handlers and internal health.

- [ ] **Step 5: Redact SpacetimeDB live state**

In `spacetimedb/src/index.ts`, split full service snapshot from public live state:

```ts
function publicSnapshot(tx: any, ticketId: string, now: string): any {
  const full = JSON.parse(snapshot(tx, ticketId, now)).state;
  return {
    ticket: full.ticket,
    viewerCount: Array.isArray(full.viewers) ? full.viewers.length : 0,
    activeControl: full.activeControl ? {
      ownerEmail: full.activeControl.email,
      claimedAt: full.activeControl.claimedAt,
      expiresAt: full.activeControl.expiresAt,
      remainingMs: full.activeControl.remainingMs,
    } : null,
    phone: full.phone ? {
      id: full.phone.id,
      attachName: full.phone.attachName,
      desiredState: full.phone.desiredState,
      lastSeenAt: full.phone.lastSeenAt,
    } : null,
    serverTime: full.serverTime,
    stateBackend: full.stateBackend,
  };
}
```

Change `writeLiveState` to insert `serialize(publicSnapshot(tx, id, now))`.

- [ ] **Step 6: Adapt browser rendering to redacted state**

In `internal/web/static/app.js`, support `state.viewerCount` in addition to the old `state.viewers` array while rollout is in progress. Treat active control ownership by comparing `state.activeControl.ownerEmail || state.activeControl.email` to `cfg.email`, never by comparing session ids.

- [ ] **Step 7: Run tests**

Run:

```bash
cd workloads/ticket-remote
go test ./internal/web -run 'TestMemberStateRedactionHidesAdminOnlyDetails|TestTicketSpacetimeLiveStateIsRedacted|TestTicketWebAssets' -count=1
make spacetime-build
make web-client-build
```

Expected: PASS.

---

### Task 5: Remove Persistent Browser Storage for Sign-In Material

**Files:**
- Modify: `workloads/ticket-remote/internal/web/static/app.js`
- Modify: `workloads/ticket-remote/internal/web/server.go`
- Modify: `workloads/ticket-remote/internal/web/server_setup_test.go`

- [ ] **Step 1: Keep the existing failing storage guard and add exact token-storage checks**

`server_setup_test.go` already rejects `localStorage.*` and `sessionStorage.*` in ticket static assets. Add these forbidden snippets:

```go
"ticket_remote_spacetime_token",
"ticket_remote_spacetime_token_expires_at",
"ticket_remote_pkce_verifier",
"ticket_remote_pkce_state",
```

- [ ] **Step 2: Run the test and verify it fails**

Run:

```bash
cd workloads/ticket-remote
go test ./internal/web -run TestTicketWebAssets -count=1
```

Expected: FAIL because `app.js` stores SpacetimeAuth and PKCE material in browser storage.

- [ ] **Step 3: Move login state to server-owned cookies and memory-only browser state**

Replace browser storage helpers in `app.js` with module variables:

```js
let directSpacetimeToken = '';
let directSpacetimeTokenExpiresAt = 0;
```

`rememberSpacetimeToken(token, expiresAt)` only assigns those variables. `spacetimeToken()` returns the in-memory token if still valid; otherwise it calls the server auth endpoint or restarts the SpacetimeAuth redirect.

Move PKCE verifier/state handling to server endpoints using `HttpOnly`, `Secure`, `SameSite=Lax` cookies:

- `GET /api/v1/auth/start`
- `GET /auth/callback`

The callback validates state from the HttpOnly cookie, exchanges the code, validates the ID token, sets the existing server session cookie, and renders the ticket shell with a memory-only direct token in `ConfigJSON.spacetime.token`.

- [ ] **Step 4: Keep direct SpacetimeDB token out of persistent APIs**

Ensure `GET /api/v1/auth/session` does not return a server session token as a direct SpacetimeDB token. Preserve the existing test around `directSpacetimeSessionFromRequest`.

- [ ] **Step 5: Run tests**

Run:

```bash
cd workloads/ticket-remote
go test ./internal/web -run 'TestTicketWebAssets|TestSpacetimeAuthSessionDoesNotExposeServerSessionToken' -count=1
```

Expected: PASS.

---

### Task 6: Rate Limit Stream Recovery and Keyframe Requests

**Files:**
- Modify: `workloads/ticket-remote/internal/web/server.go`
- Modify: `workloads/ticket-remote/internal/web/server_stream_test.go`
- Modify: `workloads/ticket-remote/internal/web/direct_stream.go`

- [ ] **Step 1: Write failing tests for keyframe and recovery throttling**

Add to `server_stream_test.go`:

```go
func TestVideoKeyframeRequestsAreRateLimitedPerViewer(t *testing.T) {
	server, phoneSignals, videoConn := newTicketVideoStreamTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	for i := 0; i < 5; i++ {
		if err := videoConn.Write(ctx, websocket.MessageText, []byte(`{"type":"keyframe","reason":"spam"}`)); err != nil {
			t.Fatal(err)
		}
	}

	got := countPhoneSignalsWithin(t, phoneSignals, "keyframe", 250*time.Millisecond)
	if got != 1 {
		t.Fatalf("keyframe requests forwarded = %d want 1", got)
	}
	_ = server
}

func TestVideoRecoveryRequestsAreRateLimitedGlobally(t *testing.T) {
	server, phoneSignals, videoConn := newTicketVideoStreamTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	for i := 0; i < 3; i++ {
		if err := videoConn.Write(ctx, websocket.MessageText, []byte(`{"type":"recover_stream","reason":"spam"}`)); err != nil {
			t.Fatal(err)
		}
	}

	got := countPhoneSignalsWithin(t, phoneSignals, "start", 250*time.Millisecond)
	if got != 1 {
		t.Fatalf("stream recovery requests forwarded = %d want 1", got)
	}
	_ = server
}
```

Use the existing `waitForSignal` phone signal pattern already present in `server_stream_test.go` for phone signal capture.

- [ ] **Step 2: Run tests and verify they fail**

Run:

```bash
cd workloads/ticket-remote
go test ./internal/web -run 'TestVideoKeyframeRequestsAreRateLimitedPerViewer|TestVideoRecoveryRequestsAreRateLimitedGlobally' -count=1
```

Expected: FAIL because every request is currently forwarded.

- [ ] **Step 3: Implement throttling**

Add to `client`:

```go
lastKeyframeRequest time.Time
lastRecoveryRequest time.Time
```

Add to `Server`:

```go
streamRequestMu sync.Mutex
lastGlobalKeyframeRequest time.Time
lastGlobalRecoveryRequest time.Time
```

Before forwarding `"keyframe"`, require per-client interval of `2*time.Second` and global interval of `500*time.Millisecond`. Before forwarding `"recover_stream"`, require per-client and global interval of `12*time.Second`. Drop denied requests and record telemetry as `keyframe_rate_limited` or `recovery_rate_limited`.

- [ ] **Step 4: Run tests**

Run:

```bash
cd workloads/ticket-remote
go test ./internal/web -run 'TestVideoKeyframeRequestsAreRateLimitedPerViewer|TestVideoRecoveryRequestsAreRateLimitedGlobally|TestBrowserVideoRecoverStreamRestartsPhoneStream' -count=1
```

Expected: PASS.

---

### Task 7: Reduce Public Container Secret Blast Radius

**Files:**
- Modify: `infra/arbuzas/docker/compose.yml`
- Modify: `infra/arbuzas/docker/images/ticket-remote.Dockerfile`
- Modify: `workloads/ticket-remote/README.md`

- [ ] **Step 1: Remove broad secrets and ADB key mounts from `ticket_remote`**

In the `ticket_remote` service, remove these mounts:

```yaml
- /etc/arbuzas/secrets:/etc/arbuzas/secrets:ro
- /etc/arbuzas/secrets/android-adb/adbkey:/root/.android/adbkey:ro
- /etc/arbuzas/secrets/android-adb/adbkey.pub:/root/.android/adbkey.pub:ro
- /etc/arbuzas/secrets/android-adb/adb_known_hosts.pb:/root/.android/adb_known_hosts.pb:ro
```

Replace them with only the exact ticket remote JWT key mount when key-file mode is used:

```yaml
- /etc/arbuzas/secrets/ticket-remote:/run/secrets/ticket-remote:ro
```

Set `TICKET_REMOTE_SPACETIME_JWT_PRIVATE_KEY_FILE=/run/secrets/ticket-remote/spacetime-jwt-private-key.pem` in `/etc/arbuzas/env/ticket-remote.env` on Arbuzas.

- [ ] **Step 2: Decide whether ADB remains in the web image**

If simulator setup still requires ADB from `ticket_remote`, keep `android-tools-adb` installed but rely only on private Docker-network simulator access with no Pixel ADB keys. If simulator setup is split into `ticket_android_sim_bridge`, remove `android-tools-adb` from `ticket-remote.Dockerfile`.

- [ ] **Step 3: Add deploy validation for removed mounts**

In `tools/arbuzas/deploy.sh`, add a validation command:

```sh
compose exec -T ticket_remote sh -lc '
  test ! -e /root/.android/adbkey &&
  test ! -e /etc/arbuzas/secrets/android-adb/adbkey &&
  test ! -d /etc/arbuzas/secrets || test -d /run/secrets/ticket-remote
'
```

- [ ] **Step 4: Validate Compose syntax**

Run:

```bash
docker compose -f infra/arbuzas/docker/compose.yml config >/tmp/ticket-compose-rendered.yml
```

Expected: exit 0 and rendered `ticket_remote` has no broad secrets or ADB key mounts.

---

### Task 8: Rebuild, Test, Deploy, and Prove the Public Version

**Files:**
- Uses all modified files.

- [ ] **Step 1: Run local verification**

Run:

```bash
cd workloads/ticket-remote
go test ./...
make spacetime-build
make web-client-build
go test ./...
```

Expected: all commands exit 0. The second `go test ./...` confirms generated browser code is in sync with Go embedded assets.

- [ ] **Step 2: Build the Docker image locally**

Run:

```bash
cd workloads/ticket-remote
make docker-image-build
```

Expected: image `arbuzas/ticket-remote:dev` builds successfully.

- [ ] **Step 3: Deploy using the required Arbuzas SSH profile**

Run:

```bash
./tools/arbuzas/deploy.sh deploy --services ticket_remote --ssh-host arbuzas --ssh-user ropepop
```

Expected: deploy exits 0.

- [ ] **Step 4: Run deploy validation using the required Arbuzas SSH profile**

Run:

```bash
./tools/arbuzas/deploy.sh validate --services ticket_remote --ssh-host arbuzas --ssh-user ropepop
```

Expected: validation exits 0 and includes checks for strict SpacetimeDB state, safe auth mode, HTTP redirect, HSTS, CSP, static assets, and stale string absence.

- [ ] **Step 5: Verify public HTTP/HTTPS behavior**

Run:

```bash
curl -sS -o /dev/null -w '%{http_code} %{redirect_url}\n' http://ticket.jolkins.id.lv/
curl -fsSI https://ticket.jolkins.id.lv/ | tr -d '\r' | grep -Ei 'strict-transport-security|content-security-policy|x-frame-options|x-content-type-options|referrer-policy|permissions-policy'
curl -fsS https://ticket.jolkins.id.lv/api/v1/livez
curl -fsS https://ticket.jolkins.id.lv/static/app.js | grep -E 'claimDialog|showModal|claim-dialog|confirmClaim|localStorage|sessionStorage' && exit 1 || true
```

Expected: HTTP redirects to HTTPS; HTTPS headers are present; `/api/v1/livez` returns `ok:true`; stale strings and browser-storage APIs are absent from served assets.

- [ ] **Step 6: Verify authenticated browser behavior**

Use the shared browser profile as required by this repo:

```bash
browser-use --session default --profile "Your Chrome"
```

Open `https://ticket.jolkins.id.lv/`, confirm the existing authenticated session loads the ticket page, and capture evidence under `ops/evidence/ticket-remote/<timestamp>/`. Check:

- ticket page renders the stream,
- control claim/release still works for the signed-in member,
- another signed-in member cannot release or overwrite that member's direct SpacetimeDB presence/control state,
- Safari/mobile double-tap zoom guards still behave as before on a mobile viewport,
- `/api/v1/health` works in the authenticated browser session and reports `state.stateBackend = "spacetime"` and `stateBackendFresh = true`.

- [ ] **Step 7: Publish SpacetimeDB module if not included in deploy**

If deploy does not publish the module, run the SpacetimeDB CLI publish separately with the production database configured in Arbuzas env:

```bash
cd workloads/ticket-remote
spacetime publish "$TICKET_REMOTE_SPACETIME_DATABASE" --module-path ./spacetimedb --yes
spacetime generate --lang typescript --out-dir ./web-client/src/generated --module-path ./spacetimedb --yes
```

Expected: publish succeeds and generated bindings match the published module.

---

## Self-Review Checklist

- Finding 3, plain HTTP serves the page: Task 1 and Task 8 verify redirect and HSTS.
- Finding 4, caller clock can affect control: Task 3 removes `now` from direct member reducers and uses `ctx.timestamp`.
- Finding 5, presence tampering by session id: Task 3 removes direct `sessionId` arguments and uses `ctx.connectionId`.
- Broad Arbuzas secrets and ADB key material: Task 7 removes mounts and validates absence.
- Deploy validation passes on memory fallback: Task 2 rejects production memory/auto state and validates live strict state.
- Deploy validation accepts dev/none auth: Task 2 rejects unsafe production auth modes.
- Missing browser safety headers: Task 1 adds and tests HSTS, CSP, frame, content-type, referrer, and permissions headers.
- Sign-in material in browser storage: Task 5 removes persistent browser storage and keeps the existing static asset guard.
- Repeated stream recovery/keyframe requests: Task 6 adds server-side throttles and tests them.
- Member-visible state too broad: Task 4 redacts REST, WebSocket, and SpacetimeDB public live state.
- Secure direct-to-user SpacetimeDB: Tasks 3 and 4 enforce `ctx.sender`, verified membership, `ctx.connectionId`, `ctx.timestamp`, admin role checks, and safe public state.
- Required SpacetimeDB workflow: Task 8 includes `make spacetime-build`, generated bindings, and publish/generate commands.
- Required ticket deploy validation: Task 8 includes Arbuzas SSH profile, public `/livez`, authenticated `/health`, static assets, stale strings, and live browser proof.
