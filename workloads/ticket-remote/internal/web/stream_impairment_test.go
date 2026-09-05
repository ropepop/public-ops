package web

import (
	"fmt"
	"math"
	"testing"
	"time"
)

// This is a deterministic scheduler harness, not a TCP emulator. It turns a
// nominal complete-picture size plus bandwidth, RTT, and loss into a receipt
// delay, then drives the production queue and Feedback v2 credit methods with
// virtual time. Loss is modeled conservatively as reduced useful goodput and
// one repair RTT. The model distinguishes write completion, receipt liveness,
// and useful presentation; packet-level behavior remains the job of the live
// impairment suite.
const (
	impairmentPictureBytes      = 1_000_000
	impairmentCapturePeriod     = time.Second
	impairmentPresentationDelay = 20 * time.Millisecond
	impairmentReconnectDelay    = 250 * time.Millisecond
)

type impairmentPath struct {
	bandwidthMbps float64
	rtt           time.Duration
	lossPercent   float64
	blackhole     bool
	ackBlackhole  bool
}

func (p impairmentPath) serializationDelay() time.Duration {
	if p.blackhole || p.bandwidthMbps <= 0 || p.lossPercent >= 100 {
		return time.Duration(math.MaxInt64)
	}
	usefulMbps := p.bandwidthMbps * (1 - p.lossPercent/100)
	seconds := float64(impairmentPictureBytes*8) / (usefulMbps * 1_000_000)
	return time.Duration(math.Ceil(seconds*1000)) * time.Millisecond
}

func (p impairmentPath) receiptDelay() (time.Duration, bool) {
	arrival, ok := p.browserArrivalDelay()
	if !ok || p.ackBlackhole {
		return 0, false
	}
	return arrival + p.rtt/2, true
}

func (p impairmentPath) browserArrivalDelay() (time.Duration, bool) {
	serialization := p.serializationDelay()
	if serialization == time.Duration(math.MaxInt64) {
		return 0, false
	}
	repair := time.Duration(0)
	if p.lossPercent > 0 {
		repair = p.rtt
	}
	return serialization + p.rtt/2 + repair, true
}

func (p impairmentPath) feasibility() string {
	serialization := p.serializationDelay()
	if serialization > liveFreshMaxAge {
		return "serialization_infeasible"
	}
	arrival, ok := p.browserArrivalDelay()
	if !ok || arrival+impairmentPresentationDelay > liveFreshMaxAge {
		return "presentation_infeasible"
	}
	receipt, ok := p.receiptDelay()
	if !ok || receipt-serialization >= videoReceiptLivenessTimeout {
		return "receipt_infeasible"
	}
	return "deadline_feasible"
}

type impairmentDelivery struct {
	sequence          uint64
	sourceAt          time.Duration
	sentAt            time.Duration
	writeAt           time.Duration
	browserArrivalAt  time.Duration
	receiptAt         time.Duration
	sourceExpiresAt   time.Duration
	receiptDeadlineAt time.Duration
	written           bool
	browserArrived    bool
	frame             queuedVideoFrame
}

type impairmentReceipt struct {
	sequence uint64
	sourceAt time.Duration
	at       time.Duration
}

type impairmentPresentation struct {
	sequence uint64
	sourceAt time.Duration
	at       time.Duration
}

type impairmentResult struct {
	offered         uint64
	sent            []impairmentDelivery
	received        []impairmentReceipt
	presented       []impairmentReceipt
	timeouts        int
	writeTimeouts   int
	receiptTimeouts int
	maxSlots        int
	maxPending      int
}

type impairmentHarness struct {
	t             *testing.T
	base          time.Time
	now           time.Duration
	viewer        *client
	reconnectAt   time.Duration
	outstanding   *impairmentDelivery
	presentations []impairmentPresentation
	sourceAt      map[uint64]time.Duration
	latestOffered uint64
	pathAt        func(time.Duration) impairmentPath
	result        impairmentResult
}

func newImpairmentViewer(t *testing.T) *client {
	t.Helper()
	viewer := &client{}
	viewer.enqueueControl([]byte(`{"type":"config","streamEpoch":7}`))
	config, ok := viewer.nextVideoWriteItem()
	if !ok || config.control == nil || !config.control.config || config.frame != nil {
		t.Fatalf("impairment viewer did not start with one configuration: %#v", config)
	}
	viewer.noteVideoConfigWritten(*config.control)
	return viewer
}

