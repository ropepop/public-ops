package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nhooyr.io/websocket"

	"ticketremote/internal/auth"
	"ticketremote/internal/state"
)

const (
	experimentalMediaPipelineVersion = "relay-copy-v1"
	experimentalMediaVisualMode      = "bright-sdr-preview-v1"
	experimentalHDRPipelineVersion   = "iso-gainmap-keyframe-v1"
	experimentalHDRVisualMode        = "hdr-gainmap-img-edr-v1"
	maxExperimentalMediaClients      = 2
	maxExperimentalHDRBytes          = 2 * 1024 * 1024
)

var errExperimentalFrameSkipped = errors.New("experimental media frame skipped")

type experimentalSourceConfig struct {
	codec     string
	transport string
	width     int
	height    int
	epoch     uint64
}

type experimentalFrameJob struct {
	frame      []byte
	enqueuedAt time.Time
}

type experimentalFrameTransform func(context.Context, []byte, experimentalSourceConfig) ([]byte, error)

type experimentalMediaMetrics struct {
	mu                            sync.Mutex
	jobsAdmitted                  uint64
	jobsCompleted                 uint64
	jobsDroppedQueue              uint64
	deltaFramesSkipped            uint64
	failures                      uint64
	timeouts                      uint64
	lastConversionMillis          int64
	maxConversionMillis           int64
	lastServerAdditionalLatencyMS int64
	firstServerOutputDelayMillis  int64
	lastOutputEpoch               uint64
	lastOutputSequence            uint64
	activeSince                   time.Time
}

// experimentalMediaHub is deliberately downstream of the authoritative SDR
// relay. It owns only a bounded, in-memory latest-frame slot, so experimental
// work can never delay phone ingestion or the normal browser stream.
type experimentalMediaHub struct {
	frames    chan experimentalFrameJob
	done      chan struct{}
	cancel    context.CancelFunc
	onFrame   func([]byte)
	onFailure func(error)

	mu          sync.RWMutex
	transform   experimentalFrameTransform
	transformMu sync.Mutex
	source      experimentalSourceConfig
	pipeline    string
	visual      string
	hdr         bool
	timeout     time.Duration
	closeOnce   sync.Once
	clients     atomic.Int32
	metrics     experimentalMediaMetrics
}

func newExperimentalMediaHub(transformerURL string, timeout time.Duration, onFrame func([]byte), onFailure func(error)) *experimentalMediaHub {
	ctx, cancel := context.WithCancel(context.Background())
	h := &experimentalMediaHub{
		frames:    make(chan experimentalFrameJob, 1),
		done:      make(chan struct{}),
		cancel:    cancel,
		onFrame:   onFrame,
		onFailure: onFailure,
		pipeline:  experimentalMediaPipelineVersion,
		visual:    experimentalMediaVisualMode,
		timeout:   timeout,
	}
	if h.timeout <= 0 {
		h.timeout = 1500 * time.Millisecond
	}
	if strings.TrimSpace(transformerURL) != "" {
		h.hdr = true
		h.pipeline = experimentalHDRPipelineVersion
		h.visual = experimentalHDRVisualMode
		h.transform = newHDRGainMapTransform(strings.TrimRight(transformerURL, "/"), h.timeout)
	} else {
		h.transform = copyExperimentalTSF2Frame
	}
	go h.run(ctx)
	return h
}

func (h *experimentalMediaHub) Close() {
	if h == nil || h.cancel == nil {
		return
	}
	h.closeOnce.Do(func() {
		h.cancel()
		<-h.done
	})
}

func (h *experimentalMediaHub) HasClients() bool {
	return h != nil && h.clients.Load() > 0
}

