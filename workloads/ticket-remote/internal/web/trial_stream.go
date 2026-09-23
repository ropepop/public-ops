package web

import (
	"context"
	"errors"
	"sync"
	"time"

	"ticketremote/internal/auth"
	"ticketremote/internal/state"
)

// A meter belongs to one socket. The database reserves time before any fresh
// binary frame is sent. No browser timestamp, activity outbox or repeated
// presentation receipt can mint viewing time.
type trialStreamMeter struct {
	mu          sync.Mutex
	store       state.InvitationStore
	input       state.TrialStreamInput
	until       time.Time
	timer       *time.Timer
	writeCancel context.CancelFunc
	generation  uint64
	closed      bool
}

func newTrialStreamMeter(store state.InvitationStore, ticket string, guest auth.GuestSession, invite state.Invitation) *trialStreamMeter {
	return &trialStreamMeter{store: store, input: state.TrialStreamInput{TicketID: ticket, InvitationID: guest.InvitationID, SessionID: guest.SessionID, Sequence: invite.LeaseSequence}}
}

func (m *trialStreamMeter) admit(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("trial socket closed")
	}
	if time.Now().Before(m.until) {
		return nil
	}
	// A prior slice's timer may already be waiting for this lock. It must
	// never release the replacement reservation after the database returns.
	if m.timer != nil {
		m.timer.Stop()
	}
	m.generation++
	m.input.Sequence++
	m.input.ResultOnly = false
	ctx, cancel := context.WithTimeout(ctx, stateLookupTimeout)
	defer cancel()
	row, err := m.store.ReserveTrialStream(ctx, m.input)
	if err != nil {
		// The database only grants result-only delivery for an existing,
		// bounded accepted action; callers cannot establish that grant.
		m.input.ResultOnly = true
		row, err = m.store.ReserveTrialStream(ctx, m.input)
	}
	if err != nil {
		return err
	}
	m.until = time.UnixMilli(row.LeaseUntilMS)
	if !time.Now().Before(m.until) {
		return errors.New("trial viewing allowance ended")
	}
	return nil
}

func (m *trialStreamMeter) written(frame queuedVideoFrame) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	if m.timer != nil {
		m.timer.Stop()
	}
	m.generation++
	generation := m.generation
	deadline := queuedFrameExpiresAt(frame)
	if deadline.IsZero() || m.until.Before(deadline) {
		deadline = m.until
	}
	m.timer = time.AfterFunc(time.Until(deadline), func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if generation == m.generation {
			m.releaseLocked()
		}
	})
}

func (m *trialStreamMeter) writeContext(parent context.Context, deadline time.Time) (context.Context, context.CancelFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.timer != nil {
		m.timer.Stop()
	}
	m.generation++
	if m.until.Before(deadline) {
		deadline = m.until
	}
	ctx, cancel := context.WithDeadline(parent, deadline)
	m.writeCancel = cancel
	if m.closed {
		cancel()
	}
	return ctx, cancel
}

func (m *trialStreamMeter) releaseLocked() {
	if m.writeCancel != nil {
		m.writeCancel()
		m.writeCancel = nil
	}
	if m.until.IsZero() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), stateLookupTimeout)
	defer cancel()
	_, _ = m.store.ReleaseTrialStream(ctx, m.input)
	m.until = time.Time{}
}

func (m *trialStreamMeter) pause(close bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.timer != nil {
		m.timer.Stop()
	}
	m.generation++
	m.closed = m.closed || close
	m.releaseLocked()
}

func (s *Server) closeTrialClients(invitationID string) {
	for _, c := range s.clientSnapshot() {
		if c.trial != nil && c.trial.input.InvitationID == invitationID {
			c.trial.pause(true)
			if c.conn != nil {
				_ = c.conn.CloseNow()
			}
		}
	}
}
