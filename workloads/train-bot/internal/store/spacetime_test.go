package store

import (
	"errors"
	"testing"
	"time"
)

func TestIsSpacetimePrivateRiderTableError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "private rider table",
			err:  errors.New("spacetime sql failed: no such table: `trainbot_rider`. If the table exists, it may be marked private."),
			want: true,
		},
		{
			name: "marked private rider table",
			err:  errors.New("trainbot_rider may be marked private"),
			want: true,
		},
		{
			name: "other table",
			err:  errors.New("spacetime sql failed: no such table: `trainbot_activity`"),
			want: false,
		},
		{
			name: "nil",
			err:  nil,
			want: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isSpacetimePrivateRiderTableError(tc.err); got != tc.want {
				t.Fatalf("isSpacetimePrivateRiderTableError() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLocationReportSignalRoundTrip(t *testing.T) {
	t.Parallel()

	latitude := 56.95721
	longitude := 23.68939
	encoded := encodeLocationReportSignal(&latitude, &longitude, 250)
	if encoded != "LOC:56.95721,23.68939,250" {
		t.Fatalf("encodeLocationReportSignal() = %q", encoded)
	}

	decoded, ok := decodeLocationReportSignal(encoded)
	if !ok {
		t.Fatalf("decodeLocationReportSignal() ok = false")
	}
	if decoded.Latitude == nil || *decoded.Latitude != latitude {
		t.Fatalf("decoded latitude = %v, want %v", decoded.Latitude, latitude)
	}
	if decoded.Longitude == nil || *decoded.Longitude != longitude {
		t.Fatalf("decoded longitude = %v, want %v", decoded.Longitude, longitude)
	}
	if decoded.RadiusMeters != 250 {
		t.Fatalf("decoded radius = %d, want 250", decoded.RadiusMeters)
	}
}

func TestLocationReportSignalIgnoresOtherSignals(t *testing.T) {
	t.Parallel()

	if _, ok := decodeLocationReportSignal("INSPECTION_STARTED"); ok {
		t.Fatalf("decodeLocationReportSignal() ok = true for report signal")
	}
}

func TestMapSpacetimeMutationErrorTreatsStationDuplicateAsDedupe(t *testing.T) {
	t.Parallel()

	wantRemaining := 90 * time.Second
	err := mapSpacetimeMutationError(errors.New("reducer failed: duplicate station sighting ignored"), wantRemaining, time.Hour)
	var rejected *MutationRejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("mapSpacetimeMutationError() = %v, want MutationRejectedError", err)
	}
	if rejected.Reason != MutationReportDuplicate {
		t.Fatalf("reason = %q, want %q", rejected.Reason, MutationReportDuplicate)
	}
	if rejected.Remaining != wantRemaining {
		t.Fatalf("remaining = %v, want %v", rejected.Remaining, wantRemaining)
	}
}
