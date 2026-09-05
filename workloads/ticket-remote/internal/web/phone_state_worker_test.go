package web

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ticketremote/internal/phone"
	"ticketremote/internal/state"
)

type blockingPhoneStatusStore struct {
	state.Store
	started chan struct{}
	release chan struct{}
	writes  chan state.PhoneInput
	count   atomic.Int32
}

type retryingPhoneStatusStore struct {
	state.Store
	failures atomic.Int32
	attempts chan state.PhoneInput
	writes   chan state.PhoneInput
}

func (s *retryingPhoneStatusStore) UpdatePhoneStatus(ctx context.Context, input state.PhoneInput) error {
	select {
	case s.attempts <- input:
	case <-ctx.Done():
		return ctx.Err()
	}
	if s.failures.Add(-1) >= 0 {
		return errors.New("temporary phone-state failure")
	}
	if err := s.Store.UpdatePhoneStatus(ctx, input); err != nil {
		return err
	}
	select {
	case s.writes <- input:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func (s *blockingPhoneStatusStore) UpdatePhoneStatus(ctx context.Context, input state.PhoneInput) error {
	if s.count.Add(1) == 1 {
		close(s.started)
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := s.Store.UpdatePhoneStatus(ctx, input); err != nil {
		return err
	}
	select {
	case s.writes <- input:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func TestPhoneStateWritesDoNotBlockMediaAndCoalesceToLatest(t *testing.T) {
	base := NewMemoryStore()
	if err := base.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID: "vivi-default", PhoneBackendID: "pixel", PhoneAttachName: "Pixel", PhoneBaseURL: "http://phone.test",
	}); err != nil {
		t.Fatal(err)
	}
	store := &blockingPhoneStatusStore{
		Store:   base,
		started: make(chan struct{}),
		release: make(chan struct{}),
		writes:  make(chan state.PhoneInput, 4),
	}
	relay := phone.NewRelay(phone.RelayConfig{BackendID: "pixel", AttachName: "Pixel", BaseURL: "http://phone.test"})
	server := newTicketWebServer(t, store, relay, "http://phone.test")
	t.Cleanup(server.Close)

	server.handlePhoneText(testAllIntraConfig([]byte(`{"type":"config","streamEpoch":7,"phoneUptimeMillis":10000}`)))
	server.handlePhoneText([]byte(`{"type":"health","data":{"phoneUptimeMillis":10000,"generation":1}}`))
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("phone state worker did not start its first write")
	}

	started := time.Now()
	server.handlePhoneMessage(phone.Message{Binary: testTSF2FrameWithTimestamp(7, 1, true, 10_000)})
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("blocked state write delayed media ingestion by %s", elapsed)
	}
	if status := server.direct.streamStatus(time.Now(), phone.Health{}); status["framesForwarded"] != uint64(1) {
		t.Fatalf("media was not admitted while state storage was blocked: %#v", status)
	}

	server.handlePhoneText([]byte(`{"type":"health","data":{"phoneUptimeMillis":10001,"generation":2}}`))
	server.handlePhoneText([]byte(`{"type":"health","data":{"phoneUptimeMillis":10002,"generation":3}}`))
	close(store.release)

	var writes []state.PhoneInput
	deadline := time.After(2 * time.Second)
	for len(writes) < 2 {
		select {
		case input := <-store.writes:
			writes = append(writes, input)
		case <-deadline:
			t.Fatalf("timed out waiting for coalesced writes: %#v", writes)
		}
	}
	select {
	case extra := <-store.writes:
		t.Fatalf("intermediate replaceable health update was written: %#v", extra)
	case <-time.After(50 * time.Millisecond):
	}
	if !strings.Contains(writes[0].HealthJSON, `"generation":1`) || !strings.Contains(writes[1].HealthJSON, `"generation":3`) {
		t.Fatalf("writes were not first then latest: %#v", writes)
	}
}

func TestPhoneStateWorkerPreservesCausalTicketTransitionsDuringBlockedWrite(t *testing.T) {
	base := NewMemoryStore()
	if err := base.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID: "vivi-default", PhoneBackendID: "pixel", PhoneAttachName: "Pixel", PhoneBaseURL: "http://phone.test",
	}); err != nil {
		t.Fatal(err)
	}
	store := &blockingPhoneStatusStore{
		Store: base, started: make(chan struct{}), release: make(chan struct{}), writes: make(chan state.PhoneInput, 8),
	}
	relay := phone.NewRelay(phone.RelayConfig{BackendID: "pixel", AttachName: "Pixel", BaseURL: "http://phone.test"})
	server := newTicketWebServer(t, store, relay, "http://phone.test")
	t.Cleanup(server.Close)

	server.handlePhoneText([]byte(`{"type":"health","data":{"phoneUptimeMillis":10000,"generation":1}}`))
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("phone state worker did not enter blocked write")
	}
	server.handlePhoneText([]byte(`{"type":"ticket_state_event","eventSeq":1,"ticketState":"opening","reason":"first","requestId":"request-1","streamEpoch":7,"phoneUptimeMillis":10001}`))
	server.handlePhoneText([]byte(`{"type":"ticket_state_event","eventSeq":2,"ticketState":"opened","reason":"second","requestId":"request-1","streamEpoch":7,"phoneUptimeMillis":10002}`))
	close(store.release)

	var writes []state.PhoneInput
	deadline := time.After(2 * time.Second)
	for len(writes) < 3 {
		select {
		case input := <-store.writes:
			writes = append(writes, input)
		case <-deadline:
			t.Fatalf("timed out waiting for ordered causal writes: %#v", writes)
		}
	}
	if !strings.Contains(writes[1].HealthJSON, `"eventSeq":1`) || !strings.Contains(writes[1].HealthJSON, `"ticketState":"opening"`) ||
		!strings.Contains(writes[2].HealthJSON, `"eventSeq":2`) || !strings.Contains(writes[2].HealthJSON, `"ticketState":"opened"`) {
		t.Fatalf("causal ticket transitions were collapsed or reordered: %#v", writes)
	}
}

