package phone

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"
)

const (
	idleStopSettleDelay      = 200 * time.Millisecond
	controlKeepaliveInterval = 5 * time.Second

	DefaultRequestTimeout    = 10 * time.Second
	DefaultReconnectMinDelay = 500 * time.Millisecond
	DefaultReconnectMaxDelay = 5 * time.Second
	DefaultNoViewerStopDelay = 10 * time.Second
)

type RelayConfig struct {
	BackendID         string
	AttachName        string
	BaseURL           string
	RequestTimeout    time.Duration
	ReconnectMinDelay time.Duration
	ReconnectMaxDelay time.Duration
	NoViewerStopDelay time.Duration
	ReadLimit         int64
}

type Message struct {
	Text   []byte
	Binary []byte
}

type Health struct {
	BackendID   string `json:"backendId"`
	AttachName  string `json:"attachName"`
	BaseURL     string `json:"baseUrl"`
	Viewers     int    `json:"viewers"`
	Connected   bool   `json:"connected"`
	Desired     bool   `json:"desired"`
	LastError   string `json:"lastError,omitempty"`
	LastConfig  string `json:"lastConfig,omitempty"`
	LastSeenAt  string `json:"lastSeenAt,omitempty"`
	StreamState string `json:"streamState"`
}

type Relay struct {
	cfg RelayConfig

	mu           sync.Mutex
	writeMu      sync.Mutex
	videoWriteMu sync.Mutex
	viewers      int
	desired      bool
	connected    bool
	lastError    string
	lastConfig   string
	lastSeenAt   time.Time
	conn         *websocket.Conn
	videoConn    *websocket.Conn
	cancelLoop   context.CancelFunc
	idleStop     *time.Timer
	idleStopping bool
	idleStopDone chan struct{}
	onMessage    func(Message)
	onDisconnect func(error)
}

type relayDialResult struct {
	name string
	conn *websocket.Conn
	err  error
}

func NewRelay(cfg RelayConfig) *Relay {
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = DefaultRequestTimeout
	}
	if cfg.ReconnectMinDelay <= 0 {
		cfg.ReconnectMinDelay = DefaultReconnectMinDelay
	}
	if cfg.ReconnectMaxDelay <= 0 {
		cfg.ReconnectMaxDelay = DefaultReconnectMaxDelay
	}
	if cfg.NoViewerStopDelay < 0 {
		cfg.NoViewerStopDelay = DefaultNoViewerStopDelay
	}
	if cfg.ReadLimit <= 0 {
		cfg.ReadLimit = 32 << 20
	}
	return &Relay{cfg: cfg}
}

type Backend struct {
	ID         string
	AttachName string
	BaseURL    string
}

func (r *Relay) SetHandlers(onMessage func(Message), onDisconnect func(error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onMessage = onMessage
	r.onDisconnect = onDisconnect
}

func (r *Relay) AddViewer() {
	for {
		r.mu.Lock()
		if r.idleStopping {
			done := r.idleStopDone
			r.mu.Unlock()
			if done != nil {
				select {
				case <-done:
				case <-time.After(500 * time.Millisecond):
				}
			}
			continue
		}
		if r.idleStop != nil {
			r.idleStop.Stop()
			r.idleStop = nil
		}
		r.viewers++
		if !r.desired || (!r.connected && r.cancelLoop == nil) {
			r.desired = true
			ctx, cancel := context.WithCancel(context.Background())
			r.cancelLoop = cancel
			go r.connectLoop(ctx)
		}
		r.mu.Unlock()
		return
	}
}

func (r *Relay) EnsureActive(reason string) bool {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "ensure active"
	}
	var conn *websocket.Conn
	var videoConn *websocket.Conn
	var oldCancel context.CancelFunc
	var ctx context.Context
	r.mu.Lock()
	if r.viewers <= 0 {
		r.mu.Unlock()
		return false
	}
	if r.idleStop != nil {
		r.idleStop.Stop()
		r.idleStop = nil
	}
	if r.desired && r.connected && r.conn != nil && r.videoConn != nil {
		r.mu.Unlock()
		return true
	}
	if r.cancelLoop != nil {
		oldCancel = r.cancelLoop
		r.cancelLoop = nil
	}
	conn = r.conn
	videoConn = r.videoConn
	r.conn = nil
	r.videoConn = nil
	r.connected = false
	r.desired = true
	r.lastError = reason
	var cancel context.CancelFunc
	ctx, cancel = context.WithCancel(context.Background())
	r.cancelLoop = cancel
	r.mu.Unlock()

	if conn != nil {
		_ = conn.Close(websocket.StatusInternalError, reason)
	}
	if videoConn != nil {
		_ = videoConn.Close(websocket.StatusInternalError, reason)
	}
	if oldCancel != nil {
		oldCancel()
	}
	go r.connectLoop(ctx)
	return true
}

