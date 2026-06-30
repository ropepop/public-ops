package broker

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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
	defaultTicketGrace = 10 * time.Second

	healthUpstreamProbeTimeout   = time.Second
	websocketProxyReadLimitBytes = 8 << 20

	defaultTicketLeaseTTL = 45 * time.Second
	minTicketLeaseTTL     = 3 * time.Second
	maxTicketLeaseTTL     = 3 * time.Minute
)

type Config struct {
	BindAddr        string
	Port            int
	UpstreamBaseURL string
	TicketGrace     time.Duration
}

type Broker struct {
	cfg Config

	mu                   sync.Mutex
	ticketViewers        int
	ticketSockets        int
	ticketGraceUntil     time.Time
	ticketLeases         map[string]ticketLease
	lastPreemptionReason string
	lastPreemptionAt     time.Time
}

type TicketPresenceInput struct {
	Viewers int
	Now     time.Time
}

type TicketLeaseInput struct {
	LeaseID   string
	RequestID string
	Reason    string
	TTL       time.Duration
	Now       time.Time
}

type ticketLease struct {
	ID         string
	RequestID  string
	Reason     string
	AcquiredAt time.Time
	UpdatedAt  time.Time
	ExpiresAt  time.Time
}

type TicketLeaseSnapshot struct {
	ID                   string `json:"id"`
	Owner                string `json:"owner"`
	RequestID            string `json:"requestId,omitempty"`
	Reason               string `json:"reason,omitempty"`
	AcquiredAt           string `json:"acquiredAt,omitempty"`
	UpdatedAt            string `json:"updatedAt,omitempty"`
	ExpiresAt            string `json:"expiresAt,omitempty"`
	RemainingMillis      int64  `json:"remainingMillis,omitempty"`
	LastPreemptionReason string `json:"lastPreemptionReason,omitempty"`
	LastPreemptionAt     string `json:"lastPreemptionAt,omitempty"`
}

type StateSnapshot struct {
	CurrentOwner         string               `json:"currentOwner"`
	DesiredOwner         string               `json:"desiredOwner"`
	DesiredPriority      []string             `json:"desiredPriority,omitempty"`
	ActiveLease          *TicketLeaseSnapshot `json:"activeLease,omitempty"`
	LeaseReason          string               `json:"leaseReason,omitempty"`
	LeaseRequestID       string               `json:"leaseRequestId,omitempty"`
	LastPreemptionReason string               `json:"lastPreemptionReason,omitempty"`
	LastPreemptionAt     string               `json:"lastPreemptionAt,omitempty"`
	TicketViewers        int                  `json:"ticketViewers"`
	TicketSockets        int                  `json:"ticketSockets"`
	TicketActive         bool                 `json:"ticketActive"`
}

func New(cfg Config) (*Broker, error) {
	cfg.UpstreamBaseURL = strings.TrimRight(strings.TrimSpace(cfg.UpstreamBaseURL), "/")
	if cfg.UpstreamBaseURL == "" {
		return nil, fmt.Errorf("upstream base URL is required")
	}
	if _, err := url.Parse(cfg.UpstreamBaseURL); err != nil {
		return nil, fmt.Errorf("parse upstream base URL: %w", err)
	}
	if cfg.TicketGrace <= 0 {
		cfg.TicketGrace = defaultTicketGrace
	}
	return &Broker{
		cfg:          cfg,
		ticketLeases: map[string]ticketLease{},
	}, nil
}

func (b *Broker) Run(ctx context.Context) {
	<-ctx.Done()
}

func (b *Broker) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/health", b.handleHealth)
	mux.HandleFunc("/api/v1/state", b.handleState)
	mux.HandleFunc("/api/v1/ticket/presence", b.handleTicketPresence)
	mux.HandleFunc("/api/v1/phone/leases/ticket", b.handleTicketLease)
	mux.HandleFunc("/api/v1/phone/leases/ticket/release", b.handleTicketLeaseRelease)
	mux.HandleFunc("/api/v1/session/start", b.proxyHTTP)
	mux.HandleFunc("/api/v1/session/stop", b.proxyHTTP)
	mux.HandleFunc("/api/v1/session", func(w http.ResponseWriter, r *http.Request) {
		b.proxyWebsocket(w, r, "/api/v1/session")
	})
	mux.HandleFunc("/api/v1/stream", func(w http.ResponseWriter, r *http.Request) {
		b.proxyWebsocket(w, r, "/api/v1/stream")
	})
	return mux
}

