package bot

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"satiksmebot/internal/model"
	"satiksmebot/internal/runtime"
	"satiksmebot/internal/store"
	"satiksmebot/internal/telegram"
)

type DumpDispatcher struct {
	client       MessageClient
	chatID       string
	interval     time.Duration
	loc          *time.Location
	store        store.Store
	runtimeState *runtime.State

	pendingMu     sync.Mutex
	pendingCached int
	pendingLoaded bool
	wakeCh        chan struct{}
}

func NewDumpDispatcher(client MessageClient, st store.Store, runtimeState *runtime.State, chatID string, interval time.Duration, loc *time.Location) *DumpDispatcher {
	return &DumpDispatcher{
		client:       client,
		chatID:       strings.TrimSpace(chatID),
		interval:     interval,
		loc:          loc,
		store:        st,
		runtimeState: runtimeState,
		wakeCh:       make(chan struct{}, 1),
	}
}

func (d *DumpDispatcher) Run(ctx context.Context) error {
	if d == nil {
		<-ctx.Done()
		return nil
	}
	interval := d.interval
	if interval <= 0 {
		interval = time.Second
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	d.refreshPending(ctx)
	wakeCh := d.wakeChannel()
	delay := time.Duration(0)
	for {
		if err := waitForDumpWake(ctx, timer, wakeCh, delay); err != nil {
			return nil
		}
		if d.store == nil {
			delay = noDumpWakeDelay
			continue
		}
		now := time.Now().UTC()
		item, err := d.store.NextReportDump(ctx, now)
		if err != nil {
			log.Printf("report dump load failed: %v", err)
			if d.runtimeState != nil {
				d.runtimeState.RecordDumpError(now, err.Error(), 0)
			}
			delay = interval
			continue
		}
		if item == nil {
			pending := d.refreshPendingCount(ctx)
			if d.runtimeState != nil {
				d.runtimeState.RecordDumpSuccess(now, pending)
			}
			delay, err = d.nextWakeDelay(ctx)
			if err != nil {
				log.Printf("report dump peek failed: %v", err)
				if d.runtimeState != nil {
					d.runtimeState.RecordDumpError(now, err.Error(), pending)
				}
				delay = interval
			}
			continue
		}
		if d.runtimeState != nil {
			d.runtimeState.RecordDumpAttempt(now)
		}
		if d.client == nil || d.chatID == "" {
			if d.runtimeState != nil {
				d.runtimeState.SetDumpPending(d.pendingCount(ctx))
			}
			delay = noDumpWakeDelay
			continue
		}
		if err := d.client.SendMessage(ctx, d.chatID, item.Payload, telegram.MessageOptions{}); err != nil {
			log.Printf("report dump send failed: %v", err)
			nextAttempt := now.Add(reportDumpBackoff(item.Attempts + 1))
			if updateErr := d.store.UpdateReportDumpFailure(ctx, item.ID, item.Attempts+1, nextAttempt, now, err.Error()); updateErr != nil {
				log.Printf("report dump failure update failed: %v", updateErr)
			}
			if d.runtimeState != nil {
				d.runtimeState.RecordDumpError(now, err.Error(), d.pendingCount(ctx))
			}
			delay, err = d.nextWakeDelay(ctx)
			if err != nil {
				log.Printf("report dump peek failed after send error: %v", err)
				delay = interval
			}
			continue
		}
		if err := d.store.DeleteReportDump(ctx, item.ID); err != nil {
			log.Printf("report dump delete failed: %v", err)
			if d.runtimeState != nil {
				d.runtimeState.RecordDumpError(now, err.Error(), d.pendingCount(ctx))
			}
			delay = interval
			continue
		}
		if d.runtimeState != nil {
			d.runtimeState.RecordDumpSuccess(now, d.adjustPending(-1))
		}
		delay, err = d.nextWakeDelay(ctx)
		if err != nil {
			log.Printf("report dump peek failed after delete: %v", err)
			delay = interval
		}
	}
}

func (d *DumpDispatcher) EnqueueStop(stop model.Stop, sighting *model.StopSighting) {
	if d == nil || sighting == nil {
		return
	}
	d.enqueue(d.formatStop(stop, *sighting), sighting.CreatedAt)
}

func (d *DumpDispatcher) EnqueueVehicle(sighting *model.VehicleSighting) {
	if d == nil || sighting == nil {
		return
	}
	d.enqueue(d.formatVehicle(*sighting), sighting.CreatedAt)
}

func (d *DumpDispatcher) enqueue(message string, createdAt time.Time) {
	if d.store == nil || strings.TrimSpace(message) == "" {
		return
	}
	now := createdAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	item := store.ReportDumpItem{
		ID:            generateQueueID(),
		Payload:       message,
		Attempts:      0,
		CreatedAt:     now,
		NextAttemptAt: now,
	}
	if err := d.store.EnqueueReportDump(context.Background(), item); err != nil {
		log.Printf("report dump enqueue failed: %v", err)
		if d.runtimeState != nil {
			d.runtimeState.RecordDumpError(time.Now().UTC(), err.Error(), d.pendingCount(context.Background()))
		}
		return
	}
	if d.runtimeState != nil {
		d.runtimeState.SetDumpPending(d.adjustPending(1))
	}
	d.signalWake()
}

func (d *DumpDispatcher) formatStop(stop model.Stop, sighting model.StopSighting) string {
	return strings.Join([]string{
		fmt.Sprintf("Kontroles novērojums | %s", d.formatTime(sighting.CreatedAt)),
		fmt.Sprintf("Pietura: %s (%s)", dumpValue(stop.Name), dumpValue(stop.ID)),
		"Tips: pietura",
	}, "\n")
}

func (d *DumpDispatcher) formatVehicle(sighting model.VehicleSighting) string {
	return strings.Join([]string{
		fmt.Sprintf("Kontroles novērojums | %s", d.formatTime(sighting.CreatedAt)),
		fmt.Sprintf("Tips: %s %s", localizedModeLabel(sighting.Mode), dumpValue(sighting.RouteLabel)),
		fmt.Sprintf("Virziens: %s", dumpValue(sighting.Direction)),
		fmt.Sprintf("Galamērķis: %s", dumpValue(sighting.Destination)),
	}, "\n")
}

func (d *DumpDispatcher) formatTime(at time.Time) string {
	if d != nil && d.loc != nil {
		at = at.In(d.loc)
	}
	return at.Format("2006-01-02 15:04:05")
}

func dumpValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "nav zināms"
	}
	return value
}

