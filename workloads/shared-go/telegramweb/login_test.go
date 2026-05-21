package telegramweb

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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

func TestVerifyIDTokenCachesJWKS(t *testing.T) {
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
	var fetches int32
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&fetches, 1)
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
	for index := 0; index < 2; index++ {
		token, err := issuer.signClaims(map[string]any{
			"iss":       TelegramLoginIssuer,
			"aud":       "123456789",
			"sub":       "telegram-login-test-user",
			"iat":       now.Unix(),
			"exp":       now.Add(5 * time.Minute).Unix(),
			"auth_date": now.Unix(),
			"nonce":     "nonce-123",
			"id":        777001,
		})
		if err != nil {
			t.Fatalf("signClaims(%d) error = %v", index, err)
		}
		if _, err := verifier.VerifyIDToken(context.Background(), token, "nonce-123", now); err != nil {
			t.Fatalf("VerifyIDToken(%d) error = %v", index, err)
		}
	}
	if got := atomic.LoadInt32(&fetches); got != 1 {
		t.Fatalf("JWKS fetches = %d, want 1", got)
	}
}

func TestVerifyIDTokenSharesConcurrentJWKSRefresh(t *testing.T) {
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
	var fetches int32
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&fetches, 1)
		time.Sleep(100 * time.Millisecond)
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
		"iss":       TelegramLoginIssuer,
		"aud":       "123456789",
		"sub":       "telegram-login-test-user",
		"iat":       now.Unix(),
		"exp":       now.Add(5 * time.Minute).Unix(),
		"auth_date": now.Unix(),
		"nonce":     "nonce-123",
		"id":        777001,
	})
	if err != nil {
		t.Fatalf("signClaims() error = %v", err)
	}

	const workers = 20
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for index := 0; index < workers; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := verifier.VerifyIDToken(context.Background(), token, "nonce-123", now)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("VerifyIDToken() error = %v", err)
		}
	}
	if got := atomic.LoadInt32(&fetches); got != 1 {
		t.Fatalf("JWKS fetches = %d, want 1 shared refresh", got)
	}
}

func TestVerifyIDTokenRefreshesJWKSForUnknownKeyID(t *testing.T) {
	firstKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey(first) error = %v", err)
	}
	secondKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey(second) error = %v", err)
	}
	firstIssuer := &Issuer{
		issuer:        TelegramLoginIssuer,
		audience:      "unused",
		keyID:         oidcKeyID(&firstKey.PublicKey),
		privateKey:    firstKey,
		publicKey:     &firstKey.PublicKey,
		tokenTTL:      time.Hour,
		tokenIDPrefix: "telegram-login-test",
	}
	secondIssuer := &Issuer{
		issuer:        TelegramLoginIssuer,
		audience:      "unused",
		keyID:         oidcKeyID(&secondKey.PublicKey),
		privateKey:    secondKey,
		publicKey:     &secondKey.PublicKey,
		tokenTTL:      time.Hour,
		tokenIDPrefix: "telegram-login-test",
	}
	var fetches int32
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&fetches, 1) == 1 {
			_ = json.NewEncoder(w).Encode(firstIssuer.JWKS())
			return
		}
		_ = json.NewEncoder(w).Encode(secondIssuer.JWKS())
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
	firstToken, err := firstIssuer.signClaims(map[string]any{
		"iss":       TelegramLoginIssuer,
		"aud":       "123456789",
		"sub":       "telegram-login-test-user",
		"iat":       now.Unix(),
		"exp":       now.Add(5 * time.Minute).Unix(),
		"auth_date": now.Unix(),
		"nonce":     "nonce-123",
		"id":        777001,
	})
	if err != nil {
		t.Fatalf("signClaims(first) error = %v", err)
	}
	if _, err := verifier.VerifyIDToken(context.Background(), firstToken, "nonce-123", now); err != nil {
		t.Fatalf("VerifyIDToken(first) error = %v", err)
	}
	secondToken, err := secondIssuer.signClaims(map[string]any{
		"iss":       TelegramLoginIssuer,
		"aud":       "123456789",
		"sub":       "telegram-login-test-user",
		"iat":       now.Unix(),
		"exp":       now.Add(5 * time.Minute).Unix(),
		"auth_date": now.Unix(),
		"nonce":     "nonce-123",
		"id":        777001,
	})
	if err != nil {
		t.Fatalf("signClaims(second) error = %v", err)
	}
	if _, err := verifier.VerifyIDToken(context.Background(), secondToken, "nonce-123", now); err != nil {
		t.Fatalf("VerifyIDToken(second) error = %v", err)
	}
	if got := atomic.LoadInt32(&fetches); got != 2 {
		t.Fatalf("JWKS fetches = %d, want 2", got)
	}
}