func newImpairmentHarness(t *testing.T, pathAt func(time.Duration) impairmentPath) *impairmentHarness {
	t.Helper()
	return &impairmentHarness{
		t: t, base: time.Unix(1_800_000_000, 0), viewer: newImpairmentViewer(t),
		sourceAt: make(map[uint64]time.Duration), pathAt: pathAt,
	}
}

func (h *impairmentHarness) connect() {
	h.viewer = newImpairmentViewer(h.t)
	h.latestOffered = 0
	h.reconnectAt = 0
}

func (h *impairmentHarness) offer(sequence uint64) {
	h.result.offered++
	h.sourceAt[sequence] = h.now
	if h.viewer == nil {
		return
	}
	h.viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, sequence, true, sequence))
	h.latestOffered = sequence
	h.normalizePendingAge()
}

func (h *impairmentHarness) normalizePendingAge() {
	if h.viewer == nil {
		return
	}
	h.viewer.videoMu.Lock()
	if len(h.viewer.videoQueue) == 1 {
		frame := &h.viewer.videoQueue[0]
		frame.queuedAt = time.Now()
		frame.visualAge = h.now - h.sourceAt[frame.meta.sequence]
	}
	h.viewer.videoMu.Unlock()
}

func (h *impairmentHarness) trySend() {
	if h.viewer == nil || h.outstanding != nil {
		return
	}
	h.normalizePendingAge()
	item, ok := h.viewer.nextVideoWriteItem()
	if !ok {
		return
	}
	if item.frame == nil {
		h.t.Fatalf("unexpected control message after configured impairment start: %#v", item)
	}
	sourceAt, found := h.sourceAt[item.frame.meta.sequence]
	if !found {
		h.t.Fatalf("sent sequence %d has no source time", item.frame.meta.sequence)
	}
	if age := h.now - sourceAt; age > liveFreshMaxAge {
		h.t.Fatalf("production scheduler emitted expired sequence %d at age %s", item.frame.meta.sequence, age)
	}
	path := h.pathAt(h.now)
	serialization := path.serializationDelay()
	arrivalDelay, arrives := path.browserArrivalDelay()
	delay, completes := path.receiptDelay()
	writeAt := time.Duration(math.MaxInt64)
	browserArrivalAt := time.Duration(math.MaxInt64)
	receiptAt := time.Duration(math.MaxInt64)
	if serialization != time.Duration(math.MaxInt64) {
		writeAt = h.now + serialization
	}
	if arrives {
		browserArrivalAt = h.now + arrivalDelay
	}
	if completes {
		receiptAt = h.now + delay
	}
	delivery := impairmentDelivery{
		sequence: item.frame.meta.sequence, sourceAt: sourceAt, sentAt: h.now,
		writeAt: writeAt, browserArrivalAt: browserArrivalAt, receiptAt: receiptAt,
		sourceExpiresAt: sourceAt + liveFreshMaxAge, frame: *item.frame,
	}
	h.result.sent = append(h.result.sent, delivery)
	h.outstanding = &delivery
}

func (h *impairmentHarness) completeWrite() {
	delivery := h.outstanding
	delivery.written = true
	delivery.receiptDeadlineAt = h.now + videoReceiptLivenessTimeout
	h.viewer.noteVideoFrameWrittenAt(delivery.frame, h.base.Add(h.now))
	h.viewer.videoMu.Lock()
	got := h.viewer.videoReceiptDeadlineAt
	h.viewer.videoMu.Unlock()
	if want := h.base.Add(delivery.receiptDeadlineAt); !got.Equal(want) {
		h.t.Fatalf("receipt deadline = %s, want independent post-write deadline %s", got, want)
	}
}

func (h *impairmentHarness) arriveAtBrowser() {
	delivery := h.outstanding
	delivery.browserArrived = true
	if h.now <= delivery.sourceExpiresAt {
		h.presentations = append(h.presentations, impairmentPresentation{
			sequence: delivery.sequence,
			sourceAt: delivery.sourceAt,
			at:       h.now + impairmentPresentationDelay,
		})
	}
	// A complete stale picture still sends its transport receipt, but the
	// browser drops it before decode and presentation.
}

func (h *impairmentHarness) presentDue() {
	for len(h.presentations) > 0 && h.presentations[0].at <= h.now {
		presentation := h.presentations[0]
		h.presentations = h.presentations[1:]
		if h.now > presentation.sourceAt+liveFreshMaxAge {
			continue
		}
		h.result.presented = append(h.result.presented, impairmentReceipt{
			sequence: presentation.sequence, sourceAt: presentation.sourceAt, at: h.now,
		})
	}
}

