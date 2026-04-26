package spacetime

import (
	"bytes"
	"context"
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
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"satiksmebot/internal/model"
)

const satiksmebotDBPrefix = "satiksmebot_"

func canonicalProcedureName(name string) string {
	clean := strings.TrimSpace(name)
	if clean == "" {
		return ""
	}
	if strings.HasPrefix(clean, satiksmebotDBPrefix) {
		return clean
	}
	return satiksmebotDBPrefix + clean
}

func missingProcedureResponse(statusCode int, responseBody []byte) bool {
	if statusCode == http.StatusNotFound {
		return true
	}
	message := strings.ToLower(strings.TrimSpace(string(responseBody)))
	if message == "" {
		return false
	}
	return strings.Contains(message, "nonexistent reducer") ||
		strings.Contains(message, "nonexistent procedure") ||
		strings.Contains(message, "unknown reducer") ||
		strings.Contains(message, "unknown procedure")
}

type SQLStatementStats struct {
	RowsInserted int64 `json:"rows_inserted"`
	RowsDeleted  int64 `json:"rows_deleted"`
	RowsUpdated  int64 `json:"rows_updated"`
}

type SQLStatementResult struct {
	Schema              map[string]any    `json:"schema"`
	Rows                [][]any           `json:"rows"`
	TotalDurationMicros int64             `json:"total_duration_micros"`
	Stats               SQLStatementStats `json:"stats"`
}

type SyncConfig struct {
	Host              string
	Database          string
	Issuer            string
	Audience          string
	JWTPrivateKeyFile string
	ServiceSubject    string
	ServiceRoles      []string
	TokenTTL          time.Duration
	HTTPTimeout       time.Duration
}

type TokenOptions struct {
	Subject string
	Roles   []string
	Claims  map[string]any
}

type Syncer struct {
	baseURL  string
	database string
	client   *http.Client
	issuer   *serviceTokenIssuer
}

type serviceTokenIssuer struct {
	issuer   string
	audience string
	subject  string
	roles    []string
	tokenTTL time.Duration
	keyID    string

	privateKey *rsa.PrivateKey
}

type BundleSnapshot struct {
	Version     string        `json:"version"`
	GeneratedAt string        `json:"generatedAt"`
	Stops       []model.Stop  `json:"stops"`
	Routes      []model.Route `json:"routes"`
}

type ReportDumpItem struct {
	ID            string `json:"id"`
	Payload       string `json:"payload"`
	Attempts      int    `json:"attempts"`
	CreatedAt     string `json:"createdAt"`
	NextAttemptAt string `json:"nextAttemptAt"`
	LastAttemptAt string `json:"lastAttemptAt,omitempty"`
	LastError     string `json:"lastError,omitempty"`
}

type StateSnapshot struct {
	StopSightings      []model.StopSighting      `json:"stopSightings"`
	VehicleSightings   []model.VehicleSighting   `json:"vehicleSightings"`
	IncidentVotes      []model.IncidentVote      `json:"incidentVotes"`
	IncidentVoteEvents []model.IncidentVoteEvent `json:"incidentVoteEvents"`
	IncidentComments   []model.IncidentComment   `json:"incidentComments"`
	ReportDumpItems    []ReportDumpItem          `json:"reportDumpItems"`
}

func NewSyncer(cfg SyncConfig) (*Syncer, error) {
	host := strings.TrimRight(strings.TrimSpace(cfg.Host), "/")
	if host == "" {
		return nil, fmt.Errorf("spacetime host is required")
	}
	database := strings.TrimSpace(cfg.Database)
	if database == "" {
		return nil, fmt.Errorf("spacetime database is required")
	}
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = 10 * time.Second
	}
	if cfg.TokenTTL <= 0 {
		cfg.TokenTTL = 5 * time.Minute
	}
	privateKey, err := loadRSAPrivateKey(cfg.JWTPrivateKeyFile)
	if err != nil {
		return nil, err
	}
	issuer := strings.TrimSpace(cfg.Issuer)
	if issuer == "" {
		issuer = "satiksme-bot-runtime"
	}
	audience := strings.TrimSpace(cfg.Audience)
	if audience == "" {
		audience = "spacetimedb"
	}
	subject := strings.TrimSpace(cfg.ServiceSubject)
	if subject == "" {
		subject = "service:satiksme-bot"
	}
	roles := append([]string(nil), cfg.ServiceRoles...)
	if len(roles) == 0 {
		roles = []string{"satiksme_service"}
	}
	return &Syncer{
		baseURL:  host,
		database: database,
		client:   &http.Client{Timeout: cfg.HTTPTimeout},
		issuer: &serviceTokenIssuer{
			issuer:     issuer,
			audience:   audience,
			subject:    subject,
			roles:      roles,
			tokenTTL:   cfg.TokenTTL,
			keyID:      keyIDForPublicKey(&privateKey.PublicKey),
			privateKey: privateKey,
		},
	}, nil
}