func (r *Relay) RemoveViewer() {
	r.mu.Lock()
	if r.viewers > 0 {
		r.viewers--
	}
	if r.viewers == 0 && r.desired {
		if r.idleStop != nil {
			r.idleStop.Stop()
			r.idleStop = nil
		}
		if r.cfg.NoViewerStopDelay == 0 {
			go r.stopIfStillIdle()
		} else {
			r.idleStop = time.AfterFunc(r.cfg.NoViewerStopDelay, r.stopIfStillIdle)
		}
	}
	r.mu.Unlock()
}

func (r *Relay) stopIfStillIdle() {
	r.mu.Lock()
	if r.viewers > 0 || !r.desired {
		r.idleStop = nil
		r.mu.Unlock()
		return
	}
	r.idleStop = nil
	done := make(chan struct{})
	r.idleStopping = true
	r.idleStopDone = done
	r.desired = false
	cancelLoop := r.cancelLoop
	if r.cancelLoop != nil {
		r.cancelLoop = nil
	}
	conn := r.conn
	videoConn := r.videoConn
	r.conn = nil
	r.videoConn = nil
	r.connected = false
	r.mu.Unlock()
	if conn != nil {
		r.sendIdleStopOnControlConn(conn)
	}
	if conn != nil {
		_ = conn.Close(websocket.StatusNormalClosure, "no viewers")
	}
	if videoConn != nil {
		_ = videoConn.Close(websocket.StatusNormalClosure, "no viewers")
	}
	if cancelLoop != nil {
		cancelLoop()
	}
	time.Sleep(idleStopSettleDelay)
	r.finishIdleStop(done)
}

func (r *Relay) finishIdleStop(done chan struct{}) {
	r.mu.Lock()
	if r.idleStopDone == done {
		r.idleStopping = false
		r.idleStopDone = nil
	}
	close(done)
	r.mu.Unlock()
}