func (h *experimentalMediaHub) Enqueue(frame []byte) {
	if h == nil || len(frame) == 0 {
		return
	}
	meta := parseTSF2(frame)
	if h.hdr && (!meta.ok || !meta.keyFrame) {
		if meta.ok {
			h.metrics.mu.Lock()
			h.metrics.deltaFramesSkipped++
			h.metrics.mu.Unlock()
		}
		return
	}
	copyOfFrame := append([]byte(nil), frame...)
	job := experimentalFrameJob{frame: copyOfFrame, enqueuedAt: time.Now()}
	select {
	case h.frames <- job:
		h.recordAdmitted()
		return
	default:
	}
	select {
	case <-h.frames:
		h.metrics.mu.Lock()
		h.metrics.jobsDroppedQueue++
		h.metrics.mu.Unlock()
	default:
	}
	select {
	case h.frames <- job:
		h.recordAdmitted()
	default:
	}
}

func (h *experimentalMediaHub) run(ctx context.Context) {
	defer close(h.done)
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-h.frames:
			started := time.Now()
			transformed, err := h.apply(ctx, job.frame)
			if err != nil {
				if errors.Is(err, errExperimentalFrameSkipped) {
					continue
				}
				h.recordFailure(err)
				if h.onFailure != nil {
					h.onFailure(err)
				}
				continue
			}
			h.recordCompleted(job, transformed, started)
			if h.onFrame != nil {
				h.onFrame(transformed)
			}
		}
	}
}

func (h *experimentalMediaHub) apply(parent context.Context, frame []byte) (transformed []byte, err error) {
	h.transformMu.Lock()
	defer h.transformMu.Unlock()
	return h.applyLocked(parent, frame)
}

func (h *experimentalMediaHub) applyLocked(parent context.Context, frame []byte) (transformed []byte, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("experimental media transform panicked")
			transformed = nil
		}
	}()
	h.mu.RLock()
	transform := h.transform
	source := h.source
	timeout := h.timeout
	h.mu.RUnlock()
	if transform == nil {
		return nil, errors.New("experimental media transform is unavailable")
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	return transform(ctx, frame, source)
}

func copyExperimentalTSF2Frame(_ context.Context, frame []byte, _ experimentalSourceConfig) ([]byte, error) {
	if meta := parseTSF2(frame); !meta.ok {
		return nil, errors.New("experimental media frame is invalid")
	}
	return append([]byte(nil), frame...), nil
}

