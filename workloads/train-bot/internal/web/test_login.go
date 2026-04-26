package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"pixelops/shared/telegramweb"
	"telegramtrainapp/internal/config"
)

type testLoginTicketClaims struct {
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
	Nonce     string `json:"nonce"`
}

type testLoginTicketMeta struct {
	NonceHash string
	ExpiresAt time.Time
}

type testLoginBroker struct {
	secret []byte
	userID int64
	ttl    time.Duration
}

func newTestLoginBroker(cfg config.Config) (*testLoginBroker, error) {
	secret, err := telegramweb.LoadSessionSecret(cfg.TrainWebTestTicketSecretFile, "train web test ticket secret")
	if err != nil {
		return nil, err
	}
	ttl := time.Duration(cfg.TrainWebTestTicketTTLSec) * time.Second
	if ttl <= 0 {
		ttl = time.Minute
	}
	return &testLoginBroker{
		secret: secret,
		userID: cfg.TrainWebTestUserID,
		ttl:    ttl,
	}, nil
}

func MintTestLoginURL(cfg config.Config, now time.Time) (string, error) {
	if !cfg.TrainWebEnabled {
		return "", errors.New("TRAIN_WEB_ENABLED must be true")
	}
	if !cfg.TrainWebTestLoginEnabled {
		return "", errors.New("TRAIN_WEB_TEST_LOGIN_ENABLED must be true")
	}
	broker, err := newTestLoginBroker(cfg)
	if err != nil {
		return "", err
	}
	ticket, err := broker.Mint(now)
	if err != nil {
		return "", err
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.TrainWebPublicBaseURL), "/")
	if baseURL == "" {
		return "", errors.New("TRAIN_WEB_PUBLIC_BASE_URL is required")
	}
	return baseURL + "/app?test_ticket=" + url.QueryEscape(ticket), nil
}

func (b *testLoginBroker) Mint(now time.Time) (string, error) {
	if b == nil {
		return "", errors.New("test login broker is not configured")
	}
	nonce, err := randomTestLoginNonce()
	if err != nil {
		return "", err
	}
	return signTestLoginClaims(b.secret, testLoginTicketClaims{
		IssuedAt:  now.UTC().Unix(),
		ExpiresAt: now.UTC().Add(b.ttl).Unix(),
		Nonce:     nonce,
	})
}

func (b *testLoginBroker) Consume(raw string, now time.Time) (testLoginTicketClaims, testLoginTicketMeta, error) {
	claims, err := parseTestLoginClaims(b.secret, raw)
	if err != nil {
		return testLoginTicketClaims{}, testLoginTicketMeta{}, err
	}
	meta := testLoginTicketMeta{
		NonceHash: hashTestLoginNonce(claims.Nonce),
		ExpiresAt: time.Unix(claims.ExpiresAt, 0).UTC(),
	}
	if claims.Nonce == "" {
		return testLoginTicketClaims{}, meta, errors.New("invalid test login ticket nonce")
	}
	if claims.ExpiresAt <= 0 {
		return testLoginTicketClaims{}, meta, errors.New("invalid test login ticket expiry")
	}
	if now.UTC().Unix() > claims.ExpiresAt {
		return testLoginTicketClaims{}, meta, errors.New("test login ticket expired")
	}
	return claims, meta, nil
}

func signTestLoginClaims(secret []byte, claims testLoginTicketClaims) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal test login ticket: %w", err)
	}
	signature := testLoginHMACSHA256(secret, payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func parseTestLoginClaims(secret []byte, raw string) (testLoginTicketClaims, error) {
	if strings.TrimSpace(raw) == "" {
		return testLoginTicketClaims{}, errors.New("missing test login ticket")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return testLoginTicketClaims{}, errors.New("invalid test login ticket format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return testLoginTicketClaims{}, fmt.Errorf("decode test login ticket payload: %w", err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return testLoginTicketClaims{}, fmt.Errorf("decode test login ticket signature: %w", err)
	}
	expected := testLoginHMACSHA256(secret, payload)
	if len(signature) != len(expected) || subtle.ConstantTimeCompare(signature, expected) != 1 {
		return testLoginTicketClaims{}, errors.New("invalid test login ticket signature")
	}
	var claims testLoginTicketClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return testLoginTicketClaims{}, fmt.Errorf("decode test login ticket claims: %w", err)
	}
	return claims, nil
}

func testLoginHMACSHA256(key []byte, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(data)
	return mac.Sum(nil)
}

func randomTestLoginNonce() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate test login ticket nonce: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

func hashTestLoginNonce(nonce string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(nonce)))
	return hex.EncodeToString(sum[:8])
}
