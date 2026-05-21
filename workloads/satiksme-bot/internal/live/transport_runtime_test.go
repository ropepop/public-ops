package live

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"satiksmebot/internal/model"
)

type transportRuntimeTestCatalog struct {
	catalog *model.Catalog
}

func (c transportRuntimeTestCatalog) Current() *model.Catalog {
	return c.catalog
}

type zeroViewerTransportStateStore struct{}

func (zeroViewerTransportStateStore) CountActiveLiveViewers(context.Context, time.Time) (int, error) {
	return 0, nil
}

func (zeroViewerTransportStateStore) UpsertLiveTransportState(context.Context, model.LiveTransportState) error {
	return nil
}

func (zeroViewerTransportStateStore) CleanupLiveViewers(context.Context, time.Time) error {
	return nil
}

func TestRunTransportSnapshotLoopRefreshesFileSnapshotsWithoutActiveViewerHeartbeats(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	requests := make(chan int32, 4)
	client := &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			next := calls.Add(1)
			select {
			case requests <- next:
			default:
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(
					fmt.Sprintf("2,10,24121150,56948109,,270,I,%d,a-b-b2,1402,22,\n", 67133+next),
				)),
				Header: make(http.Header),
			}, nil
		}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- RunTransportSnapshotLoop(ctx, TransportRuntimeSettings{
			SourceURL:         "https://www.saraksti.lv/gpsdata.ashx?gps",
			HTTPClient:        client,
			Catalog:           transportRuntimeTestCatalog{catalog: &model.Catalog{}},
			StateStore:        zeroViewerTransportStateStore{},
			Publisher:         NewSnapshotPublisher(t.TempDir(), 2),
			ViewerWindow:      30 * time.Second,
			ViewerGracePeriod: 10 * time.Millisecond,
			PollInterval:      5 * time.Millisecond,
			MaxPollInterval:   5 * time.Millisecond,
			IdleCheckInterval: 5 * time.Millisecond,
		})
	}()

	deadline := time.After(250 * time.Millisecond)
	seen := int32(0)
	for seen < 2 {
		select {
		case seen = <-requests:
		case <-deadline:
			cancel()
			<-done
			t.Fatalf("snapshot loop fetched live feed %d time(s), want at least 2 without active viewer heartbeats", calls.Load())
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("RunTransportSnapshotLoop() error = %v", err)
	}
}