func newHDRGainMapTransform(baseURL string, timeout time.Duration) experimentalFrameTransform {
	client := &http.Client{Timeout: timeout}
	return func(ctx context.Context, frame []byte, source experimentalSourceConfig) ([]byte, error) {
		meta := parseTSF2(frame)
		if !meta.ok || !meta.keyFrame || len(frame) <= tsf2HeaderBytes {
			return nil, errExperimentalFrameSkipped
		}
		if source.width <= 0 || source.height <= 0 || source.epoch == 0 || source.epoch != meta.epoch || !strings.HasPrefix(source.codec, "avc1") || !isExperimentalHDRTransport(source.transport) {
			return nil, errors.New("experimental HDR source config is unavailable")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/transform", bytes.NewReader(frame[tsf2HeaderBytes:]))
		if err != nil {
			return nil, errors.New("experimental HDR request is invalid")
		}
		req.Header.Set("Content-Type", "video/h264")
		req.Header.Set("X-Ticket-Width", strconv.Itoa(source.width))
		req.Header.Set("X-Ticket-Height", strconv.Itoa(source.height))
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("experimental HDR transform unavailable: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/jpeg" || resp.Header.Get("X-HDR-Format") != "jpeg-iso-21496-gainmap" {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
			return nil, fmt.Errorf("experimental HDR transform rejected: status %d", resp.StatusCode)
		}
		jpeg, err := io.ReadAll(io.LimitReader(resp.Body, maxExperimentalHDRBytes+1))
		if err != nil || len(jpeg) < 4 || len(jpeg) > maxExperimentalHDRBytes || jpeg[0] != 0xff || jpeg[1] != 0xd8 || jpeg[len(jpeg)-2] != 0xff || jpeg[len(jpeg)-1] != 0xd9 || !bytes.Contains(jpeg, []byte("urn:iso:std:iso:ts:21496:-1")) {
			return nil, errors.New("experimental HDR transform returned an invalid image")
		}
		out := make([]byte, tsf2HeaderBytes+len(jpeg))
		copy(out, frame[:tsf2HeaderBytes])
		out[4] |= tsf2FlagKeyframe
		copy(out[tsf2HeaderBytes:], jpeg)
		return out, nil
	}
}

func isExperimentalHDRTransport(transport string) bool {
	switch transport {
	case "h264-annexb", "hardware-h264-annexb":
		return true
	default:
		return false
	}
}

func experimentalMediaConfig(raw []byte) ([]byte, bool) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil || payload["type"] != "config" {
		return nil, false
	}
	payload["experimentalPipeline"] = experimentalMediaPipelineVersion
	payload["experimentalVisualMode"] = experimentalMediaVisualMode
	encoded, err := json.Marshal(payload)
	return encoded, err == nil
}

func (h *experimentalMediaHub) configure(raw []byte) ([]byte, bool) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil || payload["type"] != "config" {
		return nil, false
	}
	if h == nil {
		return nil, false
	}
	h.mu.Lock()
	h.source = experimentalSourceConfig{
		codec:     stringValue(payload["codec"]),
		transport: stringValue(payload["transport"]),
		width:     int(numberValue(payload["width"])),
		height:    int(numberValue(payload["height"])),
		epoch:     uint64(numberValue(payload["streamEpoch"])),
	}
	pipeline := h.pipeline
	visual := h.visual
	hdr := h.hdr
	h.mu.Unlock()
	return encodeExperimentalMediaConfig(payload, pipeline, visual, hdr)
}

// clientConfig rewrites a direct-stream configuration for the independent
// preview without changing the shared transformer source. A warm-start snapshot
// can be older than a concurrently received phone configuration, so only the
// live phone configuration path may update the shared source epoch.
func (h *experimentalMediaHub) clientConfig(raw []byte) ([]byte, bool) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil || payload["type"] != "config" || h == nil {
		return nil, false
	}
	h.mu.RLock()
	pipeline := h.pipeline
	visual := h.visual
	hdr := h.hdr
	h.mu.RUnlock()
	return encodeExperimentalMediaConfig(payload, pipeline, visual, hdr)
}

func encodeExperimentalMediaConfig(payload map[string]any, pipeline, visual string, hdr bool) ([]byte, bool) {
	payload["experimentalPipeline"] = pipeline
	payload["experimentalVisualMode"] = visual
	if hdr {
		payload["sourceCodec"] = payload["codec"]
		payload["sourceTransport"] = payload["transport"]
		payload["codec"] = "jpeg-iso-21496-gainmap"
		payload["transport"] = "independent-image"
		payload["mimeType"] = "image/jpeg"
		payload["targetDisplayBoost"] = 4
	}
	encoded, err := json.Marshal(payload)
	return encoded, err == nil
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func numberValue(value any) float64 {
	number, _ := value.(float64)
	return number
}

func (h *experimentalMediaHub) recordAdmitted() {
	h.metrics.mu.Lock()
	h.metrics.jobsAdmitted++
	h.metrics.mu.Unlock()
}

func (h *experimentalMediaHub) recordFailure(err error) {
	h.metrics.mu.Lock()
	h.metrics.failures++
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "deadline exceeded") {
		h.metrics.timeouts++
	}
	h.metrics.mu.Unlock()
}

