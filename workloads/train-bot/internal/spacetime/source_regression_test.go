package spacetime

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestScheduleFinalizePathsRefreshPublicProjections(t *testing.T) {
	t.Parallel()

	source := readSpacetimeSource(t)
	cases := []struct {
		name   string
		anchor string
		want   string
	}{
		{
			name:   "commitServiceDayImport",
			anchor: "export const commitServiceDayImport",
			want:   "refreshAllPublicProjections(tx, header.serviceDate);",
		},
		{
			name:   "serviceReplaceScheduleBatch",
			anchor: "export const serviceReplaceScheduleBatch",
			want:   "refreshAllPublicProjections(ctx, cleanDate);",
		},
	}

	for _, tc := range cases {
		start := strings.Index(source, tc.anchor)
		if start < 0 {
			t.Fatalf("%s anchor not found", tc.name)
		}
		snippet := source[start:min(start+6000, len(source))]
		if !strings.Contains(snippet, tc.want) {
			t.Fatalf("%s snippet missing %q", tc.name, tc.want)
		}
	}
}

func TestScheduleContextAvoidsUTCServiceDayHeuristics(t *testing.T) {
	t.Parallel()

	source := readSpacetimeSource(t)
	for _, anchor := range []string{
		"function scheduleContextPayload",
		"function runtimeStatePayload",
	} {
		start := strings.Index(source, anchor)
		if start < 0 {
			t.Fatalf("%s anchor not found", anchor)
		}
		snippet := source[start:min(start+2500, len(source))]
		for _, forbidden := range []string{
			"getUTCHours()",
			"toISOString().slice(0, 10)",
		} {
			if strings.Contains(snippet, forbidden) {
				t.Fatalf("%s snippet should not contain %q", anchor, forbidden)
			}
		}
	}
}

func TestRuntimeStateUsesSharedRigaCutoffHelpers(t *testing.T) {
	t.Parallel()

	source := readSpacetimeSource(t)
	cases := []struct {
		anchor string
		want   []string
	}{
		{
			anchor: "function scheduleContextPayload",
			want: []string{
				"const requestedServiceDate = formatServiceDateFor(now);",
				"const fallbackServiceDate = formatServiceDateFor(new Date(now.getTime() - 24 * 60 * 60 * 1000));",
				"const cutoffHour = scheduleCutoffHour(tx);",
				"const beforeCutoff = isBeforeScheduleCutoff(now, cutoffHour);",
			},
		},
		{
			anchor: "function runtimeStatePayload",
			want: []string{
				"const requestedServiceDate = formatServiceDateFor(now);",
				"const fallbackServiceDate = formatServiceDateFor(new Date(now.getTime() - 24 * 60 * 60 * 1000));",
				"const cutoffHour = scheduleCutoffHour(tx);",
				"const beforeCutoff = isBeforeScheduleCutoff(now, cutoffHour);",
			},
		},
	}

	for _, tc := range cases {
		start := strings.Index(source, tc.anchor)
		if start < 0 {
			t.Fatalf("%s anchor not found", tc.anchor)
		}
		snippet := source[start:min(start+2500, len(source))]
		for _, want := range tc.want {
			if !strings.Contains(snippet, want) {
				t.Fatalf("%s snippet missing %q", tc.anchor, want)
			}
		}
	}
}

func TestLiveCheckInReducersShareTheSameValidationPath(t *testing.T) {
	t.Parallel()

	source := readSpacetimeSource(t)
	validateStart := strings.Index(source, "function validateCheckIn")
	if validateStart < 0 {
		t.Fatalf("validateCheckIn anchor not found")
	}
	validateSnippet := source[validateStart:min(validateStart+1500, len(source))]
	if !strings.Contains(validateSnippet, "const train = requireCheckInTrain(tx, trainId);") {
		t.Fatalf("validateCheckIn should require an existing train before allowing a ride")
	}

	for _, anchor := range []string{
		"export const checkIn = spacetimedb.reducer",
		"export const checkInMap = spacetimedb.reducer",
	} {
		start := strings.Index(source, anchor)
		if start < 0 {
			t.Fatalf("%s anchor not found", anchor)
		}
		snippet := source[start:min(start+1200, len(source))]
		if !strings.Contains(snippet, "validateCheckIn(ctx, trainId, trimOptional(boardingStationId));") {
			t.Fatalf("%s should reuse validateCheckIn", anchor)
		}
	}
}

func readSpacetimeSource(t *testing.T) string {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("resolve caller path")
	}
	sourcePath := filepath.Join(filepath.Dir(filename), "..", "..", "spacetimedb", "src", "index.ts")
	body, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	return string(body)
}

func min(left int, right int) int {
	if left < right {
		return left
	}
	return right
}
