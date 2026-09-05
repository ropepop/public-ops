package phone

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"
)

const (
	idleStopSettleDelay = 200 * time.Millisecond

	DefaultRequestTimeout     = 10 * time.Second
	DefaultReconnectMinDelay  = 500 * time.Millisecond
	DefaultReconnectMaxDelay  = 5 * time.Second
	DefaultNoViewerStopDelay  = 10 * time.Second
	DefaultLivenessIdle       = 4 * time.Second
	DefaultLivenessTimeout    = 3 * time.Second
	DefaultReconnectReset     = 10 * time.Second
	DefaultClockProbeInterval = 2 * time.Second
	ClockProbeIDMaxBytes      = 64
	CaptureDemandTTL          = 2500 * time.Millisecond
	maxProtocolGeneration     = uint64(9_007_199_254_740_991)

	// MaxVideoPayloadBytes bounds encoded H.264 only. MaxVideoMessageBytes adds
	// the largest supported frame envelope so the relay can enforce a total
	// WebSocket message bound before the envelope is parsed.
	MaxVideoPayloadBytes int64 = 2 * 1024 * 1024
	MaxVideoMessageBytes int64 = MaxVideoPayloadBytes + 93
)

type RelayConfig struct {
	BackendID          string
	AttachName         string
	BaseURL            string
	RequestTimeout     time.Duration
	ReconnectMinDelay  time.Duration
	ReconnectMaxDelay  time.Duration
	NoViewerStopDelay  time.Duration
	LivenessIdle       time.Duration
	LivenessTimeout    time.Duration
	ReconnectReset     time.Duration
	ClockProbeInterval time.Duration
}

type Message struct {
	Text                                []byte
	Binary                              []byte
	ClockProbe                          *ClockProbeResult
	ConnectionGeneration                uint64
	StartupTraceCorrelationID           string
	ConnectionStartupTraceCorrelationID string
}

// ClockProbeResult is a validated four-timestamp exchange. ServerSendUnixMicros
// is the relay's t0, the two phone fields use Android monotonic time, and
// ServerReceiveUnixMicros is the relay's t3.
type ClockProbeResult struct {
	ProbeID                  string
	ServerSendUnixMicros     int64
	PhoneReceiveUptimeMicros int64
	PhoneSendUptimeMicros    int64
	ServerReceiveUnixMicros  int64
}

// CaptureDemandReceipt identifies one successfully written, connection-scoped
// ordinary capture opportunity. Proof, keyframe, startup, and prewarm capture
// paths do not use this protocol.
type CaptureDemandReceipt struct {
	StreamEpoch          uint64
	Generation           uint64
	ConnectionGeneration uint64
	SentAt               time.Time
	ExpiresAt            time.Time
}

type outstandingClockProbe struct {
	conn                 *websocket.Conn
	serverSendUnixMicros int64
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

	mu                        sync.Mutex
	videoWriteMu              sync.Mutex
	viewers                   int
	desired                   bool
	connected                 bool
	lastError                 string
	lastConfig                string
	lastSeenAt                time.Time
	videoConn                 *websocket.Conn
	startupTraceCorrelationID string
	dialAttemptGeneration     uint64
	dialWebsocket             func(context.Context, string, *websocket.DialOptions) (*websocket.Conn, *http.Response, error)
	reconnectJitter           func(time.Duration) time.Duration
	clockProbeCounter         uint64
	outstandingClockProbes    map[string]outstandingClockProbe
	videoConnectionGeneration uint64
	captureDemandGeneration   uint64
	cancelLoop                context.CancelFunc
	idleStop                  *time.Timer
	idleStopping              bool
	idleStopDone              chan struct{}
	onMessage                 func(Message)
	onDisconnect              func(error)
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
	if cfg.LivenessIdle <= 0 {
		cfg.LivenessIdle = DefaultLivenessIdle
	}
	if cfg.LivenessTimeout <= 0 {
		cfg.LivenessTimeout = DefaultLivenessTimeout
	}
	if cfg.ReconnectReset <= 0 {
		cfg.ReconnectReset = DefaultReconnectReset
	}
	if cfg.ClockProbeInterval <= 0 {
		cfg.ClockProbeInterval = DefaultClockProbeInterval
	}
	return &Relay{
		cfg:                    cfg,
		dialWebsocket:          websocket.Dial,
		reconnectJitter:        jitterReconnectDelay,
		outstandingClockProbes: make(map[string]outstandingClockProbe),
	}
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

// SetStartupTraceCorrelationID refreshes the correlation used by the next
// private video handshake without changing viewer ownership. This matters when
// a retained viewer starts a new trace before the relay reconnects.
func (r *Relay) SetStartupTraceCorrelationID(value string) {
	clean := cleanStartupTraceCorrelationID(value)
	if clean == "" {
		return
	}
	r.mu.Lock()
	r.startupTraceCorrelationID = clean
	r.mu.Unlock()
}

func (r *Relay) StartupTraceCorrelationID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.startupTraceCorrelationID
}