func (h *impairmentHarness) receive() {
	delivery := *h.outstanding
	outcome := h.viewer.acceptStreamFeedbackOutcome(
		streamFeedbackV2Fixture(7, 1, delivery.sequence, 0, 0, 0, true),
		h.base.Add(h.now),
	)
	if !outcome.receiptReleased {
		h.t.Fatalf("receipt for sequence %d did not release its production credit", delivery.sequence)
	}
	receipt := impairmentReceipt{
		sequence: delivery.sequence, sourceAt: delivery.sourceAt, at: h.now,
	}
	h.result.received = append(h.result.received, receipt)
	h.outstanding = nil
}

func (h *impairmentHarness) timeout(reason string) {
	if reason == "receipt_timeout" {
		h.viewer.videoMu.Lock()
		h.viewer.videoReceiptDeadlineAt = time.Now().Add(-time.Millisecond)
		h.viewer.videoMu.Unlock()
		h.viewer.closeExpiredVideoReceipt()
	} else {
		h.viewer.videoMu.Lock()
		h.viewer.clearVideoFrameInFlightLocked(&h.outstanding.frame)
		h.viewer.writerClosed = true
		h.viewer.writerCloseReason = "write_timeout"
		h.viewer.videoQueue = nil
		h.viewer.videoQueueBytes = 0
		h.viewer.clearVideoReceiptLocked()
		h.viewer.videoMu.Unlock()
	}
	h.viewer.videoMu.Lock()
	closed := h.viewer.writerClosed
	gotReason := h.viewer.writerCloseReason
	queue := len(h.viewer.videoQueue)
	awaiting := h.viewer.videoReceiptAwaiting
	h.viewer.videoMu.Unlock()
	if !closed || gotReason != reason || queue != 0 || awaiting {
		h.t.Fatalf("deadline did not close and clear only the expired viewer: closed=%t reason=%q want=%q queue=%d awaiting=%t", closed, gotReason, reason, queue, awaiting)
	}
	h.result.timeouts++
	if reason == "receipt_timeout" {
		h.result.receiptTimeouts++
	} else {
		h.result.writeTimeouts++
	}
	h.viewer = nil
	h.outstanding = nil
	h.reconnectAt = h.now + impairmentReconnectDelay
}

func (h *impairmentHarness) assertBounded() {
	if h.viewer == nil {
		return
	}
	h.viewer.videoMu.Lock()
	inFlight := h.viewer.videoInFlight
	awaiting := h.viewer.videoReceiptAwaiting
	queue := len(h.viewer.videoQueue)
	queueBytes := h.viewer.videoQueueBytes
	pendingSequence := uint64(0)
	if queue == 1 {
		pendingSequence = h.viewer.videoQueue[0].meta.sequence
		if queueBytes != len(h.viewer.videoQueue[0].data) {
			h.viewer.videoMu.Unlock()
			h.t.Fatalf("pending byte accounting drifted: got=%d want=%d", queueBytes, len(h.viewer.videoQueue[0].data))
		}
	}
	h.viewer.videoMu.Unlock()
	if inFlight && awaiting {
		h.t.Fatal("one viewer owned both write-in-flight and receipt debt")
	}
	if queue > 1 {
		h.t.Fatalf("viewer retained history instead of one newest picture: %d queued", queue)
	}
	if queue == 0 && queueBytes != 0 {
		h.t.Fatalf("empty queue retained %d bytes", queueBytes)
	}
	if queue == 1 && pendingSequence != h.latestOffered {
		h.t.Fatalf("pending sequence=%d, want newest offered=%d", pendingSequence, h.latestOffered)
	}
	slots := queue
	if inFlight || awaiting {
		slots++
	}
	if slots > 2 {
		h.t.Fatalf("viewer retained %d logical media slots, want at most two", slots)
	}
	if slots > h.result.maxSlots {
		h.result.maxSlots = slots
	}
	if queue > h.result.maxPending {
		h.result.maxPending = queue
	}
}

