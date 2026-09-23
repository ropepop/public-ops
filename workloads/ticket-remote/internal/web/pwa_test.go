package web

import (
	"bytes"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicInstallationAssets(t *testing.T) {
	s := &Server{}
	for _, path := range []string{"/manifest.webmanifest", "/pwa/icon-192.png", "/pwa/icon-512.png", "/pwa/icon-maskable.png", "/pwa/apple-touch-icon.png"} {
		for _, method := range []string{"GET", "HEAD", "POST"} {
			r := httptest.NewRecorder()
			s.ServeHTTP(r, httptest.NewRequest(method, path, nil))
			want := http.StatusOK
			if method == "POST" {
				want = http.StatusMethodNotAllowed
			}
			if r.Code != want {
				t.Fatalf("%s %s: %d", method, path, r.Code)
			}
			if method != "GET" {
				if r.Body.Len() != 0 {
					t.Fatal("unexpected response body")
				}
				continue
			}
			if path == "/manifest.webmanifest" {
				var m struct {
					ID, Name, StartURL, Scope, Display string
					Icons                              []struct{ Src, Sizes, Purpose string }
				}
				var raw map[string]json.RawMessage
				if err := json.Unmarshal(r.Body.Bytes(), &raw); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(r.Body.Bytes(), &m); err != nil {
					t.Fatal(err)
				}
				if m.ID != "/" || m.Name != "Ticket" || m.Scope != "/" || m.Display != "fullscreen" || string(raw["start_url"]) != `"/"` || len(m.Icons) != 3 {
					t.Fatalf("wrong installation metadata: %+v", m)
				}
				if r.Header().Get("Content-Type") != "application/manifest+json" {
					t.Fatal("wrong manifest MIME type")
				}
			} else {
				config, err := png.DecodeConfig(bytes.NewReader(r.Body.Bytes()))
				if err != nil {
					t.Fatal(err)
				}
				want := map[string]int{"/pwa/icon-192.png": 192, "/pwa/icon-512.png": 512, "/pwa/icon-maskable.png": 512, "/pwa/apple-touch-icon.png": 180}[path]
				if config.Width != want || config.Height != want {
					t.Fatalf("wrong icon dimensions: %s", path)
				}
			}
		}
	}
}

func TestPublicWelcomeAssets(t *testing.T) {
	server := &Server{}
	for _, tc := range []struct{ path, file, contentType string }{
		{"/pwa/welcome.js", "pwa/welcome.js", "text/javascript; charset=utf-8"},
		{"/pwa/install-guide.css", "pwa/install-guide.css", "text/css; charset=utf-8"},
		{"/pwa/install-apple.png", "static/install-apple.png", "image/png"},
		{"/pwa/install-safari-steps.jpg", "static/install-safari-steps.jpg", "image/jpeg"},
		{"/pwa/install-safari-add.jpg", "static/install-safari-add.jpg", "image/jpeg"},
		{"/pwa/install-firefox.png", "static/install-firefox.png", "image/png"},
		{"/pwa/install-chrome-choice.png", "static/install-chrome-choice.png", "image/png"},
		{"/pwa/install-chrome-confirm.png", "static/install-chrome-confirm.png", "image/png"},
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost} {
			t.Run(tc.path+"/"+method, func(t *testing.T) {
				response := httptest.NewRecorder()
				server.ServeHTTP(response, httptest.NewRequest(method, tc.path+"?v="+assetVersion(), nil))
				if method == http.MethodPost {
					if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "GET, HEAD" || response.Body.Len() != 0 {
						t.Fatal("public installation assets must reject writes")
					}
					return
				}
				if response.Code != http.StatusOK || response.Header().Get("Content-Type") != tc.contentType ||
					response.Header().Get("X-Content-Type-Options") != "nosniff" ||
					response.Header().Get("Cache-Control") != "public, max-age=3600" || len(response.Result().Cookies()) != 0 {
					t.Fatalf("incorrect public asset response: status=%d, headers=%v", response.Code, response.Header())
				}
				if method == http.MethodHead {
					if response.Body.Len() != 0 {
						t.Fatal("HEAD must not send an asset body")
					}
					return
				}
				body, err := staticFS.ReadFile(tc.file)
				if err != nil || !bytes.Equal(response.Body.Bytes(), body) {
					t.Fatalf("public URL did not serve the existing asset: %v", err)
				}
			})
		}
	}
	for _, path := range []string{
		"/pwa/", "/pwa/app.js", "/pwa/index.html.tmpl", "/pwa/ticket-notifications-sw.js",
		"/pwa/welcome.js/", "/pwa/../static/app.js", "/pwa/install-apple.png/", "/welcome/index.html.tmpl",
	} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "<script") {
			t.Errorf("non-allowlisted public path %s returned %d", path, response.Code)
		}
	}
}