func (h *experimentalMediaHub) recordCompleted(job experimentalFrameJob, transformed []byte, started time.Time) {
	now := time.Now()
	conversionMillis := now.Sub(started).Milliseconds()
	latencyMillis := now.Sub(job.enqueuedAt).Milliseconds()
	meta := parseTSF2(transformed)
	h.metrics.mu.Lock()
	h.metrics.jobsCompleted++
	h.metrics.lastConversionMillis = conversionMillis
	if conversionMillis > h.metrics.maxConversionMillis {
		h.metrics.maxConversionMillis = conversionMillis
	}
	h.metrics.lastServerAdditionalLatencyMS = latencyMillis
	if h.metrics.firstServerOutputDelayMillis == 0 && !h.metrics.activeSince.IsZero() {
		h.metrics.firstServerOutputDelayMillis = now.Sub(h.metrics.activeSince).Milliseconds()
	}
	if meta.ok {
		h.metrics.lastOutputEpoch = meta.epoch
		h.metrics.lastOutputSequence = meta.sequence
	}
	h.metrics.mu.Unlock()
}

func (h *experimentalMediaHub) clientAdded() {
	if h == nil || h.clients.Load() != 1 {
		return
	}
	h.metrics.mu.Lock()
	h.metrics.activeSince = time.Now()
	h.metrics.firstServerOutputDelayMillis = 0
	h.metrics.mu.Unlock()
}

func (h *experimentalMediaHub) clientRemoved() {
	if h == nil || h.clients.Load() != 0 {
		return
	}
	h.metrics.mu.Lock()
	h.metrics.activeSince = time.Time{}
	h.metrics.mu.Unlock()
}

func (h *experimentalMediaHub) snapshot() map[string]any {
	if h == nil {
		return map[string]any{"enabled": false}
	}
	h.metrics.mu.Lock()
	defer h.metrics.mu.Unlock()
	return map[string]any{
		"enabled":                           true,
		"pipelineVersion":                   h.pipeline,
		"visualMode":                        h.visual,
		"activeClients":                     h.clients.Load(),
		"jobsAdmitted":                      h.metrics.jobsAdmitted,
		"jobsCompleted":                     h.metrics.jobsCompleted,
		"jobsDroppedQueue":                  h.metrics.jobsDroppedQueue,
		"deltaFramesSkipped":                h.metrics.deltaFramesSkipped,
		"failures":                          h.metrics.failures,
		"timeouts":                          h.metrics.timeouts,
		"lastConversionMillis":              h.metrics.lastConversionMillis,
		"maxConversionMillis":               h.metrics.maxConversionMillis,
		"lastServerAdditionalLatencyMillis": h.metrics.lastServerAdditionalLatencyMS,
		"firstServerOutputDelayMillis":      h.metrics.firstServerOutputDelayMillis,
		"lastOutputEpoch":                   h.metrics.lastOutputEpoch,
		"lastOutputSequence":                h.metrics.lastOutputSequence,
	}
}

func (s *Server) handleExperimentalMediaCapability(w http.ResponseWriter, r *http.Request, _ auth.Identity, _ string, _ state.Snapshot) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"allowed":         true,
		"pipelineVersion": s.experimental.pipeline,
		"visualMode":      s.experimental.visual,
		"mimeType":        map[bool]string{true: "image/jpeg", false: "video/h264"}[s.experimental.hdr],
		"requiresHDR":     s.experimental.hdr,
		"fixtureURL":      map[bool]string{true: "/static/hdr-capability-fixture.jpg", false: ""}[s.experimental.hdr],
	})
}

func (s *Server) handleExperimentalMediaSocket(w http.ResponseWriter, r *http.Request) {
	id, sessionID, _, ok := s.identifyMember(w, r)
	if !ok {
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionDisabled,
		Subprotocols:    []string{"ticket.experimental-media.v1"},
	})
	if err != nil {
		return
	}
	c := &client{conn: conn, sessionID: sessionID, email: id.Email}
	if !s.tryAddExperimentalClient(c) {
		_ = conn.Close(websocket.StatusPolicyViolation, "experimental connection limit reached")
		return
	}
	c.startVideoWriter()
	s.sendExperimentalMediaWarmStart(c)
	defer func() {
		c.stopVideoWriter()
		s.removeExperimentalClient(c)
		_ = conn.Close(websocket.StatusNormalClosure, "closed")
	}()
	for {
		if _, _, err := conn.Read(r.Context()); err != nil {
			return
		}
	}
}