func (r *Relay) ClearStartupTraceCorrelationIDIf(expected string) bool {
	expected = cleanStartupTraceCorrelationID(expected)
	if expected == "" {
		return false
	}
	r.mu.Lock()
	if r.startupTraceCorrelationID != expected {
		r.mu.Unlock()
		return false
	}
	r.startupTraceCorrelationID = ""
	r.mu.Unlock()
	return true
}

func (r *Relay) AddViewer(startupTraceCorrelationID ...string) {
	if len(startupTraceCorrelationID) > 0 {
		r.SetStartupTraceCorrelationID(startupTraceCorrelationID[0])
	}
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
	// An existing desired loop already owns dialing, liveness, and retries. Treat it as active
	// even before the HTTP upgrade completes so a parallel browser wake cannot cancel the first
	// legitimate handshake and create a duplicate Pixel socket.
	if r.desired && r.cancelLoop != nil {
		r.mu.Unlock()
		return true
	}
	if r.cancelLoop != nil {
		oldCancel = r.cancelLoop
		r.cancelLoop = nil
	}
	videoConn = r.videoConn
	r.videoConn = nil
	r.connected = false
	r.desired = true
	r.lastError = reason
	var cancel context.CancelFunc
	ctx, cancel = context.WithCancel(context.Background())
	r.cancelLoop = cancel
	r.mu.Unlock()

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
	videoConn := r.videoConn
	r.videoConn = nil
	r.connected = false
	r.mu.Unlock()
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
	videoConn := r.videoConn
	r.videoConn = nil
	r.connected = false
	r.desired = false
	r.viewers = 0
	r.mu.Unlock()
	if videoConn != nil {
		_ = videoConn.Close(websocket.StatusNormalClosure, "relay closed")
	}
}