func localizedModeLabel(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "bus":
		return "autobuss"
	case "tram":
		return "tramvajs"
	case "trol":
		return "trolejbuss"
	case "minibus":
		return "mikroautobuss"
	case "seasonalbus":
		return "sezonas autobuss"
	case "suburbanbus":
		return "piepilsētas autobuss"
	default:
		return dumpValue(mode)
	}
}

func (d *DumpDispatcher) pendingCount(ctx context.Context) int {
	if d == nil || d.store == nil {
		return 0
	}
	d.pendingMu.Lock()
	if d.pendingLoaded {
		pending := d.pendingCached
		d.pendingMu.Unlock()
		return pending
	}
	d.pendingMu.Unlock()
	count, err := d.store.PendingReportDumpCount(ctx)
	if err != nil {
		return 0
	}
	d.pendingMu.Lock()
	d.pendingCached = count
	d.pendingLoaded = true
	d.pendingMu.Unlock()
	return count
}

func (d *DumpDispatcher) refreshPendingCount(ctx context.Context) int {
	if d == nil || d.store == nil {
		return 0
	}
	count, err := d.store.PendingReportDumpCount(ctx)
	if err != nil {
		d.pendingMu.Lock()
		defer d.pendingMu.Unlock()
		if d.pendingLoaded {
			return d.pendingCached
		}
		return 0
	}
	d.pendingMu.Lock()
	d.pendingCached = count
	d.pendingLoaded = true
	d.pendingMu.Unlock()
	return count
}

func (d *DumpDispatcher) refreshPending(ctx context.Context) {
	if d.runtimeState != nil {
		d.runtimeState.SetDumpPending(d.refreshPendingCount(ctx))
	}
}

func (d *DumpDispatcher) adjustPending(delta int) int {
	if d == nil {
		return 0
	}
	d.pendingMu.Lock()
	defer d.pendingMu.Unlock()
	if !d.pendingLoaded {
		d.pendingLoaded = true
	}
	d.pendingCached += delta
	if d.pendingCached < 0 {
		d.pendingCached = 0
	}
	return d.pendingCached
}

func (d *DumpDispatcher) wakeChannel() chan struct{} {
	if d == nil {
		return nil
	}
	d.pendingMu.Lock()
	defer d.pendingMu.Unlock()
	if d.wakeCh == nil {
		d.wakeCh = make(chan struct{}, 1)
	}
	return d.wakeCh
}

func (d *DumpDispatcher) signalWake() {
	if d == nil {
		return
	}
	wakeCh := d.wakeChannel()
	if wakeCh == nil {
		return
	}
	select {
	case wakeCh <- struct{}{}:
	default:
	}
}

func (d *DumpDispatcher) nextWakeDelay(ctx context.Context) (time.Duration, error) {
	if d == nil || d.store == nil {
		return noDumpWakeDelay, nil
	}
	item, err := d.store.PeekNextReportDump(ctx)
	if err != nil {
		return 0, err
	}
	if item == nil {
		return noDumpWakeDelay, nil
	}
	delay := time.Until(item.NextAttemptAt)
	if delay < 0 {
		return 0, nil
	}
	return delay, nil
}

func reportDumpBackoff(attempts int) time.Duration {
	if attempts <= 1 {
		return time.Second
	}
	backoff := time.Second << min(attempts-1, 8)
	if backoff > 5*time.Minute {
		return 5 * time.Minute
	}
	return backoff
}

const noDumpWakeDelay time.Duration = -1

func resetDumpTimer(timer *time.Timer, delay time.Duration) {
	if timer == nil {
		return
	}
	if delay <= 0 {
		delay = time.Second
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(delay)
}

func stopDumpTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func waitForDumpWake(ctx context.Context, timer *time.Timer, wakeCh <-chan struct{}, delay time.Duration) error {
	switch {
	case delay == 0:
		return nil
	case delay < 0:
		stopDumpTimer(timer)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wakeCh:
			return nil
		}
	default:
		resetDumpTimer(timer, delay)
		select {
		case <-ctx.Done():
			stopDumpTimer(timer)
			return ctx.Err()
		case <-timer.C:
			return nil
		case <-wakeCh:
			stopDumpTimer(timer)
			return nil
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func generateQueueID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("dump-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
