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
	controlQueueMaxMessages      = 16
	controlQueueMaxBytes         = 64 * 1024
	feedbackMinInterval          = 250 * time.Millisecond
	feedbackMaxAgeMillis         = 60_000
	feedbackMaxQueueSize         = 32
	feedbackStalledAgeMillis     = 3_000
	videoReceiptLivenessTimeout  = 3 * time.Second
	browserClockProbeInterval    = 250 * time.Millisecond
	browserClockProbeIDMax       = 64
	maxSafeJSONInteger           = int64(9_007_199_254_740_991)
	videoWrittenEvidenceLimit    = 128
	wallClockMicrosFloor         = uint64(1_000_000_000_000_000)
	resultPriorityWorkflowWindow = 5 * time.Minute
	resultPriorityDeliveryWindow = 5 * time.Second
	resultPriorityArm            = "arm"
	resultPriorityMark           = "mark"
)

type videoWrittenFrameEvidence struct {
	epoch     uint64
	sequence  uint64
	decodable bool
}

type queuedControlMessage struct {
	data       []byte
	config     bool
	epoch      uint64
	generation uint64
}

type queuedVideoFrame struct {
	data             []byte
	meta             tsf2Metadata
	queuedAt         time.Time
	visualAge        time.Duration
	configGeneration uint64
}

// resultPriority is a requester-local scheduling hint. It intentionally carries
// no request, account, or control-code data: the authenticated browser only
// arms its own existing one-frame slot and later supplies the public stream
// marker that already fences exact result presentation. Arming does not pause
// ordinary media; only the marked result takes delivery priority.
type resultPriority struct {
	Type             string `json:"type"`
	Version          int    `json:"version"`
	Phase            string `json:"phase"`
	ConfigGeneration uint64 `json:"configGeneration"`
	Epoch            uint64 `json:"epoch"`
	MinSequence      uint64 `json:"minSequence,omitempty"`
}

// streamFeedback is cumulative. Version 2's receivedSequence is also the
// per-socket delivery credit; version 1 remains diagnostic only.
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
	accepted        bool
	receiptReleased bool
	becameVisible   bool
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

func decodeResultPriority(data []byte) (resultPriority, bool) {
	var message resultPriority
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&message); err != nil || message.Type != "result_priority" || message.Version != 1 {
		return resultPriority{}, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return resultPriority{}, false
	}
	if message.ConfigGeneration == 0 || message.Epoch == 0 ||
		message.ConfigGeneration > uint64(maxSafeJSONInteger) || message.Epoch > uint64(maxSafeJSONInteger) ||
		message.MinSequence > uint64(maxSafeJSONInteger) {
		return resultPriority{}, false
	}
	switch message.Phase {
	case resultPriorityArm, "clear":
		if message.MinSequence != 0 {
			return resultPriority{}, false
		}
	case resultPriorityMark:
		if message.MinSequence == 0 {
			return resultPriority{}, false
		}
	default:
		return resultPriority{}, false
	}
	return message, true
}

func (c *client) clearResultPriorityLocked() {
	if c.videoResultPriorityTimer != nil {
		c.videoResultPriorityTimer.Stop()
	}
	c.videoResultPriorityPhase = ""
	c.videoResultPriorityEpoch = 0
	c.videoResultPrioritySeq = 0
	c.videoResultPriorityGen = 0
	c.videoResultPriorityUntil = time.Time{}
}

func (c *client) resultPriorityActiveLocked(now time.Time) bool {
	if c.videoResultPriorityPhase == "" {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}
	if c.videoResultPriorityUntil.IsZero() || !now.Before(c.videoResultPriorityUntil) ||
		c.videoResultPriorityGen != c.videoConfigGeneration || c.videoResultPriorityEpoch != c.videoEpoch {
		c.clearResultPriorityLocked()
		return false
	}
	return true
}

