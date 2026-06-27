package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type AccessRemoteStore interface {
	Load(ctx context.Context) (AccessState, bool, error)
	Save(ctx context.Context, state AccessState) error
}

type SpacetimeAccessConfig struct {
	Host        string
	Database    string
	BearerToken string
	HTTPTimeout time.Duration
}

type SpacetimeAccessStore struct {
	baseURL  string
	database string
	token    string
	client   *http.Client
}

func NewSpacetimeAccessStore(cfg SpacetimeAccessConfig) (*SpacetimeAccessStore, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.Host), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("spacetime host is required")
	}
	database := strings.TrimSpace(cfg.Database)
	if database == "" {
		return nil, fmt.Errorf("spacetime database is required")
	}
	token := strings.TrimSpace(cfg.BearerToken)
	if token == "" {
		return nil, fmt.Errorf("spacetime bearer token is required")
	}
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = 10 * time.Second
	}
	return &SpacetimeAccessStore{
		baseURL:  baseURL,
		database: database,
		token:    token,
		client:   &http.Client{Timeout: cfg.HTTPTimeout},
	}, nil
}

func (s *SpacetimeAccessStore) Load(ctx context.Context) (AccessState, bool, error) {
	payload, err := s.callReducer(ctx, "rigassatiksmeqrbot_export_access_state", nil)
	if err != nil {
		return AccessState{}, false, err
	}
	if payload == nil {
		return AccessState{}, false, nil
	}
	if text, ok := payload.(string); ok {
		var decoded any
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.UseNumber()
		if err := decoder.Decode(&decoded); err != nil {
			return AccessState{}, false, err
		}
		payload = decoded
	}
	var wrapper struct {
		State *AccessState `json:"state"`
	}
	if err := decodeAccessPayload(payload, &wrapper); err == nil && wrapper.State != nil {
		return *wrapper.State, true, nil
	}
	var state AccessState
	if err := decodeAccessPayload(payload, &state); err != nil {
		return AccessState{}, false, err
	}
	if state.Version == 0 && len(state.Users) == 0 && len(state.Groups) == 0 && len(state.Chats) == 0 && len(state.Admins) == 0 {
		return AccessState{}, false, nil
	}
	return state, true, nil
}

func (s *SpacetimeAccessStore) Save(ctx context.Context, state AccessState) error {
	body, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = s.callReducer(ctx, "rigassatiksmeqrbot_import_access_state", []any{string(body)})
	return err
}

func (s *SpacetimeAccessStore) callReducer(ctx context.Context, name string, args []any) (any, error) {
	if args == nil {
		args = []any{}
	}
	body, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("marshal spacetime access args: %w", err)
	}
	requestURL := fmt.Sprintf("%s/v1/database/%s/call/%s", s.baseURL, url.PathEscape(s.database), url.PathEscape(strings.TrimSpace(name)))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build spacetime access request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.token)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call spacetime access reducer: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read spacetime access response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("spacetime access reducer %s failed with HTTP %d: %s", name, resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if len(bytes.TrimSpace(responseBody)) == 0 {
		return nil, nil
	}
	var raw any
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode spacetime access response: %w", err)
	}
	return raw, nil
}

func decodeAccessPayload(payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	return decoder.Decode(out)
}
