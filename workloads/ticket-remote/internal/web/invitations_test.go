package web

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"ticketremote/internal/auth"
	"ticketremote/internal/config"
	"ticketremote/internal/phone"
	"ticketremote/internal/state"
)

const invitationTestToken = "0123456789abcdefghijklmnopqrstuv"
const invitationTestOwner = "owner@example.test"

// This fixture deliberately leaves invitation policy to the real module tests.
// It records gateway calls, so a GET or a denied request cannot silently start
// a trial, consume an allowance, or grant membership.
type invitationHTTPStore struct {
	*MemoryStore
	invite                                                        state.Invitation
	lookups, starts, reserves, releases, redeems, tokens, revokes int
	created                                                       state.CreateInvitationInput
	started                                                       state.StartTrialInput
	redeemedHash, redeemedEmail                                   string
	lookupErr, startErr, statusErr, redeemErr                     error
}

func (s *invitationHTTPStore) Invitations(context.Context, string, string) ([]state.Invitation, error) {
	return []state.Invitation{s.invite}, nil
}
func (s *invitationHTTPStore) CreateInvitation(_ context.Context, in state.CreateInvitationInput) (state.Invitation, error) {
	s.created = in
	return s.invite, nil
}
func (s *invitationHTTPStore) RevokeInvitation(context.Context, string, string, string) error {
	s.revokes++
	return nil
}
func (s *invitationHTTPStore) InvitationByFingerprint(_ context.Context, ticket, hash string) (state.Invitation, error) {
	s.lookups++
	if s.lookupErr != nil {
		return state.Invitation{}, s.lookupErr
	}
	if ticket != s.invite.TicketID || hash != invitationFingerprint(invitationTestToken) {
		return state.Invitation{}, state.ErrInvitationUnavailable
	}
	return s.invite, nil
}
func (s *invitationHTTPStore) StartTrial(_ context.Context, in state.StartTrialInput) (state.Invitation, error) {
	s.starts++
	s.started = in
	if s.startErr != nil {
		return state.Invitation{}, s.startErr
	}
	s.invite.ActiveSessionID, s.invite.Status = in.SessionID, "trial_active"
	return s.invite, nil
}
func (s *invitationHTTPStore) TrialStatus(context.Context, string, string, string) (state.Invitation, error) {
	return s.invite, s.statusErr
}
func (s *invitationHTTPStore) ReserveTrialStream(context.Context, state.TrialStreamInput) (state.Invitation, error) {
	s.reserves++
	return s.invite, nil
}
func (s *invitationHTTPStore) ReleaseTrialStream(context.Context, state.TrialStreamInput) (state.Invitation, error) {
	s.releases++
	return s.invite, nil
}
func (s *invitationHTTPStore) RedeemInvitation(ctx context.Context, ticket, hash, email string) (state.Invitation, error) {
	s.redeems++
	s.redeemedHash, s.redeemedEmail = hash, email
	if s.redeemErr != nil {
		return state.Invitation{}, s.redeemErr
	}
	if _, err := s.UpsertMember(ctx, ticket, invitationTestOwner, email, state.RoleMember); err != nil {
		return state.Invitation{}, err
	}
	s.invite.RedeemedAt, s.invite.RedeemedEmail, s.invite.Status = time.Now().UTC().Format(time.RFC3339), email, "registered"
	return s.invite, nil
}
func (s *invitationHTTPStore) IssueGuestToken(context.Context, string, string, string) (string, string, error) {
	s.tokens++
	return "fixture-guest-token", time.Now().Add(time.Minute).UTC().Format(time.RFC3339), nil
}

var _ state.InvitationStore = (*invitationHTTPStore)(nil)