func (s *Syncer) PublishCatalogBundle(ctx context.Context, snapshot BundleSnapshot) error {
	importID := "bundle-" + randomTokenID()
	if _, err := s.CallProcedure(ctx, "satiksmebot_begin_bundle_import", []any{importID, strings.TrimSpace(snapshot.Version), strings.TrimSpace(snapshot.GeneratedAt)}); err != nil {
		return err
	}
	aborted := false
	abortImport := func(cause error) error {
		if aborted {
			return cause
		}
		aborted = true
		if _, abortErr := s.CallProcedure(ctx, "satiksmebot_abort_bundle_import", []any{importID}); abortErr != nil {
			return fmt.Errorf("%w (abort failed: %v)", cause, abortErr)
		}
		return cause
	}
	for _, batch := range batchBundleStops(snapshot.Stops, 200) {
		if _, err := s.CallProcedure(ctx, "satiksmebot_append_bundle_chunk", []any{importID, "stops", mustJSON(batch)}); err != nil {
			return abortImport(err)
		}
	}
	for _, batch := range batchBundleRoutes(snapshot.Routes, 50) {
		if _, err := s.CallProcedure(ctx, "satiksmebot_append_bundle_chunk", []any{importID, "routes", mustJSON(batch)}); err != nil {
			return abortImport(err)
		}
	}
	if _, err := s.CallProcedure(ctx, "satiksmebot_commit_bundle_import", []any{importID}); err != nil {
		return abortImport(err)
	}
	return nil
}

func (s *Syncer) ImportStateSnapshot(ctx context.Context, snapshot StateSnapshot) error {
	_, err := s.CallProcedure(ctx, "satiksmebot_service_import_state_snapshot", []any{mustJSON(snapshot)})
	return err
}

func (s *Syncer) UpsertLiveTransportState(ctx context.Context, state model.LiveTransportState) error {
	_, err := s.CallProcedure(ctx, "satiksmebot_service_upsert_live_snapshot_state", []any{mustJSON(state)})
	return err
}

func (s *Syncer) CountActiveLiveViewers(ctx context.Context, activeSince time.Time) (int, error) {
	payload, err := s.CallProcedure(ctx, "satiksmebot_service_count_live_viewers", []any{activeSince.UTC().Format(time.RFC3339)})
	if err != nil {
		return 0, err
	}
	var raw struct {
		Count int `json:"count"`
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return 0, err
	}
	return raw.Count, nil
}

func (s *Syncer) CleanupLiveViewers(ctx context.Context, cutoff time.Time) error {
	_, err := s.CallProcedure(ctx, "satiksmebot_service_cleanup_live_viewers", []any{cutoff.UTC().Format(time.RFC3339)})
	return err
}

func (s *Syncer) CallProcedure(ctx context.Context, name string, args []any) (any, error) {
	token, err := s.IssueToken(time.Now().UTC(), TokenOptions{})
	if err != nil {
		return nil, err
	}
	return s.CallProcedureWithToken(ctx, name, args, token)
}

func (s *Syncer) CallProcedureWithToken(ctx context.Context, name string, args []any, token string) (any, error) {
	canonical := canonicalProcedureName(name)
	if canonical == "" {
		return nil, fmt.Errorf("spacetime procedure name is required")
	}
	payload, err, _ := s.callJSONProcedureExactWithToken(ctx, canonical, args, token)
	if err != nil {
		return nil, err
	}
	return payload, nil
}

