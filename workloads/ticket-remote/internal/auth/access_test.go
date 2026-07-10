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
	"strings"
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
	missingExpiry := signTestJWT(t, key, kid, map[string]any{
		"iss":            server.URL,
		"aud":            []string{"client-a"},
		"email":          "member@example.com",
		"email_verified": true,
	})
	if _, err := validator.ValidateOIDCJWT(context.Background(), missingExpiry); err == nil || !strings.Contains(err.Error(), "expiry is missing") {
		t.Fatalf("OIDC token without expiry was accepted: %v", err)
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

func TestValidatorAcceptsServerSessionToken(t *testing.T) {
	validator := NewValidator(AccessConfig{
		Mode:              "spacetime",
		AuthCookieName:    "ticket_remote_auth",
		SessionSigningKey: "test-signing-key",
	})
	now := time.Now()
	token, expiresAt, err := validator.IssueServerSession(Identity{
		Email:         "Member@Example.com",
		Subject:       "user_123",
		EmailVerified: true,
	}, time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if !IsServerSessionToken(token) {
		t.Fatalf("server session token was not recognized: %q", token)
	}
	if !expiresAt.After(now) {
		t.Fatalf("expiresAt = %s, want after %s", expiresAt, now)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "ticket_remote_auth", Value: token})
	identity, err := validator.IdentityFromRequest(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Email != "member@example.com" || identity.Subject != "user_123" || !identity.EmailVerified {
		t.Fatalf("identity = %#v", identity)
	}
}

func TestValidatorConvertsNeverTTLToFiniteServerSession(t *testing.T) {
	validator := NewValidator(AccessConfig{
		Mode:              "spacetime",
		AuthCookieName:    "ticket_remote_auth",
		SessionSigningKey: "test-signing-key",
	})
	now := time.Now()
	token, expiresAt, err := validator.IssueServerSession(Identity{
		Email:         "member@example.com",
		Subject:       "user_123",
		EmailVerified: true,
	}, -1, now)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := expiresAt.Sub(now), DefaultServerSessionTTL; got != want {
		t.Fatalf("session TTL = %s, want %s", got, want)
	}
	claims := decodeServerSessionClaimsForTest(t, token)
	if claims.ExpiresAt == 0 || claims.Version != serverSessionVersion {
		t.Fatalf("finite versioned claims missing: %#v", claims)
	}
	identity, info, err := validator.ValidateServerSessionWithInfo(token, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if info.ExpiresAt.IsZero() || info.Legacy || info.Version != serverSessionVersion {
		t.Fatalf("session info = %#v", info)
	}
	if identity.Email != "member@example.com" || identity.Subject != "user_123" || !identity.EmailVerified {
		t.Fatalf("identity = %#v", identity)
	}
}

func TestValidatorAcceptsLegacySessionOnlyForMigration(t *testing.T) {
	validator := NewValidator(AccessConfig{SessionSigningKey: "test-signing-key"})
	now := time.Now().UTC()
	claims := serverSessionClaims{
		Email:     "member@example.com",
		IssuedAt:  now.Add(-time.Hour).Unix(),
		ExpiresAt: 0,
	}
	raw, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	token := serverSessionTokenPrefix + payload + "." + validator.signSessionPayload(payload)
	_, info, err := validator.ValidateServerSessionWithInfo(token, now)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Legacy || !info.ExpiresAt.IsZero() {
		t.Fatalf("legacy session info = %#v", info)
	}
}

func TestValidatorRejectsExpiredServerSessionToken(t *testing.T) {
	validator := NewValidator(AccessConfig{
		Mode:              "spacetime",
		SessionSigningKey: "test-signing-key",
	})
	now := time.Now()
	token, _, err := validator.IssueServerSession(Identity{Email: "member@example.com"}, time.Minute, now.Add(-2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validator.ValidateServerSession(token, now); err == nil {
		t.Fatal("expected expired server session to be rejected")
	}
}

func decodeServerSessionClaimsForTest(t *testing.T, token string) serverSessionClaims {
	t.Helper()
	rest := strings.TrimPrefix(token, serverSessionTokenPrefix)
	payload, _, ok := strings.Cut(rest, ".")
	if !ok {
		t.Fatalf("invalid token shape: %q", token)
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	var claims serverSessionClaims
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	return claims
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