func newInvitationHTTPFixture(t *testing.T) (*Server, *invitationHTTPStore) {
	t.Helper()
	store := &invitationHTTPStore{MemoryStore: NewMemoryStore(), invite: state.Invitation{
		ID: "invitation-fixture", TicketID: "invitation-test", Label: "Private invitation label", CreatedBy: invitationTestOwner,
		CreatedAt: time.Now().UTC().Format(time.RFC3339), ExpiresAt: time.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339),
		StreamAllowanceMS: 900000, RemainingStreamMS: 900000, Status: "not_started",
	}}
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{TicketID: store.invite.TicketID, AdminEmail: invitationTestOwner}); err != nil {
		t.Fatal(err)
	}
	access := auth.AccessConfig{Mode: "spacetime", AuthCookieName: "test_auth", SessionSigningKey: "invitation-fixture-only", OIDCClientID: "invitation-client", OIDCIssuer: "https://auth.example.test", OIDCRedirect: "https://ticket.example.test/auth/callback", OIDCScope: "openid email"}
	server, err := NewServer(config.Config{TicketID: store.invite.TicketID, PublicBaseURL: "https://ticket.example.test", CookieName: "test_session", CookieTTL: time.Hour, Access: access}, store, phone.NewRelay(phone.RelayConfig{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	return server, store
}

func invitationHTTP(server *Server, method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://ticket.example.test"+path, strings.NewReader(body))
	r.Header.Set("Origin", "https://ticket.example.test")
	r.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	server.ServeHTTP(w, r)
	return w
}

func invitationResponseCookie(t *testing.T, response *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == name && cookie.Value != "" {
			return cookie
		}
	}
	t.Fatalf("response has no %s cookie", name)
	return nil
}

func invitationMemberCookie(t *testing.T, server *Server, email string) *http.Cookie {
	t.Helper()
	token, _, err := server.auth.IssueServerSession(auth.Identity{Email: email, EmailVerified: true}, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: server.cfg.Access.AuthCookieName, Value: token}
}

func invitationGuestCookie(t *testing.T, server *Server, session string) *http.Cookie {
	t.Helper()
	token, err := server.auth.IssueGuestSession("invitation-fixture", session, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: trialCookie, Value: token}
}

func TestInvitationLandingDoesNotConsumeTrial(t *testing.T) {
	server, store := newInvitationHTTPFixture(t)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		response := invitationHTTP(server, method, "/?invite="+invitationTestToken, "")
		if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/invite" {
			t.Fatalf("landing = %d %s", response.Code, response.Header().Get("Location"))
		}
		cookie := invitationResponseCookie(t, response, invitationCookie)
		if cookie.Value != invitationTestToken || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
			t.Fatal("invitation navigation must use a private same-site cookie")
		}
		response = invitationHTTP(server, method, "/invite", "", cookie)
		if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Cache-Control"), "no-store") || response.Header().Get("Referrer-Policy") != "origin" {
			t.Fatalf("invite shell = %d; headers %v", response.Code, response.Header())
		}
		if method == http.MethodHead && response.Body.Len() != 0 {
			t.Error("HEAD invitation must not send a body")
		}
		if method == http.MethodGet {
			body := response.Body.String()
			if !strings.Contains(body, "/pwa/welcome.js") || !strings.Contains(body, "/manifest.webmanifest?invite="+invitationTestToken) {
				t.Fatal("invitation shell must retain the personalized installation journey")
			}
			for _, private := range []string{"TICKET_REMOTE_CONFIG", "<canvas", "/api/v1/stream", store.invite.Label, store.invite.CreatedBy, invitationFingerprint(invitationTestToken)} {
				if strings.Contains(body, private) {
					t.Errorf("invitation shell exposes %q", private)
				}
			}
		}
	}
	if store.starts != 0 || store.reserves != 0 || store.redeems != 0 || store.tokens != 0 || len(server.streamPageOpenWarmUntil) != 0 || len(server.streamPrewarmTimers) != 0 {
		t.Fatal("opening an invitation started private viewer work")
	}
}

