package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nhooyr.io/websocket"
	"ticketremote/internal/auth"
	"ticketremote/internal/state"
)

type trialMeterStore struct {
	state.InvitationStore
	mu                    sync.Mutex
	reserves, releases    []state.TrialStreamInput
	duration              time.Duration
	reserveErr, resultErr error
	expired               bool
	reserved              chan state.TrialStreamInput
	released              chan state.TrialStreamInput
	allowReserve          <-chan struct{}
}

func (s *trialMeterStore) ReserveTrialStream(ctx context.Context, input state.TrialStreamInput) (state.Invitation, error) {
	s.mu.Lock()
	s.reserves = append(s.reserves, input)
	err := s.reserveErr
	if input.ResultOnly {
		err = s.resultErr
	}
	duration, expired, allow := s.duration, s.expired, s.allowReserve
	s.mu.Unlock()
	select {
	case s.reserved <- input:
	default:
	}
	if allow != nil {
		select {
		case <-allow:
		case <-ctx.Done():
			return state.Invitation{}, ctx.Err()
		}
	}
	if err != nil {
		return state.Invitation{}, err
	}
	if expired {
		duration = -time.Second
	}
	return state.Invitation{LeaseSequence: input.Sequence, LeaseUntilMS: time.Now().Add(duration).UnixMilli()}, nil
}

func (s *trialMeterStore) ReleaseTrialStream(_ context.Context, input state.TrialStreamInput) (state.Invitation, error) {
	s.mu.Lock()
	s.releases = append(s.releases, input)
	s.mu.Unlock()
	select {
	case s.released <- input:
	default:
	}
	return state.Invitation{}, nil
}

func (s *trialMeterStore) calls() (reserves, releases []state.TrialStreamInput) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]state.TrialStreamInput(nil), s.reserves...), append([]state.TrialStreamInput(nil), s.releases...)
}

func newTrialMeterFixture(t *testing.T) (*trialStreamMeter, *trialMeterStore) {
	t.Helper()
	store := &trialMeterStore{duration: 5 * time.Second, reserved: make(chan state.TrialStreamInput, 20), released: make(chan state.TrialStreamInput, 20)}
	meter := newTrialStreamMeter(store, "ticket", auth.GuestSession{InvitationID: "invite", SessionID: "session"}, state.Invitation{LeaseSequence: 8})
	t.Cleanup(func() { meter.pause(true) })
	return meter, store
}