func (h *impairmentHarness) run(captureThrough, settleThrough time.Duration) impairmentResult {
	nextCapture := time.Duration(0)
	for {
		next := time.Duration(math.MaxInt64)
		if nextCapture <= captureThrough && nextCapture < next {
			next = nextCapture
		}
		if len(h.presentations) > 0 && h.presentations[0].at < next {
			next = h.presentations[0].at
		}
		if h.outstanding != nil {
			due := h.outstanding.sourceExpiresAt
			if h.outstanding.written {
				due = h.outstanding.receiptDeadlineAt
				if !h.outstanding.browserArrived && h.outstanding.browserArrivalAt < due {
					due = h.outstanding.browserArrivalAt
				}
				if h.outstanding.receiptAt < due {
					due = h.outstanding.receiptAt
				}
			} else if h.outstanding.writeAt <= due {
				due = h.outstanding.writeAt
			}
			if due < next {
				next = due
			}
		}
		if h.viewer == nil && h.reconnectAt > 0 && h.reconnectAt < next {
			next = h.reconnectAt
		}
		if next == time.Duration(math.MaxInt64) || next > settleThrough {
			break
		}
		h.now = next

		if h.outstanding != nil {
			if !h.outstanding.written {
				switch {
				case h.outstanding.writeAt <= h.now && h.outstanding.writeAt <= h.outstanding.sourceExpiresAt:
					h.completeWrite()
				case h.outstanding.sourceExpiresAt <= h.now:
					h.timeout("write_timeout")
				}
			}
			if h.outstanding != nil && h.outstanding.written {
				// Production closes at the liveness deadline, so an ACK at the
				// exact boundary loses rather than depending on goroutine order.
				if h.outstanding.receiptDeadlineAt <= h.now {
					h.timeout("receipt_timeout")
				} else {
					if !h.outstanding.browserArrived && h.outstanding.browserArrivalAt <= h.now {
						h.arriveAtBrowser()
					}
					if h.outstanding != nil && h.outstanding.receiptAt <= h.now {
						h.receive()
					}
				}
			}
		}
		// Presentation belongs to the browser, not the server's receipt slot.
		// Process it after ACK handling so an equal or earlier ACK cannot erase a
		// complete frame that is still waiting for the browser's next paint.
		h.presentDue()
		if h.viewer == nil && h.reconnectAt > 0 && h.reconnectAt <= h.now {
			h.connect()
		}
		if nextCapture <= captureThrough && nextCapture <= h.now {
			sequence := h.result.offered + 1
			h.offer(sequence)
			nextCapture += impairmentCapturePeriod
		}
		h.trySend()
		h.assertBounded()
	}
	return h.result
}

func hasSequenceGap(deliveries []impairmentDelivery) bool {
	for i := 1; i < len(deliveries); i++ {
		if deliveries[i].sequence > deliveries[i-1].sequence+1 {
			return true
		}
	}
	return false
}

func runImpairmentGrid(t *testing.T, losses []float64) {
	t.Helper()
	bandwidths := []float64{20, 10, 8, 6, 4, 3, 2, 1, 0.8} // 0.8 Mbps is 10% of the nominal 8 Mbps source offer.
	rtts := []time.Duration{40, 100, 200, 400, 600, 1000, 1500}
	classes := map[string]int{}
	for _, bandwidth := range bandwidths {
		for _, rttMillis := range rtts {
			for _, loss := range losses {
				path := impairmentPath{bandwidthMbps: bandwidth, rtt: rttMillis * time.Millisecond, lossPercent: loss}
				class := path.feasibility()
				classes[class]++
				name := fmt.Sprintf("bw_%gMbps/rtt_%dms/loss_%g_pct/%s", bandwidth, rttMillis, loss, class)
				t.Run(name, func(t *testing.T) {
					harness := newImpairmentHarness(t, func(time.Duration) impairmentPath { return path })
					result := harness.run(6*time.Second, 8*time.Second)
					if result.maxSlots > 2 || result.maxPending > 1 {
						t.Fatalf("unbounded scheduler state: slots=%d pending=%d", result.maxSlots, result.maxPending)
					}
					for _, receipt := range result.presented {
						if age := receipt.at - receipt.sourceAt; age > liveFreshMaxAge {
							t.Fatalf("presented expired sequence %d at age %s", receipt.sequence, age)
						}
					}
					if class == "deadline_feasible" && len(result.presented) == 0 {
						t.Fatal("deadline-feasible case presented no useful picture")
					}
					switch class {
					case "serialization_infeasible":
						if len(result.presented) != 0 {
							t.Fatalf("serialization-infeasible case presented pictures: %v", result.presented)
						}
						if result.writeTimeouts == 0 {
							t.Fatal("serialization-infeasible case never expired its bounded write")
						}
					case "presentation_infeasible":
						if len(result.presented) != 0 {
							t.Fatalf("presentation-infeasible case presented stale pictures: %v", result.presented)
						}
					case "receipt_infeasible":
						if result.receiptTimeouts == 0 {
							t.Fatal("receipt-infeasible case never expired its bounded receipt debt")
						}
					}
					if class != "deadline_feasible" {
						if !hasSequenceGap(result.sent) {
							t.Fatalf("infeasible case retained cadence history instead of shedding: sent=%v", result.sent)
						}
					}
				})
			}
		}
	}
	t.Logf("deterministic impairment cases=%d: deadline-feasible=%d presentation-infeasible=%d receipt-infeasible=%d serialization-infeasible=%d",
		len(bandwidths)*len(rtts)*len(losses), classes["deadline_feasible"], classes["presentation_infeasible"],
		classes["receipt_infeasible"], classes["serialization_infeasible"])
}