func (s *Server) tryAddExperimentalClient(c *client) bool {
	if s == nil || c == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.experimentalClients) >= maxExperimentalMediaClients {
		return false
	}
	for existing := range s.experimentalClients {
		if existing.sessionID == c.sessionID {
			return false
		}
	}
	s.experimentalClients[c] = struct{}{}
	if s.experimental != nil {
		s.experimental.clients.Add(1)
		s.experimental.clientAdded()
	}
	return true
}

func (s *Server) removeExperimentalClient(c *client) {
	if s == nil || c == nil {
		return
	}
	s.mu.Lock()
	if _, exists := s.experimentalClients[c]; exists {
		delete(s.experimentalClients, c)
		if s.experimental != nil {
			s.experimental.clients.Add(-1)
			s.experimental.clientRemoved()
		}
	}
	s.mu.Unlock()
}

func (s *Server) experimentalClientSnapshot() []*client {
	s.mu.Lock()
	defer s.mu.Unlock()
	clients := make([]*client, 0, len(s.experimentalClients))
	for c := range s.experimentalClients {
		clients = append(clients, c)
	}
	return clients
}

func (s *Server) sendExperimentalMediaWarmStart(c *client) {
	expectedConfigGeneration := c.videoConfigGenerationSnapshot()
	config, keyFrame := s.direct.experimentalWarmStart()
	config, ok := s.experimental.clientConfig(config)
	if !ok {
		return
	}
	transformedKeyFrame := []byte(nil)
	if len(keyFrame) > 0 && s.experimental != nil {
		s.experimental.transformMu.Lock()
		defer s.experimental.transformMu.Unlock()
		_, latestKeyFrame := s.direct.warmStart()
		if !experimentalWarmStartStillCurrent(keyFrame, latestKeyFrame) {
			c.enqueueWarmStart(config, nil, expectedConfigGeneration)
			return
		}
		job := experimentalFrameJob{frame: keyFrame, enqueuedAt: time.Now()}
		s.experimental.recordAdmitted()
		started := time.Now()
		if transformed, err := s.experimental.applyLocked(context.Background(), keyFrame); err == nil {
			transformedKeyFrame = transformed
			s.experimental.recordCompleted(job, transformed, started)
		} else if !errors.Is(err, errExperimentalFrameSkipped) {
			s.experimental.recordFailure(err)
		}
		c.enqueueWarmStart(config, transformedKeyFrame, expectedConfigGeneration)
		return
	}
	c.enqueueWarmStart(config, nil, expectedConfigGeneration)
}

func experimentalWarmStartStillCurrent(original, latest []byte) bool {
	originalMeta := parseTSF2(original)
	latestMeta := parseTSF2(latest)
	return originalMeta.ok && originalMeta.keyFrame && latestMeta.ok && latestMeta.keyFrame &&
		originalMeta.epoch == latestMeta.epoch && originalMeta.sequence == latestMeta.sequence
}

func (s *Server) broadcastExperimentalConfig(raw []byte, _ []byte) {
	config, ok := s.experimental.configure(raw)
	if !ok {
		return
	}
	for _, c := range s.experimentalClientSnapshot() {
		c.enqueuePhoneConfig(config, nil)
	}
}

func (s *Server) broadcastExperimentalFrame(frame []byte) {
	for _, c := range s.experimentalClientSnapshot() {
		c.enqueueVideoFrame(frame)
	}
}

func (s *Server) failExperimentalClients(_ error) {
	for _, c := range s.experimentalClientSnapshot() {
		_ = c.conn.Close(websocket.StatusInternalError, "experimental media unavailable")
	}
}
