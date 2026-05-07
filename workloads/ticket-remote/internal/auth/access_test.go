package auth

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
	"testing"
	"time"
)

func TestValidatorAcceptsSpacetimeAuthJWT(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	kid := "test-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			_ = json.NewEncoder(w).Encode(map[string]any{"jwks_uri": "http://" + r.Host + "/jwks.json"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kid": kid,
				"kty": "RSA",
				"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			}},
		})
	}))
	defer server.Close()

	token := signTestJWT(t, key, kid, map[string]any{
		"iss":            server.URL,
		"aud":            []string{"client-a"},
		"email":          "Member@Example.com",
		"email_verified": true,
		"sub":            "user_123",
		"iat":            time.Now().Add(-time.Minute).Unix(),
		"nbf":            time.Now().Add(-time.Minute).Unix(),
		"exp":            time.Now().Add(time.Hour).Unix(),
	})
	validator := NewValidator(AccessConfig{
		Mode:         "spacetime",
		OIDCIssuer:   server.URL,
		OIDCClientID: "client-a",
	})
	identity, err := validator.ValidateOIDCJWT(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Email != "member@example.com" {
		t.Fatalf("email = %q", identity.Email)
	}
	if !identity.EmailVerified || identity.Subject != "user_123" {
		t.Fatalf("identity = %#v", identity)
	}
}

func TestValidatorRejectsAudienceMismatch(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	kid := "test-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			_ = json.NewEncoder(w).Encode(map[string]any{"jwks_uri": "http://" + r.Host + "/jwks.json"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kid": kid,
				"kty": "RSA",
				"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			}},
		})
	}))
	defer server.Close()
	token := signTestJWT(t, key, kid, map[string]any{
		"iss":            server.URL,
		"aud":            []string{"other"},
		"email":          "member@example.com",
		"email_verified": true,
		"exp":            time.Now().Add(time.Hour).Unix(),
	})
	validator := NewValidator(AccessConfig{Mode: "spacetime", OIDCIssuer: server.URL, OIDCClientID: "wanted"})
	if _, err := validator.ValidateOIDCJWT(context.Background(), token); err == nil {
		t.Fatal("expected audience mismatch")
	}
}

func TestValidatorAcceptsCloudflareAccessJWT(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	kid := "cf-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cdn-cgi/access/certs" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kid": kid,
				"kty": "RSA",
				"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			}},
		})
	}))
	defer server.Close()

	token := signTestJWT(t, key, kid, map[string]any{
		"iss":   server.URL,
		"aud":   []string{"ticket-aud"},
		"email": "Ticket@Jolkins.ID.LV",
		"sub":   "ticket@jolkins.id.lv",
		"iat":   time.Now().Add(-time.Minute).Unix(),
		"nbf":   time.Now().Add(-time.Minute).Unix(),
		"exp":   time.Now().Add(time.Hour).Unix(),
	})
	validator := NewValidator(AccessConfig{
		Mode:       "cloudflare",
		TeamDomain: server.URL,
		Audience:   "ticket-aud",
	})
	identity, err := validator.IdentityFromRequest(context.Background(), httptest.NewRequest(http.MethodGet, "/", nil))
	if err == nil || identity.Email != "" {
		t.Fatal("expected request without assertion to be rejected")
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Cf-Access-Jwt-Assertion", token)
	identity, err = validator.IdentityFromRequest(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Email != "ticket@jolkins.id.lv" {
		t.Fatalf("email = %q", identity.Email)
	}
}

func signTestJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	headerRaw, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": kid})
	claimsRaw, _ := json.Marshal(claims)
	signingInput := base64.RawURLEncoding.EncodeToString(headerRaw) + "." + base64.RawURLEncoding.EncodeToString(claimsRaw)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}
