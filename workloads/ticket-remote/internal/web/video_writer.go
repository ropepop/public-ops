package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"nhooyr.io/websocket"

	"ticketremote/internal/phone"
)

// Each viewer owns an independent writer. A slow socket can therefore retain
// at most one newer independent frame without delaying phone ingestion or any
// other viewer.
const (
	controlQueueMaxMessages     = 16
	controlQueueMaxBytes        = 64 * 1024
	videoReceiptLivenessTimeout = 3 * time.Second
	browserClockProbeInterval   = 250 * time.Millisecond
	browserClockProbeIDMax      = 64
	maxSafeJSONInteger          = int64(9_007_199_254_740_991)
	videoWrittenEvidenceLimit   = 128
	wallClockMicrosFloor        = uint64(1_000_000_000_000_000)
)

type queuedControlMessage struct {
	data       []byte
	config     bool
	epoch      uint64
	generation uint64
}

type queuedVideoFrame struct {
	data             []byte
	meta             tsf3Metadata
	queuedAt         time.Time
	visualAge        time.Duration
	configGeneration uint64
}

// streamFeedback is cumulative. Version 2's receivedSequence is also the
// per-socket delivery credit. Older feedback is rejected at the V2 cutover.
type streamFeedback struct {
	Type                     string `json:"type"`
	Version                  int    `json:"version"`
	Epoch                    uint64 `json:"epoch"`
	ConfigGeneration         uint64 `json:"configGeneration,omitempty"`
	ReceivedSequence         uint64 `json:"receivedSequence"`
	DecodedSequence          uint64 `json:"decodedSequence"`
	RenderedSequence         uint64 `json:"renderedSequence"`
	PresentedSequence        uint64 `json:"presentedSequence,omitempty"`
	RenderedKeyframeSequence uint64 `json:"renderedKeyframeSequence"`
	DecoderQueueSize         int64  `json:"decoderQueueSize"`
	RenderedVisualAgeMillis  int64  `json:"renderedVisualAgeMillis"`
	AgeKnown                 bool   `json:"ageKnown,omitempty"`
	Visibility               string `json:"visibility,omitempty"`
}

type streamFeedbackOutcome struct {
	presented       bool
	receiptReleased bool
	becameVisible   bool
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
		c.videoInFlight = nil
		c.videoPending = nil
		c.clearVideoReceiptLocked()
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

func (c *client) videoWriterCloseReason() string {
	c.videoMu.Lock()
	defer c.videoMu.Unlock()
	return c.writerCloseReason
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
		for c.videoWriterHasRunnableWork() {
			if deadline := c.videoReceiptDeadline(); !deadline.IsZero() && !time.Now().Before(deadline) {
				if c.closeExpiredVideoReceipt() {
					return
				}
				continue
			}
			if !c.writeNextVideoItem(ctx) {
				return
			}
		}
		deadline := c.videoReceiptDeadline()
		if deadline.IsZero() {
			select {
			case <-ctx.Done():
				return
			case <-wake:
			}
			continue
		}
		wait := time.Until(deadline)
		if wait <= 0 {
			if c.closeExpiredVideoReceipt() {
				return
			}
			continue
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-wake:
			timer.Stop()
		case <-timer.C:
			if c.closeExpiredVideoReceipt() {
				return
			}
		}
	}
}

func (c *client) videoWriterHasRunnableWork() bool {
	c.videoMu.Lock()
	defer c.videoMu.Unlock()
	return !c.writerClosed && (len(c.controlQueue) > 0 || (c.videoPending != nil && c.videoReceiptSequence == 0))
}

func (c *client) videoReceiptDeadline() time.Time {
	c.videoMu.Lock()
	defer c.videoMu.Unlock()
	return c.videoReceiptDeadlineAt
}

func (c *client) clearVideoReceiptLocked() {
	c.videoReceiptSequence = 0
	c.videoReceiptDeadlineAt = time.Time{}
}