func (s *Syncer) SQL(ctx context.Context, query string) ([]SQLStatementResult, error) {
	token, err := s.IssueToken(time.Now().UTC(), TokenOptions{})
	if err != nil {
		return nil, err
	}
	return s.SQLWithToken(ctx, query, token)
}

func (s *Syncer) SQLWithToken(ctx context.Context, query string, token string) ([]SQLStatementResult, error) {
	requestURL := fmt.Sprintf("%s/v1/database/%s/sql", s.baseURL, url.PathEscape(s.database))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, strings.NewReader(strings.TrimSpace(query)))
	if err != nil {
		return nil, fmt.Errorf("build spacetime sql request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call spacetime sql: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read spacetime sql response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("spacetime sql failed: %s", strings.TrimSpace(string(body)))
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var payload []SQLStatementResult
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode spacetime sql response: %w", err)
	}
	return payload, nil
}

func (s *Syncer) IssueToken(now time.Time, opts TokenOptions) (string, error) {
	return s.issuer.issueWith(now, opts)
}

func (s *Syncer) callJSONProcedureExactWithToken(ctx context.Context, name string, args []any, token string) (any, error, bool) {
	body, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("marshal spacetime args: %w", err), false
	}
	requestURL := fmt.Sprintf("%s/v1/database/%s/call/%s", s.baseURL, url.PathEscape(s.database), url.PathEscape(strings.TrimSpace(name)))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build spacetime request: %w", err), false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call spacetime procedure %s: %w", name, err), false
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read spacetime response %s: %w", name, err), false
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("spacetime procedure %s failed: %s", name, strings.TrimSpace(string(responseBody))), missingProcedureResponse(resp.StatusCode, responseBody)
	}
	payload, err := decodeProcedureResponseBody(responseBody)
	if err != nil {
		return nil, fmt.Errorf("decode spacetime response %s: %w", name, err), false
	}
	return payload, nil, false
}

func (i *serviceTokenIssuer) issueWith(now time.Time, opts TokenOptions) (string, error) {
	subject := strings.TrimSpace(opts.Subject)
	if subject == "" {
		subject = i.subject
	}
	roles := append([]string(nil), opts.Roles...)
	if len(roles) == 0 {
		roles = append([]string(nil), i.roles...)
	}
	expiresAt := now.UTC().Add(i.tokenTTL)
	claims := map[string]any{
		"iss":   i.issuer,
		"sub":   subject,
		"aud":   []string{i.audience},
		"iat":   now.UTC().Unix(),
		"nbf":   now.UTC().Unix(),
		"exp":   expiresAt.Unix(),
		"jti":   randomTokenID(),
		"roles": roles,
	}
	for key, value := range opts.Claims {
		claims[key] = value
	}
	return signClaims(i.privateKey, i.keyID, claims)
}

func loadRSAPrivateKey(path string) (*rsa.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Spacetime private key: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("decode Spacetime private key: invalid PEM")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS#1 Spacetime private key: %w", err)
		}
		return key, nil
	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS#8 Spacetime private key: %w", err)
		}
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("Spacetime private key must be RSA")
		}
		return rsaKey, nil
	default:
		return nil, fmt.Errorf("unsupported Spacetime private key type %q", block.Type)
	}
}

func keyIDForPublicKey(publicKey *rsa.PublicKey) string {
	sum := sha256.Sum256(x509.MarshalPKCS1PublicKey(publicKey))
	return hex.EncodeToString(sum[:8])
}

func signClaims(privateKey *rsa.PrivateKey, keyID string, claims map[string]any) (string, error) {
	headerJSON, err := json.Marshal(map[string]any{
		"typ": "JWT",
		"alg": "RS256",
		"kid": keyID,
	})
	if err != nil {
		return "", fmt.Errorf("marshal JWT header: %w", err)
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal JWT claims: %w", err)
	}
	header := base64.RawURLEncoding.EncodeToString(headerJSON)
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := header + "." + payload
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func validateProcedureJSON(value string) error {
	var payload any
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return fmt.Errorf("decode procedure JSON payload: %w", err)
	}
	return nil
}

