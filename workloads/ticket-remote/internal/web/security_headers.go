package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
)

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	writeNoStoreHeaders(w)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeErrorPage(w http.ResponseWriter, status int, message string) {
	title, action := "Ticket could not complete this request", ""
	switch status {
	case http.StatusUnauthorized:
		title, action = "Sign-in needs to restart", `<a href="/api/v1/auth/start">Start sign-in again</a>`
	case http.StatusForbidden:
		title = "Ticket access required"
		if message == "Admin access is required." {
			title, action = "Admin access required", `<a href="/">Back to Ticket</a>`
		}
	case http.StatusServiceUnavailable:
		title, action = "Ticket is temporarily unavailable", `<a href="/">Try Ticket again</a>`
	case http.StatusInternalServerError:
		title, action = "Ticket could not finish signing in", `<a href="/api/v1/auth/start">Start sign-in again</a>`
	}
	nonce := randomID()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	writeHTMLHeaders(w, nonce)
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<meta name="theme-color" content="#020304">
<title>Ticket</title>
<style nonce="%s">
html { background: #020304; color: #eef3f8; -webkit-text-size-adjust: 100%%; text-size-adjust: 100%%; }
body { margin: 0; padding: max(24px, env(safe-area-inset-top)) max(20px, env(safe-area-inset-right)) max(24px, env(safe-area-inset-bottom)) max(20px, env(safe-area-inset-left)); font: 1rem/1.5 system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
main { box-sizing: border-box; max-width: 36rem; min-height: calc(100svh - 48px); margin: auto; display: grid; place-content: center; justify-items: center; gap: 16px; text-align: center; overflow-wrap: anywhere; }
.error-status { margin: 0; color: #9cabbc; font-size: .875rem; }
h1 { margin: 0; font-size: clamp(1.75rem, 6vw, 2.25rem); line-height: 1.2; }
.error-message { margin: 0; color: #bac8d9; }
a { display: inline-grid; place-items: center; box-sizing: border-box; min-height: 48px; margin-top: 12px; border: 1px solid #b8d9ff; border-radius: 12px; padding: 10px 22px; background: #b8d9ff; color: #07111b; font-weight: 650; text-decoration: none; }
a:focus-visible { outline: 2px solid #9dccff; outline-offset: 4px; }
</style></head>
<body><main><p class="error-status">Ticket · %d</p><h1>%s</h1><p class="error-message">%s</p>%s</main></body></html>`, nonce, status, template.HTMLEscapeString(title), template.HTMLEscapeString(message), action)
}

func writeNoStoreHeaders(w http.ResponseWriter) {
	writeSecurityHeaders(w, "")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("Surrogate-Control", "no-store")
	w.Header().Set("CDN-Cache-Control", "no-store")
	w.Header().Set("Cloudflare-CDN-Cache-Control", "no-store")
}

func writeHTMLHeaders(w http.ResponseWriter, nonce string) {
	writeNoStoreHeaders(w)
	writeSecurityHeaders(w, nonce)
}

func writeSecurityHeaders(w http.ResponseWriter, nonce string) {
	writeSecurityHeadersWithConnect(w, nonce, nil)
}

func (s *Server) writeHTMLHeaders(w http.ResponseWriter, nonce string) {
	writeNoStoreHeaders(w)
	writeSecurityHeadersWithConnect(w, nonce, s.cspConnectSources())
}

func writeSecurityHeadersWithConnect(w http.ResponseWriter, nonce string, connectSources []string) {
	w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	// The origin gate rejects opaque origins on state-changing requests. Keep
	// referrers private outside this site while allowing same-origin form POSTs
	// to carry their real Origin instead of the Fetch-standard "null" value.
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=(), serial=()")
	scriptSrc := "script-src 'self'"
	styleSrc := "style-src 'self'"
	if strings.TrimSpace(nonce) != "" {
		scriptSrc += " 'nonce-" + strings.TrimSpace(nonce) + "'"
		styleSrc += " 'nonce-" + strings.TrimSpace(nonce) + "'"
	}
	connectSrc := []string{"connect-src", "'self'"}
	connectSrc = append(connectSrc, connectSources...)
	w.Header().Set("Content-Security-Policy", strings.Join([]string{
		"default-src 'self'",
		scriptSrc,
		"worker-src 'self'",
		styleSrc,
		"img-src 'self' data: blob:",
		"font-src 'self'",
		strings.Join(connectSrc, " "),
		"media-src 'self' blob:",
		"object-src 'none'",
		"base-uri 'none'",
		"frame-ancestors 'none'",
		"form-action 'self'",
	}, "; "))
}

func (s *Server) cspConnectSources() []string {
	sources := []string{}
	seen := map[string]struct{}{}
	appendOrigin := func(raw string, includeWebSocket bool) {
		origin, websocketOrigin := cspOrigins(raw)
		for _, candidate := range []string{origin, websocketOrigin} {
			if candidate == "" || (!includeWebSocket && candidate == websocketOrigin) {
				continue
			}
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}
			sources = append(sources, candidate)
		}
	}
	appendOrigin(s.cfg.State.SpacetimeHost, true)
	appendOrigin(s.cfg.Access.OIDCIssuer, false)
	return sources
}

func cspOrigins(raw string) (string, string) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", ""
	}
	origin := parsed.Scheme + "://" + parsed.Host
	websocketScheme := "ws"
	if parsed.Scheme == "https" {
		websocketScheme = "wss"
	}
	return origin, websocketScheme + "://" + parsed.Host
}
