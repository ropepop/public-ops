package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	rsLoginStateIdle          = "idle"
	rsLoginStateWaitingForSMS = "waiting_for_sms"
	rsLoginStateSucceeded     = "succeeded"
	rsLoginStateFailed        = "failed"

	rsLoginFailurePhoneFieldMissing = "phone_field_missing"
	rsLoginFailureSMSFieldMissing   = "sms_field_missing"
	rsLoginFailureWrongSMSCode      = "wrong_sms_code"
	rsLoginFailureSMSTimeout        = "sms_timeout"
	rsLoginFailureSubmitFailed      = "submit_failed"
	rsLoginFailurePhoneUnreachable  = "phone_unavailable"
	rsLoginFailureTicketPreempted   = "ticket_preempted"
	rsLoginFailureCanceled          = "canceled"
	rsLoginFailureLoginUnreachable  = "login_unreachable"
	rsLoginFailureNoActiveLogin     = "no_active_login"

	rsLoginSMSTimeout     = 90 * time.Second
	rsLoginPhoneMinDigits = 6
	rsLoginPhoneMaxDigits = 16
	rsLoginCodeMinLen     = 4
	rsLoginCodeMaxLen     = 64
	rsLoginMaxSMSAttempts = 1

	rsLoginPollInterval = 1 * time.Second
	rsLoginHTTPTimeout  = 5 * time.Second
)

type RSLoginJob struct {
	ID            string
	Phone         string
	PhoneLast4    string
	State         string
	FailureReason string
	SMSAttempts   int
	StartedAt     time.Time
	CompletedAt   time.Time
	PhoneLocale   string
	StateChanged  chan struct{}
}

type RSLoginPublicSnapshot struct {
	State         string `json:"state"`
	PhoneLast4    string `json:"phoneLast4,omitempty"`
	StartedAt     string `json:"startedAt,omitempty"`
	CompletedAt   string `json:"completedAt,omitempty"`
	FailureReason string `json:"failureReason,omitempty"`
	SMSAttempts   int    `json:"smsAttempts,omitempty"`
}

type RSLoginAnalytics struct {
	Attempts          int            `json:"attempts"`
	Successes         int            `json:"successes"`
	Failures          int            `json:"failures"`
	FailureByReason   map[string]int `json:"failureByReason,omitempty"`
	LastState         string         `json:"lastState,omitempty"`
	LastFailureReason string         `json:"lastFailureReason,omitempty"`
	LastPhoneLast4    string         `json:"lastPhoneLast4,omitempty"`
	LastAt            string         `json:"lastAt,omitempty"`
}

type rsLoginPixelStatus struct {
	State         string `json:"state"`
	RequestID     string `json:"requestId"`
	PhoneLast4    string `json:"phoneLast4"`
	FailureReason string `json:"failureReason"`
	StartedAtMillis int64 `json:"startedAtMillis"`
	CompletedAtMillis int64 `json:"completedAtMillis"`
	DurationMillis int64 `json:"durationMillis"`
	AwaitingSms   bool   `json:"awaitingSms"`
	Attempts      int64  `json:"attempts"`
	Successes     int64  `json:"successes"`
	Failures      int64  `json:"failures"`
	LastResult    string `json:"lastResult"`
}

func normalizeRSLoginPhoneLast4(phone string) string {
	clean := strings.Builder{}
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			clean.WriteRune(r)
		}
	}
	digits := clean.String()
	if len(digits) <= 4 {
		return digits
	}
	return digits[len(digits)-4:]
}

func isValidRSLoginPhone(phone string) bool {
	clean := strings.Builder{}
	for _, r := range phone {
		if (r >= '0' && r <= '9') || r == '+' || r == '-' || r == ' ' || r == '(' || r == ')' {
			clean.WriteRune(r)
		}
	}
	normalized := clean.String()
	digits := 0
	for _, r := range normalized {
		if r >= '0' && r <= '9' {
			digits++
		}
	}
	return digits >= rsLoginPhoneMinDigits && digits <= rsLoginPhoneMaxDigits
}

func isValidRSLoginCode(code string) bool {
	clean := strings.TrimSpace(code)
	if clean == "" {
		return false
	}
	runeLen := len([]rune(clean))
	return runeLen >= rsLoginCodeMinLen && runeLen <= rsLoginCodeMaxLen
}

