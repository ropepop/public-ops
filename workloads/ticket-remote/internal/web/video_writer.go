package web

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	"nhooyr.io/websocket"
)

// A viewer is deliberately kept independent from every other viewer.  The
// relay can therefore discard a dependent GOP for one slow socket without
// delaying phone-frame ingestion or a healthy socket.
type videoDeliveryMode string

const (
	videoDeliveryFull             videoDeliveryMode = "full"
	videoDeliveryKeyframeOnly     videoDeliveryMode = "keyframe_only"
	videoDeliveryProbe            videoDeliveryMode = "probe"
	videoDeliveryAwaitingKeyframe videoDeliveryMode = "awaiting_keyframe"

	controlQueueMaxMessages = 16
	controlQueueMaxBytes    = 64 * 1024
	videoQueueMaxFrames     = 6
	videoQueueMaxBytes      = 2 * 1024 * 1024
	videoQueueMaxAge        = 500 * time.Millisecond
	feedbackMinInterval     = 250 * time.Millisecond
	feedbackMaxPerSecond    = 4
	feedbackMaxAgeMillis    = 60_000
	feedbackMaxQueueSize    = 32
)

type queuedControlMessage struct {
	data   []byte
	config bool
	epoch  uint64
}

type queuedVideoFrame struct {
	data      []byte
	meta      tsf2Metadata
	queuedAt  time.Time
	visualAge time.Duration
}

// streamFeedback is cumulative rather than an acknowledgement for an
// individual frame.  It intentionally has a closed schema: diagnostics and
// browser state must not become an arbitrary command channel.
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

func (c *client) startVideoWriter() {
	// Unit fixtures and detached clients may exercise queue admission without
	// owning a socket. Keep those queues inspectable; live browser clients
	// always have a non-nil connection before enqueueing.
	if c.conn == nil {
		return
	}
	c.writerStartOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		c.videoMu.Lock()
		c.writerCancel = cancel
		c.writerWake = make(chan struct{}, 1)
		c.writerDone = make(chan struct{})
		if c.videoDeliveryMode == "" {
			c.videoDeliveryMode = videoDeliveryAwaitingKeyframe
		}
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
		// Drain already queued work without waiting for another wakeup.  The
		// queue itself remains bounded, so this cannot turn into an unbounded
		// goroutine or memory backlog.
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

func (c *client) nextVideoWriteItem() (videoWriteItem, bool) {
	c.videoMu.Lock()
	defer c.videoMu.Unlock()
	now := time.Now()
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
		c.videoQueue = c.videoQueue[1:]
		c.videoQueueBytes -= len(item.data)
		if c.videoQueueBytes < 0 {
			c.videoQueueBytes = 0
		}
		if !item.queuedAt.IsZero() && now.Sub(item.queuedAt) > videoQueueMaxAge {
			c.enterAwaitingKeyframeLocked("queued_frame_expired")
			continue
		}
		return videoWriteItem{messageType: websocket.MessageBinary, data: item.data, frame: &item}, true
	}
	return videoWriteItem{}, false
}

