package web

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"ticketremote/internal/phone"
	"ticketremote/internal/state"
)

const (
	phoneStateEventQueueMax       = 64
	phoneStateRetryInitialCeiling = 200 * time.Millisecond
	phoneStateRetryMaximumCeiling = 5 * time.Second
)

// phoneStateUpdate is either replaceable health or an ordered causal ticket
// transition. Causal transitions reserve bounded FIFO capacity before they are
// accepted and retry storage until success or server shutdown.
type phoneStateUpdate struct {
	sequence      uint64
	replaceable   bool
	reservedEvent bool
	input         state.PhoneInput
	errorEvent    string
	correlationID string
	errorDetail   map[string]any
}

// enqueuePhoneStateUpdate keeps the upstream WebSocket reader independent of
// Spacetime latency. A single worker owns writes, and newer current-state
// observations replace older queued observations while one write is in flight.
func (s *Server) enqueuePhoneStateUpdate(update phoneStateUpdate) bool {
	if s == nil || s.store == nil {
		return false
	}
	s.phoneStateMu.Lock()
	if s.phoneStateClosed {
		if update.reservedEvent && s.phoneStateEventReservations > 0 {
			s.phoneStateEventReservations--
		}
		s.phoneStateMu.Unlock()
		return false
	}
	if !update.replaceable && update.reservedEvent {
		if s.phoneStateEventReservations <= 0 {
			s.phoneStateMu.Unlock()
			return false
		}
		s.phoneStateEventReservations--
	} else if !update.replaceable && s.phoneStateEventCountLocked() >= phoneStateEventQueueMax {
		reconnect := s.latchPhoneStateOverflowIncidentLocked()
		s.phoneStateMu.Unlock()
		s.failPhoneStateEventOverflow(update.correlationID, update.errorDetail, reconnect)
		return false
	}
	s.phoneStateSequence++
	update.sequence = s.phoneStateSequence
	if update.replaceable {
		s.phoneStatePending = &update
	} else {
		s.phoneStateEvents = append(s.phoneStateEvents, update)
	}
	s.phoneStateLatest = update.input
	wake := s.phoneStateWake
	s.phoneStateMu.Unlock()
	if wake == nil {
		return true
	}
	select {
	case wake <- struct{}{}:
	default:
	}
	return true
}

func (s *Server) reservePhoneStateEvent(correlationID string, detail map[string]any) bool {
	if s == nil || s.store == nil {
		return false
	}
	s.phoneStateMu.Lock()
	if s.phoneStateClosed || s.phoneStateEventCountLocked() >= phoneStateEventQueueMax {
		reconnect := !s.phoneStateClosed && s.latchPhoneStateOverflowIncidentLocked()
		s.phoneStateMu.Unlock()
		s.failPhoneStateEventOverflow(correlationID, detail, reconnect)
		return false
	}
	s.phoneStateEventReservations++
	s.phoneStateMu.Unlock()
	return true
}

func (s *Server) phoneStateEventCountLocked() int {
	return len(s.phoneStateEvents) + s.phoneStateEventReservations + s.phoneStateEventInFlight
}

func (s *Server) releasePhoneStateEventReservation() {
	s.phoneStateMu.Lock()
	if s.phoneStateEventReservations > 0 {
		s.phoneStateEventReservations--
	}
	s.rearmPhoneStateOverflowIncidentLocked()
	s.phoneStateMu.Unlock()
}

func (s *Server) failPhoneStateEventOverflow(correlationID string, detail map[string]any, reconnect bool) {
	err := fmt.Errorf("causal phone-state queue is full at %d events", phoneStateEventQueueMax)
	s.recordRuntimeErrorAsync("phone_state_event_queue_overflow", correlationID, err, detail)
	// Reconnect outside the sole media reader. The overflowing transition was
	// rejected before source-state admission, so no accepted event is silently
	// lost and a fresh ordered stream can be established. Concurrent overflows
	// retain their observable rejection but coalesce behind one reconnect.
	if reconnect && s.relay != nil {
		s.schedulePhoneStateOverflowReconnect(func() {
			s.relay.Reconnect("phone state event queue overflow")
		})
	}
}

func (s *Server) latchPhoneStateOverflowIncidentLocked() bool {
	if s.phoneStateClosed || s.phoneStateOverflowIncidentLatched {
		return false
	}
	s.phoneStateOverflowIncidentLatched = true
	return true
}

func (s *Server) rearmPhoneStateOverflowIncidentLocked() {
	if s.phoneStateClosed || s.phoneStateEventCountLocked() < phoneStateEventQueueMax {
		s.phoneStateOverflowIncidentLatched = false
	}
}

func (s *Server) startPhoneStateOverflowReconnect(reconnect func()) bool {
	if s == nil || reconnect == nil {
		return false
	}
	s.phoneStateMu.Lock()
	if !s.latchPhoneStateOverflowIncidentLocked() {
		s.phoneStateMu.Unlock()
		return false
	}
	s.phoneStateMu.Unlock()
	return s.schedulePhoneStateOverflowReconnect(reconnect)
}