func (r *Relay) sendIdleStopOnControlConn(conn *websocket.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	body, err := json.Marshal(map[string]any{
		"type":     "stop",
		"reason":   "relay_no_viewers",
		"explicit": true,
	})
	if err != nil {
		return
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	if err := conn.Write(ctx, websocket.MessageText, body); err != nil {
		log.Printf("ticket phone relay: websocket idle stop failed: %v", err)
	}
}

func (r *Relay) Close() {
	r.mu.Lock()
	if r.idleStop != nil {
		r.idleStop.Stop()
		r.idleStop = nil
	}
	if r.cancelLoop != nil {
		r.cancelLoop()
		r.cancelLoop = nil
	}
	conn := r.conn
	videoConn := r.videoConn
	r.conn = nil
	r.videoConn = nil
	r.connected = false
	r.desired = false
	r.viewers = 0
	r.mu.Unlock()
	if conn != nil {
		_ = conn.Close(websocket.StatusNormalClosure, "relay closed")
	}
	if videoConn != nil {
		_ = videoConn.Close(websocket.StatusNormalClosure, "relay closed")
	}
	r.stopPhoneSession()
}

func (r *Relay) StartPhoneSession(ctx context.Context) error {
	r.mu.Lock()
	base := r.cfg.BaseURL
	timeout := r.cfg.RequestTimeout
	r.mu.Unlock()
	return r.startPhoneSessionAt(ctx, base, timeout)
}

func (r *Relay) SwitchBackend(backend Backend) {
	cleanBaseURL := strings.TrimRight(strings.TrimSpace(backend.BaseURL), "/")
	r.mu.Lock()
	oldBaseURL := r.cfg.BaseURL
	same := r.cfg.BackendID == strings.TrimSpace(backend.ID) && r.cfg.BaseURL == cleanBaseURL
	if same {
		r.cfg.AttachName = strings.TrimSpace(backend.AttachName)
		r.mu.Unlock()
		return
	}
	if r.idleStop != nil {
		r.idleStop.Stop()
		r.idleStop = nil
	}
	if r.cancelLoop != nil {
		r.cancelLoop()
		r.cancelLoop = nil
	}
	conn := r.conn
	videoConn := r.videoConn
	shouldReconnect := r.desired && r.viewers > 0
	r.conn = nil
	r.videoConn = nil
	r.connected = false
	r.lastError = ""
	r.lastConfig = ""
	r.lastSeenAt = time.Time{}
	r.cfg.BackendID = strings.TrimSpace(backend.ID)
	r.cfg.AttachName = strings.TrimSpace(backend.AttachName)
	r.cfg.BaseURL = cleanBaseURL
	var ctx context.Context
	if shouldReconnect {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(context.Background())
		r.cancelLoop = cancel
	}
	r.mu.Unlock()

	if conn != nil {
		_ = conn.Close(websocket.StatusNormalClosure, "phone backend switched")
	}
	if videoConn != nil {
		_ = videoConn.Close(websocket.StatusNormalClosure, "phone backend switched")
	}
	r.stopPhoneSessionAt(oldBaseURL)
	r.mu.Lock()
	if r.cfg.BaseURL == cleanBaseURL {
		r.lastError = ""
	}
	r.mu.Unlock()
	if shouldReconnect {
		go r.connectLoop(ctx)
	}
}

func (r *Relay) SendJSON(ctx context.Context, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return r.SendText(ctx, body)
}

func (r *Relay) SendControlExit(ctx context.Context, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "control_session_ended"
	}
	return r.SendJSON(ctx, map[string]any{
		"type":   "control_exit",
		"reason": reason,
	})
}

