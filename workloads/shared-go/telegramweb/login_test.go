package telegramweb

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestVerifyIDToken(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	issuer := &Issuer{
		issuer:        TelegramLoginIssuer,
		audience:      "unused",
		keyID:         oidcKeyID(&privateKey.PublicKey),
		privateKey:    privateKey,
		publicKey:     &privateKey.PublicKey,
		tokenTTL:      time.Hour,
		tokenIDPrefix: "telegram-login-test",
	}
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(issuer.JWKS())
	}))
	defer jwksServer.Close()

	verifier, err := NewLoginVerifier(LoginVerifierConfig{
		ClientID: "123456789",
		JWKSURL:  jwksServer.URL,
	})
	if err != nil {
		t.Fatalf("NewLoginVerifier() error = %v", err)
	}

	now := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)
	token, err := issuer.signClaims(map[string]any{
		"iss":                TelegramLoginIssuer,
		"aud":                "123456789",
		"sub":                "telegram-login-test-user",
		"iat":                now.Unix(),
		"exp":                now.Add(5 * time.Minute).Unix(),
		"auth_date":          now.Unix(),
		"nonce":              "nonce-123",
		"id":                 777001,
		"name":               "Satiksme Tester",
		"preferred_username": "satiksmetester",
		"picture":            "https://example.com/picture.png",
	})
	if err != nil {
		t.Fatalf("signClaims() error = %v", err)
	}

	claims, err := verifier.VerifyIDToken(context.Background(), token, "nonce-123", now)
	if err != nil {
		t.Fatalf("VerifyIDToken() error = %v", err)
	}
	if claims.TelegramID != 777001 {
		t.Fatalf("TelegramID = %d, want 777001", claims.TelegramID)
	}
	if claims.Name != "Satiksme Tester" {
		t.Fatalf("Name = %q", claims.Name)
	}
	if claims.PreferredUsername != "satiksmetester" {
		t.Fatalf("PreferredUsername = %q", claims.PreferredUsername)
	}
}

func TestVerifyIDTokenRejectsInvalidClaims(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	issuer := &Issuer{
		issuer:        TelegramLoginIssuer,
		audience:      "unused",
		keyID:         oidcKeyID(&privateKey.PublicKey),
		privateKey:    privateKey,
		publicKey:     &privateKey.PublicKey,
		tokenTTL:      time.Hour,
		tokenIDPrefix: "telegram-login-test",
	}
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(issuer.JWKS())
	}))
	defer jwksServer.Close()

	verifier, err := NewLoginVerifier(LoginVerifierConfig{
		ClientID: "123456789",
		JWKSURL:  jwksServer.URL,
	})
	if err != nil {
		t.Fatalf("NewLoginVerifier() error = %v", err)
	}

	now := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)
	baseClaims := map[string]any{
		"iss":       TelegramLoginIssuer,
		"aud":       "123456789",
		"sub":       "telegram-login-test-user",
		"iat":       now.Unix(),
		"exp":       now.Add(5 * time.Minute).Unix(),
		"auth_date": now.Unix(),
		"nonce":     "nonce-123",
		"id":        777001,
	}

	testCases := []struct {
		name    string
		mutate  func(map[string]any)
		nonce   string
		wantErr string
	}{
		{
			name: "bad issuer",
			mutate: func(claims map[string]any) {
				claims["iss"] = "https://example.com"
			},
			nonce:   "nonce-123",
			wantErr: "issuer",
		},
		{
			name: "bad audience",
			mutate: func(claims map[string]any) {
				claims["aud"] = "987654321"
			},
			nonce:   "nonce-123",
			wantErr: "audience",
		},
		{
			name: "expired",
			mutate: func(claims map[string]any) {
				claims["exp"] = now.Add(-time.Minute).Unix()
			},
			nonce:   "nonce-123",
			wantErr: "expired",
		},
		{
			name: "missing id",
			mutate: func(claims map[string]any) {
				delete(claims, "id")
			},
			nonce:   "nonce-123",
			wantErr: "id",
		},
		{
			name: "nonce mismatch",
			mutate: func(claims map[string]any) {
			},
			nonce:   "other-nonce",
			wantErr: "nonce",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			claims := cloneMap(baseClaims)
			tc.mutate(claims)
			token, err := issuer.signClaims(claims)
			if err != nil {
				t.Fatalf("signClaims() error = %v", err)
			}
			if _, err := verifier.VerifyIDToken(context.Background(), token, tc.nonce, now); err == nil {
				t.Fatalf("VerifyIDToken() error = nil")
			} else if !containsSubstring(err.Error(), tc.wantErr) {
				t.Fatalf("VerifyIDToken() error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestVerifyIDTokenRejectsInvalidSignature(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	issuer := &Issuer{
		issuer:        TelegramLoginIssuer,
		audience:      "unused",
		keyID:         oidcKeyID(&privateKey.PublicKey),
		privateKey:    privateKey,
		publicKey:     &privateKey.PublicKey,
		tokenTTL:      time.Hour,
		tokenIDPrefix: "telegram-login-test",
	}
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(issuer.JWKS())
	}))
	defer jwksServer.Close()

	verifier, err := NewLoginVerifier(LoginVerifierConfig{
		ClientID: "123456789",
		JWKSURL:  jwksServer.URL,
	})
	if err != nil {
		t.Fatalf("NewLoginVerifier() error = %v", err)
	}

	now := time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC)
	token, err := issuer.signClaims(map[string]any{
		"iss":   TelegramLoginIssuer,
		"aud":   "123456789",
		"sub":   "telegram-login-test-user",
		"iat":   now.Unix(),
		"exp":   now.Add(5 * time.Minute).Unix(),
		"nonce": "nonce-123",
		"id":    777001,
	})
	if err != nil {
		t.Fatalf("signClaims() error = %v", err)
	}
	parts := splitToken(t, token)
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("DecodeString(signature) error = %v", err)
	}
	signature[0] ^= 0xFF
	parts[2] = base64.RawURLEncoding.EncodeToString(signature)
	badToken := parts[0] + "." + parts[1] + "." + parts[2]

	if _, err := verifier.VerifyIDToken(context.Background(), badToken, "nonce-123", now); err == nil {
		t.Fatalf("VerifyIDToken() error = nil")
	}
}

func splitToken(t *testing.T, token string) []string {
	t.Helper()
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		t.Fatalf("unexpected token format: %q", token)
	}
	return parts
}

func cloneMap(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func containsSubstring(haystack string, needle string) bool {
	return needle == "" || strings.Contains(haystack, needle)
}
