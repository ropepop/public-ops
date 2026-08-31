package web

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	"nhooyr.io/websocket"
)

// Each viewer owns an independent writer. A slow socket can therefore retain
// at most one newer independent frame without delaying phone ingestion or any
// other viewer.
const (
	controlQueueMaxMessages   = 16
	controlQueueMaxBytes      = 64 * 1024
	videoQueueMaxBytes        = 2 * 1024 * 1024
	videoFrameWriteTimeout    = 250 * time.Millisecond
	feedbackMinInterval       = 250 * time.Millisecond
	feedbackMaxAgeMillis      = 60_000
	feedbackMaxQueueSize      = 32
	feedbackStalledAgeMillis  = 3_000
	videoWrittenEvidenceLimit = 128
	wallClockMicrosFloor      = uint64(1_000_000_000_000_000)
)

type videoWrittenFrameEvidence struct {
	epoch     uint64
	sequence  uint64
	decodable bool
}

type queuedControlMessage struct {
	data   []byte
	config bool
	epoch  uint64
}

type queuedVideoFrame struct {
	data             []byte
	meta             tsf2Metadata
	queuedAt         time.Time
	visualAge        time.Duration
	configGeneration uint64
}

// streamFeedback is cumulative rather than an acknowledgement for an
// individual frame. renderedKeyframeSequence remains accepted during the
// rolling browser transition, but independent all-intra delivery does not use
// it for admission, pacing, or recovery decisions.
type streamFeedback struct {
	Type                     string `json:"type"`
	Version                  int    `json:"version"`
	Epoch                    uint64 `json:"epoch"`
	ReceivedSequence         uint64 `json:"receivedSequence"`
	DecodedSequence          uint64 `json:"decodedSequence"`
	RenderedSequence         uint64 `json:"renderedSequence"`
	RenderedKeyframeSequence uint64 `json:"renderedKeyframeSequence"`
	DecoderQueueSize         int64  `json:"decoderQueueSize"`
	RenderedVisualAgeMillis  int64  `json:"renderedVisualAgeMillis"`
	Visibility               string `json:"visibility,omitempty"`
}

type streamFeedbackOutcome struct {
	accepted        bool
	transition      bool
	cause           string
	state           string
	receivedDelta   uint64
	decodedDelta    uint64
	renderedDelta   uint64
	lag             uint64
	queue           int64
	visualAgeMillis int64
}

func (c *client) startVideoWriter() {
	// Detached clients are useful for admission tests. Live clients always own
	// a connection before the first item is enqueued.
	if c.conn == nil {
		return
	}
	c.writerStartOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		c.videoMu.Lock()
		c.writerCancel = cancel
		c.writerWake = make(chan struct{}, 1)
		c.writerDone = make(chan struct{})
		c.videoMu.Unlock()
		go c.videoWriterLoop(ctx)
	})
}

func (c *client) stopVideoWriter() {
	c.writerStopOnce.Do(func() {
		c.videoMu.Lock()
		cancel := c.writerCancel
		done := c.writerDone
		c.writerClosed = true
		c.clearVideoFrameInFlightLocked(nil)
		c.videoQueue = nil
		c.videoQueueBytes = 0
		c.videoMu.Unlock()
		if cancel != nil {
			cancel()
		}
		if done != nil {
			select {
			case <-done:
			case <-time.After(streamControlWriteTimeout + time.Second):
			}
		}
	})
}

func (c *client) signalVideoWriter() {
	c.videoMu.Lock()
	wake := c.writerWake
	c.videoMu.Unlock()
	if wake == nil {
		return
	}
	select {
	case wake <- struct{}{}:
	default:
	}
}