func TestPhoneStateCausalRetryBackoffIsCappedEqualJitter(t *testing.T) {
	cases := []struct {
		attempt uint
		ceiling time.Duration
	}{
		{attempt: 0, ceiling: 200 * time.Millisecond},
		{attempt: 1, ceiling: 400 * time.Millisecond},
		{attempt: 2, ceiling: 800 * time.Millisecond},
		{attempt: 3, ceiling: 1600 * time.Millisecond},
		{attempt: 4, ceiling: 3200 * time.Millisecond},
		{attempt: 5, ceiling: 5 * time.Second},
		{attempt: 100, ceiling: 5 * time.Second},
	}
	for _, test := range cases {
		floor := test.ceiling / 2
		if got := phoneStateRetryDelay(test.attempt, 0); got != floor {
			t.Fatalf("attempt %d minimum delay = %s, want %s", test.attempt, got, floor)
		}
		span := uint64(test.ceiling-floor) + 1
		if got := phoneStateRetryDelay(test.attempt, span-1); got != test.ceiling {
			t.Fatalf("attempt %d maximum delay = %s, want %s", test.attempt, got, test.ceiling)
		}
	}
}

func TestPhoneStateWorkerRetriesCausalHeadBeforeFollowingFIFOEvent(t *testing.T) {
	base := NewMemoryStore()
	if err := base.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID: "vivi-default", PhoneBackendID: "pixel", PhoneAttachName: "Pixel", PhoneBaseURL: "http://phone.test",
	}); err != nil {
		t.Fatal(err)
	}
	store := &retryingPhoneStatusStore{
		Store: base, attempts: make(chan state.PhoneInput, 4), writes: make(chan state.PhoneInput, 2),
	}
	store.failures.Store(2)
	relay := phone.NewRelay(phone.RelayConfig{BackendID: "pixel", AttachName: "Pixel", BaseURL: "http://phone.test"})
	server := newTicketWebServer(t, store, relay, "http://phone.test")
	t.Cleanup(server.Close)

	server.handlePhoneText([]byte(`{"type":"ticket_state_event","eventSeq":1,"ticketState":"opening","requestId":"retry-fifo","streamEpoch":7}`))
	server.handlePhoneText([]byte(`{"type":"ticket_state_event","eventSeq":2,"ticketState":"opened","requestId":"retry-fifo","streamEpoch":7}`))

	attempts := make([]state.PhoneInput, 0, 4)
	deadline := time.After(3 * time.Second)
	for len(attempts) < 4 {
		select {
		case input := <-store.attempts:
			attempts = append(attempts, input)
		case <-deadline:
			t.Fatalf("timed out waiting for causal retries: %#v", attempts)
		}
	}
	for index := 0; index < 3; index++ {
		if !strings.Contains(attempts[index].HealthJSON, `"eventSeq":1`) {
			t.Fatalf("attempt %d bypassed the failing FIFO head: %s", index+1, attempts[index].HealthJSON)
		}
	}
	if !strings.Contains(attempts[3].HealthJSON, `"eventSeq":2`) {
		t.Fatalf("following causal event did not wait for head success: %s", attempts[3].HealthJSON)
	}
	for sequence := 1; sequence <= 2; sequence++ {
		select {
		case input := <-store.writes:
			if !strings.Contains(input.HealthJSON, fmt.Sprintf(`"eventSeq":%d`, sequence)) {
				t.Fatalf("successful causal writes lost FIFO order at %d: %s", sequence, input.HealthJSON)
			}
		case <-deadline:
			t.Fatalf("timed out waiting for successful causal write %d", sequence)
		}
	}
}