func decodeProcedureResponseBody(responseBody []byte) (any, error) {
	if len(bytes.TrimSpace(responseBody)) == 0 {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.UseNumber()
	var payload any
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	if text, ok := payload.(string); ok {
		if err := validateProcedureJSON(text); err != nil {
			return nil, err
		}
		var nested any
		nestedDecoder := json.NewDecoder(strings.NewReader(text))
		nestedDecoder.UseNumber()
		if err := nestedDecoder.Decode(&nested); err == nil {
			return nested, nil
		}
	}
	return payload, nil
}

func decodeInto(payload any, target any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	return nil
}

func sqlRows(results []SQLStatementResult) ([]map[string]any, error) {
	out := make([]map[string]any, 0)
	for _, result := range results {
		rows, err := sqlStatementRows(result)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

func sqlStatementRows(result SQLStatementResult) ([]map[string]any, error) {
	names, err := sqlColumnNames(result.Schema)
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, len(result.Rows))
	for _, row := range result.Rows {
		item := make(map[string]any, len(names))
		for index, name := range names {
			if index < len(row) {
				item[name] = row[index]
				continue
			}
			item[name] = nil
		}
		rows = append(rows, item)
	}
	return rows, nil
}

func sqlColumnNames(schema map[string]any) ([]string, error) {
	if len(schema) == 0 {
		return nil, nil
	}
	rawElements, ok := schema["elements"]
	if !ok {
		return nil, nil
	}
	elements, ok := rawElements.([]any)
	if !ok {
		return nil, fmt.Errorf("decode spacetime sql schema: unexpected elements payload %T", rawElements)
	}
	names := make([]string, 0, len(elements))
	for index, rawElement := range elements {
		element, ok := rawElement.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("decode spacetime sql schema element: unexpected element payload %T", rawElement)
		}
		name := fmt.Sprintf("col_%d", index)
		switch rawName := element["name"].(type) {
		case string:
			if strings.TrimSpace(rawName) != "" {
				name = strings.TrimSpace(rawName)
			}
		case map[string]any:
			if rawSome, ok := rawName["some"].(string); ok && strings.TrimSpace(rawSome) != "" {
				name = strings.TrimSpace(rawSome)
			}
		}
		names = append(names, name)
	}
	return names, nil
}

func sqlRowTime(row map[string]any, key string) (time.Time, error) {
	raw := strings.TrimSpace(fmt.Sprint(row[key]))
	if raw == "" || raw == "<nil>" {
		return time.Time{}, fmt.Errorf("decode spacetime sql row %s: missing timestamp", key)
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("decode spacetime sql row %s: %w", key, err)
	}
	return value, nil
}

func batchBundleStops(items []model.Stop, batchSize int) [][]model.Stop {
	if batchSize <= 0 || len(items) == 0 {
		return nil
	}
	out := make([][]model.Stop, 0, (len(items)+batchSize-1)/batchSize)
	for start := 0; start < len(items); start += batchSize {
		end := start + batchSize
		if end > len(items) {
			end = len(items)
		}
		out = append(out, items[start:end])
	}
	return out
}

func batchBundleRoutes(items []model.Route, batchSize int) [][]model.Route {
	if batchSize <= 0 || len(items) == 0 {
		return nil
	}
	out := make([][]model.Route, 0, (len(items)+batchSize-1)/batchSize)
	for start := 0; start < len(items); start += batchSize {
		end := start + batchSize
		if end > len(items) {
			end = len(items)
		}
		out = append(out, items[start:end])
	}
	return out
}

func mustJSON(value any) string {
	body, _ := json.Marshal(value)
	return string(body)
}

func randomTokenID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("satiksme-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw)
}

func JWKSRSA(publicKey *rsa.PublicKey, keyID string) map[string]any {
	return map[string]any{
		"kty": "RSA",
		"use": "sig",
		"alg": "RS256",
		"kid": keyID,
		"n":   base64.RawURLEncoding.EncodeToString(publicKey.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(publicKey.E)).Bytes()),
	}
}