func (c *client) videoWriterLoop(ctx context.Context) {
	c.videoMu.Lock()
	done := c.writerDone
	wake := c.writerWake
	c.videoMu.Unlock()
	if done != nil {
		defer close(done)
	}
	for {
		if !c.writeNextVideoItem(ctx) {
			return
		}
		for {
			c.videoMu.Lock()
			hasWork := len(c.controlQueue) > 0 || len(c.videoQueue) > 0
			c.videoMu.Unlock()
			if !hasWork {
				break
			}
			if !c.writeNextVideoItem(ctx) {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		}
	}
}

type videoWriteItem struct {
	messageType websocket.MessageType
	data        []byte
	frame       *queuedVideoFrame
}

func (c *client) markVideoFrameInFlightLocked(frame queuedVideoFrame) {
	c.videoInFlight = frame.meta.ok
	c.videoInFlightEpoch = frame.meta.epoch
	c.videoInFlightSeq = frame.meta.sequence
	c.videoInFlightConfigGen = frame.configGeneration
}

// Matching the frame prevents a late completion from clearing a newer
// generation's in-flight marker.
func (c *client) clearVideoFrameInFlightLocked(frame *queuedVideoFrame) {
	if frame != nil && (!c.videoInFlight ||
		c.videoInFlightEpoch != frame.meta.epoch ||
		c.videoInFlightSeq != frame.meta.sequence ||
		c.videoInFlightConfigGen != frame.configGeneration) {
		return
	}
	c.videoInFlight = false
	c.videoInFlightEpoch = 0
	c.videoInFlightSeq = 0
	c.videoInFlightConfigGen = 0
}

func (c *client) clearVideoFrameInFlight(frame *queuedVideoFrame) {
	c.videoMu.Lock()
	c.clearVideoFrameInFlightLocked(frame)
	c.videoMu.Unlock()
}

func queuedFrameExpired(frame queuedVideoFrame, now time.Time) bool {
	if frame.queuedAt.IsZero() {
		return false
	}
	queuedFor := now.Sub(frame.queuedAt)
	if queuedFor < 0 {
		queuedFor = 0
	}
	return frame.visualAge+queuedFor > liveFreshMaxAge
}

func (c *client) nextVideoWriteItem() (videoWriteItem, bool) {
	c.videoMu.Lock()
	defer c.videoMu.Unlock()
	if len(c.controlQueue) > 0 {
		item := c.controlQueue[0]
		c.controlQueue = c.controlQueue[1:]
		c.controlQueueBytes -= len(item.data)
		if c.controlQueueBytes < 0 {
			c.controlQueueBytes = 0
		}
		return videoWriteItem{messageType: websocket.MessageText, data: item.data}, true
	}
	for len(c.videoQueue) > 0 {
		item := c.videoQueue[0]
		c.videoQueue = nil
		c.videoQueueBytes = 0
		if queuedFrameExpired(item, time.Now()) {
			continue
		}
		c.markVideoFrameInFlightLocked(item)
		return videoWriteItem{messageType: websocket.MessageBinary, data: item.data, frame: &item}, true
	}
	return videoWriteItem{}, false
}

// Control messages always have priority. A config and its optional warm frame
// are admitted under one lock so media cannot overtake decoder configuration.
func (c *client) writeNextVideoItem(ctx context.Context) bool {
	item, ok := c.nextVideoWriteItem()
	if !ok {
		return true
	}
	if c.conn == nil {
		c.clearVideoFrameInFlight(item.frame)
		return false
	}
	if item.frame != nil {
		c.startupTraceOrderMu.Lock()
		defer c.startupTraceOrderMu.Unlock()
	}
	writeTimeout := streamControlWriteTimeout
	if item.frame != nil {
		writeTimeout = videoFrameWriteTimeout
	}
	writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
	err := c.conn.Write(writeCtx, item.messageType, item.data)
	canceled := writeCtx.Err()
	cancel()
	if err != nil || canceled != nil {
		c.videoMu.Lock()
		c.clearVideoFrameInFlightLocked(item.frame)
		c.writerClosed = true
		if canceled != nil {
			c.writerCloseReason = "write_timeout"
		} else {
			c.writerCloseReason = "write_failed"
		}
		c.videoMu.Unlock()
		_ = c.conn.Close(websocket.StatusPolicyViolation, "video client too slow")
		return false
	}
	if item.frame != nil {
		c.noteVideoFrameWritten(*item.frame)
	}
	return true
}

func (c *client) noteVideoFrameWritten(frame queuedVideoFrame) {
	c.noteVideoFrameWrittenAt(frame, time.Now())
}

func (c *client) noteVideoFrameWrittenAt(frame queuedVideoFrame, writtenAt time.Time) {
	if writtenAt.IsZero() {
		writtenAt = time.Now()
	}
	c.videoMu.Lock()
	c.clearVideoFrameInFlightLocked(&frame)
	frameBelongsToCurrentConfig := frame.configGeneration == c.videoConfigGeneration
	if frame.meta.ok && frame.meta.keyFrame && frameBelongsToCurrentConfig {
		c.videoEpoch = frame.meta.epoch
		c.videoLastWrittenSeq = frame.meta.sequence
		c.videoWrittenEpoch = frame.meta.epoch
		c.videoWrittenSequence = frame.meta.sequence
		c.videoWrittenEvidence = append(c.videoWrittenEvidence, videoWrittenFrameEvidence{
			epoch: frame.meta.epoch, sequence: frame.meta.sequence, decodable: true,
		})
		if len(c.videoWrittenEvidence) > videoWrittenEvidenceLimit {
			copy(c.videoWrittenEvidence, c.videoWrittenEvidence[len(c.videoWrittenEvidence)-videoWrittenEvidenceLimit:])
			c.videoWrittenEvidence = c.videoWrittenEvidence[:videoWrittenEvidenceLimit]
		}
	}
	c.videoMu.Unlock()
	if frame.meta.ok && frame.meta.keyFrame && frameBelongsToCurrentConfig && c.onVideoFrameWritten != nil {
		c.onVideoFrameWritten(frame.meta)
	}
}

func (c *client) enqueueControl(value []byte) {
	if len(value) == 0 {
		return
	}
	c.startVideoWriter()
	data := append([]byte(nil), value...)
	var payload struct {
		Type        string `json:"type"`
		StreamEpoch uint64 `json:"streamEpoch"`
	}
	_ = json.Unmarshal(data, &payload)
	message := queuedControlMessage{data: data, config: payload.Type == "config", epoch: payload.StreamEpoch}
	c.videoMu.Lock()
	accepted := c.enqueueControlLocked(message)
	if accepted && message.config {
		c.videoBroadcastReady = true
	}
	c.videoMu.Unlock()
	if accepted {
		c.signalVideoWriter()
	}
}

func (c *client) readyForVideoBroadcast() bool {
	c.videoMu.Lock()
	defer c.videoMu.Unlock()
	return c.videoBroadcastReady
}

// A new decoder configuration supersedes all pending media and invalidates
// every previous write-evidence generation.
func (c *client) enqueueControlLocked(message queuedControlMessage) bool {
	if len(message.data) == 0 || len(message.data) > controlQueueMaxBytes {
		return false
	}
	if message.config {
		c.clearVideoFrameInFlightLocked(nil)
		for i := len(c.controlQueue) - 1; i >= 0; i-- {
			if c.controlQueue[i].config {
				c.controlQueueBytes -= len(c.controlQueue[i].data)
				c.controlQueue = append(c.controlQueue[:i], c.controlQueue[i+1:]...)
			}
		}
		c.videoQueue = nil
		c.videoQueueBytes = 0
		c.videoLastWrittenSeq = 0
		c.videoEpoch = message.epoch
		c.videoConfigGeneration++
		c.videoWrittenEpoch = 0
		c.videoWrittenSequence = 0
		c.videoWrittenEvidence = nil
	}
	for len(c.controlQueue) >= controlQueueMaxMessages || c.controlQueueBytes+len(message.data) > controlQueueMaxBytes {
		removeAt := -1
		for i := range c.controlQueue {
			if !c.controlQueue[i].config {
				removeAt = i
				break
			}
		}
		if removeAt < 0 {
			if !message.config {
				return false
			}
			removeAt = 0
		}
		c.controlQueueBytes -= len(c.controlQueue[removeAt].data)
		c.controlQueue = append(c.controlQueue[:removeAt], c.controlQueue[removeAt+1:]...)
	}
	c.controlQueue = append(c.controlQueue, message)
	c.controlQueueBytes += len(message.data)
	return true
}

func (c *client) videoConfigGenerationSnapshot() uint64 {
	c.videoMu.Lock()
	defer c.videoMu.Unlock()
	return c.videoConfigGeneration
}

func (c *client) enqueueWarmStart(config, keyFrame []byte, expectedConfigGeneration uint64) (bool, bool, bool) {
	return c.enqueueConfigAndKeyframe(config, keyFrame, &expectedConfigGeneration)
}

func (c *client) enqueuePhoneConfig(config, keyFrame []byte) (bool, bool) {
	configAccepted, frameAccepted, _ := c.enqueueConfigAndKeyframe(config, keyFrame, nil)
	return configAccepted, frameAccepted
}

func (c *client) enqueueConfigAndKeyframe(config, keyFrame []byte, expectedConfigGeneration *uint64) (bool, bool, bool) {
	if len(config) == 0 {
		return false, false, false
	}
	c.startVideoWriter()
	var payload struct {
		Type        string `json:"type"`
		StreamEpoch uint64 `json:"streamEpoch"`
	}
	if err := json.Unmarshal(config, &payload); err != nil || payload.Type != "config" {
		return false, false, false
	}
	message := queuedControlMessage{data: append([]byte(nil), config...), config: true, epoch: payload.StreamEpoch}
	var frameMeta tsf2Metadata
	frameMatches := false
	if len(keyFrame) > 0 && len(keyFrame) <= videoQueueMaxBytes {
		frameMeta = parseTSF2(keyFrame)
		frameMatches = frameMeta.ok && frameMeta.keyFrame && frameMeta.epoch == payload.StreamEpoch
	}
	now := time.Now()
	c.videoMu.Lock()
	if expectedConfigGeneration != nil && c.videoConfigGeneration != *expectedConfigGeneration {
		c.videoMu.Unlock()
		return false, false, true
	}
	acceptedConfig := c.enqueueControlLocked(message)
	if acceptedConfig {
		c.videoBroadcastReady = true
	}
	acceptedFrame := false
	if acceptedConfig && frameMatches {
		visualAge, fresh := frameVisualAge(frameMeta, now)
		if fresh {
			c.videoQueue = []queuedVideoFrame{{
				data: append([]byte(nil), keyFrame...), meta: frameMeta, queuedAt: now,
				visualAge: visualAge, configGeneration: c.videoConfigGeneration,
			}}
			c.videoQueueBytes = len(keyFrame)
			acceptedFrame = true
		}
	}
	c.videoMu.Unlock()
	if acceptedConfig {
		c.signalVideoWriter()
	}
	return acceptedConfig, acceptedFrame, false
}

func frameVisualAge(meta tsf2Metadata, now time.Time) (time.Duration, bool) {
	if meta.timestamp < wallClockMicrosFloor {
		return 0, true
	}
	capturedAt := time.UnixMicro(int64(meta.timestamp))
	age := now.Sub(capturedAt)
	if age < -phoneClockFutureTolerance {
		return 0, false
	}
	if age < 0 {
		age = 0
	}
	return age, age <= liveFreshMaxAge
}

func (c *client) enqueueVideoFrame(value []byte) {
	if len(value) == 0 || len(value) > videoQueueMaxBytes {
		return
	}
	c.startVideoWriter()
	meta := parseTSF2(value)
	if !meta.ok || !meta.keyFrame {
		return
	}
	now := time.Now()
	visualAge, fresh := frameVisualAge(meta, now)
	if !fresh {
		return
	}
	c.videoMu.Lock()
	if c.videoEpoch != 0 && meta.epoch != c.videoEpoch {
		c.videoMu.Unlock()
		return
	}
	if c.videoEpoch == 0 {
		c.videoEpoch = meta.epoch
	}
	newest := c.videoLastWrittenSeq
	if c.videoInFlight && c.videoInFlightConfigGen == c.videoConfigGeneration && c.videoInFlightEpoch == meta.epoch && c.videoInFlightSeq > newest {
		newest = c.videoInFlightSeq
	}
	if len(c.videoQueue) > 0 && c.videoQueue[0].meta.epoch == meta.epoch && c.videoQueue[0].meta.sequence > newest {
		newest = c.videoQueue[0].meta.sequence
	}
	if meta.sequence <= newest {
		c.videoMu.Unlock()
		return
	}
	c.videoQueue = []queuedVideoFrame{{
		data: append([]byte(nil), value...), meta: meta, queuedAt: now,
		visualAge: visualAge, configGeneration: c.videoConfigGeneration,
	}}
	c.videoQueueBytes = len(value)
	c.videoMu.Unlock()
	c.signalVideoWriter()
}

func (c *client) sendText(_ context.Context, value []byte) {
	c.enqueueControl(value)
}

func (c *client) sendBinary(_ context.Context, value []byte) {
	c.enqueueVideoFrame(value)
}

func (c *client) sendBinaryLatest(_ context.Context, value []byte) {
	c.enqueueVideoFrame(value)
}

func decodeStreamFeedback(data []byte, _ ...time.Time) (streamFeedback, bool) {
	var feedback streamFeedback
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&feedback); err != nil || feedback.Type != "stream_feedback" || feedback.Version != 1 {
		return streamFeedback{}, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return streamFeedback{}, false
	}
	if feedback.Visibility != "" && feedback.Visibility != "visible" && feedback.Visibility != "hidden" {
		return streamFeedback{}, false
	}
	if feedback.DecoderQueueSize < 0 {
		feedback.DecoderQueueSize = 0
	}
	if feedback.DecoderQueueSize > feedbackMaxQueueSize {
		feedback.DecoderQueueSize = feedbackMaxQueueSize
	}
	if feedback.RenderedVisualAgeMillis < 0 {
		feedback.RenderedVisualAgeMillis = 0
	}
	if feedback.RenderedVisualAgeMillis > feedbackMaxAgeMillis {
		feedback.RenderedVisualAgeMillis = feedbackMaxAgeMillis
	}
	return feedback, true
}