func TestVerifyIDTokenIgnoresUnrelatedUnsupportedJWKSKeys(t *testing.T) {
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
		jwks := issuer.JWKS()
		keys := jwks["keys"].([]map[string]any)
		jwks["keys"] = append([]map[string]any{
			{
				"kty": "oct",
				"use": "enc",
				"kid": "unrelated-encryption-key",
				"k":   "ignored",
			},
		}, keys...)
		_ = json.NewEncoder(w).Encode(jwks)
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
		"iss":       TelegramLoginIssuer,
		"aud":       "123456789",
		"sub":       "telegram-login-test-user",
		"iat":       now.Unix(),
		"exp":       now.Add(5 * time.Minute).Unix(),
		"auth_date": now.Unix(),
		"nonce":     "nonce-123",
		"id":        777001,
	})
	if err != nil {
		t.Fatalf("signClaims() error = %v", err)
	}

	if _, err := verifier.VerifyIDToken(context.Background(), token, "nonce-123", now); err != nil {
		t.Fatalf("VerifyIDToken() error = %v", err)
	}
}

func TestVerifyIDTokenRetriesPreviouslyUnknownKeyAfterRefreshInterval(t *testing.T) {
	firstKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey(first) error = %v", err)
	}
	secondKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey(second) error = %v", err)
	}
	firstIssuer := &Issuer{
		issuer:        TelegramLoginIssuer,
		audience:      "unused",
		keyID:         oidcKeyID(&firstKey.PublicKey),
		privateKey:    firstKey,
		publicKey:     &firstKey.PublicKey,
		tokenTTL:      time.Hour,
		tokenIDPrefix: "telegram-login-test",
	}
	secondIssuer := &Issuer{
		issuer:        TelegramLoginIssuer,
		audience:      "unused",
		keyID:         oidcKeyID(&secondKey.PublicKey),
		privateKey:    secondKey,
		publicKey:     &secondKey.PublicKey,
		tokenTTL:      time.Hour,
		tokenIDPrefix: "telegram-login-test",
	}
	var fetches int32
	var secondPublished atomic.Bool
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&fetches, 1)
		if !secondPublished.Load() {
			_ = json.NewEncoder(w).Encode(firstIssuer.JWKS())
			return
		}
		_ = json.NewEncoder(w).Encode(secondIssuer.JWKS())
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
	firstToken, err := firstIssuer.signClaims(map[string]any{
		"iss":       TelegramLoginIssuer,
		"aud":       "123456789",
		"sub":       "telegram-login-test-user",
		"iat":       now.Unix(),
		"exp":       now.Add(5 * time.Minute).Unix(),
		"auth_date": now.Unix(),
		"nonce":     "nonce-123",
		"id":        777001,
	})
	if err != nil {
		t.Fatalf("signClaims(first) error = %v", err)
	}
	if _, err := verifier.VerifyIDToken(context.Background(), firstToken, "nonce-123", now); err != nil {
		t.Fatalf("VerifyIDToken(first) error = %v", err)
	}
	secondToken, err := secondIssuer.signClaims(map[string]any{
		"iss":       TelegramLoginIssuer,
		"aud":       "123456789",
		"sub":       "telegram-login-test-user",
		"iat":       now.Unix(),
		"exp":       now.Add(5 * time.Minute).Unix(),
		"auth_date": now.Unix(),
		"nonce":     "nonce-123",
		"id":        777001,
	})
	if err != nil {
		t.Fatalf("signClaims(second) error = %v", err)
	}
	if _, err := verifier.VerifyIDToken(context.Background(), secondToken, "nonce-123", now); err == nil {
		t.Fatalf("VerifyIDToken(second before key published) error = nil")
	}
	secondPublished.Store(true)
	if _, err := verifier.VerifyIDToken(context.Background(), secondToken, "nonce-123", now); err == nil {
		t.Fatalf("VerifyIDToken(second before refresh interval) error = nil")
	}
	verifier.jwksMu.Lock()
	verifier.jwksCache.lastUnknownKeyRefreshAt = time.Now().Add(-telegramLoginUnknownKeyRefreshInterval - time.Second)
	verifier.jwksMu.Unlock()

	if _, err := verifier.VerifyIDToken(context.Background(), secondToken, "nonce-123", now); err != nil {
		t.Fatalf("VerifyIDToken(second after refresh interval) error = %v", err)
	}
	if got := atomic.LoadInt32(&fetches); got != 3 {
		t.Fatalf("JWKS fetches = %d, want 3", got)
	}
}