func TestPhoneStateOverflowReconnectStaysLatchedUntilCapacityReopens(t *testing.T) {
	server := &Server{phoneStateEventInFlight: phoneStateEventQueueMax}
	completed := make(chan struct{}, 2)
	var calls atomic.Int32
	reconnect := func() {
		calls.Add(1)
		completed <- struct{}{}
	}
	if !server.startPhoneStateOverflowReconnect(reconnect) {
		t.Fatal("first overflow reconnect was not started")
	}
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("first overflow reconnect did not complete")
	}
	deadline := time.Now().Add(time.Second)
	for {
		server.phoneStateMu.Lock()
		inFlight := server.phoneStateOverflowReconnectInFlight
		server.phoneStateMu.Unlock()
		if !inFlight {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first overflow reconnect remained in flight")
		}
		time.Sleep(time.Millisecond)
	}

	const contenders = 64
	results := make(chan bool, contenders)
	var group sync.WaitGroup
	for range contenders {
		group.Add(1)
		go func() {
			defer group.Done()
			results <- server.startPhoneStateOverflowReconnect(reconnect)
		}()
	}
	group.Wait()
	close(results)
	for accepted := range results {
		if accepted {
			t.Fatal("full-queue overflow bypassed the incident latch after reconnect returned")
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("reconnect calls while the queue remained full = %d, want 1", got)
	}

	server.finishPhoneStateUpdate(phoneStateUpdate{})
	server.phoneStateMu.Lock()
	if server.phoneStateOverflowIncidentLatched {
		server.phoneStateMu.Unlock()
		t.Fatal("capacity reopening did not re-arm overflow detection")
	}
	server.phoneStateEventInFlight = phoneStateEventQueueMax
	server.phoneStateMu.Unlock()
	if !server.startPhoneStateOverflowReconnect(reconnect) {
		t.Fatal("a later full-queue incident was not accepted after capacity reopened")
	}
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("later overflow reconnect did not run")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("reconnect calls after capacity reopened = %d, want 2", got)
	}
}

