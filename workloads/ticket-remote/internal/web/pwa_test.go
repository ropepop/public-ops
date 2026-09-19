package web

import (
	"bytes"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
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
