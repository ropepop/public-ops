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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const callAttempts = 4

type Config struct {
	Host        string
	Database    string
	BearerToken string
	KeyFile     string
	Issuer      string
	Audience    string
	Subject     string
	Roles       []string
	TokenTTL    time.Duration
	HTTPTimeout time.Duration
	ServiceName string
	Role        string
}

type Client struct {
	cfg        Config
	httpClient *http.Client
	issuer     *serviceTokenIssuer
	identityMu sync.Mutex
	identity   string
}

type Job struct {
	ID                 string    `json:"id"`
	TelegramChatIDHash string    `json:"telegramChatIdHash,omitempty"`
	TelegramUserIDHash string    `json:"telegramUserIdHash,omitempty"`
	Status             string    `json:"status"`
	ProjectName        string    `json:"projectName,omitempty"`
	PublicStatus       string    `json:"publicStatus"`
	ActiveAttemptID    string    `json:"activeAttemptId,omitempty"`
	ClaimedBy          string    `json:"claimedBy,omitempty"`
	BackendID          string    `json:"backendId,omitempty"`
	ResultRef          string    `json:"resultRef,omitempty"`
	FailureCode        string    `json:"failureCode,omitempty"`
	CancelRequested    bool      `json:"cancelRequested,omitempty"`
	CreatedAt          time.Time `json:"createdAt,omitempty"`
	UpdatedAt          time.Time `json:"updatedAt,omitempty"`
}

type Notification struct {
	ID             string `json:"id"`
	TelegramChatID string `json:"telegramChatId"`
	TelegramUserID string `json:"telegramUserId"`
	Status         string `json:"status"`
	PublicStatus   string `json:"publicStatus"`
	ResultText     string `json:"resultText,omitempty"`
	FailureCode    string `json:"failureCode,omitempty"`
}

type EventInput struct {
	Component       string
	Level           string
	Kind            string
	JobID           string
	AttemptID       string
	PublicText      string
	SafeDetailsJSON string
	Retention       time.Duration
}

func New(cfg Config) (*Client, error) {
	cfg.Host = strings.TrimRight(strings.TrimSpace(cfg.Host), "/")
	cfg.Database = strings.TrimSpace(cfg.Database)
	cfg.BearerToken = strings.TrimSpace(cfg.BearerToken)
	cfg.KeyFile = strings.TrimSpace(cfg.KeyFile)
	cfg.ServiceName = strings.TrimSpace(cfg.ServiceName)
	cfg.Role = strings.TrimSpace(cfg.Role)
	if cfg.Host == "" {
		return nil, fmt.Errorf("SpacetimeDB host is required")
	}
	if cfg.Database == "" {
		return nil, fmt.Errorf("SpacetimeDB database is required")
	}
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = 10 * time.Second
	}
	var issuer *serviceTokenIssuer
	if cfg.BearerToken == "" {
		if cfg.KeyFile == "" {
			return nil, fmt.Errorf("SpacetimeDB bearer token or private key file is required")
		}
		privateKey, err := loadRSAPrivateKey(cfg.KeyFile)
		if err != nil {
			return nil, err
		}
		tokenTTL := cfg.TokenTTL
		if tokenTTL == 0 {
			tokenTTL = 5 * time.Minute
		}
		issuer = &serviceTokenIssuer{
			issuer:     nonEmpty(cfg.Issuer, "chatgpt-broker-runtime"),
			audience:   nonEmpty(cfg.Audience, "spacetimedb"),
			subject:    nonEmpty(cfg.Subject, "service:chatgpt-broker"),
			roles:      append([]string(nil), cfg.Roles...),
			tokenTTL:   tokenTTL,
			keyID:      keyIDForPublicKey(&privateKey.PublicKey),
			privateKey: privateKey,
		}
		if len(issuer.roles) == 0 {
			issuer.roles = []string{"chatgptbroker_" + nonEmpty(cfg.Role, "broker")}
		}
	}
	return &Client{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: cfg.HTTPTimeout},
		issuer:     issuer,
	}, nil
}

func (c *Client) Register(ctx context.Context) error {
	if c == nil || c.cfg.ServiceName == "" || c.cfg.Role == "" {
		return nil
	}
	_, err := c.Call(ctx, "chatgptbroker_register_service_identity", []any{c.cfg.ServiceName, c.cfg.Role})
	return err
}

