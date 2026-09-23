package web

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"ticketremote/internal/auth"
	"ticketremote/internal/config"
	"ticketremote/internal/phone"
	"ticketremote/internal/state"
)

func TestSignedOutPagesReachLoginWithoutJavaScript(t *testing.T) {
	access := auth.AccessConfig{
		Mode: "spacetime", OIDCClientID: "test-client",
		OIDCIssuer:   "https://auth.example.test/oidc",
		OIDCRedirect: "https://ticket.test/auth/callback",
		OIDCScope:    "openid profile email", AuthCookieName: "ticket_remote_auth",
	}
	// No store or phone: signed-out navigation must not require either.
	server := &Server{cfg: config.Config{Access: access}, auth: auth.NewValidator(access)}
	for _, path := range []string{"/", "/?from=home&value=a%2Bb", "/admin?tab=statistics"} {
		for _, stale := range []bool{false, true} {
			t.Run(path+map[bool]string{false: "/fresh", true: "/stale"}[stale], func(t *testing.T) {
				request := httptest.NewRequest(http.MethodGet, path, nil)
				if stale {
					request.AddCookie(&http.Cookie{Name: access.AuthCookieName, Value: "expired-session"})
				}
				response := httptest.NewRecorder()
				server.ServeHTTP(response, request)
				var next *url.URL
				var err error
				if strings.HasPrefix(path, "/admin") {
					if response.Code != http.StatusFound || strings.Contains(response.Body.String(), "<script") {
						t.Fatalf("signed-out admin must retain its direct HTTP login redirect: %d", response.Code)
					}
					next, err = response.Result().Location()
				} else {
					if response.Code != http.StatusOK {
						t.Fatalf("signed-out root returned %d instead of the welcome page", response.Code)
					}
					body := response.Body.String()
					link := regexp.MustCompile(`href="(/api/v1/auth/start\?returnTo=[^"]+)"`).FindStringSubmatch(body)
					if len(link) != 2 {
						t.Fatal("welcome must expose a working sign-in link without JavaScript")
					}
					next, err = url.Parse(html.UnescapeString(link[1]))
					nonce := regexp.MustCompile(`nonce="([^"]+)"`).FindStringSubmatch(body)
					if len(nonce) != 2 || !strings.Contains(response.Header().Get("Content-Security-Policy"), "'nonce-"+nonce[1]+"'") {
						t.Fatal("welcome inline content must use its response CSP nonce")
					}
					for _, private := range []string{"/static/", "TICKET_REMOTE_CONFIG", "/api/v1/stream", "sessionId", "accountScopeId", "ticketId", "<canvas", "prewarm"} {
						if strings.Contains(body, private) {
							t.Errorf("welcome disclosed private viewer content: %s", private)
						}
					}
					if len(response.Result().Cookies()) != 0 {
						t.Fatal("fresh or expired-session welcome must not create session cookies")
					}
				}
				if err != nil || next.Path != "/api/v1/auth/start" || next.Query().Get("returnTo") != path {
					t.Fatalf("login redirect did not preserve requested page: %v, %v", next, err)
				}
				if !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
					t.Fatal("signed-out navigation must be uncached")
				}
				login := httptest.NewRecorder()
				server.ServeHTTP(login, httptest.NewRequest(http.MethodGet, next.String(), nil))
				provider, err := login.Result().Location()
				if login.Code != http.StatusFound || err != nil || provider.Host != "auth.example.test" || provider.Path != "/oidc/auth" {
					t.Fatalf("login did not reach the identity provider: status=%d location=%v error=%v", login.Code, provider, err)
				}
				cookies := map[string]string{}
				for _, cookie := range login.Result().Cookies() {
					cookies[cookie.Name] = cookie.Value
				}
				if cookies[authFlowCookie("return_to")] != path || cookies[authFlowCookie("state")] == "" ||
					provider.Query().Get("state") != cookies[authFlowCookie("state")] ||
					provider.Query().Get("code_challenge") != pkceChallenge(cookies[authFlowCookie("verifier")]) {
					t.Fatal("login lost its return path or browser-bound verification")
				}
			})
		}
	}
}

func TestSignedInRootBypassesWelcome(t *testing.T) {
	const owner = "owner@example.test"
	store := NewMemoryStore()
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{TicketID: "welcome-test", AdminEmail: owner}); err != nil {
		t.Fatal(err)
	}
	access := auth.AccessConfig{Mode: "spacetime", AuthCookieName: "test_auth", SessionSigningKey: "welcome-fixture-only"}
	server, err := NewServer(config.Config{TicketID: "welcome-test", CookieName: "test_session", CookieTTL: time.Hour, Access: access}, store, phone.NewRelay(phone.RelayConfig{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	token, _, err := server.auth.IssueServerSession(auth.Identity{Email: owner, EmailVerified: true}, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/?from=home", nil)
	request.AddCookie(&http.Cookie{Name: access.AuthCookieName, Value: token})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `<canvas id="screen"`) ||
		!strings.Contains(body, `"authenticated":true`) || strings.Contains(body, "/pwa/welcome.js") {
		t.Fatal("an approved session must go straight to the authenticated viewer")
	}
}

func TestWelcomeMethodsAndUnsafeReturnPath(t *testing.T) {
	access := auth.AccessConfig{Mode: "spacetime", AuthCookieName: "ticket_remote_auth"}
	server := &Server{cfg: config.Config{Access: access}, auth: auth.NewValidator(access)}
	for _, method := range []string{http.MethodHead, http.MethodPost, http.MethodPut, http.MethodDelete} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(method, "/", nil))
		want := http.StatusMethodNotAllowed
		if method == http.MethodHead {
			want = http.StatusOK
		}
		if response.Code != want || response.Body.Len() != 0 {
			t.Fatalf("%s welcome: status=%d, body length=%d", method, response.Code, response.Body.Len())
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.URL.Path = "//"
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `href="/api/v1/auth/start?returnTo=%2F"`) {
		t.Fatal("welcome must normalize an unsafe return destination")
	}
}