func TestVideoWriterDeterministicImpairmentPrimaryGrid(t *testing.T) {
	runImpairmentGrid(t, []float64{0, 0.1, 0.5, 1, 3})
}

func TestVideoWriterDeterministicImpairmentFivePercentLossStress(t *testing.T) {
	runImpairmentGrid(t, []float64{5})
}

func firstPresentationAtOrAfter(presentations []impairmentReceipt, boundary time.Duration) (impairmentReceipt, bool) {
	for _, presentation := range presentations {
		if presentation.at >= boundary {
			return presentation, true
		}
	}
	return impairmentReceipt{}, false
}

func TestVideoWriterDeterministicBandwidthStepShedsAndRecovers(t *testing.T) {
	fast := impairmentPath{bandwidthMbps: 20, rtt: 40 * time.Millisecond}
	harness := newImpairmentHarness(t, func(at time.Duration) impairmentPath {
		switch {
		case at < 3*time.Second:
			return fast
		case at < 6*time.Second:
			return impairmentPath{bandwidthMbps: 6, rtt: 200 * time.Millisecond}
		case at < 9*time.Second:
			return impairmentPath{bandwidthMbps: 2, rtt: 400 * time.Millisecond}
		default:
			return fast
		}
	})
	result := harness.run(13*time.Second, 15*time.Second)
	if result.timeouts == 0 || !hasSequenceGap(result.sent) {
		t.Fatalf("bandwidth collapse did not shed obsolete cadence: timeouts=%d sent=%v", result.timeouts, result.sent)
	}
	recovered, ok := firstPresentationAtOrAfter(result.presented, 9*time.Second)
	if !ok {
		t.Fatal("no useful picture was presented after bandwidth recovery")
	}
	fastDelay, _ := fast.browserArrivalDelay()
	fastDelay += impairmentPresentationDelay
	if recovered.at > 9*time.Second+2*impairmentCapturePeriod+fastDelay {
		t.Fatalf("first recovered sequence %d was presented too late at %s", recovered.sequence, recovered.at)
	}
}

func TestVideoWriterDeterministicBlackholeExpiresAndRecovers(t *testing.T) {
	fast := impairmentPath{bandwidthMbps: 20, rtt: 40 * time.Millisecond}
	harness := newImpairmentHarness(t, func(at time.Duration) impairmentPath {
		if at >= 2*time.Second && at < 7*time.Second {
			return impairmentPath{blackhole: true}
		}
		return fast
	})
	result := harness.run(11*time.Second, 13*time.Second)
	if result.timeouts == 0 || !hasSequenceGap(result.sent) {
		t.Fatalf("blackhole retained history instead of expiring it: timeouts=%d sent=%v", result.timeouts, result.sent)
	}
	for _, presentation := range result.presented {
		if presentation.at >= 2*time.Second && presentation.at < 7*time.Second {
			t.Fatalf("blackholed sequence %d was presented at %s", presentation.sequence, presentation.at)
		}
	}
	if recovered, ok := firstPresentationAtOrAfter(result.presented, 7*time.Second); !ok || recovered.at > 10*time.Second {
		t.Fatalf("blackhole recovery missed two capture opportunities: presentation=%#v ok=%t", recovered, ok)
	}
}