func (b *Broker) StartRSLogin(ctx context.Context, phone string, now time.Time) (RSLoginPublicSnapshot, error) {
	if now.IsZero() {
		now = time.Now()
	}
	phone = strings.TrimSpace(phone)
	if !isValidRSLoginPhone(phone) {
		return RSLoginPublicSnapshot{}, fmt.Errorf("phone must contain between %d and %d digits", rsLoginPhoneMinDigits, rsLoginPhoneMaxDigits)
	}
	b.mu.Lock()
	if b.ticketBlocksQRLocked(now) {
		b.mu.Unlock()
		return RSLoginPublicSnapshot{}, fmt.Errorf("ticket page is active; retry after the ticket page is free")
	}
	if b.runningJobID != "" || len(b.runningJobIDs) > 0 {
		b.preemptRunningLocked(now, "login_preempted")
	}
	if b.rsLogin != nil && (b.rsLogin.State == rsLoginStateWaitingForSMS) {
		previous := b.rsLogin
		previous.State = rsLoginStateFailed
		previous.FailureReason = rsLoginFailureCanceled
		previous.CompletedAt = now
		b.recordRSLoginOutcomeLocked(previous)
		b.rsLogin = nil
	}
	job := &RSLoginJob{
		ID:           "rslogin-" + randomID(),
		Phone:        phone,
		PhoneLast4:   normalizeRSLoginPhoneLast4(phone),
		State:        rsLoginStateIdle,
		StartedAt:    now,
		StateChanged: make(chan struct{}, 1),
	}
	b.rsLogin = job
	b.rsLoginAttempts++
	out := b.publicRSLoginSnapshotLocked()
	_ = b.saveLocked()
	b.mu.Unlock()
	b.signalRunner()
	go b.runRSLogin(job)
	return out, nil
}

func (b *Broker) SubmitRSLoginSMS(ctx context.Context, code string, now time.Time) (RSLoginPublicSnapshot, error) {
	if now.IsZero() {
		now = time.Now()
	}
	code = strings.TrimSpace(code)
	if !isValidRSLoginCode(code) {
		return RSLoginPublicSnapshot{}, fmt.Errorf("code must be %d-%d characters", rsLoginCodeMinLen, rsLoginCodeMaxLen)
	}
	b.mu.Lock()
	if b.rsLogin == nil {
		b.mu.Unlock()
		return RSLoginPublicSnapshot{}, fmt.Errorf("no active RS login")
	}
	if b.rsLogin.State != rsLoginStateWaitingForSMS {
		b.mu.Unlock()
		return RSLoginPublicSnapshot{}, fmt.Errorf("login is not waiting for sms (state=%s)", b.rsLogin.State)
	}
	if b.rsLogin.SMSAttempts >= rsLoginMaxSMSAttempts {
		b.mu.Unlock()
		return RSLoginPublicSnapshot{}, fmt.Errorf("login has already used its single SMS attempt")
	}
	b.rsLogin.SMSAttempts++
	pending := b.rsLogin
	out := b.publicRSLoginSnapshotLocked()
	b.mu.Unlock()
	b.signalRunner()
	b.dispatchRSLoginSMS(pending, code)
	return out, nil
}

func (b *Broker) CancelRSLogin(ctx context.Context, now time.Time) (RSLoginPublicSnapshot, error) {
	if now.IsZero() {
		now = time.Now()
	}
	b.mu.Lock()
	if b.rsLogin == nil || b.rsLogin.State == rsLoginStateSucceeded || b.rsLogin.State == rsLoginStateFailed {
		snapshot := b.publicRSLoginSnapshotLocked()
		b.mu.Unlock()
		return snapshot, nil
	}
	job := b.rsLogin
	job.State = rsLoginStateFailed
	job.FailureReason = rsLoginFailureCanceled
	job.CompletedAt = now
	b.recordRSLoginOutcomeLocked(job)
	b.rsLogin = nil
	snapshot := b.publicRSLoginSnapshotLocked()
	_ = b.saveLocked()
	cancelCtx, cancelFn := context.WithTimeout(context.Background(), rsLoginHTTPTimeout)
	defer cancelFn()
	_ = b.postPixelJSON(cancelCtx, "/api/v1/rs/login/cancel", map[string]any{
		"type":      "cancel_rigassatiksme_login",
		"requestId": job.ID,
		"reason":    rsLoginFailureCanceled,
	})
	b.mu.Unlock()
	b.signalRunner()
	return snapshot, nil
}