func (b *Broker) UpdateTicketPresence(_ context.Context, input TicketPresenceInput) error {
	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}
	b.mu.Lock()
	if input.Viewers < 0 {
		input.Viewers = 0
	}
	b.ticketViewers = input.Viewers
	if input.Viewers > 0 {
		b.ticketGraceUntil = time.Time{}
	} else if b.ticketSockets == 0 {
		b.ticketGraceUntil = now.Add(b.cfg.TicketGrace)
	}
	b.mu.Unlock()
	return nil
}

func (b *Broker) AcquireTicketLease(_ context.Context, input TicketLeaseInput) (TicketLeaseSnapshot, error) {
	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}
	leaseID := strings.TrimSpace(input.LeaseID)
	if leaseID == "" {
		leaseID = "ticket:" + randomID()
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		reason = "ticket_active"
	}
	ttl := normalizeTicketLeaseTTL(input.TTL)
	lease := ticketLease{
		ID:         leaseID,
		RequestID:  strings.TrimSpace(input.RequestID),
		Reason:     reason,
		AcquiredAt: now.UTC(),
		UpdatedAt:  now.UTC(),
		ExpiresAt:  now.Add(ttl).UTC(),
	}
	b.mu.Lock()
	if b.ticketLeases == nil {
		b.ticketLeases = map[string]ticketLease{}
	}
	b.pruneExpiredTicketLeasesLocked(now)
	if previous, ok := b.ticketLeases[leaseID]; ok && !previous.AcquiredAt.IsZero() {
		lease.AcquiredAt = previous.AcquiredAt
	}
	b.ticketLeases[leaseID] = lease
	snapshot := ticketLeaseSnapshot(lease, now, b.lastPreemptionReason, b.lastPreemptionAt)
	b.mu.Unlock()
	return snapshot, nil
}

func (b *Broker) ReleaseTicketLease(_ context.Context, input TicketLeaseInput) error {
	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}
	leaseID := strings.TrimSpace(input.LeaseID)
	requestID := strings.TrimSpace(input.RequestID)
	b.mu.Lock()
	b.pruneExpiredTicketLeasesLocked(now)
	if leaseID != "" {
		delete(b.ticketLeases, leaseID)
	} else if requestID != "" {
		for id, lease := range b.ticketLeases {
			if strings.TrimSpace(lease.RequestID) == requestID {
				delete(b.ticketLeases, id)
			}
		}
	}
	b.mu.Unlock()
	return nil
}

func (b *Broker) Snapshot(now time.Time) StateSnapshot {
	if now.IsZero() {
		now = time.Now()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneExpiredTicketLeasesLocked(now)
	activeLease := b.activeTicketLeaseSnapshotLocked(now)
	leaseReason := ""
	leaseRequestID := ""
	if activeLease != nil {
		leaseReason = activeLease.Reason
		leaseRequestID = activeLease.RequestID
	}
	lastPreemptionAt := ""
	if !b.lastPreemptionAt.IsZero() {
		lastPreemptionAt = b.lastPreemptionAt.UTC().Format(time.RFC3339Nano)
	}
	currentOwner, desiredPriority := b.ownerSnapshotLocked(now)
	return StateSnapshot{
		CurrentOwner:         currentOwner,
		DesiredOwner:         currentOwner,
		DesiredPriority:      desiredPriority,
		ActiveLease:          activeLease,
		LeaseReason:          leaseReason,
		LeaseRequestID:       leaseRequestID,
		LastPreemptionReason: b.lastPreemptionReason,
		LastPreemptionAt:     lastPreemptionAt,
		TicketViewers:        b.ticketViewers,
		TicketSockets:        b.ticketSockets,
		TicketActive:         b.ticketActiveLocked(now),
	}
}

func (b *Broker) upstreamHealthSnapshot(ctx context.Context) (upstreamHealth, bool, error) {
	probeCtx, cancel := context.WithTimeout(ctx, healthUpstreamProbeTimeout)
	defer cancel()
	health, err := b.fetchUpstreamHealth(probeCtx)
	return health, err == nil, err
}

func (b *Broker) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	setReleaseHeaders(w)
	response := map[string]any{
		"ok":    true,
		"state": b.Snapshot(time.Now()),
	}
	status := http.StatusOK
	strict := r.URL.Query().Get("strict") == "1" || r.URL.Query().Get("requireUpstream") == "1"
	health, upstreamOK, upstreamErr := b.upstreamHealthSnapshot(r.Context())
	upstreamStatus := map[string]any{"ok": upstreamOK}
	if upstreamErr != nil {
		upstreamStatus["error"] = upstreamErr.Error()
	}
	response["upstream"] = upstreamStatus
	if upstreamOK {
		if strings.TrimSpace(health.ControlCodeRequest.RequestID) != "" {
			response["controlCodeRequest"] = health.ControlCodeRequest
		}
	} else if strict {
		response["ok"] = false
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, response)
}