func TestVideoWriterDeterministicAckBlackholeExpiresReceiptAfterSuccessfulWrite(t *testing.T) {
	path := impairmentPath{bandwidthMbps: 20, rtt: 40 * time.Millisecond, ackBlackhole: true}
	harness := newImpairmentHarness(t, func(time.Duration) impairmentPath { return path })
	result := harness.run(2*time.Second, 5*time.Second)
	if result.writeTimeouts != 0 || result.receiptTimeouts == 0 {
		t.Fatalf("ACK blackhole classification drifted: write timeouts=%d receipt timeouts=%d", result.writeTimeouts, result.receiptTimeouts)
	}
	if len(result.presented) == 0 {
		t.Fatal("ACK blackhole hid the distinction between browser presentation and the missing return ACK")
	}
}

func TestVideoWriterDeterministicAckMayPrecedePresentation(t *testing.T) {
	path := impairmentPath{bandwidthMbps: 20, rtt: 10 * time.Millisecond}
	harness := newImpairmentHarness(t, func(time.Duration) impairmentPath { return path })
	result := harness.run(2*time.Second, 3*time.Second)
	if len(result.received) == 0 || len(result.presented) != len(result.received) {
		t.Fatalf("early ACK erased a later browser paint: received=%v presented=%v", result.received, result.presented)
	}
	for index := range result.received {
		if result.received[index].sequence != result.presented[index].sequence ||
			result.received[index].at >= result.presented[index].at {
			t.Fatalf("sequence %d did not ACK before its independent presentation: receipt=%#v presentation=%#v",
				result.received[index].sequence, result.received[index], result.presented[index])
		}
	}
}

func TestVideoWriterDeterministicFastViewerUnaffectedBySlowViewer(t *testing.T) {
	fast := newImpairmentViewer(t)
	slow := newImpairmentViewer(t)
	base := time.Unix(1_800_000_000, 0)
	fastPath := impairmentPath{bandwidthMbps: 20, rtt: 40 * time.Millisecond}
	fastDelay, _ := fastPath.receiptDelay()
	var fastSequences []uint64

	for sequence := uint64(1); sequence <= 6; sequence++ {
		if sequence > 1 {
			previous := sequence - 1
			outcome := fast.acceptStreamFeedbackOutcome(
				streamFeedbackV2Fixture(7, 1, previous, previous, previous, previous, true),
				base.Add(time.Duration(previous-1)*time.Second+fastDelay),
			)
			if !outcome.receiptReleased {
				t.Fatalf("fast viewer receipt %d did not release credit", previous)
			}
		}
		frame := testTSF2FrameWithTimestamp(7, sequence, true, sequence)
		fast.enqueueVideoFrame(frame)
		slow.enqueueVideoFrame(frame)

		item, ok := fast.nextVideoWriteItem()
		if !ok || item.frame == nil || item.frame.meta.sequence != sequence {
			t.Fatalf("slow viewer interfered with fast sequence %d: %#v", sequence, item)
		}
		fast.noteVideoFrameWrittenAt(*item.frame, time.Now())
		fastSequences = append(fastSequences, sequence)
		if sequence == 1 {
			slowItem, slowOK := slow.nextVideoWriteItem()
			if !slowOK || slowItem.frame == nil || slowItem.frame.meta.sequence != 1 {
				t.Fatalf("slow viewer did not establish independent receipt debt: %#v", slowItem)
			}
			slow.noteVideoFrameWrittenAt(*slowItem.frame, time.Now())
		}
	}

	if len(fastSequences) != 6 {
		t.Fatalf("fast viewer lost cadence: %v", fastSequences)
	}
	slow.videoMu.Lock()
	slowAwaiting := slow.videoReceiptAwaiting
	slowPending := len(slow.videoQueue)
	slowPendingSequence := uint64(0)
	if slowPending == 1 {
		slowPendingSequence = slow.videoQueue[0].meta.sequence
	}
	slow.videoReceiptDeadlineAt = time.Now().Add(-time.Millisecond)
	slow.videoMu.Unlock()
	if !slowAwaiting || slowPending != 1 || slowPendingSequence != 6 {
		t.Fatalf("slow viewer retained history: awaiting=%t pending=%d sequence=%d", slowAwaiting, slowPending, slowPendingSequence)
	}
	slow.closeExpiredVideoReceipt()
	fast.videoMu.Lock()
	fastClosed := fast.writerClosed
	fastAwaiting := fast.videoReceiptAwaiting
	fast.videoMu.Unlock()
	if fastClosed || !fastAwaiting {
		t.Fatalf("slow viewer expiry changed fast viewer state: closed=%t awaiting=%t", fastClosed, fastAwaiting)
	}
}