func (c *Client) SubmitJob(ctx context.Context, id, chatID, userID, chatHash, userHash, project, prompt string, retention time.Duration) (Job, error) {
	retentionMillis := retention.Milliseconds()
	if retentionMillis <= 0 {
		retentionMillis = int64((24 * time.Hour) / time.Millisecond)
	}
	_, err := c.Call(ctx, "chatgptbroker_submit_job", []any{
		id,
		chatID,
		userID,
		chatHash,
		userHash,
		project,
		prompt,
		uint64(retentionMillis),
	})
	if err != nil {
		return Job{}, err
	}
	job, ok, err := c.GetJob(ctx, id)
	if err != nil || !ok {
		return Job{ID: id, Status: "queued", PublicStatus: "Queued", ProjectName: project}, err
	}
	return job, nil
}

func (c *Client) RequestCancel(ctx context.Context, id string) (Job, error) {
	_, err := c.Call(ctx, "chatgptbroker_request_cancel", []any{id})
	if err != nil {
		return Job{}, err
	}
	job, ok, err := c.GetJob(ctx, id)
	if err != nil {
		return Job{}, err
	}
	if !ok {
		return Job{}, fmt.Errorf("job not found")
	}
	return job, nil
}

func (c *Client) MarkNotified(ctx context.Context, jobID string) error {
	_, err := c.Call(ctx, "chatgptbroker_mark_notified", []any{jobID})
	return err
}

func (c *Client) RecordEvent(ctx context.Context, input EventInput) error {
	retentionMillis := input.Retention.Milliseconds()
	if retentionMillis <= 0 {
		retentionMillis = int64((24 * time.Hour) / time.Millisecond)
	}
	_, err := c.Call(ctx, "chatgptbroker_record_event", []any{
		input.Component,
		input.Level,
		input.Kind,
		input.JobID,
		input.AttemptID,
		input.PublicText,
		input.SafeDetailsJSON,
		uint64(retentionMillis),
	})
	return err
}

func (c *Client) ListJobs(ctx context.Context) ([]Job, error) {
	rows, err := c.Query(ctx, "SELECT * FROM chatgptbroker_job")
	if err != nil {
		return nil, err
	}
	jobs := make([]Job, 0, len(rows))
	for _, row := range rows {
		jobs = append(jobs, jobFromRow(row))
	}
	sort.SliceStable(jobs, func(i, j int) bool {
		return jobs[i].CreatedAt.After(jobs[j].CreatedAt)
	})
	return jobs, nil
}

func (c *Client) GetJob(ctx context.Context, id string) (Job, bool, error) {
	rows, err := c.Query(ctx, "SELECT * FROM chatgptbroker_job WHERE id = '"+sqlQuote(id)+"'")
	if err != nil {
		return Job{}, false, err
	}
	if len(rows) == 0 {
		return Job{}, false, nil
	}
	return jobFromRow(rows[0]), true, nil
}

func (c *Client) Notifications(ctx context.Context) ([]Notification, error) {
	rows, err := c.Query(ctx, "SELECT * FROM chatgptbroker_bot_notifications")
	if err != nil {
		return nil, err
	}
	out := make([]Notification, 0, len(rows))
	for _, row := range rows {
		out = append(out, Notification{
			ID:             stringValue(row["id"]),
			TelegramChatID: stringValue(row["telegramChatId"]),
			TelegramUserID: stringValue(row["telegramUserId"]),
			Status:         stringValue(row["status"]),
			PublicStatus:   stringValue(row["publicStatus"]),
			ResultText:     stringValue(row["resultText"]),
			FailureCode:    stringValue(row["failureCode"]),
		})
	}
	return out, nil
}

func (c *Client) Query(ctx context.Context, query string) ([]map[string]any, error) {
	token, err := c.token(time.Now())
	if err != nil {
		return nil, err
	}
	requestURL := fmt.Sprintf("%s/v1/database/%s/sql", c.cfg.Host, url.PathEscape(c.cfg.Database))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, strings.NewReader(query))
	if err != nil {
		return nil, fmt.Errorf("build SpacetimeDB SQL request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call SpacetimeDB SQL: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read SpacetimeDB SQL response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, HTTPError{Call: "sql", Status: resp.StatusCode, Detail: sanitizeResponseDetail(body)}
	}
	return decodeSQLRows(body)
}

