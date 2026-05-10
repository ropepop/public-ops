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

func TestViewerProceduresRequireRealTrainUserSession(t *testing.T) {
	t.Parallel()

	source := readSpacetimeSource(t)
	start := strings.Index(source, "function requireUserSession")
	if start < 0 {
		t.Fatalf("requireUserSession anchor not found")
	}
	snippet := source[start:min(start+1400, len(source))]
	for _, want := range []string{
		"roles.includes('train_user')",
		"stableId.startsWith('telegram:')",
	} {
		if !strings.Contains(snippet, want) {
			t.Fatalf("requireUserSession should require a real Telegram-backed train_user session; missing %q in:\n%s", want, snippet)
		}
	}

	for _, tc := range []struct {
		name   string
		anchor string
	}{
		{name: "bindSession", anchor: "export const bindSession = spacetimedb.reducer"},
		{name: "bootstrapMe", anchor: "export const bootstrapMe = spacetimedb.procedure"},
		{name: "getCurrentRide", anchor: "export const getCurrentRide = spacetimedb.procedure"},
		{name: "getUserSettings", anchor: "export const getUserSettings = spacetimedb.procedure"},
		{name: "listFavoriteRoutes", anchor: "export const listFavoriteRoutes = spacetimedb.procedure"},
	} {
		start := strings.Index(source, tc.anchor)
		if start < 0 {
			t.Fatalf("%s anchor not found", tc.name)
		}
		snippet := source[start:min(start+700, len(source))]
		if !strings.Contains(snippet, "requireUserSession") &&
			!strings.Contains(snippet, "loadViewer") &&
			!strings.Contains(snippet, "ensureRider") &&
			!strings.Contains(snippet, "buildBootstrapPayload") &&
			!strings.Contains(snippet, "favoriteListPayload") {
			t.Fatalf("%s should flow through the real viewer session guard, snippet:\n%s", tc.name, snippet)
		}
	}
}

func TestServiceProceduresRequireServiceRoleBeforeReads(t *testing.T) {
	t.Parallel()

	source := readSpacetimeSource(t)
	for _, tc := range []struct {
		name   string
		anchor string
		read   string
	}{
		{
			name:   "serviceGetSchedule",
			anchor: "export const serviceGetSchedule = spacetimedb.procedure",
			read:   "serviceGetSchedulePayload(",
		},
		{
			name:   "serviceListActivities",
			anchor: "export const serviceListActivities = spacetimedb.procedure",
			read:   "listActivitiesFiltered(",
		},
	} {
		start := strings.Index(source, tc.anchor)
		if start < 0 {
			t.Fatalf("%s anchor not found", tc.name)
		}
		snippet := source[start:min(start+900, len(source))]
		guardIndex := strings.Index(snippet, "requireServiceRole(tx);")
		readIndex := strings.Index(snippet, tc.read)
		if guardIndex < 0 {
			t.Fatalf("%s missing requireServiceRole guard in:\n%s", tc.name, snippet)
		}
		if readIndex < 0 {
			t.Fatalf("%s read anchor %q not found in:\n%s", tc.name, tc.read, snippet)
		}
		if guardIndex > readIndex {
			t.Fatalf("%s should require service role before reading data, snippet:\n%s", tc.name, snippet)
		}
	}
}

func TestPublicIncidentShapesUseOpaquePublicIDs(t *testing.T) {
	t.Parallel()

	source := readSpacetimeSource(t)
	start := strings.Index(source, "function incidentDetailPayload")
	if start < 0 {
		t.Fatalf("incidentDetailPayload anchor not found")
	}
	detailSnippet := source[start:min(start+1800, len(source))]
	for _, forbidden := range []string{
		"id: item.id",
		"id: `${activity.id}|vote|${item.stableId}`",
		"comments: (activity.comments || []).slice()",
	} {
		if strings.Contains(detailSnippet, forbidden) {
			t.Fatalf("public incident detail should not expose identity-derived IDs; found %q in:\n%s", forbidden, detailSnippet)
		}
	}
	if !strings.Contains(detailSnippet, "publicOpaqueId(") {
		t.Fatalf("public incident detail should use opaque public IDs, snippet:\n%s", detailSnippet)
	}

	start = strings.Index(source, "function refreshActivityProjection")
	if start < 0 {
		t.Fatalf("refreshActivityProjection anchor not found")
	}
	end := strings.Index(source[start:], "\nfunction refreshAllPublicProjections")
	if end < 0 {
		end = 5200
	}
	projectionSnippet := source[start:min(start+end, len(source))]
	for _, forbidden := range []string{
		"id: event.id",
		"id: `${incidentId}|vote|${vote.stableId}`",
		"id: comment.id",
	} {
		if strings.Contains(projectionSnippet, forbidden) {
			t.Fatalf("public incident projections should not expose identity-derived IDs; found %q in:\n%s", forbidden, projectionSnippet)
		}
	}
	if strings.Count(projectionSnippet, "publicOpaqueId(") < 3 {
		t.Fatalf("public incident projections should use opaque IDs for events, sightings, comments, and votes, snippet:\n%s", projectionSnippet)
	}
}

func TestCleanupExpiredStateRemovesEmptyAnonymousViewerRows(t *testing.T) {
	t.Parallel()

	source := readSpacetimeSource(t)
	start := strings.Index(source, "export const cleanupExpiredState = spacetimedb.reducer")
	if start < 0 {
		t.Fatalf("cleanupExpiredState anchor not found")
	}
	snippet := source[start:min(start+1700, len(source))]
	for _, want := range []string{
		"cleanupEmptyAnonymousViewerState(ctx)",
		"anonymousViewersDeleted",
	} {
		if !strings.Contains(snippet, want) {
			t.Fatalf("cleanupExpiredState should clean empty anonymous viewer rows; missing %q in:\n%s", want, snippet)
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