func TestVerifyIDTokenThrottlesAndCapsUniqueUnknownKeyIDs(t *testing.T) {
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
	var fetches int32
	jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&fetches, 1)
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
	validToken, err := issuer.signClaims(map[string]any{
		"iss":       TelegramLoginIssuer,
		"aud":       "123456789",
		"sub":       "telegram-login-test-user",
		"iat":       now.Unix(),
		"exp":       now.Add(5 * time.Minute).Unix(),
		"auth_date": now.Unix(),
		"nonce":     "nonce-123",
		"id":        777001,
	})
	if err != nil {
		t.Fatalf("signClaims() error = %v", err)
	}
	if _, err := verifier.VerifyIDToken(context.Background(), validToken, "nonce-123", now); err != nil {
		t.Fatalf("VerifyIDToken(valid) error = %v", err)
	}

	for index := 0; index < 300; index++ {
		token := loginTokenWithKeyID(t, fmt.Sprintf("unknown-%03d", index))
		if _, err := verifier.VerifyIDToken(context.Background(), token, "nonce-123", now); err == nil {
			t.Fatalf("VerifyIDToken(unknown %d) error = nil", index)
		}
	}

	if got := atomic.LoadInt32(&fetches); got > 3 {
		t.Fatalf("JWKS fetches = %d, want at most 3 for repeated unknown key IDs", got)
	}
	verifier.jwksMu.Lock()
	remembered := len(verifier.jwksCache.unknownKeyExpires)
	verifier.jwksMu.Unlock()
	if remembered > 256 {
		t.Fatalf("remembered unknown key IDs = %d, want at most 256", remembered)
	}
}

func TestVerifyIDTokenUsesNumericSubjectAsTelegramID(t *testing.T) {
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
		"iss":       TelegramLoginIssuer,
		"aud":       "123456789",
		"sub":       "777001",
		"iat":       now.Unix(),
		"exp":       now.Add(5 * time.Minute).Unix(),
		"auth_date": now.Unix(),
		"nonce":     "nonce-123",
		"name":      "Satiksme Tester",
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
}

func loginTokenWithKeyID(t *testing.T, keyID string) string {
	t.Helper()
	header, err := json.Marshal(map[string]any{
		"alg": "RS256",
		"kid": keyID,
		"typ": "JWT",
	})
	if err != nil {
		t.Fatalf("Marshal(header) error = %v", err)
	}
	payload, err := json.Marshal(map[string]any{
		"iss":       TelegramLoginIssuer,
		"aud":       "123456789",
		"sub":       "telegram-login-test-user",
		"iat":       time.Now().Unix(),
		"exp":       time.Now().Add(5 * time.Minute).Unix(),
		"auth_date": time.Now().Unix(),
		"nonce":     "nonce-123",
		"id":        777001,
	})
	if err != nil {
		t.Fatalf("Marshal(payload) error = %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload) + ".signature"
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
