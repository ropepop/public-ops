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

type HTTPBroker struct {
	baseURL    string
	httpClient *http.Client
}

func NewHTTPBroker(baseURL string, timeout time.Duration) *HTTPBroker {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &HTTPBroker{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		httpClient: &http.Client{Timeout: timeout},
	}
}

func (b *HTTPBroker) CreateQRJob(ctx context.Context, chatID string, userID string, code string) (QRJob, error) {
	var out struct {
		OK  bool  `json:"ok"`
		Job QRJob `json:"job"`
	}
	err := b.doJSON(ctx, http.MethodPost, "/api/v1/qr/jobs", map[string]string{
		"chatId": chatID,
		"userId": userID,
		"code":   code,
	}, &out)
	return out.Job, err
}

func (b *HTTPBroker) Job(ctx context.Context, id string) (QRJob, error) {
	var out struct {
		OK  bool  `json:"ok"`
		Job QRJob `json:"job"`
	}
	err := b.doJSON(ctx, http.MethodGet, "/api/v1/qr/jobs/"+url.PathEscape(strings.TrimSpace(id)), nil, &out)
	return out.Job, err
}

func (b *HTTPBroker) LatestJob(ctx context.Context, userID string) (QRJob, bool, error) {
	var out struct {
		OK  bool  `json:"ok"`
		Job QRJob `json:"job"`
	}
	err := b.doJSON(ctx, http.MethodGet, "/api/v1/qr/jobs/latest?userId="+url.QueryEscape(strings.TrimSpace(userID)), nil, &out)
	if err != nil {
		if strings.Contains(err.Error(), "status 404") {
			return QRJob{}, false, nil
		}
		return QRJob{}, false, err
	}
	return out.Job, true, nil
}

func (b *HTTPBroker) CancelLatestJob(ctx context.Context, userID string) (QRJob, bool, error) {
	latest, ok, err := b.LatestJob(ctx, userID)
	if err != nil || !ok {
		return latest, ok, err
	}
	var out struct {
		OK  bool  `json:"ok"`
		Job QRJob `json:"job"`
	}
	err = b.doJSON(ctx, http.MethodPost, "/api/v1/qr/jobs/"+url.PathEscape(latest.ID)+"/cancel", nil, &out)
	return out.Job, true, err
}

func (b *HTTPBroker) JobImage(ctx context.Context, id string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.baseURL+"/api/v1/qr/jobs/"+url.PathEscape(strings.TrimSpace(id))+"/image", nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("broker image status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, "", err
	}
	return body, resp.Header.Get("Content-Type"), nil
}

func (b *HTTPBroker) StartRSLogin(ctx context.Context, phone string) (RSLoginSnapshot, error) {
	var out struct {
		OK      bool            `json:"ok"`
		RSLogin RSLoginSnapshot `json:"rsLogin"`
	}
	err := b.doJSON(ctx, http.MethodPost, "/api/v1/rs/login/start", map[string]string{"phone": phone}, &out)
	return out.RSLogin, err
}

func (b *HTTPBroker) SubmitRSLoginSMS(ctx context.Context, code string) (RSLoginSnapshot, error) {
	var out struct {
		OK      bool            `json:"ok"`
		RSLogin RSLoginSnapshot `json:"rsLogin"`
	}
	err := b.doJSON(ctx, http.MethodPost, "/api/v1/rs/login/sms", map[string]string{"code": code}, &out)
	return out.RSLogin, err
}

func (b *HTTPBroker) CancelRSLogin(ctx context.Context) (RSLoginSnapshot, error) {
	var out struct {
		OK      bool            `json:"ok"`
		RSLogin RSLoginSnapshot `json:"rsLogin"`
	}
	err := b.doJSON(ctx, http.MethodPost, "/api/v1/rs/login/cancel", nil, &out)
	return out.RSLogin, err
}

func (b *HTTPBroker) RSLoginStatus(ctx context.Context) (RSLoginSnapshot, error) {
	var out struct {
		OK      bool            `json:"ok"`
		RSLogin RSLoginSnapshot `json:"rsLogin"`
	}
	err := b.doJSON(ctx, http.MethodGet, "/api/v1/rs/login", nil, &out)
	return out.RSLogin, err
}

func (b *HTTPBroker) doJSON(ctx context.Context, method string, path string, payload any, out any) error {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, b.baseURL+path, body)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := b.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("broker %s status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