func (r *Relay) SwitchBackend(backend Backend) {
	cleanBaseURL := strings.TrimRight(strings.TrimSpace(backend.BaseURL), "/")
	r.mu.Lock()
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
	videoConn := r.videoConn
	shouldReconnect := r.desired && r.viewers > 0
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

	if videoConn != nil {
		_ = videoConn.Close(websocket.StatusNormalClosure, "phone backend switched")
	}
	r.mu.Lock()
	if r.cfg.BaseURL == cleanBaseURL {
		r.lastError = ""
	}
	r.mu.Unlock()
	if shouldReconnect {
		go r.connectLoop(ctx)
	}
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
		connectedFor, err := r.connectOnceMeasured(ctx)
		if ctx.Err() != nil || !r.shouldRun() {
			return
		}
		if err != nil {
			r.recordError(err)
		}
		if connectedFor >= r.cfg.ReconnectReset {
			delay = r.cfg.ReconnectMinDelay
		}
		wait := delay
		if r.reconnectJitter != nil {
			wait = r.reconnectJitter(delay)
		}
		timer := time.NewTimer(wait)
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

func jitterReconnectDelay(delay time.Duration) time.Duration {
	if delay <= 1 {
		return delay
	}
	half := delay / 2
	return half + time.Duration(rand.Int64N(int64(delay-half)+1))
}

func (r *Relay) connectOnce(ctx context.Context) error {
	_, err := r.connectOnceMeasured(ctx)
	return err
}

func (r *Relay) connectOnceMeasured(ctx context.Context) (connectedFor time.Duration, retErr error) {
	videoURL, err := r.websocketURL("/api/v1/stream")
	if err != nil {
		return 0, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, r.cfg.RequestTimeout)
	defer cancel()
	r.mu.Lock()
	r.dialAttemptGeneration++
	dialAttemptGeneration := r.dialAttemptGeneration
	startupTraceCorrelationID := r.startupTraceCorrelationID
	r.mu.Unlock()
	requestHeader := http.Header{}
	if startupTraceCorrelationID != "" {
		requestHeader.Set("X-Ticket-Startup-Trace", startupTraceCorrelationID)
	}
	dialResults := make(chan relayDialResult, 1)
	go r.dialPhoneWebsocket(dialCtx, "video", videoURL, requestHeader, dialResults)

	var videoConn *websocket.Conn
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case result := <-dialResults:
		if result.err != nil {
			return 0, result.err
		}
		videoConn = result.conn
	}
	videoConn.SetReadLimit(MaxVideoMessageBytes)
	r.mu.Lock()
	if !r.desired || r.dialAttemptGeneration != dialAttemptGeneration || ctx.Err() != nil {
		r.mu.Unlock()
		_ = videoConn.CloseNow()
		return 0, nil
	}
	r.videoConn = videoConn
	r.connected = true
	r.lastError = ""
	r.lastSeenAt = time.Now()
	r.outstandingClockProbes = make(map[string]outstandingClockProbe)
	r.videoConnectionGeneration++
	if r.videoConnectionGeneration == 0 || r.videoConnectionGeneration > maxProtocolGeneration {
		r.videoConnectionGeneration = 1
	}
	r.captureDemandGeneration = 0
	r.mu.Unlock()
	connectedAt := time.Now()
	defer func() {
		connectedFor = time.Since(connectedAt)
		r.mu.Lock()
		wasCurrent := r.videoConn == videoConn
		if r.videoConn == videoConn {
			r.videoConn = nil
		}
		if wasCurrent {
			r.connected = false
		}
		for probeID, probe := range r.outstandingClockProbes {
			if probe.conn == videoConn {
				delete(r.outstandingClockProbes, probeID)
			}
		}
		var onDisconnect func(error)
		if wasCurrent {
			onDisconnect = r.onDisconnect
		}
		r.mu.Unlock()
		// The connection has already ended or been superseded. Do not wait for a
		// close handshake before publishing disconnect and starting recovery.
		_ = videoConn.CloseNow()
		if onDisconnect != nil {
			onDisconnect(retErr)
		}
	}()
	connectionCtx, cancelConnection := context.WithCancel(ctx)
	defer cancelConnection()
	activity := make(chan struct{}, 1)
	errCh := make(chan error, 2)
	go func() { errCh <- r.readLoop(connectionCtx, videoConn, startupTraceCorrelationID, activity) }()
	go func() { errCh <- r.livenessLoop(connectionCtx, videoConn, activity) }()
	select {
	case <-ctx.Done():
		return connectedFor, ctx.Err()
	case err := <-errCh:
		return connectedFor, err
	}
}

func (r *Relay) dialPhoneWebsocket(ctx context.Context, name string, targetURL string, requestHeader http.Header, results chan<- relayDialResult) {
	dialWebsocket := r.dialWebsocket
	if dialWebsocket == nil {
		dialWebsocket = websocket.Dial
	}
	conn, _, err := dialWebsocket(ctx, targetURL, &websocket.DialOptions{
		CompressionMode: websocket.CompressionDisabled,
		HTTPHeader:      requestHeader,
	})
	if err != nil {
		results <- relayDialResult{name: name, err: fmt.Errorf("dial phone %s: %w", name, err)}
		return
	}
	results <- relayDialResult{name: name, conn: conn}
}

func cleanStartupTraceCorrelationID(value string) string {
	clean := strings.TrimSpace(value)
	if len(clean) != len("startup_")+8 || !strings.HasPrefix(clean, "startup_") {
		return ""
	}
	for _, char := range strings.TrimPrefix(clean, "startup_") {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return ""
		}
	}
	return clean
}