func TestTrialStreamChargesOnlyBeforeDeliveryAndReusesPrepaidSlice(t *testing.T) {
	meter, store := newTrialMeterFixture(t)
	if reserves, _ := store.calls(); len(reserves) != 0 {
		t.Fatal("creating a socket meter consumed trial time")
	}
	before := time.Now()
	if err := meter.admit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if meter.until.Before(before) || meter.until.After(time.Now().Add(5*time.Second)) {
		t.Fatal("meter did not retain the bounded prepaid slice")
	}
	for i := 0; i < 5; i++ {
		if err := meter.admit(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	reserves, _ := store.calls()
	if len(reserves) != 1 || reserves[0].Sequence != 9 || reserves[0].TicketID != "ticket" || reserves[0].InvitationID != "invite" || reserves[0].SessionID != "session" || reserves[0].ResultOnly {
		t.Fatalf("frames within the paid slice were not idempotent: %+v", reserves)
	}
	meter.pause(false)
	if err := meter.admit(context.Background()); err != nil {
		t.Fatal(err)
	}
	reserves, releases := store.calls()
	if len(reserves) != 2 || reserves[1].Sequence != 10 || len(releases) != 1 || releases[0].Sequence != 9 {
		t.Fatalf("resumed stream reused released credit: reserves=%+v releases=%+v", reserves, releases)
	}
}

func TestTrialStreamPauseRefundsOnceAndClosedSocketCannotResume(t *testing.T) {
	meter, store := newTrialMeterFixture(t)
	meter.pause(false)
	if _, releases := store.calls(); len(releases) != 0 {
		t.Fatal("an unopened stream attempted a refund")
	}
	if err := meter.admit(context.Background()); err != nil {
		t.Fatal(err)
	}
	meter.pause(false)
	meter.pause(true)
	meter.pause(true)
	if err := meter.admit(context.Background()); err == nil {
		t.Fatal("closed socket regained stream admission")
	}
	reserves, releases := store.calls()
	if len(reserves) != 1 || len(releases) != 1 || releases[0] != reserves[0] {
		t.Fatalf("pause/close did not refund the exact lease once: %+v %+v", reserves, releases)
	}
}

func TestTrialStreamUnavailableFrameRefundsRemainingTime(t *testing.T) {
	meter, store := newTrialMeterFixture(t)
	if err := meter.admit(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The last sent frame stops being usable before the five-second lease.
	meter.written(queuedVideoFrame{queuedAt: time.Now(), visualAge: liveFreshMaxAge - 30*time.Millisecond})
	select {
	case release := <-store.released:
		if release.Sequence != 9 {
			t.Fatal("freshness expiry released the wrong lease")
		}
	case <-time.After(time.Second):
		t.Fatal("stream outage kept charging after the last picture became stale")
	}
	meter.mu.Lock()
	until := meter.until
	meter.mu.Unlock()
	if !until.IsZero() {
		t.Fatal("expired picture retained spendable credit")
	}
}

func TestTrialStreamOldFreshnessTimerCannotReleaseNewSlice(t *testing.T) {
	meter, store := newTrialMeterFixture(t)
	if err := meter.admit(context.Background()); err != nil {
		t.Fatal(err)
	}
	meter.written(queuedVideoFrame{queuedAt: time.Now(), visualAge: liveFreshMaxAge - 40*time.Millisecond})
	meter.pause(false)
	if err := meter.admit(context.Background()); err != nil {
		t.Fatal(err)
	}
	meter.written(queuedVideoFrame{queuedAt: time.Now()})
	select {
	case <-time.After(100 * time.Millisecond):
	}
	_, releases := store.calls()
	if len(releases) != 1 || releases[0].Sequence != 9 {
		t.Fatalf("old timer released the new stream slice: %+v", releases)
	}
}

func TestTrialStreamRenewalFencesAlreadyWaitingOldTimer(t *testing.T) {
	meter, store := newTrialMeterFixture(t)
	if err := meter.admit(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-store.reserved
	meter.written(queuedVideoFrame{queuedAt: time.Now(), visualAge: liveFreshMaxAge - 30*time.Millisecond})
	// Expire local credit while retaining the old frame timer, then hold the
	// next durable reservation long enough for that timer to become runnable.
	meter.mu.Lock()
	meter.until = time.Now().Add(-time.Millisecond)
	meter.mu.Unlock()
	allow := make(chan struct{})
	store.mu.Lock()
	store.allowReserve = allow
	store.mu.Unlock()
	finished := make(chan error, 1)
	go func() { finished <- meter.admit(context.Background()) }()
	select {
	case <-store.reserved:
	case <-time.After(time.Second):
		t.Fatal("renewal did not begin")
	}
	<-time.After(60 * time.Millisecond)
	close(allow)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	select {
	case release := <-store.released:
		t.Fatalf("old frame timer refunded newly reserved sequence %d", release.Sequence)
	case <-time.After(60 * time.Millisecond):
	}
	meter.mu.Lock()
	until := meter.until
	meter.mu.Unlock()
	if !time.Now().Before(until) {
		t.Fatal("old frame timer removed renewed stream credit")
	}
}

func TestTrialStreamResultFallbackCannotMintAnotherReservation(t *testing.T) {
	meter, store := newTrialMeterFixture(t)
	store.reserveErr = state.ErrTrialEnded
	if err := meter.admit(context.Background()); err != nil {
		t.Fatal(err)
	}
	reserves, _ := store.calls()
	if len(reserves) != 2 || reserves[0].ResultOnly || !reserves[1].ResultOnly || reserves[0].Sequence != reserves[1].Sequence {
		t.Fatalf("result delivery must ask database authority using the same attempt: %+v", reserves)
	}
	meter.pause(false)
	store.resultErr = state.ErrInvitationUnavailable
	if err := meter.admit(context.Background()); err == nil {
		t.Fatal("missing result entitlement still granted stream time")
	}
	meter.mu.Lock()
	until := meter.until
	meter.mu.Unlock()
	if !until.IsZero() {
		t.Fatal("failed admission retained usable credit")
	}
}

func TestTrialStreamRejectsExpiredReservation(t *testing.T) {
	meter, store := newTrialMeterFixture(t)
	store.expired = true
	if err := meter.admit(context.Background()); err == nil {
		t.Fatal("an already expired reservation admitted a picture")
	}
}

func TestTrialStreamDuplicateFeedbackDoesNotRenewAllowance(t *testing.T) {
	meter, store := newTrialMeterFixture(t)
	if err := meter.admit(context.Background()); err != nil {
		t.Fatal(err)
	}
	viewer := &client{trial: meter, videoV2Visibility: "visible", videoEpoch: 7, videoConfigGeneration: 1, videoWrittenSequence: 2, videoWrittenEvidence: []uint64{2}, firstVideoFrameRendered: true}
	server := &Server{}
	feedback, _ := json.Marshal(streamFeedback{Type: "stream_feedback", Version: 2, Epoch: 7, ConfigGeneration: 1, ReceivedSequence: 2, DecodedSequence: 2, RenderedSequence: 2, PresentedSequence: 2, Visibility: "visible"})
	for i := 0; i < 10; i++ {
		server.handleStreamFeedback(viewer, feedback)
	}
	reserves, _ := store.calls()
	if len(reserves) != 1 {
		t.Fatal("repeated receipt/presentation feedback renewed trial time")
	}
	feedback, _ = json.Marshal(streamFeedback{Type: "stream_feedback", Version: 2, Epoch: 7, ConfigGeneration: 1, ReceivedSequence: 2, DecodedSequence: 2, RenderedSequence: 2, PresentedSequence: 2, Visibility: "hidden"})
	server.handleStreamFeedback(viewer, feedback)
	_, releases := store.calls()
	if len(releases) != 1 {
		t.Fatal("hidden visibility did not refund remaining viewing time")
	}
}

// A loopback WebSocket exercises the real writer without starting a phone or
// relying on the browser to report that delivery was blocked.
func trialWriterPair(t *testing.T, meter *trialStreamMeter) (*client, *websocket.Conn) {
	t.Helper()
	ready := make(chan *client, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		viewer := &client{conn: conn, trial: meter, videoV2Visibility: "visible", videoConfigGeneration: 1}
		ready <- viewer
		for {
			if _, _, err := conn.Read(r.Context()); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	select {
	case viewer := <-ready:
		return viewer, conn
	case <-ctx.Done():
		t.Fatal("writer fixture did not connect")
	}
	return nil, nil
}

func queueTrialFrame(viewer *client) {
	viewer.videoMu.Lock()
	defer viewer.videoMu.Unlock()
	viewer.videoPending = &queuedVideoFrame{data: []byte("fixture-picture"), queuedAt: time.Now(), configGeneration: 1}
}

func TestTrialWriterCannotSendFirstFrameBeforeDurableReservation(t *testing.T) {
	meter, store := newTrialMeterFixture(t)
	allow := make(chan struct{})
	store.allowReserve = allow
	viewer, conn := trialWriterPair(t, meter)
	queueTrialFrame(viewer)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	written := make(chan bool, 1)
	go func() { written <- viewer.writeNextVideoItem(ctx) }()
	select {
	case <-store.reserved:
	case <-ctx.Done():
		t.Fatal("first frame did not request durable allowance")
	}
	received := make(chan error, 1)
	go func() {
		kind, data, err := conn.Read(ctx)
		if err == nil && (kind != websocket.MessageBinary || string(data) != "fixture-picture") {
			err = errors.New("unexpected delivered frame")
		}
		received <- err
	}()
	select {
	case err := <-received:
		t.Fatalf("first frame escaped before allowance was reserved: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(allow)
	select {
	case ok := <-written:
		if !ok {
			t.Fatal("writer rejected the admitted frame")
		}
	case <-ctx.Done():
		t.Fatal("writer did not resume after allowance was reserved")
	}
	if err := <-received; err != nil {
		t.Fatal(err)
	}
}

func TestTrialWriterHidesFrameWithoutSpendingTime(t *testing.T) {
	meter, store := newTrialMeterFixture(t)
	viewer, _ := trialWriterPair(t, meter)
	viewer.videoV2Visibility = "hidden"
	queueTrialFrame(viewer)
	if !viewer.writeNextVideoItem(context.Background()) {
		t.Fatal("hidden frame unnecessarily failed the socket")
	}
	reserves, _ := store.calls()
	if len(reserves) != 0 || viewer.videoInFlight != nil {
		t.Fatal("hidden frame was charged or remained in flight")
	}
}

func TestTrialWriterDeniesFirstFrameWhenAllowanceFails(t *testing.T) {
	meter, store := newTrialMeterFixture(t)
	store.reserveErr, store.resultErr = state.ErrTrialEnded, state.ErrInvitationUnavailable
	viewer, conn := trialWriterPair(t, meter)
	queueTrialFrame(viewer)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	finished := make(chan bool, 1)
	go func() { finished <- viewer.writeNextVideoItem(ctx) }()
	kind, _, err := conn.Read(ctx)
	if err == nil && kind == websocket.MessageBinary {
		t.Fatal("denied trial received a free first picture")
	}
	if <-finished {
		t.Fatal("denied writer reported successful admission")
	}
	if viewer.videoWriterCloseReason() != "trial_ended" {
		t.Fatal("denied allowance did not close the trial writer")
	}
}

type trialBlockedConnection struct {
	net.Conn
	block   atomic.Bool
	closed  chan struct{}
	entered chan struct{}
	once    sync.Once
}

func (c *trialBlockedConnection) Write(value []byte) (int, error) {
	if c.block.Load() {
		select {
		case c.entered <- struct{}{}:
		default:
		}
		<-c.closed
		return 0, io.ErrClosedPipe
	}
	return c.Conn.Write(value)
}

func (c *trialBlockedConnection) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

type trialBlockedListener struct {
	net.Listener
	accepted chan *trialBlockedConnection
}

func (l *trialBlockedListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	blocked := &trialBlockedConnection{Conn: conn, closed: make(chan struct{}), entered: make(chan struct{}, 1)}
	l.accepted <- blocked
	return blocked, nil
}

func blockedTrialWriterPair(t *testing.T, meter *trialStreamMeter) (*client, *trialBlockedConnection) {
	t.Helper()
	ready := make(chan *client, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ready <- &client{conn: conn, trial: meter, videoV2Visibility: "visible", videoConfigGeneration: 1}
		for {
			if _, _, err := conn.Read(r.Context()); err != nil {
				return
			}
		}
	}))
	listener := &trialBlockedListener{Listener: server.Listener, accepted: make(chan *trialBlockedConnection, 1)}
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	viewer := <-ready
	wire := <-listener.accepted
	wire.block.Store(true)
	return viewer, wire
}

func TestTrialWriterSlowWriteStopsAtPrepaidDeadline(t *testing.T) {
	meter, store := newTrialMeterFixture(t)
	store.duration = 40 * time.Millisecond
	viewer, wire := blockedTrialWriterPair(t, meter)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	queueTrialFrame(viewer)
	finished := make(chan bool, 1)
	go func() { finished <- viewer.writeNextVideoItem(ctx) }()
	select {
	case ok := <-finished:
		if ok || viewer.videoWriterCloseReason() != "write_timeout" {
			t.Fatal("slow frame was accepted after its paid time ended")
		}
	case <-time.After(350 * time.Millisecond):
		_ = wire.Close()
		<-finished
		t.Fatal("slow write used the three-second freshness budget instead of its shorter prepaid allowance")
	}
}

func TestTrialWriterHiddenFeedbackCancelsBlockedFrameBeforeRefund(t *testing.T) {
	meter, store := newTrialMeterFixture(t)
	viewer, wire := blockedTrialWriterPair(t, meter)
	viewer.videoEpoch = 7
	queueTrialFrame(viewer)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	finished := make(chan bool, 1)
	go func() { finished <- viewer.writeNextVideoItem(ctx) }()
	select {
	case <-wire.entered:
	case <-ctx.Done():
		t.Fatal("fixture frame never entered a blocked write")
	}
	feedback, _ := json.Marshal(streamFeedback{Type: "stream_feedback", Version: 2, Epoch: 7, ConfigGeneration: 1, Visibility: "hidden"})
	server := &Server{}
	server.handleStreamFeedback(viewer, feedback)
	select {
	case ok := <-finished:
		if ok {
			t.Fatal("refunded hidden frame was accepted for delivery")
		}
	case <-time.After(350 * time.Millisecond):
		_ = wire.Close()
		<-finished
		t.Fatal("hidden feedback refunded time while leaving its binary write active")
	}
	_, releases := store.calls()
	if len(releases) != 1 {
		t.Fatal("hidden blocked frame did not release its unused allowance exactly once")
	}
}

func TestTrialTakeoverClosesOnlyMatchingInvitationMeters(t *testing.T) {
	meter, store := newTrialMeterFixture(t)
	other, otherStore := newTrialMeterFixture(t)
	other.input.InvitationID = "other-invitation"
	if err := meter.admit(context.Background()); err != nil {
		t.Fatal(err)
	}
	server := &Server{clients: map[*client]struct{}{{trial: meter}: {}, {trial: other}: {}, {email: "member@example.test"}: {}}}
	server.closeTrialClients("invite")
	if err := meter.admit(context.Background()); err == nil {
		t.Fatal("taken-over socket regained stream authority")
	}
	if err := other.admit(context.Background()); err != nil {
		t.Fatal("takeover closed a different invitation")
	}
	_, releases := store.calls()
	if len(releases) != 1 {
		t.Fatal("takeover did not release the old socket's unused allowance")
	}
	_, releases = otherStore.calls()
	if len(releases) != 0 {
		t.Fatal("takeover refunded another invitation")
	}
}

func TestSecondTrialPageRequiresTakeoverWithoutClosingCurrentPage(t *testing.T) {
	server, store := newInvitationHTTPFixture(t)
	store.invite.Status, store.invite.ActiveSessionID = "trial_active", "same-cookie-session"
	meter, _ := newTrialMeterFixture(t)
	meter.input.InvitationID = store.invite.ID
	active := &client{trial: meter, trialPageID: strings.Repeat("a", 32)}
	server.mu.Lock()
	server.clients[active] = struct{}{}
	server.mu.Unlock()
	t.Cleanup(func() { server.mu.Lock(); delete(server.clients, active); server.mu.Unlock() })
	response := invitationHTTP(server, "GET", "/api/v1/stream?trial_page="+strings.Repeat("b", 32), "", invitationGuestCookie(t, server, "same-cookie-session"))
	if response.Code != 409 || !strings.Contains(response.Body.String(), `"needsTakeover":true`) {
		t.Fatalf("second page did not receive explicit takeover requirement: %d", response.Code)
	}
	meter.mu.Lock()
	closed := meter.closed
	meter.mu.Unlock()
	if closed {
		t.Fatal("second page silently closed the current trial socket")
	}
}

type trialTakeoverRaceStore struct {
	*invitationHTTPStore
	statuses int
}

func (s *trialTakeoverRaceStore) TrialStatus(context.Context, string, string, string) (state.Invitation, error) {
	s.statuses++
	row := s.invite
	if s.statuses > 1 {
		row.ActiveSessionID = "new-session"
	}
	return row, nil
}

func TestTrialSocketRechecksTakeoverBeforeClosingCurrentSession(t *testing.T) {
	server, base := newInvitationHTTPFixture(t)
	base.invite.Status, base.invite.ActiveSessionID = "trial_active", "old-session"
	store := &trialTakeoverRaceStore{invitationHTTPStore: base}
	server.store = store
	meter, _ := newTrialMeterFixture(t)
	meter.input.InvitationID, meter.input.SessionID = base.invite.ID, "new-session"
	active := &client{trial: meter, trialPageID: strings.Repeat("a", 32)}
	server.mu.Lock()
	server.clients[active] = struct{}{}
	server.mu.Unlock()
	t.Cleanup(func() { server.mu.Lock(); delete(server.clients, active); server.mu.Unlock() })
	response := invitationHTTP(server, "GET", "/api/v1/stream?trial_page="+strings.Repeat("a", 32), "", invitationGuestCookie(t, server, "old-session"))
	if response.Code != 403 || store.statuses != 2 {
		t.Fatalf("socket admission did not revalidate durable takeover: %d calls=%d", response.Code, store.statuses)
	}
	meter.mu.Lock()
	closed := meter.closed
	meter.mu.Unlock()
	if closed {
		t.Fatal("stale session racing takeover closed the current socket")
	}
}