func (c *client) resultPriorityAllowsQueuedFrameLocked(now time.Time) bool {
	if !c.resultPriorityActiveLocked(now) || c.videoResultPriorityPhase == resultPriorityArm {
		return true
	}
	if c.videoResultPriorityPhase != resultPriorityMark || len(c.videoQueue) == 0 {
		return false
	}
	frame := c.videoQueue[0]
	return frame.configGeneration == c.videoResultPriorityGen &&
		frame.meta.epoch == c.videoResultPriorityEpoch && frame.meta.sequence >= c.videoResultPrioritySeq
}

func (c *client) scheduleResultPriorityExpiryLocked(deadline time.Time) {
	wait := time.Until(deadline)
	if wait < 0 {
		wait = 0
	}
	if c.videoResultPriorityTimer == nil {
		c.videoResultPriorityTimer = time.AfterFunc(wait, c.expireResultPriority)
		return
	}
	c.videoResultPriorityTimer.Stop()
	c.videoResultPriorityTimer.Reset(wait)
}

func (c *client) expireResultPriority() {
	c.videoMu.Lock()
	if c.videoResultPriorityPhase == "" || time.Now().Before(c.videoResultPriorityUntil) {
		c.videoMu.Unlock()
		return
	}
	markedDeliveryExpired := c.videoResultPriorityPhase == resultPriorityMark
	c.clearResultPriorityLocked()
	if markedDeliveryExpired {
		// A marked result that cannot traverse this reliable stream inside its
		// separate delivery budget must not fall back to ordinary media on the
		// same potentially backlogged socket. Close only this viewer so its normal
		// reconnect path can obtain a fresh generation and retry within the durable
		// workflow deadline.
		c.writerClosed = true
		c.writerCloseReason = "result_priority_timeout"
		c.clearVideoFrameInFlightLocked(nil)
		c.videoQueue = nil
		c.videoQueueBytes = 0
		c.clearVideoReceiptLocked()
	}
	conn := c.conn
	c.videoMu.Unlock()
	if markedDeliveryExpired {
		if conn != nil {
			_ = conn.CloseNow()
		}
		return
	}
	c.signalVideoWriter()
}

func (c *client) acceptResultPriority(data []byte, now time.Time) bool {
	message, ok := decodeResultPriority(data)
	if !ok {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}
	c.videoMu.Lock()
	if c.writerClosed || c.videoFeedbackVersion != 2 ||
		message.ConfigGeneration != c.videoConfigGeneration || message.Epoch != c.videoEpoch {
		c.videoMu.Unlock()
		return false
	}
	active := c.resultPriorityActiveLocked(now)
	deadline := time.Time{}
	switch message.Phase {
	case resultPriorityArm:
		if active {
			// A repeated arm is an idempotent retransmission. Once marked, this
			// reservation cannot be restarted or extended by untrusted input.
			accepted := c.videoResultPriorityPhase == resultPriorityArm
			c.videoMu.Unlock()
			return accepted
		}
		// Until the result marker exists, keep ordinary delivery and its
		// existing bounded queue and receipt credit unchanged.
		c.videoResultPriorityPhase = resultPriorityArm
		c.videoResultPrioritySeq = 0
		deadline = now.Add(resultPriorityWorkflowWindow)
	case resultPriorityMark:
		if !active {
			c.videoMu.Unlock()
			return false
		}
		if c.videoResultPriorityPhase == resultPriorityMark {
			accepted := c.videoResultPrioritySeq == message.MinSequence
			c.videoMu.Unlock()
			return accepted
		}
		if c.videoResultPriorityPhase != resultPriorityArm {
			c.videoMu.Unlock()
			return false
		}
		c.videoResultPriorityPhase = resultPriorityMark
		c.videoResultPrioritySeq = message.MinSequence
		deadline = now.Add(resultPriorityDeliveryWindow)
		if len(c.videoQueue) > 0 &&
			(c.videoQueue[0].configGeneration != message.ConfigGeneration ||
				c.videoQueue[0].meta.epoch != message.Epoch ||
				c.videoQueue[0].meta.sequence < message.MinSequence) {
			c.videoQueue = nil
			c.videoQueueBytes = 0
		}
	case "clear":
		c.clearResultPriorityLocked()
	}
	if !deadline.IsZero() {
		c.videoResultPriorityEpoch = message.Epoch
		c.videoResultPriorityGen = message.ConfigGeneration
		c.videoResultPriorityUntil = deadline
		c.scheduleResultPriorityExpiryLocked(deadline)
	}
	c.videoMu.Unlock()
	c.signalVideoWriter()
	return true
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
		c.clearVideoReceiptLocked()
		c.clearResultPriorityLocked()
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
	return len(c.controlQueue) > 0 || (len(c.videoQueue) > 0 &&
		(c.videoFeedbackVersion < 2 || !c.videoReceiptAwaiting) &&
		c.resultPriorityAllowsQueuedFrameLocked(time.Now()))
}