func (b *Broker) RSLoginSnapshot() RSLoginPublicSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.publicRSLoginSnapshotLocked()
}

func (b *Broker) publicRSLoginSnapshotLocked() RSLoginPublicSnapshot {
	if b.rsLogin == nil {
		return RSLoginPublicSnapshot{State: rsLoginStateIdle}
	}
	job := b.rsLogin
	snapshot := RSLoginPublicSnapshot{
		State:         job.State,
		PhoneLast4:    job.PhoneLast4,
		SMSAttempts:   job.SMSAttempts,
		FailureReason: job.FailureReason,
	}
	if !job.StartedAt.IsZero() {
		snapshot.StartedAt = job.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	if !job.CompletedAt.IsZero() {
		snapshot.CompletedAt = job.CompletedAt.UTC().Format(time.RFC3339Nano)
	}
	return snapshot
}

func (b *Broker) recordRSLoginOutcomeLocked(job *RSLoginJob) {
	if job == nil {
		return
	}
	b.rsLoginLastState = job.State
	if job.State == rsLoginStateSucceeded {
		b.rsLoginSuccesses++
		b.rsLoginLastFailureReason = ""
	} else if job.State == rsLoginStateFailed {
		b.rsLoginFailures++
		reason := strings.TrimSpace(job.FailureReason)
		if reason == "" {
			reason = "unknown"
		}
		b.rsLoginFailureByReason[reason]++
		b.rsLoginLastFailureReason = reason
	}
	b.rsLoginLastAt = time.Now().UTC()
	if !job.CompletedAt.IsZero() {
		b.rsLoginLastAt = job.CompletedAt.UTC()
	}
	b.rsLoginLastPhoneLast4 = job.PhoneLast4
}

func (b *Broker) finalizeRSLoginLocked(now time.Time, state string, failureReason string) {
	job := b.rsLogin
	if job == nil {
		return
	}
	job.State = state
	job.FailureReason = failureReason
	job.CompletedAt = now
	b.recordRSLoginOutcomeLocked(job)
	if job.StateChanged != nil {
		select {
		case job.StateChanged <- struct{}{}:
		default:
		}
	}
}

func (b *Broker) runRSLogin(job *RSLoginJob) {
	ctx, cancel := context.WithTimeout(context.Background(), rsLoginSMSTimeout+30*time.Second)
	defer cancel()
	_ = b.startRSLoginOnPixel(ctx, job)

	b.mu.Lock()
	if b.rsLogin != nil && b.rsLogin.ID == job.ID {
		b.finalizeRSLoginLocked(time.Now(), rsLoginStateWaitingForSMS, "")
	}
	b.mu.Unlock()
	b.signalRunner()

	ticker := time.NewTicker(rsLoginPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			b.mu.Lock()
			if b.rsLogin != nil && b.rsLogin.ID == job.ID {
				b.finalizeRSLoginLocked(time.Now(), rsLoginStateFailed, rsLoginFailureSMSTimeout)
				b.rsLogin = nil
			}
			b.mu.Unlock()
			b.signalRunner()
			return
		case <-ticker.C:
			status, err := b.fetchRSLoginStatus(ctx)
			if err != nil {
				continue
			}
			if status == nil {
				continue
			}
			if status.RequestID != job.ID {
				continue
			}
			b.mu.Lock()
			if b.rsLogin == nil || b.rsLogin.ID != job.ID {
				b.mu.Unlock()
				return
			}
			switch status.State {
			case rsLoginStateSucceeded:
				b.finalizeRSLoginLocked(time.Now(), rsLoginStateSucceeded, "")
				b.rsLogin = nil
				b.mu.Unlock()
				b.signalRunner()
				return
			case rsLoginStateFailed:
				reason := strings.TrimSpace(status.FailureReason)
				if reason == "" {
					reason = rsLoginFailureLoginUnreachable
				}
				b.finalizeRSLoginLocked(time.Now(), rsLoginStateFailed, reason)
				b.rsLogin = nil
				b.mu.Unlock()
				b.signalRunner()
				return
			case rsLoginStateWaitingForSMS:
				if b.rsLogin.State != rsLoginStateWaitingForSMS {
					b.rsLogin.State = rsLoginStateWaitingForSMS
					if b.rsLogin.StateChanged != nil {
						select {
						case b.rsLogin.StateChanged <- struct{}{}:
						default:
						}
					}
				}
			}
			b.mu.Unlock()
			b.signalRunner()
		}
	}
}

