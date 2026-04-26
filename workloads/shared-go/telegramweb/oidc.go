package telegramweb

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"
)

type IssuerConfig struct {
	Issuer          string
	FallbackIssuer  string
	Audience        string
	PrivateKeyFile  string
	TokenTTL        time.Duration
	Roles           []string
	ClaimsSupported []string
	TokenIDPrefix   string
}

type Issuer struct {
	issuer          string
	audience        string
	keyID           string
	tokenTTL        time.Duration
	roles           []string
	claimsSupported []string
	tokenIDPrefix   string

	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
}

type IssuedToken struct {
	Token     string
	ExpiresAt time.Time
}

type IssueTokenOptions struct {
	ExtraClaims map[string]any
}

func NewIssuer(cfg IssuerConfig, keyLabel string) (*Issuer, error) {
	issuer := strings.TrimRight(strings.TrimSpace(cfg.Issuer), "/")
	if issuer == "" {
		issuer = strings.TrimRight(strings.TrimSpace(cfg.FallbackIssuer), "/")
	}
	if issuer == "" {
		return nil, fmt.Errorf("OIDC issuer is required")
	}
	audience := strings.TrimSpace(cfg.Audience)
	if audience == "" {
		return nil, fmt.Errorf("OIDC audience is required")
	}
	tokenTTL := cfg.TokenTTL
	if tokenTTL <= 0 {
		tokenTTL = 24 * time.Hour
	}
	roles := append([]string(nil), cfg.Roles...)
	if len(roles) == 0 {
		roles = []string{"telegram_user"}
	}
	claimsSupported := append([]string(nil), cfg.ClaimsSupported...)
	if len(claimsSupported) == 0 {
		claimsSupported = defaultClaimsSupported()
	}
	tokenIDPrefix := strings.TrimSpace(cfg.TokenIDPrefix)
	if tokenIDPrefix == "" {
		tokenIDPrefix = "telegram"
	}
	privateKey, err := loadOIDCPrivateKey(cfg.PrivateKeyFile, keyLabel)
	if err != nil {
		return nil, err
	}
	publicKey := &privateKey.PublicKey
	return &Issuer{
		issuer:          issuer,
		audience:        audience,
		keyID:           oidcKeyID(publicKey),
		tokenTTL:        tokenTTL,
		roles:           roles,
		claimsSupported: claimsSupported,
		tokenIDPrefix:   tokenIDPrefix,
		privateKey:      privateKey,
		publicKey:       publicKey,
	}, nil
}

func (i *Issuer) Issuer() string {
	if i == nil {
		return ""
	}
	return i.issuer
}

func (i *Issuer) Audience() string {
	if i == nil {
		return ""
	}
	return i.audience
}

func (i *Issuer) KeyID() string {
	if i == nil {
		return ""
	}
	return i.keyID
}

func (i *Issuer) PublicKey() *rsa.PublicKey {
	if i == nil {
		return nil
	}
	return i.publicKey
}

func (i *Issuer) OpenIDConfiguration() map[string]any {
	return map[string]any{
		"issuer":                                i.issuer,
		"jwks_uri":                              i.issuer + "/jwks.json",
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"subject_types_supported":               []string{"public"},
		"response_types_supported":              []string{"id_token"},
		"grant_types_supported":                 []string{"implicit"},
		"claims_supported":                      append([]string(nil), i.claimsSupported...),
	}
}

func (i *Issuer) JWKS() map[string]any {
	return map[string]any{
		"keys": []map[string]any{
			{
				"kty": "RSA",
				"use": "sig",
				"alg": "RS256",
				"kid": i.keyID,
				"n":   base64.RawURLEncoding.EncodeToString(i.publicKey.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(i.publicKey.E)).Bytes()),
			},
		},
	}
}

func (i *Issuer) IssueTelegramToken(auth Auth, now time.Time, opts IssueTokenOptions) (IssuedToken, error) {
	expiresAt := now.UTC().Add(i.tokenTTL)
	claims := map[string]any{
		"iss":              i.issuer,
		"sub":              fmt.Sprintf("telegram:%d", auth.User.ID),
		"aud":              []string{i.audience},
		"iat":              now.UTC().Unix(),
		"nbf":              now.UTC().Unix(),
		"exp":              expiresAt.Unix(),
		"jti":              randomTokenID(i.tokenIDPrefix),
		"telegram_user_id": fmt.Sprintf("%d", auth.User.ID),
		"given_name":       strings.TrimSpace(auth.User.FirstName),
		"language":         strings.TrimSpace(auth.User.LanguageCode),
		"roles":            append([]string(nil), i.roles...),
	}
	for key, value := range opts.ExtraClaims {
		claims[key] = value
	}
	token, err := i.signClaims(claims)
	if err != nil {
		return IssuedToken{}, err
	}
	return IssuedToken{
		Token:     token,
		ExpiresAt: expiresAt,
	}, nil
}

func (i *Issuer) signClaims(claims map[string]any) (string, error) {
	headerJSON, err := json.Marshal(map[string]any{
		"typ": "JWT",
		"alg": "RS256",
		"kid": i.keyID,
	})
	if err != nil {
		return "", fmt.Errorf("marshal OIDC token header: %w", err)
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal OIDC token claims: %w", err)
	}
	header := base64.RawURLEncoding.EncodeToString(headerJSON)
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := header + "." + payload
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, i.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign OIDC token: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func loadOIDCPrivateKey(path string, label string) (*rsa.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", keyLabel(label), err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("decode %s: invalid PEM", keyLabel(label))
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS#1 %s: %w", keyLabel(label), err)
		}
		return key, nil
	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS#8 %s: %w", keyLabel(label), err)
		}
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("%s must be RSA", keyLabel(label))
		}
		return rsaKey, nil
	default:
		return nil, fmt.Errorf("unsupported %s type %q", keyLabel(label), block.Type)
	}
}

func oidcKeyID(publicKey *rsa.PublicKey) string {
	sum := sha256.Sum256(x509.MarshalPKCS1PublicKey(publicKey))
	return hex.EncodeToString(sum[:8])
}

func randomTokenID(prefix string) string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return hex.EncodeToString(raw)
}

func defaultClaimsSupported() []string {
	return []string{
		"iss",
		"sub",
		"aud",
		"iat",
		"nbf",
		"exp",
		"jti",
		"telegram_user_id",
		"given_name",
		"language",
		"roles",
	}
}

func keyLabel(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return "OIDC private key"
	}
	return label
}