func (r *Relay) readLoop(ctx context.Context, conn *websocket.Conn, connectionStartupTraceCorrelationID string, activity chan<- struct{}) error {
	for {
		msgType, reader, readErr := conn.Reader(ctx)
		if readErr != nil {
			return readErr
		}
		data, readErr := io.ReadAll(io.LimitReader(reader, MaxVideoMessageBytes+1))
		if readErr != nil {
			return readErr
		}
		if int64(len(data)) > MaxVideoMessageBytes {
			return fmt.Errorf("phone media message exceeds %d bytes", MaxVideoMessageBytes)
		}
		r.mu.Lock()
		if r.videoConn != conn {
			r.mu.Unlock()
			return nil
		}
		r.lastSeenAt = time.Now()
		if msgType == websocket.MessageText && bytes.Contains(data, []byte(`"type":"config"`)) {
			r.lastConfig = string(data)
		}
		handler := r.onMessage
		connectionGeneration := r.videoConnectionGeneration
		startupTraceCorrelationID := r.startupTraceCorrelationID
		r.mu.Unlock()
		select {
		case activity <- struct{}{}:
		default:
		}
		if msgType == websocket.MessageText {
			probe, recognized := r.consumeClockProbeResult(conn, data, time.Now())
			if recognized {
				if handler != nil && probe != nil {
					handler(Message{
						ClockProbe:                          probe,
						ConnectionGeneration:                connectionGeneration,
						StartupTraceCorrelationID:           startupTraceCorrelationID,
						ConnectionStartupTraceCorrelationID: connectionStartupTraceCorrelationID,
					})
				}
				continue
			}
		}
		if handler != nil {
			switch msgType {
			case websocket.MessageText:
				handler(Message{
					Text:                                append([]byte(nil), data...),
					ConnectionGeneration:                connectionGeneration,
					StartupTraceCorrelationID:           startupTraceCorrelationID,
					ConnectionStartupTraceCorrelationID: connectionStartupTraceCorrelationID,
				})
			case websocket.MessageBinary:
				handler(Message{
					Binary:                              append([]byte(nil), data...),
					ConnectionGeneration:                connectionGeneration,
					StartupTraceCorrelationID:           startupTraceCorrelationID,
					ConnectionStartupTraceCorrelationID: connectionStartupTraceCorrelationID,
				})
			}
		}
	}
}

func (r *Relay) livenessLoop(ctx context.Context, conn *websocket.Conn, activity <-chan struct{}) error {
	// TSF3 frames need a bounded clock mapping. Probe as soon as the connection
	// is current so the first 1 FPS frame is not deterministically discarded.
	if err := r.sendClockProbe(ctx, conn); err != nil {
		return fmt.Errorf("initial phone clock probe: %w", err)
	}
	timer := time.NewTimer(r.cfg.LivenessIdle)
	defer timer.Stop()
	probeTicker := time.NewTicker(r.cfg.ClockProbeInterval)
	defer probeTicker.Stop()
	reset := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(r.cfg.LivenessIdle)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-activity:
			reset()
		case <-probeTicker.C:
			if err := r.sendClockProbe(ctx, conn); err != nil {
				return fmt.Errorf("phone clock probe: %w", err)
			}
		case <-timer.C:
			pingCtx, cancel := context.WithTimeout(ctx, r.cfg.LivenessTimeout)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return fmt.Errorf("phone media liveness: %w", err)
			}
			timer.Reset(r.cfg.LivenessIdle)
		}
	}
}

func (r *Relay) sendClockProbe(ctx context.Context, conn *websocket.Conn) error {
	writeCtx, cancel := context.WithTimeout(ctx, r.cfg.LivenessTimeout)
	defer cancel()
	r.videoWriteMu.Lock()
	defer r.videoWriteMu.Unlock()
	// Sample t0 after waiting for the only application-data writer and as close
	// as practical to the actual WebSocket write.
	serverSendUnixMicros := time.Now().UnixMicro()
	r.mu.Lock()
	if serverSendUnixMicros <= 0 || r.videoConn != conn {
		r.mu.Unlock()
		return context.Canceled
	}
	r.clockProbeCounter++
	probeID := fmt.Sprintf("p-%016x", r.clockProbeCounter)
	if len(r.outstandingClockProbes) >= 4 {
		for existingID, existing := range r.outstandingClockProbes {
			if existing.conn == conn {
				delete(r.outstandingClockProbes, existingID)
			}
		}
	}
	r.outstandingClockProbes[probeID] = outstandingClockProbe{
		conn: conn, serverSendUnixMicros: serverSendUnixMicros,
	}
	r.mu.Unlock()

	payload, err := json.Marshal(map[string]any{
		"type":                 "clock_probe",
		"probeId":              probeID,
		"serverSendUnixMicros": serverSendUnixMicros,
	})
	if err != nil {
		r.mu.Lock()
		delete(r.outstandingClockProbes, probeID)
		r.mu.Unlock()
		return err
	}
	err = conn.Write(writeCtx, websocket.MessageText, payload)
	if err != nil {
		r.mu.Lock()
		delete(r.outstandingClockProbes, probeID)
		r.mu.Unlock()
	}
	return err
}