func (b *Broker) startRSLoginOnPixel(ctx context.Context, job *RSLoginJob) error {
	startCtx, cancel := context.WithTimeout(ctx, rsLoginHTTPTimeout)
	defer cancel()
	body, _ := json.Marshal(map[string]any{
		"type":         "rigassatiksme_login_start",
		"requestId":    job.ID,
		"phone":        job.Phone,
		"locale":       job.PhoneLocale,
		"serverSentAt": time.Now().UTC().Format(time.RFC3339Nano),
	})
	return b.postPixelJSONRaw(startCtx, "/api/v1/rs/login/start", body)
}

func (b *Broker) fetchRSLoginStatus(ctx context.Context) (*rsLoginPixelStatus, error) {
	url, err := b.upstreamURL("/api/v1/rs/login/status")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: rsLoginHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch rs login status: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("rs login status status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read rs login status: %w", err)
	}
	var status rsLoginPixelStatus
	if err := json.Unmarshal(raw, &status); err != nil {
		return nil, fmt.Errorf("parse rs login status: %w", err)
	}
	return &status, nil
}

func (b *Broker) dispatchRSLoginSMS(job *RSLoginJob, code string) {
	smsCtx, cancel := context.WithTimeout(context.Background(), rsLoginHTTPTimeout)
	defer cancel()
	defer func() {
		for i := 0; i < len(code); i++ {
			codeBytes := []byte(code)
			codeBytes[i] = 0
		}
	}()
	body, _ := json.Marshal(map[string]any{
		"type":         "rigassatiksme_login_sms",
		"requestId":    job.ID,
		"code":         code,
		"serverSentAt": time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err := b.postPixelJSONRaw(smsCtx, "/api/v1/rs/login/sms", body); err != nil {
		log.Printf("phone broker: dispatch rs login sms failed: %v", err)
		b.mu.Lock()
		if b.rsLogin != nil && b.rsLogin.ID == job.ID {
			b.finalizeRSLoginLocked(time.Now(), rsLoginStateFailed, rsLoginFailureLoginUnreachable)
			b.rsLogin = nil
		}
		b.mu.Unlock()
		b.signalRunner()
	}
}

func (b *Broker) postPixelJSON(ctx context.Context, path string, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return b.postPixelJSONRaw(ctx, path, body)
}

func (b *Broker) postPixelJSONRaw(ctx context.Context, path string, body []byte) error {
	url, err := b.upstreamURL(path)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: rsLoginHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("post %s: %w", path, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("post %s status %d", path, resp.StatusCode)
	}
	return nil
}

func (b *Broker) upstreamURL(targetPath string) (string, error) {
	parsed, err := url.Parse(b.cfg.UpstreamBaseURL)
	if err != nil {
		return "", err
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + targetPath
	parsed.RawQuery = ""
	return parsed.String(), nil
}

func (b *Broker) ticketBlocksRSLoginLocked(now time.Time) bool {
	if b.ticketLeaseActiveLocked(now) {
		return true
	}
	if b.ticketViewers > 0 {
		return true
	}
	return !b.ticketGraceUntil.IsZero() && now.Before(b.ticketGraceUntil)
}

func (b *Broker) preemptRSLoginForTicketLocked(now time.Time) bool {
	if b.rsLogin == nil {
		return false
	}
	if b.rsLogin.State == rsLoginStateSucceeded || b.rsLogin.State == rsLoginStateFailed {
		return false
	}
	job := b.rsLogin
	job.State = rsLoginStateFailed
	job.FailureReason = rsLoginFailureTicketPreempted
	job.CompletedAt = now
	b.recordRSLoginOutcomeLocked(job)
	b.rsLogin = nil
	cancelCtx, cancelFn := context.WithTimeout(context.Background(), rsLoginHTTPTimeout)
	defer cancelFn()
	_ = b.postPixelJSON(cancelCtx, "/api/v1/rs/login/cancel", map[string]any{
		"type":      "cancel_rigassatiksme_login",
		"requestId": job.ID,
		"reason":    rsLoginFailureTicketPreempted,
	})
	return true
}
