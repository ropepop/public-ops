package web

import (
	"net/http"
	"strings"
	"time"

	"pixelops/shared/telegramweb"
	"satiksmebot/internal/config"
)

type spacetimeTokenIssuer struct {
	issuer   string
	audience string
	inner    *telegramweb.Issuer
}

type spacetimeIssuedToken = telegramweb.IssuedToken

func newSpacetimeTokenIssuer(cfg config.Config) (*spacetimeTokenIssuer, error) {
	if !cfg.SatiksmeWebSpacetimeEnabled {
		return nil, nil
	}
	inner, err := telegramweb.NewIssuer(telegramweb.IssuerConfig{
		Issuer:         cfg.SatiksmeWebSpacetimeOIDCIssuer,
		FallbackIssuer: strings.TrimRight(strings.TrimSpace(cfg.SatiksmeWebPublicBaseURL), "/") + "/oidc",
		Audience:       cfg.SatiksmeWebSpacetimeOIDCAudience,
		PrivateKeyFile: cfg.SatiksmeWebSpacetimeJWTPrivateKeyFile,
		TokenTTL:       time.Duration(cfg.SatiksmeWebSpacetimeTokenTTLSec) * time.Second,
		Roles:          []string{"satiksme_user"},
		ClaimsSupported: []string{
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
			"smoke",
		},
		TokenIDPrefix: "satiksme",
	}, "satiksme web Spacetime private key")
	if err != nil {
		return nil, err
	}
	return &spacetimeTokenIssuer{
		issuer:   inner.Issuer(),
		audience: inner.Audience(),
		inner:    inner,
	}, nil
}

func (i *spacetimeTokenIssuer) openIDConfiguration() map[string]any {
	return i.inner.OpenIDConfiguration()
}

func (i *spacetimeTokenIssuer) jwks() map[string]any {
	return i.inner.JWKS()
}

func (s *Server) handleSpacetimeOpenIDConfiguration(w http.ResponseWriter, r *http.Request) {
	s.setNoStoreHeaders(w)
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.spacetime == nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, s.spacetime.openIDConfiguration())
}

func (s *Server) handleSpacetimeJWKS(w http.ResponseWriter, r *http.Request) {
	s.setNoStoreHeaders(w)
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.spacetime == nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, s.spacetime.jwks())
}