// SendCaptureDemand writes one strict, additive ordinary-capture opportunity
// to the current private media socket. Unknown text remains harmless to older
// Pixel releases, which continue their legacy periodic capture behavior.
func (r *Relay) SendCaptureDemand(ctx context.Context, streamEpoch uint64) (CaptureDemandReceipt, error) {
	if streamEpoch == 0 || streamEpoch > maxProtocolGeneration {
		return CaptureDemandReceipt{}, fmt.Errorf("capture demand stream epoch is invalid")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.videoWriteMu.Lock()
	defer r.videoWriteMu.Unlock()

	r.mu.Lock()
	conn := r.videoConn
	connectionGeneration := r.videoConnectionGeneration
	if conn == nil || !r.connected || !r.desired || connectionGeneration == 0 {
		r.mu.Unlock()
		return CaptureDemandReceipt{}, fmt.Errorf("phone media socket is not current")
	}
	if r.captureDemandGeneration >= maxProtocolGeneration {
		r.mu.Unlock()
		return CaptureDemandReceipt{}, fmt.Errorf("capture demand generation exhausted")
	}
	r.captureDemandGeneration++
	generation := r.captureDemandGeneration
	writeTimeout := r.cfg.LivenessTimeout
	r.mu.Unlock()

	payload, err := json.Marshal(struct {
		Type        string `json:"type"`
		Version     int    `json:"version"`
		StreamEpoch uint64 `json:"streamEpoch"`
		Generation  uint64 `json:"generation"`
		TTLMillis   int64  `json:"ttlMillis"`
	}{
		Type: "capture_demand", Version: 1, StreamEpoch: streamEpoch,
		Generation: generation, TTLMillis: CaptureDemandTTL.Milliseconds(),
	})
	if err != nil {
		return CaptureDemandReceipt{}, err
	}
	if writeTimeout <= 0 || writeTimeout > CaptureDemandTTL {
		writeTimeout = CaptureDemandTTL
	}
	writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
	sentAt := time.Now()
	err = conn.Write(writeCtx, websocket.MessageText, payload)
	cancel()
	if err != nil {
		return CaptureDemandReceipt{}, err
	}
	r.mu.Lock()
	stillCurrent := r.videoConn == conn && r.connected && r.desired && r.videoConnectionGeneration == connectionGeneration
	r.mu.Unlock()
	if !stillCurrent {
		return CaptureDemandReceipt{}, fmt.Errorf("phone media socket changed during capture demand")
	}
	return CaptureDemandReceipt{
		StreamEpoch: streamEpoch, Generation: generation, ConnectionGeneration: connectionGeneration,
		SentAt: sentAt, ExpiresAt: sentAt.Add(CaptureDemandTTL),
	}, nil
}

func (r *Relay) consumeClockProbeResult(conn *websocket.Conn, data []byte, receivedAt time.Time) (*ClockProbeResult, bool) {
	var kind struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &kind); err != nil || kind.Type != "clock_probe_result" {
		return nil, false
	}
	var payload struct {
		Type                     string `json:"type"`
		ProbeID                  string `json:"probeId"`
		ServerSendUnixMicros     int64  `json:"serverSendUnixMicros"`
		PhoneReceiveUptimeMicros int64  `json:"phoneReceiveUptimeMicros"`
		PhoneSendUptimeMicros    int64  `json:"phoneSendUptimeMicros"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return nil, true
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, true
	}
	if payload.Type != "clock_probe_result" || !validClockProbeID(payload.ProbeID) ||
		payload.ServerSendUnixMicros <= 0 || payload.PhoneReceiveUptimeMicros <= 0 ||
		payload.PhoneSendUptimeMicros < payload.PhoneReceiveUptimeMicros || receivedAt.UnixMicro() <= 0 {
		return nil, true
	}
	r.mu.Lock()
	outstanding, ok := r.outstandingClockProbes[payload.ProbeID]
	if ok && outstanding.conn == conn {
		delete(r.outstandingClockProbes, payload.ProbeID)
	}
	r.mu.Unlock()
	if !ok || outstanding.conn != conn || outstanding.serverSendUnixMicros != payload.ServerSendUnixMicros {
		return nil, true
	}
	return &ClockProbeResult{
		ProbeID:                  payload.ProbeID,
		ServerSendUnixMicros:     payload.ServerSendUnixMicros,
		PhoneReceiveUptimeMicros: payload.PhoneReceiveUptimeMicros,
		PhoneSendUptimeMicros:    payload.PhoneSendUptimeMicros,
		ServerReceiveUnixMicros:  receivedAt.UnixMicro(),
	}, true
}

func validClockProbeID(value string) bool {
	if value == "" || len(value) > ClockProbeIDMaxBytes {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || strings.ContainsRune("._:-", char) {
			continue
		}
		return false
	}
	return true
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
	r.videoConn = nil
	r.connected = false
	r.lastError = reason
	r.mu.Unlock()
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
