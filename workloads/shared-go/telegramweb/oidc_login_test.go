package telegramweb

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestBuildAuthorizationURLIncludesCodeFlowAndPKCE(t *testing.T) {
	rawURL, err := BuildAuthorizationURL(AuthorizationRequest{
		ClientID:            "123456789",
		Origin:              "https://kontrole.info",
		RedirectURI:         "https://kontrole.info/api/v1/auth/telegram/callback",
		Scope:               []string{"openid", "profile"},
		State:               "state-123",
		CodeChallenge:       PKCEChallengeS256("verifier-123"),
		CodeChallengeMethod: "S256",
	})
	if err != nil {
		t.Fatalf("BuildAuthorizationURL() error = %v", err)
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	if got, want := parsed.Scheme+"://"+parsed.Host+parsed.Path, TelegramLoginAuthURL; got != want {
		t.Fatalf("authorization url = %q, want %q", got, want)
	}
	if got, want := parsed.Query().Get("client_id"), "123456789"; got != want {
		t.Fatalf("client_id = %q, want %q", got, want)
	}
	if got, want := parsed.Query().Get("origin"), "https://kontrole.info"; got != want {
		t.Fatalf("origin = %q, want %q", got, want)
	}
	if got, want := parsed.Query().Get("response_type"), "code"; got != want {
		t.Fatalf("response_type = %q, want %q", got, want)
	}
	if got, want := parsed.Query().Get("scope"), "openid profile"; got != want {
		t.Fatalf("scope = %q, want %q", got, want)
	}
	if got, want := parsed.Query().Get("state"), "state-123"; got != want {
		t.Fatalf("state = %q, want %q", got, want)
	}
	if got, want := parsed.Query().Get("code_challenge_method"), "S256"; got != want {
		t.Fatalf("code_challenge_method = %q, want %q", got, want)
	}
}

func TestTokenExchangerUsesBasicAuthAndReturnsIDToken(t *testing.T) {
	var (
		gotAuthorization string
		gotForm          url.Values
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		body, err := url.ParseQuery(readRequestBody(t, r))
		if err != nil {
			t.Fatalf("url.ParseQuery() error = %v", err)
		}
		gotForm = body
		_ = json.NewEncoder(w).Encode(CodeExchangeResponse{
			AccessToken: "access-token",
			TokenType:   "Bearer",
			ExpiresIn:   3600,
			IDToken:     "id-token",
		})
	}))
	defer server.Close()

	exchanger, err := NewTokenExchanger(TokenExchangerConfig{
		ClientID:     "123456789",
		ClientSecret: "top-secret",
		TokenURL:     server.URL,
	})
	if err != nil {
		t.Fatalf("NewTokenExchanger() error = %v", err)
	}

	payload, err := exchanger.ExchangeCode(context.Background(), CodeExchangeRequest{
		Code:         "code-123",
		RedirectURI:  "https://kontrole.info/api/v1/auth/telegram/callback",
		CodeVerifier: "verifier-123",
	})
	if err != nil {
		t.Fatalf("ExchangeCode() error = %v", err)
	}
	if payload.IDToken != "id-token" {
		t.Fatalf("payload.IDToken = %q, want id-token", payload.IDToken)
	}

	if got, want := gotAuthorization, "Basic "+base64.StdEncoding.EncodeToString([]byte("123456789:top-secret")); got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
	if got, want := gotForm.Get("grant_type"), "authorization_code"; got != want {
		t.Fatalf("grant_type = %q, want %q", got, want)
	}
	if got, want := gotForm.Get("code"), "code-123"; got != want {
		t.Fatalf("code = %q, want %q", got, want)
	}
	if got, want := gotForm.Get("code_verifier"), "verifier-123"; got != want {
		t.Fatalf("code_verifier = %q, want %q", got, want)
	}
}

func TestTokenExchangerSurfacesTelegramErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"code expired"}`))
	}))
	defer server.Close()

	exchanger, err := NewTokenExchanger(TokenExchangerConfig{
		ClientID:     "123456789",
		ClientSecret: "top-secret",
		TokenURL:     server.URL,
	})
	if err != nil {
		t.Fatalf("NewTokenExchanger() error = %v", err)
	}

	_, err = exchanger.ExchangeCode(context.Background(), CodeExchangeRequest{
		Code:         "code-123",
		RedirectURI:  "https://kontrole.info/api/v1/auth/telegram/callback",
		CodeVerifier: "verifier-123",
	})
	if err == nil {
		t.Fatalf("ExchangeCode() error = nil")
	}
	if !strings.Contains(err.Error(), "code expired") {
		t.Fatalf("ExchangeCode() error = %v, want message to include code expired", err)
	}
}

func readRequestBody(t *testing.T, r *http.Request) string {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("io.ReadAll() error = %v", err)
	}
	return string(body)
}
