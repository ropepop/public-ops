package qbit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"
)

const maxErrorBody = 512

type Torrent struct {
	Hash         string  `json:"hash"`
	Name         string  `json:"name"`
	Size         int64   `json:"size"`
	Completed    int64   `json:"completed"`
	AmountLeft   int64   `json:"amount_left"`
	Progress     float64 `json:"progress"`
	Ratio        float64 `json:"ratio"`
	State        string  `json:"state"`
	Tags         string  `json:"tags"`
	SavePath     string  `json:"save_path"`
	DownloadPath string  `json:"download_path"`
	AddedOn      int64   `json:"added_on"`
	CompletionOn int64   `json:"completion_on"`
}

type API interface {
	List(context.Context) ([]Torrent, error)
	Stop(context.Context, string) error
	Start(context.Context, string) error
	AddTags(context.Context, string, ...string) error
	RemoveTags(context.Context, string, ...string) error
	Delete(context.Context, string, bool) error
}

type Client struct {
	baseURL  *url.URL
	http     *http.Client
	username string
	password string
	authMu   sync.Mutex
}

func NewClient(rawURL, username, password string, timeout time.Duration) (*Client, error) {
	baseURL, err := url.Parse(strings.TrimRight(strings.TrimSpace(rawURL), "/"))
	if err != nil {
		return nil, fmt.Errorf("parse qBittorrent URL: %w", err)
	}
	if baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return nil, errors.New("qBittorrent URL must use http or https")
	}
	if baseURL.Host == "" {
		return nil, errors.New("qBittorrent URL must include a host")
	}
	if (username == "") != (password == "") {
		return nil, errors.New("qBittorrent username and password must be configured together")
	}
	if timeout <= 0 {
		return nil, errors.New("qBittorrent request timeout must be positive")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("create qBittorrent cookie jar: %w", err)
	}
	return &Client{
		baseURL:  baseURL,
		http:     &http.Client{Timeout: timeout, Jar: jar},
		username: username,
		password: password,
	}, nil
}

func (c *Client) List(ctx context.Context) ([]Torrent, error) {
	var torrents []Torrent
	err := c.do(ctx, http.MethodGet, "/api/v2/torrents/info", nil, &torrents)
	if err == nil || !c.authEnabled() || !isAuthenticationError(err) {
		return torrents, err
	}
	if err := c.login(ctx); err != nil {
		return nil, err
	}
	if err := c.do(ctx, http.MethodGet, "/api/v2/torrents/info", nil, &torrents); err != nil {
		return nil, err
	}
	return torrents, nil
}

func (c *Client) Stop(ctx context.Context, hash string) error {
	return c.mutate(ctx, "/api/v2/torrents/stop", url.Values{"hashes": {hash}})
}

func (c *Client) Start(ctx context.Context, hash string) error {
	return c.mutate(ctx, "/api/v2/torrents/start", url.Values{"hashes": {hash}})
}

func (c *Client) AddTags(ctx context.Context, hash string, tags ...string) error {
	if len(tags) == 0 {
		return nil
	}
	return c.mutate(ctx, "/api/v2/torrents/addTags", url.Values{
		"hashes": {hash},
		"tags":   {strings.Join(tags, ",")},
	})
}

func (c *Client) RemoveTags(ctx context.Context, hash string, tags ...string) error {
	if len(tags) == 0 {
		return nil
	}
	return c.mutate(ctx, "/api/v2/torrents/removeTags", url.Values{
		"hashes": {hash},
		"tags":   {strings.Join(tags, ",")},
	})
}

func (c *Client) Delete(ctx context.Context, hash string, deleteFiles bool) error {
	return c.mutate(ctx, "/api/v2/torrents/delete", url.Values{
		"hashes":      {hash},
		"deleteFiles": {fmt.Sprintf("%t", deleteFiles)},
	})
}

func (c *Client) mutate(ctx context.Context, path string, values url.Values) error {
	err := c.do(ctx, http.MethodPost, path, values, nil)
	if err == nil || !c.authEnabled() || !isAuthenticationError(err) {
		return err
	}
	if err := c.login(ctx); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, path, values, nil)
}

func (c *Client) login(ctx context.Context) error {
	if !c.authEnabled() {
		return nil
	}
	c.authMu.Lock()
	defer c.authMu.Unlock()

	values := url.Values{
		"username": {c.username},
		"password": {c.password},
	}
	request, err := c.newRequest(ctx, http.MethodPost, "/api/v2/auth/login", values)
	if err != nil {
		return err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("qBittorrent login request: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxErrorBody+1))
	if err != nil {
		return fmt.Errorf("read qBittorrent login response: %w", err)
	}
	if response.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "Ok." {
		return fmt.Errorf("qBittorrent login failed with status %d", response.StatusCode)
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, values url.Values, output any) error {
	request, err := c.newRequest(ctx, method, path, values)
	if err != nil {
		return err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("qBittorrent %s request: %w", path, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBody))
		return &httpStatusError{status: response.StatusCode, body: strings.TrimSpace(string(body))}
	}
	if output == nil {
		_, err := io.Copy(io.Discard, response.Body)
		if err != nil {
			return fmt.Errorf("read qBittorrent %s response: %w", path, err)
		}
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(output); err != nil {
		return fmt.Errorf("decode qBittorrent %s response: %w", path, err)
	}
	return nil
}

func (c *Client) newRequest(ctx context.Context, method, path string, values url.Values) (*http.Request, error) {
	target := *c.baseURL
	target.Path = strings.TrimRight(c.baseURL.Path, "/") + path
	var body io.Reader
	if method == http.MethodGet && len(values) > 0 {
		target.RawQuery = values.Encode()
	} else if len(values) > 0 {
		body = strings.NewReader(values.Encode())
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, fmt.Errorf("create qBittorrent request: %w", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	request.Header.Set("Referer", c.baseURL.String())
	request.Header.Set("Origin", c.baseURL.Scheme+"://"+c.baseURL.Host)
	return request, nil
}

func (c *Client) authEnabled() bool {
	return c.username != ""
}

type httpStatusError struct {
	status int
	body   string
}

func (e *httpStatusError) Error() string {
	if e.body == "" {
		return fmt.Sprintf("qBittorrent API returned status %d", e.status)
	}
	return fmt.Sprintf("qBittorrent API returned status %d: %s", e.status, e.body)
}

func isAuthenticationError(err error) bool {
	var statusErr *httpStatusError
	return errors.As(err, &statusErr) && (statusErr.status == http.StatusUnauthorized || statusErr.status == http.StatusForbidden)
}
