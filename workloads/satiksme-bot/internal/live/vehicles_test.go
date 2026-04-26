package live

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"satiksmebot/internal/model"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestFetchVehiclesForcesFreshNoCacheRequest(t *testing.T) {
	t.Parallel()

	type capturedRequest struct {
		cacheControl string
		pragma       string
		originCustom string
		requestClose bool
	}

	requests := make(chan capturedRequest, 1)
	client := &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			requests <- capturedRequest{
				cacheControl: r.Header.Get("Cache-Control"),
				pragma:       r.Header.Get("Pragma"),
				originCustom: r.Header.Get("Origin-Custom"),
				requestClose: r.Close,
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("2,10,24121150,56948109,,270,I,67133,a-b-b2,1402,22,\n")),
				Header:     make(http.Header),
			}, nil
		}),
	}

	now := time.Date(2026, 3, 11, 12, 0, 0, 0, time.UTC)
	vehicles, err := FetchVehicles(context.Background(), client, "https://www.saraksti.lv/gpsdata.ashx?gps", nil, now)
	if err != nil {
		t.Fatalf("FetchVehicles() error = %v", err)
	}
	if len(vehicles) != 1 {
		t.Fatalf("expected 1 vehicle, got %d", len(vehicles))
	}
	if !vehicles[0].UpdatedAt.Equal(now) {
		t.Fatalf("vehicle updatedAt = %s, want %s", vehicles[0].UpdatedAt, now)
	}
	if vehicles[0].LiveRowID != "67133" {
		t.Fatalf("vehicle liveRowId = %q, want 67133", vehicles[0].LiveRowID)
	}

	req := <-requests
	if req.originCustom != "saraksti.lv" {
		t.Fatalf("expected Origin-Custom header, got %q", req.originCustom)
	}
	if req.cacheControl != "no-cache" {
		t.Fatalf("expected Cache-Control=no-cache, got %q", req.cacheControl)
	}
	if req.pragma != "no-cache" {
		t.Fatalf("expected Pragma=no-cache, got %q", req.pragma)
	}
	if !req.requestClose {
		t.Fatalf("expected request Close=true")
	}
}

func TestParseVehiclesResolvesStopNameThroughStopAlias(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 11, 12, 0, 0, 0, time.UTC)
	vehicles := ParseVehicles(
		"2,15,24121150,56948109,,270,I,67133,a-b-b2,432,22,\n",
		&model.Catalog{
			Stops: []model.Stop{{ID: "0432", Name: "Slavu iela"}},
		},
		now,
	)
	if len(vehicles) != 1 {
		t.Fatalf("expected 1 vehicle, got %d", len(vehicles))
	}
	if vehicles[0].StopName != "Slavu iela" {
		t.Fatalf("vehicles[0].StopName = %q, want Slavu iela", vehicles[0].StopName)
	}
}