func (c *client) acceptStreamFeedback(data []byte, now time.Time) bool {
	return c.acceptStreamFeedbackOutcome(data, now).accepted
}

func (c *client) acceptStreamFeedbackOutcome(data []byte, now time.Time) (outcome streamFeedbackOutcome) {
	feedback, ok := decodeStreamFeedback(data)
	if !ok {
		return outcome
	}
	if now.IsZero() {
		now = time.Now()
	}
	c.feedbackMu.Lock()
	defer c.feedbackMu.Unlock()
	if !c.lastFeedbackAt.IsZero() && (now.Before(c.lastFeedbackAt) || now.Sub(c.lastFeedbackAt) < feedbackMinInterval) {
		c.feedbackDropped++
		return outcome
	}
	c.videoMu.Lock()
	expectedEpoch := c.videoEpoch
	c.videoMu.Unlock()
	if expectedEpoch != 0 && feedback.Epoch != expectedEpoch {
		c.feedbackDropped++
		return outcome
	}
	if feedback.ReceivedSequence < c.lastFeedbackReceived ||
		feedback.DecodedSequence < c.lastFeedbackDecoded ||
		feedback.RenderedSequence < c.lastFeedbackRendered {
		c.feedbackDropped++
		return outcome
	}

	previousReceived := c.lastFeedbackReceived
	previousDecoded := c.lastFeedbackDecoded
	previousRendered := c.lastFeedbackRendered
	previousState := c.feedbackState
	previousCause := c.feedbackCause
	hadPrevious := c.feedbackCount > 0

	outcome.accepted = true
	outcome.queue = feedback.DecoderQueueSize
	outcome.visualAgeMillis = feedback.RenderedVisualAgeMillis
	if hadPrevious {
		outcome.receivedDelta = feedback.ReceivedSequence - previousReceived
		outcome.decodedDelta = feedback.DecodedSequence - previousDecoded
		outcome.renderedDelta = feedback.RenderedSequence - previousRendered
	}
	lag := uint64(0)
	if feedback.ReceivedSequence >= feedback.RenderedSequence {
		lag = feedback.ReceivedSequence - feedback.RenderedSequence
	}
	outcome.lag = lag

	state, cause := "flowing", "healthy"
	renderStalled := hadPrevious &&
		feedback.RenderedVisualAgeMillis > feedbackStalledAgeMillis &&
		feedback.RenderedSequence == previousRendered
	switch {
	case feedback.Visibility == "hidden":
		state, cause = "hidden", "browser_hidden"
	case feedback.DecoderQueueSize >= 5:
		state, cause = "congested", "decoder_queue_hard"
	case feedback.DecoderQueueSize > 2:
		state, cause = "congested", "decoder_queue_soft"
	case lag > 5:
		state, cause = "congested", "browser_render_lag"
	case renderStalled && (feedback.ReceivedSequence > previousReceived || feedback.DecodedSequence > previousDecoded):
		state, cause = "congested", "browser_render_stall"
	case renderStalled:
		state, cause = "upstream_or_delivery_stalled", "upstream_or_delivery_stall"
	}

	c.lastFeedbackAt = now
	c.lastFeedbackEpoch = feedback.Epoch
	c.lastFeedbackReceived = feedback.ReceivedSequence
	c.lastFeedbackDecoded = feedback.DecodedSequence
	c.lastFeedbackRendered = feedback.RenderedSequence
	c.lastFeedbackQueue = uint64(feedback.DecoderQueueSize)
	c.lastFeedbackAge = uint64(feedback.RenderedVisualAgeMillis)
	c.feedbackState = state
	c.feedbackCause = cause
	c.feedbackVisibility = feedback.Visibility
	c.feedbackCount++

	outcome.state = state
	outcome.cause = cause
	outcome.transition = hadPrevious && (state != previousState || cause != previousCause)
	return outcome
}