func (c *Client) Call(ctx context.Context, reducer string, args []any) (any, error) {
	reducer = strings.TrimSpace(reducer)
	if reducer == "" {
		return nil, fmt.Errorf("reducer is required")
	}
	body, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("marshal SpacetimeDB args: %w", err)
	}
	token, err := c.token(time.Now())
	if err != nil {
		return nil, err
	}
	requestURL := fmt.Sprintf(
		"%s/v1/database/%s/call/%s",
		c.cfg.Host,
		url.PathEscape(c.cfg.Database),
		url.PathEscape(reducer),
	)
	var lastErr error
	for attempt := 1; attempt <= callAttempts; attempt++ {
		payload, err := c.callOnce(ctx, reducer, requestURL, token, body)
		if err == nil {
			return payload, nil
		}
		lastErr = err
		if attempt == callAttempts || !shouldRetry(err) || ctx.Err() != nil {
			break
		}
		if !sleepWithContext(ctx, time.Duration(attempt)*100*time.Millisecond) {
			break
		}
	}
	return nil, lastErr
}

func (c *Client) callOnce(ctx context.Context, reducer, requestURL, token string, body []byte) (any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build SpacetimeDB request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call SpacetimeDB %s: %w", reducer, err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read SpacetimeDB response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, HTTPError{Call: reducer, Status: resp.StatusCode, Detail: sanitizeResponseDetail(responseBody)}
	}
	if len(bytes.TrimSpace(responseBody)) == 0 {
		return nil, nil
	}
	var payload any
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode SpacetimeDB response: %w", err)
	}
	return payload, nil
}

type HTTPError struct {
	Call   string
	Status int
	Detail string
}

func (e HTTPError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("SpacetimeDB %s failed with HTTP %d", e.Call, e.Status)
	}
	return fmt.Sprintf("SpacetimeDB %s failed with HTTP %d: %s", e.Call, e.Status, e.Detail)
}

func shouldRetry(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var httpErr HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Status == http.StatusTooManyRequests || httpErr.Status >= http.StatusInternalServerError
	}
	return strings.Contains(err.Error(), "call SpacetimeDB ")
}

func sleepWithContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (c *Client) token(now time.Time) (string, error) {
	if token := strings.TrimSpace(c.cfg.BearerToken); token != "" {
		return token, nil
	}
	if c.issuer == nil {
		return "", fmt.Errorf("SpacetimeDB token issuer is not configured")
	}
	return c.issuer.issueWith(now)
}

