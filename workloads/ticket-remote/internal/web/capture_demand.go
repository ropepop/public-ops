package web

import (
	"context"
	"time"

	"ticketremote/internal/phone"
)

// requestOrdinaryCaptureIfUseful coalesces all browser credit into one source
// opportunity. It does not participate in proof, keyframe, startup, prewarm,
// or durable command ownership.
func (s *Server) requestOrdinaryCaptureIfUseful() {
	if s == nil || s.direct == nil {
		return
	}
	epoch := s.direct.currentStreamEpoch()
	if epoch == 0 || !s.hasUsefulOrdinaryCaptureViewer(epoch) {
		return
	}
	now := time.Now()
	s.captureDemandMu.Lock()
	if s.captureDemandClosed {
		s.captureDemandMu.Unlock()
		return
	}
	if s.captureDemandEpoch != 0 && s.captureDemandEpoch != epoch {
		s.cancelCaptureDemandSendLocked()
		s.captureDemandFence++
		s.clearCaptureDemandReceiptLocked()
	}
	s.captureDemandEpoch = epoch
	if s.captureDemandReceipt.Generation != 0 {
		if s.captureDemandReceipt.ExpiresAt.IsZero() || now.Before(s.captureDemandReceipt.ExpiresAt) {
			s.captureDemandMu.Unlock()
			return
		}
		s.clearCaptureDemandReceiptLocked()
	}
	if s.captureDemandSending {
		s.captureDemandMu.Unlock()
		return
	}
	s.captureDemandFence++
	fence := s.captureDemandFence
	ctx, cancel := context.WithCancel(context.Background())
	s.captureDemandSending = true
	s.captureDemandCancel = cancel
	sender := s.captureDemandSend
	if sender == nil && s.relay != nil {
		sender = s.relay.SendCaptureDemand
	}
	s.captureDemandMu.Unlock()
	if sender == nil {
		cancel()
		s.finishOrdinaryCaptureDemandSend(fence, epoch, phone.CaptureDemandReceipt{}, context.Canceled)
		return
	}

	go func() {
		if !s.hasUsefulOrdinaryCaptureViewer(epoch) {
			cancel()
			s.finishOrdinaryCaptureDemandSend(fence, epoch, phone.CaptureDemandReceipt{}, context.Canceled)
			return
		}
		receipt, err := sender(ctx, epoch)
		cancel()
		s.finishOrdinaryCaptureDemandSend(fence, epoch, receipt, err)
	}()
}

func (s *Server) finishOrdinaryCaptureDemandSend(fence uint64, epoch uint64, receipt phone.CaptureDemandReceipt, err error) {
	s.captureDemandMu.Lock()
	defer s.captureDemandMu.Unlock()
	if s.captureDemandClosed || fence != s.captureDemandFence || !s.captureDemandSending || s.captureDemandEpoch != epoch {
		return
	}
	s.captureDemandSending = false
	s.captureDemandCancel = nil
	if err != nil || receipt.StreamEpoch != epoch || receipt.Generation == 0 || receipt.ConnectionGeneration == 0 {
		return
	}
	if s.captureDemandConnection != 0 && receipt.ConnectionGeneration != s.captureDemandConnection {
		return
	}
	if receipt.SentAt.IsZero() {
		receipt.SentAt = time.Now()
	}
	if receipt.ExpiresAt.IsZero() || !receipt.ExpiresAt.After(receipt.SentAt) {
		receipt.ExpiresAt = receipt.SentAt.Add(phone.CaptureDemandTTL)
	}
	s.captureDemandReceipt = receipt
	delay := time.Until(receipt.ExpiresAt)
	if delay < 0 {
		delay = 0
	}
	s.captureDemandExpiry = time.AfterFunc(delay, func() {
		s.expireOrdinaryCaptureDemand(receipt)
	})
}

// observeOrdinaryCaptureConnection fences a still-live opportunity as soon as
// the first message from a replacement phone media socket arrives. A same-epoch
// reconnect therefore cannot inherit the old socket's permit or wait for its
// expiry timer before recovery.
func (s *Server) observeOrdinaryCaptureConnection(connectionGeneration uint64) {
	if s == nil || connectionGeneration == 0 {
		return
	}
	s.captureDemandMu.Lock()
	if s.captureDemandConnection != 0 && s.captureDemandConnection != connectionGeneration {
		s.cancelCaptureDemandSendLocked()
		s.captureDemandFence++
		s.clearCaptureDemandReceiptLocked()
	}
	s.captureDemandConnection = connectionGeneration
	s.captureDemandMu.Unlock()
}