func (c *client) closeExpiredVideoReceipt() bool {
	now := time.Now()
	c.videoMu.Lock()
	if c.videoReceiptDeadlineAt.IsZero() || now.Before(c.videoReceiptDeadlineAt) {
		c.videoMu.Unlock()
		return false
	}
	c.writerClosed = true
	c.writerCloseReason = "receipt_timeout"
	c.videoPending = nil
	c.clearVideoReceiptLocked()
	c.videoMu.Unlock()
	if c.conn != nil {
		_ = c.conn.CloseNow()
	}
	return true
}

type videoWriteItem struct {
	messageType websocket.MessageType
	data        []byte
	frame       *queuedVideoFrame
	control     *queuedControlMessage
}

// Matching the frame prevents a late completion from clearing a newer
// generation's in-flight marker.
func (c *client) clearVideoFrameInFlightLocked(frame *queuedVideoFrame) {
	if c.videoInFlight == frame {
		c.videoInFlight = nil
	}
}

func (c *client) feedbackMatchesVideoFrameInFlightLocked(feedback streamFeedback) bool {
	frame := c.videoInFlight
	return frame != nil && feedback.ConfigGeneration == frame.configGeneration &&
		feedback.Epoch == frame.meta.epoch && feedback.ReceivedSequence == frame.meta.sequence
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

func queuedFrameExpiresAt(frame queuedVideoFrame) time.Time {
	if frame.queuedAt.IsZero() {
		return time.Time{}
	}
	remaining := liveFreshMaxAge - frame.visualAge
	if remaining < 0 {
		remaining = 0
	}
	return frame.queuedAt.Add(remaining)
}

func videoFrameWriteDeadline(frame queuedVideoFrame, now time.Time) time.Time {
	if now.IsZero() {
		now = time.Now()
	}
	deadline := queuedFrameExpiresAt(frame)
	maximum := now.Add(liveFreshMaxAge)
	if deadline.IsZero() || deadline.After(maximum) {
		return maximum
	}
	return deadline
}

func videoWriteFailureReason(err error, timedOut bool) string {
	if timedOut {
		return "write_timeout"
	}
	if err == nil {
		return ""
	}
	return "write_failed"
}

func (c *client) nextVideoWriteItem() (videoWriteItem, bool) {
	c.videoMu.Lock()
	defer c.videoMu.Unlock()
	if c.writerClosed {
		return videoWriteItem{}, false
	}
	if len(c.controlQueue) > 0 {
		item := c.controlQueue[0]
		c.controlQueue = c.controlQueue[1:]
		c.controlQueueBytes -= len(item.data)
		if c.controlQueueBytes < 0 {
			c.controlQueueBytes = 0
		}
		return videoWriteItem{messageType: websocket.MessageText, data: item.data, control: &item}, true
	}
	if c.writerClosed || c.videoReceiptSequence != 0 {
		return videoWriteItem{}, false
	}
	if item := c.videoPending; item != nil {
		c.videoPending = nil
		if !queuedFrameExpired(*item, time.Now()) {
			c.videoInFlight = item
			return videoWriteItem{messageType: websocket.MessageBinary, data: item.data, frame: item}, true
		}
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
		return false
	}
	if item.frame != nil && c.trial != nil {
		c.videoMu.Lock()
		visible := c.videoV2Visibility == "visible"
		c.videoMu.Unlock()
		if !visible {
			c.videoMu.Lock()
			c.clearVideoFrameInFlightLocked(item.frame)
			c.videoMu.Unlock()
			c.trial.pause(false)
			return true
		}
		if err := c.trial.admit(ctx); err != nil {
			c.videoMu.Lock()
			c.clearVideoFrameInFlightLocked(item.frame)
			c.writerClosed = true
			c.writerCloseReason = "trial_ended"
			c.videoMu.Unlock()
			_ = c.conn.Close(websocket.StatusPolicyViolation, "trial unavailable")
			return false
		}
		if queuedFrameExpired(*item.frame, time.Now()) {
			c.videoMu.Lock()
			c.clearVideoFrameInFlightLocked(item.frame)
			c.videoMu.Unlock()
			c.trial.pause(false)
			return true
		}
	}
	var writeCtx context.Context
	var cancel context.CancelFunc
	if item.frame != nil {
		// A slow-but-feasible link gets the picture's complete remaining source
		// freshness budget. The global freshness window is the hard upper bound.
		deadline := videoFrameWriteDeadline(*item.frame, time.Now())
		if c.trial != nil {
			writeCtx, cancel = c.trial.writeContext(ctx, deadline)
		} else {
			writeCtx, cancel = context.WithDeadline(ctx, deadline)
		}
	} else {
		writeCtx, cancel = context.WithTimeout(ctx, streamControlWriteTimeout)
	}
	err := c.conn.Write(writeCtx, item.messageType, item.data)
	timedOut := errors.Is(writeCtx.Err(), context.DeadlineExceeded)
	cancel()
	if failureReason := videoWriteFailureReason(err, timedOut); failureReason != "" {
		c.videoMu.Lock()
		c.clearVideoFrameInFlightLocked(item.frame)
		c.writerClosed = true
		c.writerCloseReason = failureReason
		c.videoMu.Unlock()
		_ = c.conn.Close(websocket.StatusPolicyViolation, "video client too slow")
		return false
	}
	if item.frame != nil {
		c.noteVideoFrameWrittenAt(item.frame, time.Now())
		if c.trial != nil {
			c.trial.written(*item.frame)
		}
	} else if item.control != nil && item.control.config {
		c.noteVideoConfigWritten(*item.control)
	}
	return true
}

func (c *client) noteVideoFrameWrittenAt(frame *queuedVideoFrame, writtenAt time.Time) {
	c.videoMu.Lock()
	c.clearVideoFrameInFlightLocked(frame)
	frameBelongsToCurrentConfig := !c.writerClosed && frame.configGeneration == c.videoConfigGeneration
	if frame.meta.ok && frame.meta.keyFrame && frameBelongsToCurrentConfig {
		c.videoEpoch = frame.meta.epoch
		c.videoWrittenSequence = frame.meta.sequence
		c.videoWrittenEvidence = append(c.videoWrittenEvidence, frame.meta.sequence)
		if len(c.videoWrittenEvidence) > videoWrittenEvidenceLimit {
			copy(c.videoWrittenEvidence, c.videoWrittenEvidence[len(c.videoWrittenEvidence)-videoWrittenEvidenceLimit:])
			c.videoWrittenEvidence = c.videoWrittenEvidence[:videoWrittenEvidenceLimit]
		}
		// The browser reader may process and ACK a complete message after
		// conn.Write has returned but before this writer goroutine records the
		// successful write. That exact in-flight ACK already restored credit;
		// do not arm a timeout for a picture known to have arrived.
		if c.videoV2FeedbackReceived < frame.meta.sequence {
			c.videoReceiptSequence = frame.meta.sequence
			// Picture usefulness and transport liveness are separate clocks.
			// The browser ACKs even a complete picture that has become too old
			// to present, so give that receipt a bounded interval after the
			// successful write instead of reusing the consumed source deadline.
			c.videoReceiptDeadlineAt = writtenAt.Add(videoReceiptLivenessTimeout)
		}
	}
	c.videoMu.Unlock()

}

func (c *client) noteVideoConfigWritten(message queuedControlMessage) {
	if !message.config || message.epoch == 0 || message.generation == 0 {
		return
	}
	c.videoMu.Lock()
	current := message.epoch == c.videoEpoch && message.generation == c.videoConfigGeneration && !c.writerClosed
	if current {
		c.videoConfigWrittenEpoch = message.epoch
		c.videoConfigWrittenGen = message.generation
	}
	callback := c.onVideoConfigWritten
	c.videoMu.Unlock()
	if current && callback != nil {
		callback(message.epoch, message.generation)
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
	if c.writerClosed {
		return false
	}
	if message.config {
		var ok bool
		message.data, ok = videoConfigWithFeedback(message.data, c.videoConfigGeneration+1)
		if !ok {
			return false
		}
	}
	if len(message.data) == 0 || len(message.data) > controlQueueMaxBytes {
		return false
	}
	if message.config {
		c.videoInFlight = nil
		for i := len(c.controlQueue) - 1; i >= 0; i-- {
			if c.controlQueue[i].config {
				c.controlQueueBytes -= len(c.controlQueue[i].data)
				c.controlQueue = append(c.controlQueue[:i], c.controlQueue[i+1:]...)
			}
		}
		c.videoPending = nil
		c.videoEpoch = message.epoch
		c.videoConfigGeneration++
		message.generation = c.videoConfigGeneration
		c.videoConfigWrittenEpoch = 0
		c.videoConfigWrittenGen = 0
		c.videoWrittenSequence = 0
		c.videoWrittenEvidence = nil
		c.videoV2FeedbackReceived = 0
		c.videoV2FeedbackDecoded = 0
		c.videoV2FeedbackRendered = 0
		c.videoV2FeedbackPresented = 0
		c.clearVideoReceiptLocked()
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

func videoConfigWithFeedback(data []byte, generation uint64) ([]byte, bool) {
	if generation == 0 {
		return nil, false
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil || payload["type"] != "config" {
		return nil, false
	}
	payload["feedbackVersion"] = 2
	payload["feedbackConfigGeneration"] = generation
	out, err := json.Marshal(payload)
	return out, err == nil
}

func (c *client) videoConfigGenerationSnapshot() uint64 {
	c.videoMu.Lock()
	defer c.videoMu.Unlock()
	return c.videoConfigGeneration
}

func (c *client) enqueueConfigAndKeyframe(config, keyFrame []byte, expectedConfigGeneration *uint64) {
	if len(config) == 0 {
		return
	}
	c.startVideoWriter()
	var payload struct {
		Type        string `json:"type"`
		StreamEpoch uint64 `json:"streamEpoch"`
	}
	if err := json.Unmarshal(config, &payload); err != nil || payload.Type != "config" {
		return
	}
	message := queuedControlMessage{data: append([]byte(nil), config...), config: true, epoch: payload.StreamEpoch}
	var frameMeta tsf3Metadata
	frameMatches := false
	if len(keyFrame) > 0 && len(keyFrame) <= int(phone.MaxVideoMessageBytes) {
		frameMeta = parseTSF3(keyFrame)
		frameMatches = frameMeta.ok && len(keyFrame)-frameMeta.headerBytes <= int(phone.MaxVideoPayloadBytes) &&
			frameMeta.keyFrame && frameMeta.epoch == payload.StreamEpoch
	}
	now := time.Now()
	c.videoMu.Lock()
	if expectedConfigGeneration != nil && c.videoConfigGeneration != *expectedConfigGeneration {
		c.videoMu.Unlock()
		return
	}
	acceptedConfig := c.enqueueControlLocked(message)
	if acceptedConfig {
		c.videoBroadcastReady = true
	}
	if acceptedConfig && frameMatches {
		visualAge, fresh := frameVisualAge(frameMeta, now)
		if fresh {
			c.videoPending = &queuedVideoFrame{
				data: append([]byte(nil), keyFrame...), meta: frameMeta, queuedAt: now,
				visualAge: visualAge, configGeneration: c.videoConfigGeneration,
			}
		}
	}
	c.videoMu.Unlock()
	if acceptedConfig {
		c.signalVideoWriter()
	}
}

func frameVisualAge(meta tsf3Metadata, now time.Time) (time.Duration, bool) {
	if meta.version != 3 || meta.timestamp < wallClockMicrosFloor || meta.timestamp > uint64(1<<63-1) {
		return 0, false
	}
	capturedAt := time.UnixMicro(int64(meta.timestamp))
	age := now.Sub(capturedAt)
	if age < -phoneClockFutureTolerance {
		return 0, false
	}
	if age < 0 {
		age = 0
	}
	if meta.version == 3 {
		if meta.uncertaintyMicros > uint64(phoneClockUncertaintyMax/time.Microsecond) {
			return 0, false
		}
		age += time.Duration(meta.uncertaintyMicros) * time.Microsecond
	}
	return age, age <= liveFreshMaxAge
}

func (c *client) enqueueVideoFrame(value []byte) {
	if len(value) == 0 || len(value) > int(phone.MaxVideoMessageBytes) {
		return
	}
	c.startVideoWriter()
	meta := parseTSF3(value)
	if !meta.ok || len(value)-meta.headerBytes > int(phone.MaxVideoPayloadBytes) || !meta.keyFrame {
		return
	}
	now := time.Now()
	visualAge, fresh := frameVisualAge(meta, now)
	if !fresh {
		return
	}
	c.videoMu.Lock()
	if c.writerClosed || c.videoConfigGeneration == 0 || meta.epoch != c.videoEpoch {
		c.videoMu.Unlock()
		return
	}
	newest := c.videoWrittenSequence
	for _, frame := range []*queuedVideoFrame{c.videoInFlight, c.videoPending} {
		if frame != nil && frame.meta.sequence > newest {
			newest = frame.meta.sequence
		}
	}
	if meta.sequence <= newest {
		c.videoMu.Unlock()
		return
	}
	c.videoPending = &queuedVideoFrame{
		data: append([]byte(nil), value...), meta: meta, queuedAt: now,
		visualAge: visualAge, configGeneration: c.videoConfigGeneration,
	}
	c.videoMu.Unlock()
	c.signalVideoWriter()
}

func (c *client) sendText(_ context.Context, value []byte) {
	c.enqueueControl(value)
}

func (c *client) sendBinaryLatest(_ context.Context, value []byte) {
	c.enqueueVideoFrame(value)
}

func decodeStreamFeedback(data []byte) (streamFeedback, bool) {
	var feedback streamFeedback
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&feedback); err != nil || feedback.Type != "stream_feedback" || feedback.Version != 2 {
		return streamFeedback{}, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return streamFeedback{}, false
	}
	if feedback.Visibility != "" && feedback.Visibility != "visible" && feedback.Visibility != "hidden" {
		return streamFeedback{}, false
	}
	if feedback.ConfigGeneration == 0 {
		return streamFeedback{}, false
	}
	return feedback, true
}

func (c *client) acceptStreamFeedbackOutcome(data []byte) (outcome streamFeedbackOutcome) {
	feedback, ok := decodeStreamFeedback(data)
	if !ok {
		return outcome
	}
	c.videoMu.Lock()
	defer func() {
		c.videoMu.Unlock()
		if outcome.receiptReleased {
			c.signalVideoWriter()
		}
	}()
	if c.writerClosed || feedback.Epoch != c.videoEpoch || feedback.ConfigGeneration != c.videoConfigGeneration {
		return outcome
	}
	earlyInFlightReceipt := feedback.ReceivedSequence > c.videoWrittenSequence &&
		c.feedbackMatchesVideoFrameInFlightLocked(feedback)
	if feedback.DecodedSequence > feedback.ReceivedSequence ||
		feedback.RenderedSequence > feedback.DecodedSequence ||
		feedback.PresentedSequence > feedback.RenderedSequence ||
		feedback.RenderedKeyframeSequence > feedback.ReceivedSequence ||
		(feedback.ReceivedSequence > c.videoWrittenSequence && !earlyInFlightReceipt) ||
		feedback.ReceivedSequence < c.videoV2FeedbackReceived ||
		feedback.DecodedSequence < c.videoV2FeedbackDecoded ||
		feedback.RenderedSequence < c.videoV2FeedbackRendered ||
		feedback.PresentedSequence < c.videoV2FeedbackPresented {
		return outcome
	}
	c.videoV2FeedbackReceived = feedback.ReceivedSequence
	c.videoV2FeedbackDecoded = feedback.DecodedSequence
	c.videoV2FeedbackRendered = feedback.RenderedSequence
	c.videoV2FeedbackPresented = feedback.PresentedSequence
	previousVisibility := c.videoV2Visibility
	if feedback.Visibility != "" {
		c.videoV2Visibility = feedback.Visibility
	}
	outcome.becameVisible = previousVisibility == "hidden" && c.videoV2Visibility == "visible"
	outcome.receiptReleased = earlyInFlightReceipt
	if c.videoReceiptSequence != 0 && feedback.ReceivedSequence == c.videoReceiptSequence {
		c.clearVideoReceiptLocked()
		outcome.receiptReleased = true
	}
	if feedback.PresentedSequence > 0 && feedback.Visibility == "visible" {
		for _, evidence := range c.videoWrittenEvidence {
			if evidence == feedback.PresentedSequence {
				outcome.presented = true
				break
			}
		}
	}
	return outcome
}

type browserClockProbe struct {
	Type                 string `json:"type"`
	Version              int    `json:"version"`
	ProbeID              string `json:"probeId"`
	ConfigGeneration     uint64 `json:"configGeneration"`
	ClientSendUnixMicros int64  `json:"clientSendUnixMicros"`
}

func (c *client) browserClockProbeResponse(data []byte, receivedAt time.Time) ([]byte, bool) {
	var probe browserClockProbe
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&probe); err != nil || probe.Type != "clock_probe" || probe.Version != 1 ||
		!validBrowserClockProbeID(probe.ProbeID) || probe.ConfigGeneration == 0 ||
		probe.ClientSendUnixMicros <= 0 || probe.ClientSendUnixMicros > maxSafeJSONInteger {
		return nil, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, false
	}
	if receivedAt.IsZero() {
		receivedAt = time.Now()
	}
	serverReceiveUnixMicros := receivedAt.UnixMicro()
	if serverReceiveUnixMicros <= 0 || serverReceiveUnixMicros > maxSafeJSONInteger {
		return nil, false
	}
	c.videoMu.Lock()
	if probe.ConfigGeneration != c.videoConfigGeneration ||
		(!c.lastBrowserClockProbeAt.IsZero() &&
			(receivedAt.Before(c.lastBrowserClockProbeAt) || receivedAt.Sub(c.lastBrowserClockProbeAt) < browserClockProbeInterval)) {
		c.browserClockProbeDropped++
		c.videoMu.Unlock()
		return nil, false
	}
	c.lastBrowserClockProbeAt = receivedAt
	configGeneration := c.videoConfigGeneration
	c.videoMu.Unlock()
	serverSendUnixMicros := time.Now().UnixMicro()
	if serverSendUnixMicros < serverReceiveUnixMicros || serverSendUnixMicros > maxSafeJSONInteger {
		serverSendUnixMicros = serverReceiveUnixMicros
	}
	response, err := json.Marshal(map[string]any{
		"type":                    "clock_probe_result",
		"version":                 1,
		"probeId":                 probe.ProbeID,
		"configGeneration":        configGeneration,
		"clientSendUnixMicros":    probe.ClientSendUnixMicros,
		"serverReceiveUnixMicros": serverReceiveUnixMicros,
		"serverSendUnixMicros":    serverSendUnixMicros,
	})
	return response, err == nil
}

func validBrowserClockProbeID(value string) bool {
	if value == "" || len(value) > browserClockProbeIDMax {
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