// writeNextVideoItem gives control messages priority, except that config is
// also held ahead of media by enqueueing control and media under one lock.
func (c *client) writeNextVideoItem(ctx context.Context) bool {
	item, ok := c.nextVideoWriteItem()
	if !ok {
		return true
	}
	if c.conn == nil {
		return false
	}
	writeCtx, cancel := context.WithTimeout(ctx, streamControlWriteTimeout)
	err := c.conn.Write(writeCtx, item.messageType, item.data)
	canceled := writeCtx.Err()
	cancel()
	if err != nil {
		c.videoMu.Lock()
		c.writerClosed = true
		c.writerCloseReason = "write_failed"
		c.videoMu.Unlock()
		_ = c.conn.Close(websocket.StatusPolicyViolation, "video client too slow")
		return false
	}
	if canceled != nil {
		c.videoMu.Lock()
		c.writerClosed = true
		c.writerCloseReason = "write_timeout"
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
	c.videoMu.Lock()
	defer c.videoMu.Unlock()
	if frame.meta.ok {
		c.videoEpoch = frame.meta.epoch
		c.videoLastWrittenSeq = frame.meta.sequence
		if frame.meta.keyFrame {
			// Decoder readiness is granted only after conn.Write succeeded.
			c.videoReadyForDelta = true
			c.videoReadyEpoch = frame.meta.epoch
			if c.videoDeliveryMode == videoDeliveryAwaitingKeyframe {
				c.videoDeliveryMode = videoDeliveryFull
			} else if c.videoDeliveryMode == videoDeliveryProbe {
				// Probe starts a complete GOP at a natural keyframe. It remains
				// probe until feedback proves the browser stable for two seconds.
				c.videoProbeAwaitingKeyframe = false
			}
		}
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
	c.videoMu.Unlock()
	if accepted {
		c.signalVideoWriter()
	}
}

// enqueueControlLocked keeps the control queue bounded while retaining the
// newest config. Config changes reset media readiness because a keyframe from
// the previous epoch cannot safely follow the new config.
func (c *client) enqueueControlLocked(message queuedControlMessage) bool {
	if len(message.data) == 0 || len(message.data) > controlQueueMaxBytes {
		return false
	}
	if message.config {
		for i := len(c.controlQueue) - 1; i >= 0; i-- {
			if !c.controlQueue[i].config {
				continue
			}
			c.controlQueueBytes -= len(c.controlQueue[i].data)
			c.controlQueue = append(c.controlQueue[:i], c.controlQueue[i+1:]...)
		}
		c.videoQueue = nil
		c.videoQueueBytes = 0
		c.videoReadyForDelta = false
		c.videoReadyEpoch = 0
		c.videoLastWrittenSeq = 0
		c.videoEpoch = message.epoch
		c.videoDeliveryMode = videoDeliveryAwaitingKeyframe
	}
	for len(c.controlQueue) >= controlQueueMaxMessages || c.controlQueueBytes+len(message.data) > controlQueueMaxBytes {
		// Ordinary controls are disposable under pressure. Never evict the
		// current config to make room for telemetry or another command.
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
			// This can only occur in a hand-built fixture with multiple
			// configs; a new config supersedes the oldest one.
			removeAt = 0
		}
		c.controlQueueBytes -= len(c.controlQueue[removeAt].data)
		c.controlQueue = append(c.controlQueue[:removeAt], c.controlQueue[removeAt+1:]...)
	}
	c.controlQueue = append(c.controlQueue, message)
	c.controlQueueBytes += len(message.data)
	return true
}

func (c *client) enqueueWarmStart(config, keyFrame []byte) {
	c.startVideoWriter()
	c.videoMu.Lock()
	acceptedConfig := true
	if len(config) > 0 {
		var payload struct {
			Type        string `json:"type"`
			StreamEpoch uint64 `json:"streamEpoch"`
		}
		_ = json.Unmarshal(config, &payload)
		acceptedConfig = c.enqueueControlLocked(queuedControlMessage{
			data:   append([]byte(nil), config...),
			config: payload.Type == "config",
			epoch:  payload.StreamEpoch,
		})
	}
	if acceptedConfig && len(keyFrame) > 0 && len(keyFrame) <= videoQueueMaxBytes {
		meta := parseTSF2(keyFrame)
		if meta.ok {
			c.videoQueue = []queuedVideoFrame{{data: append([]byte(nil), keyFrame...), meta: meta, queuedAt: time.Now()}}
			c.videoQueueBytes = len(keyFrame)
		}
	}
	c.videoMu.Unlock()
	c.signalVideoWriter()
}

func (c *client) enqueueVideoFrame(value []byte) {
	if len(value) == 0 {
		return
	}
	c.startVideoWriter()
	meta := parseTSF2(value)
	if !meta.ok {
		return
	}
	now := time.Now()
	if len(value) > videoQueueMaxBytes {
		return
	}
	c.videoMu.Lock()
	defer c.videoMu.Unlock()
	if c.videoEpoch != 0 && meta.epoch != c.videoEpoch {
		if !meta.keyFrame {
			c.enterAwaitingKeyframeLocked("epoch_gap")
			return
		}
		c.videoEpoch = meta.epoch
		c.videoQueue = nil
		c.videoQueueBytes = 0
		c.videoLastWrittenSeq = 0
		c.videoReadyForDelta = false
		c.videoReadyEpoch = 0
		c.videoDeliveryMode = videoDeliveryAwaitingKeyframe
	}
	if c.videoEpoch == 0 {
		c.videoEpoch = meta.epoch
	}
	if !meta.keyFrame && c.videoDeliveryMode == videoDeliveryKeyframeOnly {
		return
	}
	if !meta.keyFrame && c.videoDeliveryMode != videoDeliveryFull && c.videoDeliveryMode != videoDeliveryProbe && c.videoDeliveryMode != videoDeliveryAwaitingKeyframe {
		return
	}
	if !meta.keyFrame && c.videoDeliveryMode == videoDeliveryProbe && c.videoProbeAwaitingKeyframe {
		return
	}
	if !meta.keyFrame && c.videoDeliveryMode == videoDeliveryAwaitingKeyframe {
		if len(c.videoQueue) == 0 || !c.videoQueue[len(c.videoQueue)-1].meta.keyFrame {
			return
		}
	}
	if !meta.keyFrame {
		base := c.videoLastWrittenSeq
		if len(c.videoQueue) > 0 {
			base = c.videoQueue[len(c.videoQueue)-1].meta.sequence
		}
		if base == 0 || meta.sequence != base+1 {
			c.enterAwaitingKeyframeLocked("sequence_gap")
			return
		}
	}
	if meta.keyFrame {
		// A keyframe starts a new independently decodable GOP. Any queued
		// obsolete GOP is safe to replace, but no delta may survive it.
		c.videoQueue = c.videoQueue[:0]
		c.videoQueueBytes = 0
		if c.videoDeliveryMode == videoDeliveryProbe {
			c.videoProbeAwaitingKeyframe = false
		}
	} else if len(c.videoQueue) >= videoQueueMaxFrames || c.videoQueueBytes+len(value) > videoQueueMaxBytes {
		c.enterAwaitingKeyframeLocked("queue_overflow")
		return
	}
	c.videoQueue = append(c.videoQueue, queuedVideoFrame{data: value, meta: meta, queuedAt: now})
	c.videoQueueBytes += len(value)
	if now.Sub(c.videoQueue[0].queuedAt) > videoQueueMaxAge {
		c.enterAwaitingKeyframeLocked("queue_age")
		return
	}
	go c.signalVideoWriter()
}

func (c *client) enterAwaitingKeyframeLocked(_ string) {
	c.videoQueue = nil
	c.videoQueueBytes = 0
	c.videoReadyForDelta = false
	c.videoReadyEpoch = 0
	c.videoLastWrittenSeq = 0
	c.videoProbeAwaitingKeyframe = false
	c.videoDeliveryMode = videoDeliveryKeyframeOnly
}

func (c *client) setVideoDeliveryMode(mode videoDeliveryMode) {
	c.videoMu.Lock()
	defer c.videoMu.Unlock()
	if mode == "" {
		mode = videoDeliveryAwaitingKeyframe
	}
	if mode == videoDeliveryKeyframeOnly || mode == videoDeliveryAwaitingKeyframe {
		c.enterAwaitingKeyframeLocked("feedback")
	}
	if mode == videoDeliveryProbe {
		c.videoProbeAwaitingKeyframe = true
		c.feedbackProbeSince = time.Now()
	}
	if mode == videoDeliveryFull {
		c.videoProbeAwaitingKeyframe = false
		c.feedbackProbeSince = time.Time{}
	}
	c.videoDeliveryMode = mode
}

func (c *client) deliveryMode() videoDeliveryMode {
	c.videoMu.Lock()
	defer c.videoMu.Unlock()
	if c.videoDeliveryMode == "" {
		return videoDeliveryAwaitingKeyframe
	}
	return c.videoDeliveryMode
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

// Legacy helper retained for focused unit tests of the original one-slot
// queue. Production delivery uses enqueueVideoFrame and the bounded pump.
func (c *client) noteVideoKeyFrame(frame []byte) {
	meta := parseTSF2(frame)
	c.videoMu.Lock()
	c.noteVideoKeyFrameLocked(meta)
	c.videoMu.Unlock()
}

func (c *client) noteVideoKeyFrameLocked(meta tsf2Metadata) {
	c.videoReadyForDelta = true
	if meta.ok {
		c.videoReadyEpoch = meta.epoch
	}
}

func (c *client) queuePendingVideoFrameLocked(frame []byte, keyFrame bool, now time.Time) {
	if keyFrame {
		c.videoPendingFrame = frame
		c.videoPendingKeyFrame = true
		c.videoPendingAt = now
		return
	}
	if len(c.videoPendingFrame) == 0 {
		c.videoPendingFrame = frame
		c.videoPendingKeyFrame = false
		c.videoPendingAt = now
		return
	}
	c.videoPendingFrame = nil
	c.videoPendingKeyFrame = false
	c.videoPendingAt = time.Time{}
	c.videoReadyForDelta = false
	c.videoReadyEpoch = 0
}

func videoPendingFrameStale(pendingAt time.Time, now time.Time) bool {
	return !pendingAt.IsZero() && now.Sub(pendingAt) > videoPendingFrameMaxAge
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

func clampFeedbackFPS(value int) int {
	if value <= 0 {
		return 0
	}
	for _, tier := range []int{1, 2, 5, 10, 15, 20, 30} {
		if value <= tier {
			return tier
		}
	}
	return 30
}

func (c *client) acceptStreamFeedback(data []byte, now time.Time) bool {
	feedback, ok := decodeStreamFeedback(data)
	if !ok {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}
	c.feedbackMu.Lock()
	defer c.feedbackMu.Unlock()
	if !c.lastFeedbackAt.IsZero() && (now.Before(c.lastFeedbackAt) || now.Sub(c.lastFeedbackAt) < feedbackMinInterval) {
		c.feedbackDropped++
		return false
	}
	c.videoMu.Lock()
	expectedEpoch := c.videoEpoch
	c.videoMu.Unlock()
	if expectedEpoch != 0 && feedback.Epoch != expectedEpoch {
		c.feedbackDropped++
		return false
	}
	if feedback.ReceivedSequence < c.lastFeedbackReceived || feedback.DecodedSequence < c.lastFeedbackDecoded || feedback.RenderedSequence < c.lastFeedbackRendered || feedback.RenderedKeyframeSequence < c.lastFeedbackRenderedKeyframe {
		c.feedbackDropped++
		return false
	}
	previousRenderedKeyframe := c.lastFeedbackRenderedKeyframe
	c.lastFeedbackAt = now
	c.lastFeedbackEpoch = feedback.Epoch
	c.lastFeedbackReceived = feedback.ReceivedSequence
	c.lastFeedbackDecoded = feedback.DecodedSequence
	c.lastFeedbackRendered = feedback.RenderedSequence
	c.lastFeedbackRenderedKeyframe = feedback.RenderedKeyframeSequence
	c.lastFeedbackQueue = uint64(feedback.DecoderQueueSize)
	c.lastFeedbackAge = uint64(feedback.RenderedVisualAgeMillis)
	c.feedbackState = "flowing"
	c.feedbackVisibility = feedback.Visibility
	c.feedbackCount++
	lag := uint64(0)
	if feedback.ReceivedSequence >= feedback.RenderedSequence {
		lag = feedback.ReceivedSequence - feedback.RenderedSequence
	}
	hard := feedback.RenderedVisualAgeMillis > 2000 || feedback.DecoderQueueSize >= 5
	soft := feedback.RenderedVisualAgeMillis > 1000 || feedback.DecoderQueueSize > 2 || lag > 5
	if hard {
		c.feedbackPressureStreak = 2
		c.feedbackHealthyStreak = 0
		c.feedbackKeyframeStreak = 0
		c.feedbackProbeSince = time.Time{}
		c.feedbackState = "congested_awaiting_keyframe"
		c.setVideoDeliveryMode(videoDeliveryKeyframeOnly)
		return true
	}
	if soft {
		c.feedbackHealthyStreak = 0
		c.feedbackKeyframeStreak = 0
		c.feedbackProbeSince = time.Time{}
		c.feedbackState = "congested_awaiting_keyframe"
		if c.feedbackPressureStreak < 2 {
			c.feedbackPressureStreak++
		}
		if c.feedbackPressureStreak >= 2 {
			c.setVideoDeliveryMode(videoDeliveryKeyframeOnly)
			return true
		}
	} else {
		c.feedbackPressureStreak = 0
		healthyKeyframe := feedback.RenderedVisualAgeMillis <= 750 && feedback.DecoderQueueSize <= 1
		mode := c.deliveryMode()
		if mode == videoDeliveryKeyframeOnly {
			if healthyKeyframe && c.feedbackHealthyStreak < 3 {
				c.feedbackHealthyStreak++
			}
			if healthyKeyframe && feedback.RenderedKeyframeSequence > previousRenderedKeyframe {
				if c.feedbackKeyframeStreak < 2 {
					c.feedbackKeyframeStreak++
				}
			} else if !healthyKeyframe {
				c.feedbackKeyframeStreak = 0
			}
			// The normal path requires two advancing rendered keyframes. The
			// three-sample fallback keeps compatibility with older browsers that
			// only reported their latest rendered keyframe cumulatively.
			if c.feedbackKeyframeStreak >= 2 || (c.feedbackKeyframeStreak >= 1 && c.feedbackHealthyStreak >= 3) {
				c.setVideoDeliveryMode(videoDeliveryProbe)
			}
		} else if mode == videoDeliveryProbe {
			if !healthyKeyframe {
				c.feedbackKeyframeStreak = 0
				c.setVideoDeliveryMode(videoDeliveryKeyframeOnly)
				return true
			}
			if c.feedbackProbeSince.IsZero() {
				c.feedbackProbeSince = now
			}
			c.videoMu.Lock()
			probeAwaitingKeyframe := c.videoProbeAwaitingKeyframe
			c.videoMu.Unlock()
			if !probeAwaitingKeyframe && now.Sub(c.feedbackProbeSince) >= 2*time.Second {
				c.setVideoDeliveryMode(videoDeliveryFull)
			}
		}
	}
	return true
}

func (c *client) feedbackSnapshot() map[string]any {
	c.feedbackMu.Lock()
	defer c.feedbackMu.Unlock()
	c.videoMu.Lock()
	mode := c.videoDeliveryMode
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
		"videoDeliveryMode":   string(mode),
		"videoQueueFrames":    queueFrames,
		"videoQueueBytes":     queueBytes,
		"videoLastWrittenSeq": lastWrittenSeq,
	}
}
