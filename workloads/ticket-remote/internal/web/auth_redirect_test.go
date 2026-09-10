package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ticketremote/internal/auth"
	"ticketremote/internal/config"
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
				if response.Code != http.StatusFound {
					t.Fatalf("signed-out page returned %d instead of an HTTP login redirect", response.Code)
				}
				next, err := response.Result().Location()
				if err != nil || next.Path != "/api/v1/auth/start" || next.Query().Get("returnTo") != path {
					t.Fatalf("login redirect did not preserve requested page: %v, %v", next, err)
				}
				if !strings.Contains(response.Header().Get("Cache-Control"), "no-store") || strings.Contains(response.Body.String(), "<script") {
					t.Fatal("login redirect must be uncached and independent of JavaScript")
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
