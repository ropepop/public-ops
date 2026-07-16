package web

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ticketremote/internal/auth"
)

// refreshServerSessionCookie is the migration and sliding-expiry boundary for
// locally signed browser sessions. Legacy no-expiry tokens are accepted once,
// after current membership succeeds, and replaced with a finite token.
func (s *Server) refreshServerSessionCookie(w http.ResponseWriter, r *http.Request, id auth.Identity, opts memberLookupOptions, now time.Time) {
	if !opts.writeSession || w == nil {
		return
	}
	token := s.authTokenFromRequest(r)
	if !auth.IsServerSessionToken(token) {
		return
	}
	_, info, err := s.auth.ValidateServerSessionWithInfo(token, now)
	if err != nil {
		return
	}
	ttl := s.effectiveCookieTTL()
	refreshAt := info.ExpiresAt.Add(-ttl / 2)
	if !info.Legacy && !info.ExpiresAt.IsZero() && now.Before(refreshAt) {
		return
	}
	refreshed, _, err := s.auth.IssueServerSession(id, ttl, now)
	if err != nil {
		return
	}
	s.setAuthCookie(w, refreshed, int(ttl.Seconds()))
}

func (s *Server) sessionID(w http.ResponseWriter, r *http.Request) string {
	if cookie, err := r.Cookie(s.cfg.CookieName); err == nil && strings.TrimSpace(cookie.Value) != "" {
		return strings.TrimSpace(cookie.Value)
	}
	sessionID := randomID()
	http.SetCookie(w, &http.Cookie{
		Name:     s.cfg.CookieName,
		Value:    sessionID,
		Path:     "/",
		MaxAge:   s.authCookieMaxAge(),
		HttpOnly: true,
		Secure:   strings.HasPrefix(s.cfg.PublicBaseURL, "https://"),
		SameSite: http.SameSiteLaxMode,
	})
	return sessionID
}

func (s *Server) sessionIDNoWrite(r *http.Request) string {
	if cookie, err := r.Cookie(s.cfg.CookieName); err == nil && strings.TrimSpace(cookie.Value) != "" {
		return strings.TrimSpace(cookie.Value)
	}
	return ""
}

func (s *Server) setAuthCookie(w http.ResponseWriter, token string, maxAge int) {
	cookieName := strings.TrimSpace(s.cfg.Access.AuthCookieName)
	if cookieName == "" {
		cookieName = "ticket_remote_auth"
	}
	s.setPrivateAuthCookie(w, cookieName, token, maxAge)
}

func (s *Server) setPrivateAuthCookie(w http.ResponseWriter, name string, value string, maxAge int) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   strings.HasPrefix(s.cfg.PublicBaseURL, "https://"),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) authCookieMaxAge() int {
	return int(s.effectiveCookieTTL().Seconds())
}

func (s *Server) effectiveCookieTTL() time.Duration {
	if s.cfg.CookieTTL <= 0 {
		return defaultFiniteCookieTTL
	}
	return s.cfg.CookieTTL
}

func (s *Server) authTokenFromRequest(r *http.Request) string {
	cookieName := strings.TrimSpace(s.cfg.Access.AuthCookieName)
	if cookieName == "" {
		cookieName = "ticket_remote_auth"
	}
	if cookie, err := r.Cookie(cookieName); err == nil {
		return strings.TrimSpace(cookie.Value)
	}
	return ""
}

type memberTokenIssuer interface {
	IssueMemberToken(context.Context, string) (string, string, error)
}

func (s *Server) directSpacetimeSessionFromRequest(r *http.Request, email string) map[string]any {
	out := map[string]any{
		"host":     s.cfg.State.SpacetimeHost,
		"database": s.cfg.State.SpacetimeDatabase,
	}
	if !s.usesDirectSpacetimePresence() {
		return out
	}
	issuer, ok := s.store.(memberTokenIssuer)
	if !ok {
		return out
	}
	token, expiresAt, err := issuer.IssueMemberToken(r.Context(), email)
	if err != nil || token == "" {
		return out
	}
	out["token"], out["expiresAt"] = token, expiresAt
	return out
}

func authFlowCookie(suffix string) string {
	return "ticket_remote_auth_" + strings.TrimSpace(suffix)
}

func (s *Server) clearAuthFlowCookies(w http.ResponseWriter) {
	for _, name := range []string{authFlowCookie("verifier"), authFlowCookie("state"), authFlowCookie("return_to")} {
		s.setPrivateAuthCookie(w, name, "", -1)
	}
}

func (s *Server) usesSpacetimeAuth() bool {
	mode := strings.ToLower(strings.TrimSpace(s.cfg.Access.Mode))
	return mode == "" || mode == "spacetime" || mode == "spacetimeauth" || mode == "oidc"
}

func (s *Server) usesDirectSpacetimePresence() bool {
	return s.store != nil &&
		s.store.Backend() == "spacetime" &&
		strings.TrimSpace(s.cfg.State.SpacetimeDatabase) != ""
}

func (s *Server) publicAuthMode() string {
	mode := strings.ToLower(strings.TrimSpace(s.cfg.Access.Mode))
	switch mode {
	case "cloudflare", "cloudflare-access", "cf-access":
		return "cloudflare"
	case "dev", "development", "none":
		return mode
	default:
		return "spacetime"
	}
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func safeReturnPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "/"
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || strings.HasPrefix(value, "//") {
		return "/"
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	if parsed.Path == "/auth/callback" {
		return "/"
	}
	return parsed.String()
}

func cookieValue(r *http.Request, name string) string {
	if cookie, err := r.Cookie(name); err == nil {
		return strings.TrimSpace(cookie.Value)
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if clean := strings.TrimSpace(value); clean != "" {
			return clean
		}
	}
	return ""
}

func jwtExpiresAt(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var payload struct {
		ExpiresAt int64 `json:"exp"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.ExpiresAt <= 0 {
		return time.Time{}
	}
	return time.Unix(payload.ExpiresAt, 0).UTC()
}