func (c *client) videoReceiptDeadline() time.Time {
	c.videoMu.Lock()
	defer c.videoMu.Unlock()
	if c.videoFeedbackVersion < 2 || !c.videoReceiptAwaiting {
		return time.Time{}
	}
	return c.videoReceiptDeadlineAt
}

func (c *client) clearVideoReceiptLocked() {
	c.videoReceiptAwaiting = false
	c.videoReceiptEpoch = 0
	c.videoReceiptSequence = 0
	c.videoReceiptConfigGen = 0
	c.videoReceiptDeadlineAt = time.Time{}
}

func (c *client) closeExpiredVideoReceipt() bool {
	now := time.Now()
	c.videoMu.Lock()
	if c.videoFeedbackVersion < 2 || !c.videoReceiptAwaiting || c.videoReceiptDeadlineAt.IsZero() || now.Before(c.videoReceiptDeadlineAt) {
		c.videoMu.Unlock()
		return false
	}
	c.writerClosed = true
	c.writerCloseReason = "receipt_timeout"
	c.videoQueue = nil
	c.videoQueueBytes = 0
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

func (c *client) feedbackMatchesVideoFrameInFlightLocked(feedback streamFeedback) bool {
	return c.videoInFlight &&
		feedback.ConfigGeneration == c.videoInFlightConfigGen &&
		feedback.Epoch == c.videoInFlightEpoch &&
		feedback.ReceivedSequence == c.videoInFlightSeq
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
	if err == nil {
		return ""
	}
	if timedOut {
		return "write_timeout"
	}
	return "write_failed"
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
		return videoWriteItem{messageType: websocket.MessageText, data: item.data, control: &item}, true
	}
	if c.videoFeedbackVersion >= 2 && c.videoReceiptAwaiting {
		return videoWriteItem{}, false
	}
	if len(c.videoQueue) > 0 && !c.resultPriorityAllowsQueuedFrameLocked(time.Now()) {
		return videoWriteItem{}, false
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
	var writeCtx context.Context
	var cancel context.CancelFunc
	if item.frame != nil {
		// A slow-but-feasible link gets the picture's complete remaining source
		// freshness budget. The global freshness window is the hard upper bound.
		writeCtx, cancel = context.WithDeadline(ctx, videoFrameWriteDeadline(*item.frame, time.Now()))
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
		c.noteVideoFrameWritten(*item.frame)
	} else if item.control != nil && item.control.config {
		c.noteVideoConfigWritten(*item.control)
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
		if c.videoFeedbackVersion >= 2 {
			// The browser reader may process and ACK a complete message after
			// conn.Write has returned but before this writer goroutine records the
			// successful write. That exact in-flight ACK already restored credit;
			// do not arm a timeout for a picture known to have arrived.
			if c.videoV2FeedbackReceived < frame.meta.sequence {
				c.videoReceiptAwaiting = true
				c.videoReceiptEpoch = frame.meta.epoch
				c.videoReceiptSequence = frame.meta.sequence
				c.videoReceiptConfigGen = frame.configGeneration
				// Picture usefulness and transport liveness are separate clocks.
				// The browser ACKs even a complete picture that has become too old
				// to present, so give that receipt a bounded interval after the
				// successful write instead of reusing the consumed source deadline.
				c.videoReceiptDeadlineAt = writtenAt.Add(videoReceiptLivenessTimeout)
			}
		}
	}
	c.videoMu.Unlock()
	if frame.meta.ok && frame.meta.keyFrame && frameBelongsToCurrentConfig && c.onVideoFrameWritten != nil {
		c.onVideoFrameWritten(frame.meta)
	}
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
	if accepted && message.config {
		c.resetFeedbackForConfig()
	}
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
		message.generation = c.videoConfigGeneration
		c.videoConfigWrittenEpoch = 0
		c.videoConfigWrittenGen = 0
		c.videoWrittenEpoch = 0
		c.videoWrittenSequence = 0
		c.videoWrittenEvidence = nil
		c.videoV2FeedbackReceived = 0
		c.videoV2FeedbackDecoded = 0
		c.videoV2FeedbackRendered = 0
		c.videoV2FeedbackPresented = 0
		c.clearVideoReceiptLocked()
		c.clearResultPriorityLocked()
		// The emitted config explicitly advertises v2. A stale browser receives
		// one independent frame, then is closed at that frame's freshness expiry
		// instead of silently reverting to unbounded reliable delivery.
		c.videoFeedbackVersion = 2
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

func (c *client) resetFeedbackForConfig() {
	c.feedbackMu.Lock()
	c.lastFeedbackVersion = 0
	c.lastFeedbackAt = time.Time{}
	c.lastFeedbackEpoch = 0
	c.lastFeedbackReceived = 0
	c.lastFeedbackDecoded = 0
	c.lastFeedbackRendered = 0
	c.lastFeedbackPresented = 0
	c.lastFeedbackQueue = 0
	c.lastFeedbackAge = 0
	c.lastFeedbackAgeKnown = false
	c.feedbackState = ""
	c.feedbackCause = ""
	c.feedbackVisibility = ""
	c.feedbackMu.Unlock()
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
	if len(keyFrame) > 0 && len(keyFrame) <= int(phone.MaxVideoMessageBytes) {
		frameMeta = parseTSF2(keyFrame)
		frameMatches = frameMeta.ok && len(keyFrame)-frameMeta.headerBytes <= int(phone.MaxVideoPayloadBytes) &&
			frameMeta.keyFrame && frameMeta.epoch == payload.StreamEpoch
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
		c.resetFeedbackForConfig()
	}
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
	meta := parseTSF2(value)
	if !meta.ok || len(value)-meta.headerBytes > int(phone.MaxVideoPayloadBytes) || !meta.keyFrame {
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
	if c.resultPriorityActiveLocked(now) && c.videoResultPriorityPhase == resultPriorityMark &&
		meta.sequence < c.videoResultPrioritySeq {
		c.videoMu.Unlock()
		return
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

func (c *client) sendBinaryLatest(_ context.Context, value []byte) {
	c.enqueueVideoFrame(value)
}

func decodeStreamFeedback(data []byte, _ ...time.Time) (streamFeedback, bool) {
	var feedback streamFeedback
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&feedback); err != nil || feedback.Type != "stream_feedback" || (feedback.Version != 1 && feedback.Version != 2) {
		return streamFeedback{}, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return streamFeedback{}, false
	}
	if feedback.Visibility != "" && feedback.Visibility != "visible" && feedback.Visibility != "hidden" {
		return streamFeedback{}, false
	}
	if feedback.Version == 2 && feedback.ConfigGeneration == 0 {
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
	c.videoMu.Lock()
	expectedEpoch := c.videoEpoch
	if (feedback.Version == 2 && feedback.Epoch != expectedEpoch) ||
		(feedback.Version == 1 && expectedEpoch != 0 && feedback.Epoch != expectedEpoch) {
		c.videoMu.Unlock()
		c.feedbackDropped++
		c.feedbackMu.Unlock()
		return outcome
	}
	releasedReceipt := false
	releasedPriority := false
	if feedback.Version == 2 {
		earlyInFlightReceipt := feedback.ReceivedSequence > c.videoWrittenSequence &&
			c.feedbackMatchesVideoFrameInFlightLocked(feedback)
		if feedback.ConfigGeneration != c.videoConfigGeneration ||
			feedback.DecodedSequence > feedback.ReceivedSequence ||
			feedback.RenderedSequence > feedback.DecodedSequence ||
			feedback.PresentedSequence > feedback.RenderedSequence ||
			feedback.RenderedKeyframeSequence > feedback.ReceivedSequence ||
			(feedback.ReceivedSequence > c.videoWrittenSequence && !earlyInFlightReceipt) {
			c.videoMu.Unlock()
			c.feedbackDropped++
			c.feedbackMu.Unlock()
			return outcome
		}
		if feedback.ReceivedSequence < c.videoV2FeedbackReceived ||
			feedback.DecodedSequence < c.videoV2FeedbackDecoded ||
			feedback.RenderedSequence < c.videoV2FeedbackRendered ||
			feedback.PresentedSequence < c.videoV2FeedbackPresented {
			c.videoMu.Unlock()
			c.feedbackDropped++
			c.feedbackMu.Unlock()
			return outcome
		}
		// Protocol admission is independent of diagnostic throttling. In
		// particular, a receipt ACK inside the 250 ms telemetry window must both
		// release credit and advance the cumulative v2 fence.
		c.videoV2FeedbackReceived = feedback.ReceivedSequence
		c.videoV2FeedbackDecoded = feedback.DecodedSequence
		c.videoV2FeedbackRendered = feedback.RenderedSequence
		c.videoV2FeedbackPresented = feedback.PresentedSequence
		if c.videoResultPriorityPhase == resultPriorityMark &&
			c.videoResultPriorityGen == feedback.ConfigGeneration &&
			c.videoResultPriorityEpoch == feedback.Epoch &&
			feedback.PresentedSequence >= c.videoResultPrioritySeq {
			c.clearResultPriorityLocked()
			releasedPriority = true
		}
		previousVisibility := c.videoV2Visibility
		if feedback.Visibility != "" {
			c.videoV2Visibility = feedback.Visibility
		}
		outcome.becameVisible = previousVisibility == "hidden" && c.videoV2Visibility == "visible"
		if earlyInFlightReceipt {
			releasedReceipt = true
			outcome.receiptReleased = true
		}
	}
	// v1 remains useful as telemetry, but it has a separate monotonic domain and
	// can neither poison nor reset the generation-fenced v2 delivery state.
	if feedback.Version == 1 && c.lastFeedbackVersion == 1 && feedback.Epoch == c.lastFeedbackEpoch &&
		(feedback.ReceivedSequence < c.lastFeedbackReceived ||
			feedback.DecodedSequence < c.lastFeedbackDecoded ||
			feedback.RenderedSequence < c.lastFeedbackRendered) {
		c.videoMu.Unlock()
		c.feedbackDropped++
		c.feedbackMu.Unlock()
		return outcome
	}
	if feedback.Version == 2 && c.videoReceiptAwaiting &&
		feedback.ConfigGeneration == c.videoReceiptConfigGen &&
		feedback.Epoch == c.videoReceiptEpoch &&
		feedback.ReceivedSequence == c.videoReceiptSequence {
		c.clearVideoReceiptLocked()
		releasedReceipt = true
		outcome.receiptReleased = true
	}
	c.videoMu.Unlock()
	if !c.lastFeedbackAt.IsZero() && (now.Before(c.lastFeedbackAt) || now.Sub(c.lastFeedbackAt) < feedbackMinInterval) {
		c.feedbackDropped++
		outcome.accepted = releasedReceipt || releasedPriority
		c.feedbackMu.Unlock()
		if releasedReceipt || releasedPriority {
			c.signalVideoWriter()
		}
		return outcome
	}

	previousReceived := c.lastFeedbackReceived
	previousDecoded := c.lastFeedbackDecoded
	previousRendered := c.lastFeedbackRendered
	previousState := c.feedbackState
	previousCause := c.feedbackCause
	hadPrevious := !c.lastFeedbackAt.IsZero() && c.lastFeedbackVersion == feedback.Version && c.lastFeedbackEpoch == feedback.Epoch

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
	renderStalled := hadPrevious && (feedback.Version == 1 || feedback.AgeKnown) &&
		feedback.RenderedVisualAgeMillis > feedbackStalledAgeMillis &&
		feedback.RenderedSequence == previousRendered
	switch {
	case feedback.Visibility == "hidden":
		state, cause = "hidden", "browser_hidden"
	case feedback.Version == 2 && !feedback.AgeKnown:
		state, cause = "timing_unknown", "visual_age_unknown"
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

	c.lastFeedbackVersion = feedback.Version
	c.lastFeedbackAt = now
	c.lastFeedbackEpoch = feedback.Epoch
	c.lastFeedbackReceived = feedback.ReceivedSequence
	c.lastFeedbackDecoded = feedback.DecodedSequence
	c.lastFeedbackRendered = feedback.RenderedSequence
	c.lastFeedbackPresented = feedback.PresentedSequence
	c.lastFeedbackQueue = uint64(feedback.DecoderQueueSize)
	c.lastFeedbackAge = uint64(feedback.RenderedVisualAgeMillis)
	c.lastFeedbackAgeKnown = feedback.Version == 1 || feedback.AgeKnown
	c.feedbackState = state
	c.feedbackCause = cause
	c.feedbackVisibility = feedback.Visibility
	c.feedbackCount++

	outcome.state = state
	outcome.cause = cause
	outcome.transition = hadPrevious && (state != previousState || cause != previousCause)
	c.feedbackMu.Unlock()
	if releasedReceipt || releasedPriority {
		c.signalVideoWriter()
	}
	return outcome
}

func (c *client) feedbackSnapshot() map[string]any {
	c.feedbackMu.Lock()
	defer c.feedbackMu.Unlock()
	c.videoMu.Lock()
	queueFrames := len(c.videoQueue)
	queueBytes := c.videoQueueBytes
	lastWrittenSeq := c.videoLastWrittenSeq
	feedbackVersion := c.videoFeedbackVersion
	receiptAwaiting := c.videoReceiptAwaiting
	receiptSequence := c.videoReceiptSequence
	receiptDeadline := c.videoReceiptDeadlineAt
	c.videoMu.Unlock()
	return map[string]any{
		"feedbackCount":        c.feedbackCount,
		"feedbackDropped":      c.feedbackDropped,
		"feedbackAgeMillis":    ageSinceMillis(time.Now(), c.lastFeedbackAt),
		"feedbackEpoch":        c.lastFeedbackEpoch,
		"feedbackReceived":     c.lastFeedbackReceived,
		"feedbackDecoded":      c.lastFeedbackDecoded,
		"feedbackRendered":     c.lastFeedbackRendered,
		"feedbackPresented":    c.lastFeedbackPresented,
		"feedbackQueueSize":    c.lastFeedbackQueue,
		"feedbackVisualAge":    c.lastFeedbackAge,
		"feedbackAgeKnown":     c.lastFeedbackAgeKnown,
		"feedbackVisibility":   c.feedbackVisibility,
		"feedbackState":        c.feedbackState,
		"feedbackCause":        c.feedbackCause,
		"videoQueueFrames":     queueFrames,
		"videoQueueBytes":      queueBytes,
		"videoLastWrittenSeq":  lastWrittenSeq,
		"feedbackVersion":      feedbackVersion,
		"videoReceiptAwaiting": receiptAwaiting,
		"videoReceiptSequence": receiptSequence,
		"videoReceiptDeadline": timeString(receiptDeadline),
	}
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