func (s *Server) schedulePhoneStateOverflowReconnect(reconnect func()) bool {
	if s == nil || reconnect == nil {
		return false
	}
	s.phoneStateMu.Lock()
	if s.phoneStateClosed {
		s.phoneStateMu.Unlock()
		return false
	}
	if s.phoneStateOverflowReconnectInFlight {
		s.phoneStateOverflowReconnectPending = true
		s.phoneStateMu.Unlock()
		return true
	}
	s.phoneStateOverflowReconnectInFlight = true
	s.phoneStateMu.Unlock()
	go func() {
		for {
			reconnect()
			s.phoneStateMu.Lock()
			if s.phoneStateClosed || !s.phoneStateOverflowReconnectPending {
				s.phoneStateOverflowReconnectInFlight = false
				s.phoneStateOverflowReconnectPending = false
				s.phoneStateMu.Unlock()
				return
			}
			s.phoneStateOverflowReconnectPending = false
			s.phoneStateMu.Unlock()
		}
	}()
	return true
}

func (s *Server) pendingPhoneHealthJSON(backendID string) string {
	s.phoneStateMu.Lock()
	defer s.phoneStateMu.Unlock()
	if strings.TrimSpace(s.phoneStateLatest.BackendID) != strings.TrimSpace(backendID) {
		return ""
	}
	return s.phoneStateLatest.HealthJSON
}

func (s *Server) takePhoneStateUpdate() (phoneStateUpdate, bool) {
	s.phoneStateMu.Lock()
	defer s.phoneStateMu.Unlock()
	if s.phoneStateClosed {
		return phoneStateUpdate{}, false
	}
	if len(s.phoneStateEvents) == 0 && s.phoneStatePending == nil {
		return phoneStateUpdate{}, false
	}
	if len(s.phoneStateEvents) > 0 && (s.phoneStatePending == nil || s.phoneStateEvents[0].sequence < s.phoneStatePending.sequence) {
		update := s.phoneStateEvents[0]
		copy(s.phoneStateEvents, s.phoneStateEvents[1:])
		s.phoneStateEvents = s.phoneStateEvents[:len(s.phoneStateEvents)-1]
		s.phoneStateEventInFlight++
		return update, true
	}
	update := *s.phoneStatePending
	s.phoneStatePending = nil
	return update, true
}

func (s *Server) finishPhoneStateUpdate(update phoneStateUpdate) {
	if update.replaceable {
		return
	}
	s.phoneStateMu.Lock()
	if s.phoneStateEventInFlight > 0 {
		s.phoneStateEventInFlight--
	}
	s.rearmPhoneStateOverflowIncidentLocked()
	s.phoneStateMu.Unlock()
}

func (s *Server) phoneStateUpdateIsCurrent(update phoneStateUpdate) bool {
	s.phoneStateMu.Lock()
	defer s.phoneStateMu.Unlock()
	return !s.phoneStateClosed && update.sequence == s.phoneStateSequence
}

func (s *Server) phoneStateLoop(ctx context.Context) {
	defer close(s.phoneStateDone)
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.phoneStateWake:
		}
		for {
			update, ok := s.takePhoneStateUpdate()
			if !ok {
				break
			}
			activeBackend := s.activePhoneBackend()
			if strings.TrimSpace(update.input.BackendID) == "" || (update.replaceable && update.input.BackendID != activeBackend.ID) {
				s.finishPhoneStateUpdate(update)
				continue
			}
			// A causal transition keeps its original backend identity and FIFO
			// position until it is stored or the service shuts down. Reconciling an
			// old-backend transition after an administrative backend switch is a
			// separate rare-path policy decision; never drop or reassign it here.
			persisted := s.persistPhoneStateUpdate(ctx, update)
			s.finishPhoneStateUpdate(update)
			if !persisted {
				continue
			}
			// Do not let an old backend or an older completed write overwrite the
			// in-process status cache after a switch or a newer observation.
			if !s.phoneStateUpdateIsCurrent(update) || s.activePhoneBackend().ID != update.input.BackendID {
				continue
			}
			health := phone.Health{}
			if s.relay != nil {
				health = s.relay.Snapshot()
			}
			s.cachePhoneStatusUpdate(update.input, health)
		}
	}
}

func (s *Server) persistPhoneStateUpdate(ctx context.Context, update phoneStateUpdate) bool {
	reported := false
	retryAttempt := uint(0)
	for {
		writeCtx, cancel := context.WithTimeout(ctx, streamControlWriteTimeout)
		err := s.store.UpdatePhoneStatus(writeCtx, update.input)
		cancel()
		if err == nil {
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		if !reported {
			s.recordRuntimeErrorAsync(update.errorEvent, update.correlationID, err, update.errorDetail)
			reported = true
		}
		if update.replaceable {
			return false
		}
		timer := time.NewTimer(phoneStateRetryDelay(retryAttempt, rand.Uint64()))
		if retryAttempt < ^uint(0) {
			retryAttempt++
		}
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
}

// phoneStateRetryDelay applies equal jitter to a capped exponential ceiling.
// The first retry stays at least as far apart as the former fixed 100 ms loop.
func phoneStateRetryDelay(attempt uint, random uint64) time.Duration {
	ceiling := phoneStateRetryInitialCeiling
	for step := uint(0); step < attempt && ceiling < phoneStateRetryMaximumCeiling; step++ {
		if ceiling > phoneStateRetryMaximumCeiling/2 {
			ceiling = phoneStateRetryMaximumCeiling
			break
		}
		ceiling *= 2
	}
	floor := ceiling / 2
	span := uint64(ceiling-floor) + 1
	return floor + time.Duration(random%span)
}