func (r *Relay) SendText(ctx context.Context, body []byte) error {
	r.mu.Lock()
	conn := r.conn
	connected := r.connected
	r.mu.Unlock()
	if conn == nil || !connected {
		return fmt.Errorf("phone stream is not connected")
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	return conn.Write(ctx, websocket.MessageText, body)
}

func (r *Relay) Snapshot() Health {
	r.mu.Lock()
	defer r.mu.Unlock()
	lastSeenAt := ""
	if !r.lastSeenAt.IsZero() {
		lastSeenAt = r.lastSeenAt.UTC().Format(time.RFC3339)
	}
	streamState := "idle"
	if r.desired {
		streamState = "connecting"
	}
	if r.connected {
		streamState = "streaming"
	}
	return Health{
		BackendID:   r.cfg.BackendID,
		AttachName:  r.cfg.AttachName,
		BaseURL:     r.cfg.BaseURL,
		Viewers:     r.viewers,
		Connected:   r.connected,
		Desired:     r.desired,
		LastError:   r.lastError,
		LastConfig:  r.lastConfig,
		LastSeenAt:  lastSeenAt,
		StreamState: streamState,
	}
}

func (r *Relay) connectLoop(ctx context.Context) {
	delay := r.cfg.ReconnectMinDelay
	for {
		if ctx.Err() != nil || !r.shouldRun() {
			return
		}
		err := r.connectOnce(ctx)
		if ctx.Err() != nil || !r.shouldRun() {
			return
		}
		if err != nil {
			r.recordError(err)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay *= 2
		if delay > r.cfg.ReconnectMaxDelay {
			delay = r.cfg.ReconnectMaxDelay
		}
	}
}

func (r *Relay) connectOnce(ctx context.Context) (retErr error) {
	controlURL, err := r.websocketURL("/api/v1/session")
	if err != nil {
		return err
	}
	videoURL, err := r.websocketURL("/api/v1/stream")
	if err != nil {
		return err
	}
	dialCtx, cancel := context.WithTimeout(ctx, r.cfg.RequestTimeout)
	defer cancel()
	dialResults := make(chan relayDialResult, 2)
	go r.dialPhoneWebsocket(dialCtx, "control", controlURL, dialResults)
	go r.dialPhoneWebsocket(dialCtx, "video", videoURL, dialResults)

	var controlConn *websocket.Conn
	var videoConn *websocket.Conn
	for i := 0; i < 2; i++ {
		select {
		case <-ctx.Done():
			if controlConn != nil {
				_ = controlConn.Close(websocket.StatusInternalError, "dial cancelled")
			}
			if videoConn != nil {
				_ = videoConn.Close(websocket.StatusInternalError, "dial cancelled")
			}
			return ctx.Err()
		case result := <-dialResults:
			if result.err != nil {
				if controlConn != nil {
					_ = controlConn.Close(websocket.StatusInternalError, "peer dial failed")
				}
				if videoConn != nil {
					_ = videoConn.Close(websocket.StatusInternalError, "peer dial failed")
				}
				return result.err
			}
			switch result.name {
			case "control":
				controlConn = result.conn
			case "video":
				videoConn = result.conn
			}
		}
	}
	controlConn.SetReadLimit(r.cfg.ReadLimit)
	videoConn.SetReadLimit(r.cfg.ReadLimit)
	r.mu.Lock()
	if !r.desired {
		r.mu.Unlock()
		_ = controlConn.Close(websocket.StatusNormalClosure, "relay no longer desired")
		_ = videoConn.Close(websocket.StatusNormalClosure, "relay no longer desired")
		return nil
	}
	r.conn = controlConn
	r.videoConn = videoConn
	r.connected = true
	r.lastError = ""
	r.lastSeenAt = time.Now()
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		wasCurrent := r.conn == controlConn || r.videoConn == videoConn
		if r.conn == controlConn {
			r.conn = nil
		}
		if r.videoConn == videoConn {
			r.videoConn = nil
		}
		if wasCurrent {
			r.connected = false
		}
		onDisconnect := r.onDisconnect
		r.mu.Unlock()
		_ = controlConn.Close(websocket.StatusNormalClosure, "phone relay reconnect")
		_ = videoConn.Close(websocket.StatusNormalClosure, "phone relay reconnect")
		if onDisconnect != nil {
			onDisconnect(retErr)
		}
	}()
	if err := r.SendJSON(ctx, map[string]any{"type": "start", "reason": "relay_websocket_start"}); err != nil {
		return fmt.Errorf("start phone stream: %w", err)
	}
	if err := r.sendVideoJSON(ctx, map[string]any{"type": "keyframe", "reason": "relay_video_join"}); err != nil {
		return fmt.Errorf("request phone keyframe: %w", err)
	}
	errCh := make(chan error, 2)
	go r.controlKeepaliveLoop(ctx, controlConn)
	go func() { errCh <- r.readLoop(ctx, controlConn) }()
	go func() { errCh <- r.readLoop(ctx, videoConn) }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func (r *Relay) controlKeepaliveLoop(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(controlKeepaliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		body, err := json.Marshal(map[string]any{"type": "keepalive", "active": true})
		if err != nil {
			return
		}
		writeCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		r.writeMu.Lock()
		err = conn.Write(writeCtx, websocket.MessageText, body)
		r.writeMu.Unlock()
		cancel()
		if err != nil {
			return
		}
	}
}

func (r *Relay) dialPhoneWebsocket(ctx context.Context, name string, targetURL string, results chan<- relayDialResult) {
	conn, _, err := websocket.Dial(ctx, targetURL, &websocket.DialOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		results <- relayDialResult{name: name, err: fmt.Errorf("dial phone %s: %w", name, err)}
		return
	}
	results <- relayDialResult{name: name, conn: conn}
}

func (r *Relay) readLoop(ctx context.Context, conn *websocket.Conn) error {
	for {
		msgType, data, readErr := conn.Read(ctx)
		if readErr != nil {
			return readErr
		}
		r.mu.Lock()
		r.lastSeenAt = time.Now()
		if msgType == websocket.MessageText && bytes.Contains(data, []byte(`"type":"config"`)) {
			r.lastConfig = string(data)
		}
		handler := r.onMessage
		r.mu.Unlock()
		if handler != nil {
			switch msgType {
			case websocket.MessageText:
				handler(Message{Text: append([]byte(nil), data...)})
			case websocket.MessageBinary:
				handler(Message{Binary: append([]byte(nil), data...)})
			}
		}
	}
}

func (r *Relay) shouldRun() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.desired && r.viewers > 0
}

func (r *Relay) recordError(err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	r.lastError = err.Error()
	handler := r.onMessage
	r.mu.Unlock()
	log.Printf("ticket phone relay: %v", err)
	if handler != nil {
		payload, _ := json.Marshal(map[string]any{
			"type":    "phone",
			"state":   "reconnecting",
			"message": err.Error(),
		})
		handler(Message{Text: payload})
	}
}

func (r *Relay) Reconnect(reason string) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "recovery reconnect"
	}
	r.mu.Lock()
	conn := r.conn
	videoConn := r.videoConn
	var oldCancel context.CancelFunc
	var ctx context.Context
	shouldRestart := r.desired && r.viewers > 0
	if shouldRestart {
		if r.cancelLoop != nil {
			oldCancel = r.cancelLoop
			r.cancelLoop = nil
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(context.Background())
		r.cancelLoop = cancel
	}
	r.conn = nil
	r.videoConn = nil
	r.connected = false
	r.lastError = reason
	r.mu.Unlock()
	if conn != nil {
		_ = conn.Close(websocket.StatusInternalError, reason)
	}
	if videoConn != nil {
		_ = videoConn.Close(websocket.StatusInternalError, reason)
	}
	if oldCancel != nil {
		oldCancel()
	}
	if shouldRestart {
		go r.connectLoop(ctx)
	}
}