func decodeSQLRows(body []byte) ([]map[string]any, error) {
	var statements []struct {
		Schema json.RawMessage   `json:"schema"`
		Rows   []json.RawMessage `json:"rows"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&statements); err != nil {
		return nil, fmt.Errorf("decode SpacetimeDB SQL response: %w", err)
	}
	var out []map[string]any
	for _, statement := range statements {
		fields := schemaFields(statement.Schema)
		for _, raw := range statement.Rows {
			var value any
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			if err := decoder.Decode(&value); err != nil {
				return nil, fmt.Errorf("decode SpacetimeDB SQL row: %w", err)
			}
			out = append(out, rowMap(fields, value))
		}
	}
	return out, nil
}

func schemaFields(raw json.RawMessage) []string {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	elements, ok := findElements(value)
	if !ok {
		return nil
	}
	fields := make([]string, 0, len(elements))
	for i, element := range elements {
		fields = append(fields, elementName(element, i))
	}
	return fields
}

func findElements(value any) ([]any, bool) {
	switch v := value.(type) {
	case map[string]any:
		if raw, ok := v["elements"].([]any); ok {
			return raw, true
		}
		for _, child := range v {
			if found, ok := findElements(child); ok {
				return found, true
			}
		}
	case []any:
		for _, child := range v {
			if found, ok := findElements(child); ok {
				return found, true
			}
		}
	}
	return nil, false
}

func elementName(value any, fallback int) string {
	m, _ := value.(map[string]any)
	name := unwrapValue(m["name"])
	if text, ok := name.(string); ok && strings.TrimSpace(text) != "" {
		return text
	}
	return fmt.Sprintf("field%d", fallback)
}

func rowMap(fields []string, value any) map[string]any {
	if raw, ok := value.(map[string]any); ok {
		out := map[string]any{}
		for key, item := range raw {
			out[key] = unwrapValue(item)
		}
		return out
	}
	values, ok := value.([]any)
	if !ok {
		values = []any{value}
	}
	out := map[string]any{}
	for i, item := range values {
		key := fmt.Sprintf("field%d", i)
		if i < len(fields) {
			key = fields[i]
		}
		out[key] = unwrapValue(item)
	}
	return out
}

func unwrapValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		if len(v) == 1 {
			for key, child := range v {
				switch strings.ToLower(key) {
				case "some", "string", "i64", "u64", "i32", "u32", "bool", "timestamp":
					return unwrapValue(child)
				case "none":
					return nil
				}
			}
		}
		out := map[string]any{}
		for key, child := range v {
			out[key] = unwrapValue(child)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = unwrapValue(v[i])
		}
		return out
	default:
		return value
	}
}

func jobFromRow(row map[string]any) Job {
	return Job{
		ID:                 stringValue(row["id"]),
		TelegramChatIDHash: stringValue(row["telegramChatIdHash"]),
		TelegramUserIDHash: stringValue(row["telegramUserIdHash"]),
		Status:             stringValue(row["status"]),
		ProjectName:        stringValue(row["projectName"]),
		PublicStatus:       stringValue(row["publicStatus"]),
		ActiveAttemptID:    stringValue(row["activeAttemptId"]),
		ClaimedBy:          stringValue(row["claimedBy"]),
		BackendID:          stringValue(row["backendId"]),
		ResultRef:          stringValue(row["resultRef"]),
		FailureCode:        stringValue(row["failureCode"]),
		CancelRequested:    boolValue(row["cancelRequested"]),
		CreatedAt:          timeValue(row["createdAt"]),
		UpdatedAt:          timeValue(row["updatedAt"]),
	}
}

func stringValue(value any) string {
	switch v := unwrapValue(value).(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case nil:
		return ""
	default:
		raw, _ := json.Marshal(v)
		return string(raw)
	}
}

func boolValue(value any) bool {
	switch v := unwrapValue(value).(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(v, "true") || v == "1"
	default:
		return false
	}
}

func timeValue(value any) time.Time {
	switch v := unwrapValue(value).(type) {
	case string:
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t
		}
		if micros, err := strconv.ParseInt(v, 10, 64); err == nil {
			return time.UnixMicro(micros).UTC()
		}
	case json.Number:
		if micros, err := v.Int64(); err == nil {
			return time.UnixMicro(micros).UTC()
		}
	case float64:
		return time.UnixMicro(int64(v)).UTC()
	case map[string]any:
		for _, key := range []string{"micros_since_unix_epoch", "microsSinceUnixEpoch", "timestamp"} {
			if raw, ok := v[key]; ok {
				return timeValue(raw)
			}
		}
	}
	return time.Time{}
}

func sqlQuote(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func sanitizeResponseDetail(raw []byte) string {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return ""
	}
	for _, marker := range []string{"\n\nStack backtrace:", "\nStack backtrace:", "Stack backtrace:"} {
		if index := strings.Index(text, marker); index >= 0 {
			text = text[:index]
			break
		}
	}
	text = strings.Join(strings.Fields(text), " ")
	const maxDetailLength = 240
	if len(text) > maxDetailLength {
		return strings.TrimSpace(text[:maxDetailLength]) + "..."
	}
	return text
}

func nonEmpty(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

type serviceTokenIssuer struct {
	issuer     string
	audience   string
	subject    string
	roles      []string
	tokenTTL   time.Duration
	keyID      string
	privateKey *rsa.PrivateKey
}

func (i *serviceTokenIssuer) issueWith(now time.Time) (string, error) {
	claims := map[string]any{
		"iss":   i.issuer,
		"sub":   i.subject,
		"aud":   []string{i.audience},
		"iat":   now.UTC().Unix(),
		"nbf":   now.UTC().Unix(),
		"jti":   randomTokenID(),
		"roles": i.roles,
	}
	if i.tokenTTL >= 0 {
		claims["exp"] = now.UTC().Add(i.tokenTTL).Unix()
	}
	return signClaims(i.privateKey, i.keyID, claims)
}

func loadRSAPrivateKey(path string) (*rsa.PrivateKey, error) {
	raw, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return nil, fmt.Errorf("read SpacetimeDB private key: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("decode SpacetimeDB private key: invalid PEM")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS1 SpacetimeDB private key: %w", err)
		}
		return key, nil
	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS8 SpacetimeDB private key: %w", err)
		}
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("SpacetimeDB private key is not RSA")
		}
		return rsaKey, nil
	default:
		return nil, fmt.Errorf("unsupported SpacetimeDB private key type %q", block.Type)
	}
}

func signClaims(privateKey *rsa.PrivateKey, keyID string, claims map[string]any) (string, error) {
	header := map[string]any{
		"alg": "RS256",
		"typ": "JWT",
	}
	if keyID != "" {
		header["kid"] = keyID
	}
	headerRaw, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	claimsRaw, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerRaw) + "." + base64.RawURLEncoding.EncodeToString(claimsRaw)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func keyIDForPublicKey(publicKey *rsa.PublicKey) string {
	digest := sha256.Sum256(x509.MarshalPKCS1PublicKey(publicKey))
	return hex.EncodeToString(digest[:8])
}

func randomTokenID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes[:])
}