func TestInvitationManifestKeepsAppIdentityAndIsUncached(t *testing.T) {
	server, store := newInvitationHTTPFixture(t)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		response := invitationHTTP(server, method, "/manifest.webmanifest?invite="+invitationTestToken, "")
		if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Cache-Control"), "no-store") || response.Header().Get("Content-Type") != "application/manifest+json" {
			t.Fatalf("personalized manifest: %d %v", response.Code, response.Header())
		}
		if method == http.MethodHead {
			if response.Body.Len() != 0 {
				t.Fatal("HEAD manifest has a body")
			}
			continue
		}
		var manifest map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &manifest); err != nil {
			t.Fatal(err)
		}
		if manifest["id"] != "/" || manifest["scope"] != "/" || manifest["start_url"] != "/?invite="+invitationTestToken {
			t.Fatalf("installation lost stable app identity or invitation: %v", manifest)
		}
	}
	if response := invitationHTTP(server, "GET", "/manifest.webmanifest?invite=bad", ""); response.Code != 404 {
		t.Fatalf("invalid manifest = %d", response.Code)
	}
	if store.starts+store.reserves+store.redeems != 0 {
		t.Fatal("manifest fetch consumed invitation")
	}
}

func TestUnavailableInvitationLeadsToOrdinaryAuthorization(t *testing.T) {
	for _, unavailable := range []string{"invalid", "unknown", "used", "revoked"} {
		t.Run(unavailable, func(t *testing.T) {
			server, store := newInvitationHTTPFixture(t)
			token := invitationTestToken
			switch unavailable {
			case "invalid":
				token = "bad"
			case "unknown":
				store.lookupErr = state.ErrInvitationUnavailable
			case "used":
				store.invite.RedeemedAt = time.Now().UTC().Format(time.RFC3339)
			case "revoked":
				store.invite.RevokedAt = time.Now().UTC().Format(time.RFC3339)
			}
			response := invitationHTTP(server, "GET", "/?invite="+token, "")
			next, err := response.Result().Location()
			if response.Code != http.StatusFound || err != nil || next.Path != "/api/v1/auth/start" {
				t.Fatalf("unavailable invitation: %d %v %v", response.Code, next, err)
			}
			if next.Query().Get("invite") != "" || strings.Contains(next.Query().Get("returnTo"), "invite=") {
				t.Error("ordinary authorization must discard the unusable invitation instead of redirecting back to it")
			}
			if store.starts+store.reserves+store.redeems != 0 {
				t.Fatal("unavailable invitation admitted work")
			}
		})
	}
}

func TestExistingMemberBypassesInvitationWithoutClaimingIt(t *testing.T) {
	server, store := newInvitationHTTPFixture(t)
	response := invitationHTTP(server, "GET", "/?invite="+invitationTestToken, "", invitationMemberCookie(t, server, invitationTestOwner))
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/" {
		t.Fatalf("member bypass = %d %s", response.Code, response.Header().Get("Location"))
	}
	if store.lookups+store.starts+store.reserves+store.redeems != 0 {
		t.Fatal("approved member consumed an invitation")
	}
}

func TestTrialStartRequiresExplicitActionAndTakeover(t *testing.T) {
	server, store := newInvitationHTTPFixture(t)
	invite := &http.Cookie{Name: invitationCookie, Value: invitationTestToken}
	if response := invitationHTTP(server, "GET", "/api/v1/invite/start", "", invite); response.Code != 405 {
		t.Fatalf("GET start = %d", response.Code)
	}
	if response := invitationHTTP(server, "POST", "/api/v1/invite/start", `{}`); response.Code != 401 {
		t.Fatalf("start without invitation = %d", response.Code)
	}
	response := invitationHTTP(server, "POST", "/api/v1/invite/start", `{}`, invite)
	if response.Code != 200 || store.starts != 1 || store.started.TokenHash != invitationFingerprint(invitationTestToken) || store.started.SessionID == "" {
		t.Fatalf("start did not delegate the private invitation identity: status=%d calls=%d", response.Code, store.starts)
	}
	guestCookie := invitationResponseCookie(t, response, trialCookie)
	guest, err := server.auth.ValidateGuestSession(guestCookie.Value, time.Now())
	if err != nil || guest.InvitationID != store.invite.ID || guest.SessionID != store.started.SessionID {
		t.Fatal("trial did not issue the distinct signed guest session")
	}
	response = invitationHTTP(server, "POST", "/api/v1/invite/start", `{}`, invite, guestCookie)
	if response.Code != 200 || store.started.SessionID != guest.SessionID || store.started.Takeover {
		t.Fatal("repeat start must resume the same guest session")
	}
	response = invitationHTTP(server, "POST", "/api/v1/invite/start", `{}`, invite)
	if response.Code != 409 || !strings.Contains(response.Body.String(), `"needsTakeover":true`) || store.starts != 2 {
		t.Fatal("another browser must explicitly request takeover")
	}
	response = invitationHTTP(server, "POST", "/api/v1/invite/start", `{"takeover":true}`, invite)
	if response.Code != 200 || !store.started.Takeover || store.started.SessionID == guest.SessionID || store.starts != 3 {
		t.Fatal("explicit takeover must use a new session and durable ownership check")
	}
	if store.reserves != 0 || store.redeems != 0 {
		t.Fatal("starting a trial must not consume stream time or membership")
	}
}