func (s *Server) expireOrdinaryCaptureDemand(expected phone.CaptureDemandReceipt) {
	s.captureDemandMu.Lock()
	current := s.captureDemandReceipt
	if s.captureDemandClosed || current.StreamEpoch != expected.StreamEpoch ||
		current.Generation != expected.Generation || current.ConnectionGeneration != expected.ConnectionGeneration {
		s.captureDemandMu.Unlock()
		return
	}
	s.clearCaptureDemandReceiptLocked()
	s.captureDemandMu.Unlock()
	// A successful current-socket write followed by no binary result is retried
	// once per TTL while useful browser credit still exists. Failed writes never
	// install a timer, so a missing phone cannot create a retry loop.
	s.requestOrdinaryCaptureIfUseful()
}

func (s *Server) hasUsefulOrdinaryCaptureViewer(epoch uint64) bool {
	for _, viewer := range s.clientSnapshot() {
		if viewer.canUseOrdinaryCapture(epoch) {
			return true
		}
	}
	return false
}

func (c *client) canUseOrdinaryCapture(epoch uint64) bool {
	if c == nil || epoch == 0 {
		return false
	}
	c.videoMu.Lock()
	defer c.videoMu.Unlock()
	if c.resultPriorityActiveLocked(time.Now()) && c.videoResultPriorityPhase == resultPriorityMark {
		return false
	}
	inFlightReceiptObserved := c.videoInFlight &&
		c.videoInFlightEpoch == epoch &&
		c.videoInFlightConfigGen == c.videoConfigGeneration &&
		c.videoV2FeedbackReceived == c.videoInFlightSeq
	return !c.writerClosed && c.videoFeedbackVersion == 2 &&
		c.videoConfigWrittenEpoch == epoch && c.videoConfigWrittenGen == c.videoConfigGeneration &&
		c.videoEpoch == epoch && c.videoV2Visibility != "hidden" &&
		(!c.videoInFlight || inFlightReceiptObserved) && len(c.videoQueue) == 0 && !c.videoReceiptAwaiting
}

// completeOrdinaryCaptureOpportunity runs before source-frame admission. A
// binary result consumes the aggregate opportunity even when it is malformed,
// stale, or from the wrong epoch; admission then decides whether a viewer can
// use it and a post-broadcast recheck may request one replacement opportunity.
func (s *Server) completeOrdinaryCaptureOpportunity() {
	s.captureDemandMu.Lock()
	if s.captureDemandSending || s.captureDemandReceipt.Generation != 0 {
		s.cancelCaptureDemandSendLocked()
		s.captureDemandFence++
		s.clearCaptureDemandReceiptLocked()
	}
	s.captureDemandMu.Unlock()
}

func (s *Server) fenceOrdinaryCaptureDemand(epoch uint64) {
	s.captureDemandMu.Lock()
	s.cancelCaptureDemandSendLocked()
	s.captureDemandFence++
	s.captureDemandEpoch = epoch
	s.clearCaptureDemandReceiptLocked()
	s.captureDemandMu.Unlock()
}

func (s *Server) closeOrdinaryCaptureDemand() {
	if s == nil {
		return
	}
	s.captureDemandMu.Lock()
	s.captureDemandClosed = true
	s.cancelCaptureDemandSendLocked()
	s.captureDemandFence++
	s.clearCaptureDemandReceiptLocked()
	s.captureDemandMu.Unlock()
}

func (s *Server) cancelCaptureDemandSendLocked() {
	if s.captureDemandCancel != nil {
		s.captureDemandCancel()
	}
	s.captureDemandCancel = nil
	s.captureDemandSending = false
}

func (s *Server) clearCaptureDemandReceiptLocked() {
	if s.captureDemandExpiry != nil {
		s.captureDemandExpiry.Stop()
	}
	s.captureDemandExpiry = nil
	s.captureDemandReceipt = phone.CaptureDemandReceipt{}
}