func (r *Relay) sendVideoJSON(ctx context.Context, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	r.mu.Lock()
	conn := r.videoConn
	connected := r.connected
	r.mu.Unlock()
	if conn == nil || !connected {
		return fmt.Errorf("phone video stream is not connected")
	}
	r.videoWriteMu.Lock()
	defer r.videoWriteMu.Unlock()
	return conn.Write(ctx, websocket.MessageText, body)
}

func (r *Relay) websocketURL(path string) (string, error) {
	base := strings.TrimRight(r.cfg.BaseURL, "/")
	if base == "" {
		return "", fmt.Errorf("phone base URL is empty")
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported phone base URL scheme %q", parsed.Scheme)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + path
	parsed.RawQuery = ""
	return parsed.String(), nil
}

func (r *Relay) stopPhoneSession() {
	r.mu.Lock()
	base := r.cfg.BaseURL
	r.mu.Unlock()
	r.stopPhoneSessionAt(base)
}

func (r *Relay) startPhoneSessionAt(ctx context.Context, baseURL string, timeout time.Duration) error {
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		return fmt.Errorf("phone base URL is empty")
	}
	if timeout <= 0 {
		timeout = r.cfg.RequestTimeout
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	startCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(startCtx, http.MethodPost, base+"/api/v1/session/start", nil)
	if err != nil {
		return fmt.Errorf("start phone session: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("start phone session: %w", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("start phone session status %d", resp.StatusCode)
}

func (r *Relay) stopPhoneSessionAt(baseURL string) {
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.cfg.RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/session/stop", nil)
	if err != nil {
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.mu.Lock()
		idle := !r.desired && r.viewers == 0
		if idle {
			r.lastError = ""
		}
		r.mu.Unlock()
		if idle {
			log.Printf("ticket phone relay: ignored idle stop error: %v", err)
			return
		}
		r.recordError(fmt.Errorf("stop phone session: %w", err))
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		r.mu.Lock()
		r.lastError = ""
		r.mu.Unlock()
		return
	}
	r.recordError(fmt.Errorf("stop phone session status %d", resp.StatusCode))
}