func TestTrialStatusReturnsOnlyPublicAllowancesAfterExpiry(t *testing.T) {
	server, store := newInvitationHTTPFixture(t)
	store.invite.Status, store.invite.ActiveSessionID = "registration_only", "guest-one"
	store.invite.ExpiresAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	store.invite.StreamUsedMS, store.invite.ActivationsUsed, store.invite.ControlCodesUsed = 300000, 2, 1
	response := invitationHTTP(server, "GET", "/api/v1/invite/status", "", invitationGuestCookie(t, server, "guest-other"))
	var payload struct {
		OK    bool `json:"ok"`
		Trial struct {
			Status      string `json:"status"`
			Seconds     int    `json:"streamSecondsRemaining"`
			Activations int    `json:"activationsRemaining"`
			Codes       int    `json:"controlCodesRemaining"`
			AuthURL     string `json:"authUrl"`
		} `json:"trial"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || !payload.OK || payload.Trial.Status != "registration_only" || payload.Trial.Seconds != 600 || payload.Trial.Activations != 3 || payload.Trial.Codes != 4 || !strings.Contains(payload.Trial.AuthURL, "/api/v1/auth/start") {
		t.Fatalf("expired trial lost registration or allowances: %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), `"needsTakeover":true`) {
		t.Fatal("expired trial offered an unavailable device transfer instead of registration")
	}
	for _, private := range []string{store.invite.Label, store.invite.CreatedBy, invitationFingerprint(invitationTestToken), "guest-one"} {
		if strings.Contains(response.Body.String(), private) {
			t.Errorf("public trial status contains %q", private)
		}
	}
	if store.starts+store.reserves+store.redeems != 0 {
		t.Fatal("status read consumed trial")
	}
}

func TestGuestCannotUseMemberAdministration(t *testing.T) {
	server, store := newInvitationHTTPFixture(t)
	store.invite.Status, store.invite.ActiveSessionID = "trial_active", "guest-one"
	guest := invitationGuestCookie(t, server, "guest-one")
	for _, endpoint := range []struct{ method, path, body string }{
		{"GET", "/api/v1/admin/invitations", ""}, {"POST", "/api/v1/admin/invitations", `{"durationMinutes":4320,"streamMinutes":15}`},
		{"POST", "/api/v1/admin/invitations/revoke", `{"id":"invitation-fixture"}`}, {"GET", "/api/v1/admin/state", ""},
		{"GET", "/api/v1/admin/statistics", ""}, {"GET", "/api/v1/admin/notifications", ""}, {"GET", "/owner/hdr-diagnostic", ""},
	} {
		response := invitationHTTP(server, endpoint.method, endpoint.path, endpoint.body, guest)
		if response.Code != 401 && response.Code != 403 && response.Code != 404 {
			t.Errorf("guest reached %s %s: %d", endpoint.method, endpoint.path, response.Code)
		}
	}
	if store.created.TokenHash != "" || store.revokes != 0 || store.redeems != 0 {
		t.Fatal("guest admitted administrator writes")
	}
}

func TestGuestSessionCannotBecomeVerifiedEmailOrOutliveRevocation(t *testing.T) {
	server, store := newInvitationHTTPFixture(t)
	store.invite.Status, store.invite.ActiveSessionID = "trial_active", "guest-one"
	guest := invitationGuestCookie(t, server, "guest-one")
	response := invitationHTTP(server, "GET", "/api/v1/auth/session", "", guest)
	var session struct {
		Guest     bool   `json:"guest"`
		Email     string `json:"email"`
		ActorID   string `json:"actorId"`
		Spacetime struct {
			Token string `json:"token"`
		} `json:"spacetime"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || !session.Guest || session.Email != "" || session.ActorID != "guest:"+store.invite.ID || session.Spacetime.Token != "fixture-guest-token" {
		t.Fatalf("guest session bootstrap lost separate identity: %d", response.Code)
	}
	for _, path := range []string{"/static/admin-vivi-auth.js", "/static/admin-statistics.js", "/static/admin.html.tmpl"} {
		if response := invitationHTTP(server, "GET", path, "", guest); response.Code != 401 && response.Code != 403 && response.Code != 404 {
			t.Errorf("guest received private asset %s: %d", path, response.Code)
		}
	}
	store.invite.RevokedAt, store.invite.Status = time.Now().UTC().Format(time.RFC3339), "revoked"
	response = invitationHTTP(server, "GET", "/api/v1/auth/session", "", guest)
	if response.Code != 401 || store.tokens != 1 {
		t.Fatal("revoked guest received another direct database token")
	}
}

func TestEndedInvitationStatusDoesNotReturnReusableSecret(t *testing.T) {
	for _, ended := range []string{"revoked", "registered"} {
		t.Run(ended, func(t *testing.T) {
			server, store := newInvitationHTTPFixture(t)
			store.invite.Status = ended
			store.invite.ResultDeliveryUntilMS = time.Now().Add(time.Minute).UnixMilli()
			if ended == "revoked" {
				store.invite.RevokedAt = time.Now().UTC().Format(time.RFC3339)
			} else {
				store.invite.RedeemedAt = time.Now().UTC().Format(time.RFC3339)
			}
			response := invitationHTTP(server, "GET", "/api/v1/invite/status", "", &http.Cookie{Name: invitationCookie, Value: invitationTestToken})
			if response.Code != 200 || !strings.Contains(response.Body.String(), `"status":"`+ended+`"`) || !strings.Contains(response.Body.String(), `"resultDeliveryAllowed":false`) {
				t.Fatalf("ended invitation did not provide registration state: %d %s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), invitationTestToken) || strings.Contains(response.Body.String(), `"inviteUrl"`) {
				t.Fatal("ended invitation returned a reusable invitation address")
			}
		})
	}
}

func TestManualInvitationEntryAcceptsOnlyTicketLinks(t *testing.T) {
	server, store := newInvitationHTTPFixture(t)
	for _, input := range []struct {
		link   string
		status int
	}{
		{invitationTestToken, 200}, {"https://ticket.example.test/?invite=" + invitationTestToken, 200},
		{"https://other.example.test/?invite=" + invitationTestToken, 400}, {"http://ticket.example.test/?invite=" + invitationTestToken, 400},
		{"https://ticket.example.test/admin?invite=" + invitationTestToken, 400}, {"bad", 400},
	} {
		body, _ := json.Marshal(map[string]string{"link": input.link})
		response := invitationHTTP(server, "POST", "/api/v1/invite/open", string(body))
		if response.Code != input.status {
			t.Errorf("manual link returned %d, want %d", response.Code, input.status)
		}
		if response.Code == 200 && !strings.Contains(response.Body.String(), `"url":"/?invite=`+invitationTestToken+`"`) {
			t.Error("manual invitation entry must normalize to the local landing route")
		}
	}
	if store.starts+store.reserves+store.redeems != 0 {
		t.Fatal("pasting a link consumed an invitation")
	}
}

func TestInvitationCreatorStoresOnlyFingerprint(t *testing.T) {
	server, store := newInvitationHTTPFixture(t)
	owner := invitationMemberCookie(t, server, invitationTestOwner)
	response := invitationHTTP(server, "POST", "/api/v1/admin/invitations", `{"label":"Friends","durationMinutes":4320,"streamMinutes":15}`, owner)
	var created struct {
		InviteURL string `json:"inviteUrl"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	link, err := url.Parse(created.InviteURL)
	if response.Code != 201 || err != nil || link.Host != "ticket.example.test" || invitationToken(link.Query().Get("invite")) == "" {
		t.Fatalf("creation did not return an invitation link: status %d", response.Code)
	}
	token := link.Query().Get("invite")
	if store.created.TokenHash != invitationFingerprint(token) || store.created.ActorEmail != invitationTestOwner || store.created.DurationMinutes != 4320 || store.created.StreamMinutes != 15 || store.created.Label != "Friends" {
		t.Fatal("creation did not persist bounded allowances and a fingerprint")
	}
	response = invitationHTTP(server, "GET", "/api/v1/admin/invitations", "", owner)
	if response.Code != 200 || strings.Contains(response.Body.String(), token) || strings.Contains(response.Body.String(), store.created.TokenHash) || strings.Contains(response.Body.String(), `"inviteUrl"`) {
		t.Fatal("listing must not reproduce the one-time invitation secret")
	}
}

func TestInvitationInputAndOriginBoundaries(t *testing.T) {
	server, store := newInvitationHTTPFixture(t)
	invite := &http.Cookie{Name: invitationCookie, Value: invitationTestToken}
	for _, body := range []string{`{"takeover":false,"unexpected":1}`, `{} {"takeover":true}`, `null`, strings.Repeat(" ", 4097) + `{}`} {
		response := invitationHTTP(server, "POST", "/api/v1/invite/start", body, invite)
		if response.Code != 400 {
			t.Errorf("invalid trial payload admitted: %d", response.Code)
		}
	}
	r := httptest.NewRequest("POST", "https://ticket.example.test/api/v1/invite/start", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://elsewhere.example.test")
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(invite)
	w := httptest.NewRecorder()
	server.ServeHTTP(w, r)
	if w.Code != 403 || store.starts != 0 {
		t.Fatal("malformed or cross-origin requests must not start a trial")
	}
}

func invitationOIDCFixture(t *testing.T, server *Server) func(bool) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var idToken string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"jwks_uri": "http://" + r.Host + "/jwks"})
		case "/jwks":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{"kid": "invite-key", "kty": "RSA", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
		case "/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"id_token": idToken})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(provider.Close)
	server.cfg.Access.OIDCIssuer = provider.URL
	server.auth = auth.NewValidator(server.cfg.Access)
	return func(verified bool) string {
		header, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": "invite-key"})
		claims, _ := json.Marshal(map[string]any{"iss": provider.URL, "aud": []string{"invitation-client"}, "sub": "invited-person", "email": "new@example.test", "email_verified": verified, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()})
		input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
		digest := sha256.Sum256([]byte(input))
		signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
		if err != nil {
			t.Fatal(err)
		}
		idToken = input + "." + base64.RawURLEncoding.EncodeToString(signature)
		return idToken
	}
}

func TestInvitationRedeemsOnlyAfterVerifiedBrowserCallback(t *testing.T) {
	server, store := newInvitationHTTPFixture(t)
	setToken := invitationOIDCFixture(t, server)
	invite := &http.Cookie{Name: invitationCookie, Value: invitationTestToken}
	for _, verified := range []bool{false, true} {
		setToken(verified)
		start := invitationHTTP(server, "GET", inviteRegistrationURL(), "", invite)
		if start.Code != 302 {
			t.Fatalf("invitation sign-in = %d", start.Code)
		}
		// Browsers apply the last Set-Cookie when a response clears then replaces
		// the invitation-flow cookie. Sending duplicate names is not equivalent.
		cookieJar := map[string]*http.Cookie{invite.Name: invite}
		for _, cookie := range start.Result().Cookies() {
			cookieJar[cookie.Name] = cookie
		}
		cookies := make([]*http.Cookie, 0, len(cookieJar))
		for _, cookie := range cookieJar {
			cookies = append(cookies, cookie)
		}
		var stateValue string
		for _, cookie := range cookies {
			if cookie.Name == authFlowCookie("state") {
				stateValue = cookie.Value
			}
		}
		if stateValue == "" || store.redeems != 0 {
			t.Fatal("authorization start must preserve browser binding without redeeming")
		}
		wrongBrowser := invitationHTTP(server, "GET", "/auth/callback?code=fixture-code&state="+url.QueryEscape(stateValue), "", invite)
		if wrongBrowser.Code != 401 || store.redeems != 0 {
			t.Fatal("a callback without the initiating browser cookies must not claim an invitation")
		}
		response := invitationHTTP(server, "GET", "/auth/callback?code=fixture-code&state="+url.QueryEscape(stateValue), "", cookies...)
		if !verified {
			if response.Code != 401 || store.redeems != 0 {
				t.Fatal("unverified email redeemed invitation")
			}
			continue
		}
		if response.Code != 302 || store.redeems != 1 || store.redeemedHash != invitationFingerprint(invitationTestToken) || store.redeemedEmail != "new@example.test" {
			t.Fatalf("verified callback did not durably redeem: status=%d calls=%d", response.Code, store.redeems)
		}
		cookie := invitationResponseCookie(t, response, server.cfg.Access.AuthCookieName)
		identity, err := server.auth.ValidateServerSession(cookie.Value, time.Now())
		if err != nil || identity.Email != "new@example.test" || !identity.EmailVerified {
			t.Fatal("redeemed member did not receive a normal remembered verified session")
		}
		snapshot, err := store.Snapshot(context.Background(), store.invite.TicketID, time.Now())
		if _, member := snapshot.Member("new@example.test"); err != nil || !member {
			t.Fatal("remembered session was issued without durable membership")
		}
	}
}

func TestInvitationVerifiedSessionPostRedeemsBeforeRememberingMember(t *testing.T) {
	server, store := newInvitationHTTPFixture(t)
	token := invitationOIDCFixture(t, server)
	invite := &http.Cookie{Name: invitationCookie, Value: invitationTestToken}
	guest := invitationGuestCookie(t, server, "trial-session")
	for _, verified := range []bool{false, true} {
		body, _ := json.Marshal(map[string]string{"idToken": token(verified)})
		if verified {
			withoutInvite := invitationHTTP(server, "POST", "/api/v1/auth/session", string(body))
			if withoutInvite.Code != 403 || store.redeems != 0 {
				t.Fatal("verified email without invitation was granted membership")
			}
		}
		response := invitationHTTP(server, "POST", "/api/v1/auth/session", string(body), invite, guest)
		if !verified {
			if response.Code != 401 || store.redeems != 0 {
				t.Fatal("unverified session POST redeemed invitation")
			}
			continue
		}
		if response.Code != 200 || store.redeems != 1 || store.redeemedHash != invitationFingerprint(invitationTestToken) || store.redeemedEmail != "new@example.test" {
			t.Fatalf("verified session POST did not redeem durable invitation: %d calls=%d", response.Code, store.redeems)
		}
		cookie := invitationResponseCookie(t, response, server.cfg.Access.AuthCookieName)
		identity, err := server.auth.ValidateServerSession(cookie.Value, time.Now())
		if err != nil || identity.Email != "new@example.test" || !identity.EmailVerified {
			t.Fatal("session POST did not remember the verified member")
		}
		cleared := map[string]bool{}
		for _, cookie := range response.Result().Cookies() {
			if cookie.MaxAge < 0 {
				cleared[cookie.Name] = true
			}
		}
		if !cleared[trialCookie] || !cleared[invitationCookie] {
			t.Fatal("registration left guest or invitation cookies active")
		}
	}
}
