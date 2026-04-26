package web

import (
	"crypto/rsa"
	"strings"
	"time"

	"pixelops/shared/telegramweb"
	"telegramtrainapp/internal/config"
)

type spacetimeTokenIssuer struct {
	issuer    string
	audience  string
	keyID     string
	publicKey *rsa.PublicKey
	inner     *telegramweb.Issuer
}

type spacetimeIssuedToken = telegramweb.IssuedToken

func newSpacetimeTokenIssuer(cfg config.Config) (*spacetimeTokenIssuer, error) {
	inner, err := telegramweb.NewIssuer(telegramweb.IssuerConfig{
		Issuer:         cfg.TrainWebSpacetimeOIDCIssuer,
		FallbackIssuer: strings.TrimRight(strings.TrimSpace(cfg.TrainWebPublicBaseURL), "/") + "/oidc",
		Audience:       cfg.TrainWebSpacetimeOIDCAudience,
		PrivateKeyFile: cfg.TrainWebSpacetimeJWTPrivateKeyFile,
		TokenTTL:       time.Duration(cfg.TrainWebSpacetimeTokenTTLSec) * time.Second,
		Roles:          []string{"train_user"},
		TokenIDPrefix:  "train",
	}, "train web Spacetime private key")
	if err != nil {
		return nil, err
	}
	return &spacetimeTokenIssuer{
		issuer:    inner.Issuer(),
		audience:  inner.Audience(),
		keyID:     inner.KeyID(),
		publicKey: inner.PublicKey(),
		inner:     inner,
	}, nil
}

func (i *spacetimeTokenIssuer) openIDConfiguration() map[string]any {
	return i.inner.OpenIDConfiguration()
}

func (i *spacetimeTokenIssuer) jwks() map[string]any {
	return i.inner.JWKS()
}

func (i *spacetimeTokenIssuer) issueTelegramToken(auth telegramAuth, now time.Time) (spacetimeIssuedToken, error) {
	return i.inner.IssueTelegramToken(telegramweb.Auth(auth), now, telegramweb.IssueTokenOptions{})
}