func (c *client) feedbackSnapshot() map[string]any {
	c.feedbackMu.Lock()
	defer c.feedbackMu.Unlock()
	c.videoMu.Lock()
	queueFrames := len(c.videoQueue)
	queueBytes := c.videoQueueBytes
	lastWrittenSeq := c.videoLastWrittenSeq
	c.videoMu.Unlock()
	return map[string]any{
		"feedbackCount":       c.feedbackCount,
		"feedbackDropped":     c.feedbackDropped,
		"feedbackAgeMillis":   ageSinceMillis(time.Now(), c.lastFeedbackAt),
		"feedbackEpoch":       c.lastFeedbackEpoch,
		"feedbackReceived":    c.lastFeedbackReceived,
		"feedbackDecoded":     c.lastFeedbackDecoded,
		"feedbackRendered":    c.lastFeedbackRendered,
		"feedbackQueueSize":   c.lastFeedbackQueue,
		"feedbackVisualAge":   c.lastFeedbackAge,
		"feedbackVisibility":  c.feedbackVisibility,
		"feedbackState":       c.feedbackState,
		"feedbackCause":       c.feedbackCause,
		"videoQueueFrames":    queueFrames,
		"videoQueueBytes":     queueBytes,
		"videoLastWrittenSeq": lastWrittenSeq,
	}
}