func TestPhoneStateOverflowReconnectSerializesRearmedIncident(t *testing.T) {
	server := &Server{phoneStateEventInFlight: phoneStateEventQueueMax}
	started := make(chan int32, 2)
	releaseFirst := make(chan struct{})
	var calls atomic.Int32
	var active atomic.Int32
	var maximumActive atomic.Int32
	reconnect := func() {
		call := calls.Add(1)
		current := active.Add(1)
		for maximum := maximumActive.Load(); current > maximum && !maximumActive.CompareAndSwap(maximum, current); maximum = maximumActive.Load() {
		}
		started <- call
		if call == 1 {
			<-releaseFirst
		}
		active.Add(-1)
	}
	if !server.startPhoneStateOverflowReconnect(reconnect) {
		t.Fatal("first overflow reconnect was not started")
	}
	select {
	case call := <-started:
		if call != 1 {
			t.Fatalf("first reconnect call = %d, want 1", call)
		}
	case <-time.After(time.Second):
		t.Fatal("first overflow reconnect did not start")
	}

	server.finishPhoneStateUpdate(phoneStateUpdate{})
	server.phoneStateMu.Lock()
	server.phoneStateEventInFlight = phoneStateEventQueueMax
	server.phoneStateMu.Unlock()
	if !server.startPhoneStateOverflowReconnect(reconnect) {
		t.Fatal("rearmed overflow incident was not coalesced behind the running reconnect")
	}
	server.phoneStateMu.Lock()
	pending := server.phoneStateOverflowReconnectPending
	server.phoneStateMu.Unlock()
	if !pending {
		t.Fatal("rearmed overflow incident was not retained for a serial reconnect")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("rearmed incident ran concurrently: calls=%d", got)
	}

	close(releaseFirst)
	select {
	case call := <-started:
		if call != 2 {
			t.Fatalf("serial reconnect call = %d, want 2", call)
		}
	case <-time.After(time.Second):
		t.Fatal("coalesced reconnect did not run after the first completed")
	}
	if got := maximumActive.Load(); got != 1 {
		t.Fatalf("maximum concurrent reconnect callbacks = %d, want 1", got)
	}
}

func TestPhoneStateOverflowReconnectDoesNotStartAfterShutdown(t *testing.T) {
	server := &Server{phoneStateClosed: true}
	var calls atomic.Int32
	if server.startPhoneStateOverflowReconnect(func() { calls.Add(1) }) {
		t.Fatal("closed server accepted an overflow reconnect")
	}
	if calls.Load() != 0 {
		t.Fatal("closed server ran an overflow reconnect")
	}
}

func TestPhoneStateWorkerKeepsNormal64EventStallMatrixLossless(t *testing.T) {
	base := NewMemoryStore()
	if err := base.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID: "vivi-default", PhoneBackendID: "pixel", PhoneAttachName: "Pixel", PhoneBaseURL: "http://phone.test",
	}); err != nil {
		t.Fatal(err)
	}
	store := &blockingPhoneStatusStore{
		Store: base, started: make(chan struct{}), release: make(chan struct{}), writes: make(chan state.PhoneInput, phoneStateEventQueueMax),
	}
	relay := phone.NewRelay(phone.RelayConfig{BackendID: "pixel", AttachName: "Pixel", BaseURL: "http://phone.test"})
	server := newTicketWebServer(t, store, relay, "http://phone.test")
	t.Cleanup(server.Close)

	server.handlePhoneText([]byte(`{"type":"ticket_state_event","eventSeq":1,"ticketState":"state-1","requestId":"stall-matrix","streamEpoch":7}`))
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("first causal write did not enter the simulated 40-second store stall")
	}
	for sequence := 2; sequence <= phoneStateEventQueueMax; sequence++ {
		server.handlePhoneText([]byte(fmt.Sprintf(`{"type":"ticket_state_event","eventSeq":%d,"ticketState":"state-%d","requestId":"stall-matrix","streamEpoch":7}`, sequence, sequence)))
	}
	// Capacity beyond the documented normal 40-second/64-event matrix is an
	// observable reconnect fail-safe, not an in-memory replay promise. It must
	// be rejected before source-state acceptance.
	server.handlePhoneText([]byte(fmt.Sprintf(`{"type":"ticket_state_event","eventSeq":%d,"ticketState":"overflow","requestId":"stall-matrix","streamEpoch":7}`, phoneStateEventQueueMax+1)))
	server.direct.mu.Lock()
	lastAccepted := server.direct.lastPixelTicketEvent.EventSeq
	server.direct.mu.Unlock()
	if lastAccepted != int64(phoneStateEventQueueMax) {
		t.Fatalf("overflow event crossed the pre-acceptance fail-safe: accepted=%d", lastAccepted)
	}
	close(store.release)
	for sequence := 1; sequence <= phoneStateEventQueueMax; sequence++ {
		select {
		case input := <-store.writes:
			needle := fmt.Sprintf(`"eventSeq":%d`, sequence)
			if !strings.Contains(input.HealthJSON, needle) {
				t.Fatalf("causal FIFO write %d was lost/reordered: %s", sequence, input.HealthJSON)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out at causal FIFO event %d", sequence)
		}
	}
}