func (b *Broker) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": b.Snapshot(time.Now())})
}

func (b *Broker) handleTicketPresence(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Viewers int `json:"viewers"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad_request"})
		return
	}
	if err := b.UpdateTicketPresence(r.Context(), TicketPresenceInput{Viewers: req.Viewers, Now: time.Now()}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": b.Snapshot(time.Now())})
}

func (b *Broker) handleTicketLease(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		LeaseID   string `json:"leaseId"`
		RequestID string `json:"requestId"`
		Reason    string `json:"reason"`
		TTLMillis int64  `json:"ttlMillis"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad_request"})
		return
	}
	lease, err := b.AcquireTicketLease(r.Context(), TicketLeaseInput{
		LeaseID:   req.LeaseID,
		RequestID: req.RequestID,
		Reason:    req.Reason,
		TTL:       time.Duration(req.TTLMillis) * time.Millisecond,
		Now:       time.Now(),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"lease": lease,
		"state": b.Snapshot(time.Now()),
	})
}

func (b *Broker) handleTicketLeaseRelease(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		LeaseID   string `json:"leaseId"`
		RequestID string `json:"requestId"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad_request"})
		return
	}
	if err := b.ReleaseTicketLease(r.Context(), TicketLeaseInput{
		LeaseID:   req.LeaseID,
		RequestID: req.RequestID,
		Now:       time.Now(),
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": b.Snapshot(time.Now())})
}

func (b *Broker) beginTicketSocket() {
	b.mu.Lock()
	b.ticketSockets++
	b.ticketGraceUntil = time.Time{}
	b.mu.Unlock()
}

func (b *Broker) endTicketSocket() {
	now := time.Now()
	b.mu.Lock()
	if b.ticketSockets > 0 {
		b.ticketSockets--
	}
	if b.ticketSockets == 0 && b.ticketViewers == 0 {
		b.ticketGraceUntil = now.Add(b.cfg.TicketGrace)
	}
	b.mu.Unlock()
}

func (b *Broker) proxyHTTP(w http.ResponseWriter, r *http.Request) {
	target := b.cfg.UpstreamBaseURL + r.URL.Path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header = r.Header.Clone()
	req.ContentLength = int64(len(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (b *Broker) proxyWebsocket(w http.ResponseWriter, r *http.Request, targetPath string) {
	b.beginTicketSocket()
	defer b.endTicketSocket()

	clientConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	clientConn.SetReadLimit(websocketProxyReadLimitBytes)
	defer clientConn.Close(websocket.StatusNormalClosure, "broker proxy closed")

	target, err := b.websocketURL(targetPath)
	if err != nil {
		_ = clientConn.Close(websocket.StatusInternalError, err.Error())
		return
	}
	upstreamConn, _, err := websocket.Dial(r.Context(), target, &websocket.DialOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		_ = clientConn.Close(websocket.StatusInternalError, "upstream unavailable")
		return
	}
	upstreamConn.SetReadLimit(websocketProxyReadLimitBytes)
	defer upstreamConn.Close(websocket.StatusNormalClosure, "broker proxy closed")

	errCh := make(chan error, 2)
	go func() { errCh <- proxyMessages(r.Context(), upstreamConn, clientConn) }()
	go func() { errCh <- proxyMessages(r.Context(), clientConn, upstreamConn) }()
	select {
	case <-r.Context().Done():
	case <-errCh:
	}
}

func proxyMessages(ctx context.Context, dst *websocket.Conn, src *websocket.Conn) error {
	for {
		typ, data, err := src.Read(ctx)
		if err != nil {
			return err
		}
		if err := dst.Write(ctx, typ, data); err != nil {
			return err
		}
	}
}

func (b *Broker) websocketURL(targetPath string) (string, error) {
	parsed, err := url.Parse(b.cfg.UpstreamBaseURL)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported upstream scheme %q", parsed.Scheme)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + targetPath
	parsed.RawQuery = ""
	return parsed.String(), nil
}

type upstreamControlCodeRequest struct {
	RequestID string `json:"requestId"`
	Status    string `json:"status"`
	Reason    string `json:"reason"`
	Value     string `json:"value"`
}

type upstreamHealth struct {
	ControlCodeRequest upstreamControlCodeRequest `json:"controlCodeRequest"`
}

func (b *Broker) fetchUpstreamHealth(ctx context.Context) (upstreamHealth, error) {
	var health upstreamHealth
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.cfg.UpstreamBaseURL+"/api/v1/health", nil)
	if err != nil {
		return health, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return health, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return health, fmt.Errorf("upstream health status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return health, err
	}
	return health, nil
}

func normalizeTicketLeaseTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return defaultTicketLeaseTTL
	}
	if ttl < minTicketLeaseTTL {
		return minTicketLeaseTTL
	}
	if ttl > maxTicketLeaseTTL {
		return maxTicketLeaseTTL
	}
	return ttl
}

func (b *Broker) pruneExpiredTicketLeasesLocked(now time.Time) {
	if b.ticketLeases == nil {
		return
	}
	for id, lease := range b.ticketLeases {
		if !lease.ExpiresAt.IsZero() && !now.Before(lease.ExpiresAt) {
			delete(b.ticketLeases, id)
		}
	}
}

func ticketLeaseSnapshot(lease ticketLease, now time.Time, preemptionReason string, preemptionAt time.Time) TicketLeaseSnapshot {
	remainingMillis := int64(lease.ExpiresAt.Sub(now) / time.Millisecond)
	if remainingMillis < 0 {
		remainingMillis = 0
	}
	snapshot := TicketLeaseSnapshot{
		ID:                   lease.ID,
		Owner:                "ticket",
		RequestID:            lease.RequestID,
		Reason:               lease.Reason,
		AcquiredAt:           lease.AcquiredAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:            lease.UpdatedAt.UTC().Format(time.RFC3339Nano),
		ExpiresAt:            lease.ExpiresAt.UTC().Format(time.RFC3339Nano),
		RemainingMillis:      remainingMillis,
		LastPreemptionReason: strings.TrimSpace(preemptionReason),
	}
	if !preemptionAt.IsZero() {
		snapshot.LastPreemptionAt = preemptionAt.UTC().Format(time.RFC3339Nano)
	}
	return snapshot
}

func (b *Broker) activeTicketLeaseLocked(now time.Time) (ticketLease, bool) {
	b.pruneExpiredTicketLeasesLocked(now)
	var selected ticketLease
	found := false
	for _, lease := range b.ticketLeases {
		if lease.ID == "" {
			continue
		}
		if !lease.ExpiresAt.IsZero() && !now.Before(lease.ExpiresAt) {
			continue
		}
		if !found || lease.ExpiresAt.After(selected.ExpiresAt) || (lease.ExpiresAt.Equal(selected.ExpiresAt) && lease.UpdatedAt.After(selected.UpdatedAt)) {
			selected = lease
			found = true
		}
	}
	return selected, found
}

func (b *Broker) activeTicketLeaseSnapshotLocked(now time.Time) *TicketLeaseSnapshot {
	lease, ok := b.activeTicketLeaseLocked(now)
	if !ok {
		return nil
	}
	snapshot := ticketLeaseSnapshot(lease, now, b.lastPreemptionReason, b.lastPreemptionAt)
	return &snapshot
}

func (b *Broker) ticketLeaseActiveLocked(now time.Time) bool {
	_, ok := b.activeTicketLeaseLocked(now)
	return ok
}

func (b *Broker) ticketActiveLocked(now time.Time) bool {
	return b.ticketLeaseActiveLocked(now) || b.ticketViewers > 0 || b.ticketSockets > 0 || (!b.ticketGraceUntil.IsZero() && now.Before(b.ticketGraceUntil))
}

func (b *Broker) ownerSnapshotLocked(now time.Time) (string, []string) {
	if b.ticketActiveLocked(now) {
		return "ticket", []string{"ticket"}
	}
	return "none", nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("phone broker write json failed: %v", err)
	}
}

func copyHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func randomID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}
