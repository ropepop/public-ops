package telegramweb

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	TelegramLoginAuthURL  = TelegramLoginIssuer + "/auth"
	TelegramLoginTokenURL = TelegramLoginIssuer + "/token"
)

type AuthorizationRequest struct {
	ClientID            string
	Origin              string
	RedirectURI         string
	ResponseType        string
	Scope               []string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
}

type TokenExchangerConfig struct {
	ClientID     string
	ClientSecret string
	TokenURL     string
	HTTPClient   *http.Client
}

type TokenExchanger struct {
	clientID     string
	clientSecret string
	tokenURL     string
	httpClient   *http.Client
}

type CodeExchangeRequest struct {
	Code         string
	RedirectURI  string
	CodeVerifier string
}

type CodeExchangeResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	IDToken     string `json:"id_token"`
}

func BuildAuthorizationURL(req AuthorizationRequest) (string, error) {
	clientID := strings.TrimSpace(req.ClientID)
	if clientID == "" {
		return "", fmt.Errorf("Telegram Login client ID is required")
	}
	redirectURI := strings.TrimSpace(req.RedirectURI)
	if redirectURI == "" {
		return "", fmt.Errorf("Telegram Login redirect URI is required")
	}
	responseType := strings.TrimSpace(req.ResponseType)
	if responseType == "" {
		responseType = "code"
	}
	scope := normalizeScopes(req.Scope)
	if len(scope) == 0 {
		scope = []string{"openid", "profile"}
	}

	params := url.Values{}
	params.Set("client_id", clientID)
	if origin := strings.TrimSpace(req.Origin); origin != "" {
		params.Set("origin", origin)
	}
	params.Set("redirect_uri", redirectURI)
	params.Set("response_type", responseType)
	params.Set("scope", strings.Join(scope, " "))
	if state := strings.TrimSpace(req.State); state != "" {
		params.Set("state", state)
	}
	if challenge := strings.TrimSpace(req.CodeChallenge); challenge != "" {
		params.Set("code_challenge", challenge)
		method := strings.TrimSpace(req.CodeChallengeMethod)
		if method == "" {
			method = "S256"
		}
		params.Set("code_challenge_method", method)
	}
	return TelegramLoginAuthURL + "?" + params.Encode(), nil
}

func PKCEChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(verifier)))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func NewTokenExchanger(cfg TokenExchangerConfig) (*TokenExchanger, error) {
	clientID := strings.TrimSpace(cfg.ClientID)
	if clientID == "" {
		return nil, fmt.Errorf("Telegram Login client ID is required")
	}
	clientSecret := strings.TrimSpace(cfg.ClientSecret)
	if clientSecret == "" {
		return nil, fmt.Errorf("Telegram Login client secret is required")
	}
	tokenURL := strings.TrimSpace(cfg.TokenURL)
	if tokenURL == "" {
		tokenURL = TelegramLoginTokenURL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &TokenExchanger{
		clientID:     clientID,
		clientSecret: clientSecret,
		tokenURL:     tokenURL,
		httpClient:   httpClient,
	}, nil
}

func (x *TokenExchanger) ExchangeCode(ctx context.Context, req CodeExchangeRequest) (CodeExchangeResponse, error) {
	if x == nil {
		return CodeExchangeResponse{}, fmt.Errorf("Telegram Login token exchanger is not configured")
	}
	code := strings.TrimSpace(req.Code)
	if code == "" {
		return CodeExchangeResponse{}, fmt.Errorf("Telegram authorization code is required")
	}
	redirectURI := strings.TrimSpace(req.RedirectURI)
	if redirectURI == "" {
		return CodeExchangeResponse{}, fmt.Errorf("Telegram Login redirect URI is required")
	}
	codeVerifier := strings.TrimSpace(req.CodeVerifier)
	if codeVerifier == "" {
		return CodeExchangeResponse{}, fmt.Errorf("Telegram PKCE code verifier is required")
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", x.clientID)
	form.Set("code_verifier", codeVerifier)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, x.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return CodeExchangeResponse{}, fmt.Errorf("build Telegram token exchange request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.SetBasicAuth(x.clientID, x.clientSecret)

	resp, err := x.httpClient.Do(httpReq)
	if err != nil {
		return CodeExchangeResponse{}, fmt.Errorf("exchange Telegram authorization code: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return CodeExchangeResponse{}, fmt.Errorf("read Telegram token response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return CodeExchangeResponse{}, fmt.Errorf("Telegram token exchange failed: %s", telegramTokenErrorMessage(body, resp.Status))
	}

	var payload CodeExchangeResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return CodeExchangeResponse{}, fmt.Errorf("decode Telegram token response: %w", err)
	}
	if strings.TrimSpace(payload.IDToken) == "" {
		return CodeExchangeResponse{}, fmt.Errorf("Telegram token response is missing id_token")
	}
	return payload, nil
}

func telegramTokenErrorMessage(body []byte, fallback string) string {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err == nil {
		if message := strings.TrimSpace(claimString(payload["error_description"])); message != "" {
			return message
		}
		if message := strings.TrimSpace(claimString(payload["error"])); message != "" {
			return message
		}
	}
	message := strings.TrimSpace(string(body))
	if message != "" {
		return message
	}
	return strings.TrimSpace(fallback)
}

func normalizeScopes(raw []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(raw))
	for _, scope := range raw {
		value := strings.TrimSpace(scope)
		if value == "" || seen[value] {
			continue
		}
		out = append(out, value)
		seen[value] = true
	}
	return out
}
