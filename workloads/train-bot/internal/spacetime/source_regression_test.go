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

func TestPublicAreaIncidentsAreCoarsenedInSpacetimeSource(t *testing.T) {
	t.Parallel()

	source := readSpacetimeSource(t)
	for _, required := range []string{
		"function publicIncidentSubjectId",
		"function publicIncidentSubjectName",
		"function publicIncidentLocationPayload",
		"function publicIncidentEventDetail",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("Spacetime source missing %s", required)
		}
	}
	summarySnippet := sourceSnippet(t, source, "function incidentSummaryPayload", 1600)
	for _, required := range []string{
		"subjectId: publicIncidentSubjectId(activity)",
		"subjectName: publicIncidentSubjectName(activity)",
		"location: publicIncidentLocationPayload(activity)",
	} {
		if !strings.Contains(summarySnippet, required) {
			t.Fatalf("public incident summary missing %q in:\n%s", required, summarySnippet)
		}
	}
	locationSnippet := sourceSnippet(t, source, "function publicIncidentLocationPayload", 1200)
	for _, required := range []string{
		"Math.round(location.latitude * 1000) / 1000",
		"Math.max(250, Number(location.radiusMeters) || 0)",
		"description: ''",
	} {
		if !strings.Contains(locationSnippet, required) {
			t.Fatalf("public area location redaction missing %q in:\n%s", required, locationSnippet)
		}
	}
	projectionSnippet := sourceSnippet(t, source, "function refreshActivityProjection", 2600)
	for _, required := range []string{
		"subjectId: summary.subjectId",
		"subjectName: summary.subjectName",
		"detail: publicIncidentEventDetail(activity, event)",
	} {
		if !strings.Contains(projectionSnippet, required) {
			t.Fatalf("public projection missing %q in:\n%s", required, projectionSnippet)
		}
	}
}

func TestPublicStationSightingsUseOpaquePublicIDs(t *testing.T) {
	t.Parallel()

	source := readSpacetimeSource(t)
	start := strings.Index(source, "function stationSightingsSince")
	if start < 0 {
		t.Fatalf("stationSightingsSince anchor not found")
	}
	end := strings.Index(source[start:], "\nfunction recentStationSightingsByStation")
	if end < 0 {
		t.Fatalf("recentStationSightingsByStation anchor not found")
	}
	snippet := source[start : start+end]
	if strings.Contains(snippet, "id: event.id") {
		t.Fatalf("stationSightingsSince must not expose the raw station sighting event ID:\n%s", snippet)
	}
	if !strings.Contains(snippet, "id: publicStationSightingID(event)") {
		t.Fatalf("stationSightingsSince must use opaque station sighting IDs:\n%s", snippet)
	}

	start = strings.Index(source, "const sightings = projectedSightings.length || !serviceDate")
	if start < 0 {
		t.Fatalf("publicSearch fallback sightings anchor not found")
	}
	end = strings.Index(source[start:], "\n    for (const sighting of sightings)")
	if end < 0 {
		t.Fatalf("publicSearch fallback sightings end anchor not found")
	}
	snippet = source[start : start+end]
	if strings.Contains(snippet, "id: asString(item.id).trim()") {
		t.Fatalf("publicSearch fallback sightings must not expose raw station sighting event IDs:\n%s", snippet)
	}
	if !strings.Contains(snippet, "id: publicStationSightingID(item)") {
		t.Fatalf("publicSearch fallback sightings must use opaque station sighting IDs:\n%s", snippet)
	}
}

func TestPublicRiderCountsUseBuckets(t *testing.T) {
	t.Parallel()

	source := readSpacetimeSource(t)
	if !strings.Contains(source, "function publicRiderCount(raw: number): number") {
		t.Fatalf("Spacetime source missing public rider count helper")
	}
	for _, tc := range []struct {
		name   string
		anchor string
		want   []string
	}{
		{
			name:   "buildTrainCard",
			anchor: "function buildTrainCard",
			want: []string{
				"const riders = activeRidersForTrain(tx, train.id);",
				"riders: stableId ? riders : publicRiderCount(riders),",
			},
		},
		{
			name:   "buildPublicTrainView",
			anchor: "function buildPublicTrainView",
			want: []string{
				"riders: publicRiderCount(activeRidersForTrain(tx, trainId)),",
			},
		},
		{
			name:   "publicDashboardPayload",
			anchor: "function publicDashboardPayload",
			want: []string{
				"riders: publicRiderCount(activeRidersForTrain(tx, train.id)),",
				"riders: publicRiderCount(Number(train.riders) || 0),",
			},
		},
		{
			name:   "publicServiceDayPayload",
			anchor: "function publicServiceDayPayload",
			want: []string{
				"riders: publicRiderCount(activeRidersForTrain(tx, train.id)),",
			},
		},
		{
			name:   "refreshTripProjection",
			anchor: "function refreshTripProjection",
			want: []string{
				"const publicRiders = publicRiderCount(activeRidersForTrain(tx, trainId));",
				"riders: publicRiders,",
			},
		},
		{
			name:   "publicDashboardLive",
			anchor: "export const publicDashboardLive",
			want: []string{
				"return projected.map(publicTripRow);",
				"riders: publicRiderCount(activeRidersForTrain(ctx, trip.id)),",
			},
		},
	} {
		start := strings.Index(source, tc.anchor)
		if start < 0 {
			t.Fatalf("%s anchor not found", tc.name)
		}
		snippet := source[start:min(start+4000, len(source))]
		for _, want := range tc.want {
			if !strings.Contains(snippet, want) {
				t.Fatalf("%s snippet missing %q in:\n%s", tc.name, want, snippet)
			}
		}
	}
}

func TestPublicReporterCountsUseBuckets(t *testing.T) {
	t.Parallel()

	source := readSpacetimeSource(t)
	if !strings.Contains(source, "function publicReporterCount(raw: number): number") {
		t.Fatalf("Spacetime source missing public reporter count helper")
	}
	for _, tc := range []struct {
		name   string
		anchor string
		want   []string
	}{
		{
			name:   "buildTrainCard",
			anchor: "function buildTrainCard",
			want: []string{
				"const status = buildTrainState(tx, train.id);",
				"status: stableId ? status : publicTrainStatus(status),",
			},
		},
		{
			name:   "buildPublicTrainView",
			anchor: "function buildPublicTrainView",
			want: []string{
				"status: publicTrainStatus(buildTrainState(tx, trainId)),",
			},
		},
		{
			name:   "publicDashboardPayload",
			anchor: "function publicDashboardPayload",
			want: []string{
				"uniqueReporters: publicReporterCount(Number(status.uniqueReporters) || 0),",
				"uniqueReporters: publicReporterCount(Number(train.uniqueReporters) || 0),",
			},
		},
		{
			name:   "publicServiceDayPayload",
			anchor: "function publicServiceDayPayload",
			want: []string{
				"status: publicTrainStatus(buildTrainState(tx, train.id)),",
			},
		},
		{
			name:   "refreshTripProjection",
			anchor: "function refreshTripProjection",
			want: []string{
				"const publicStatus = publicTrainStatus(status);",
				"uniqueReporters: Number(publicStatus.uniqueReporters) || 0,",
			},
		},
		{
			name:   "publicDashboardLive",
			anchor: "export const publicDashboardLive",
			want: []string{
				"return projected.map(publicTripRow);",
				"uniqueReporters: publicReporterCount(Number(status.uniqueReporters) || 0),",
			},
		},
	} {
		start := strings.Index(source, tc.anchor)
		if start < 0 {
			t.Fatalf("%s anchor not found", tc.name)
		}
		snippet := source[start:min(start+4000, len(source))]
		for _, want := range tc.want {
			if !strings.Contains(snippet, want) {
				t.Fatalf("%s snippet missing %q in:\n%s", tc.name, want, snippet)
			}
		}
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

func TestPublicIncidentActionsHaveGlobalLimitsAndAnonymousActors(t *testing.T) {
	t.Parallel()

	source := readSpacetimeSource(t)
	for _, want := range []string{
		"const REPORT_ACTION_WINDOW_MS = 30 * 60 * 1000;",
		"const REPORT_ACTION_LIMIT = 5;",
		"const VOTE_ACTION_LIMIT = 20;",
		"const VOTE_CHANGE_COOLDOWN_MS = 30 * 60 * 1000;",
		"const COMMENT_ACTION_LIMIT = 10;",
		"const INCIDENT_COMMENT_ACTION_LIMIT = 50;",
		"const PUBLIC_INCIDENT_ACTOR_LABEL = 'Anonymous';",
		"const trainbot_incident_vote_event = table(",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("Spacetime source missing %q", want)
		}
	}

	for _, tc := range []struct {
		anchor string
		want   []string
	}{
		{
			anchor: "function submitReportActionAtomic",
			want:   []string{"countReportActionsForStableIdSince", "REPORT_ACTION_LIMIT"},
		},
		{
			anchor: "function submitIncidentVoteAtomic",
			want: []string{
				"voteChangeCooldownSeconds",
				"countVoteActionsForStableIdSince",
				"trainbot_incident_vote_event.insert",
			},
		},
		{
			anchor: "function submitIncidentCommentAtomic",
			want: []string{
				"countCommentsForStableIdSince",
				"COMMENT_ACTION_LIMIT",
				"countCommentsForIncidentSince",
				"INCIDENT_COMMENT_ACTION_LIMIT",
			},
		},
		{
			anchor: "function incidentDetailPayload",
			want:   []string{"nickname: PUBLIC_INCIDENT_ACTOR_LABEL"},
		},
	} {
		snippet := sourceSnippet(t, source, tc.anchor, 3200)
		for _, want := range tc.want {
			if !strings.Contains(snippet, want) {
				t.Fatalf("%s snippet missing %q in:\n%s", tc.anchor, want, snippet)
			}
		}
	}
}

func TestDirectAndServiceIncidentMutationsShareAtomicReducers(t *testing.T) {
	t.Parallel()

	source := readSpacetimeSource(t)
	for _, tc := range []struct {
		anchor string
		want   string
	}{
		{anchor: "export const submitReport = spacetimedb.reducer", want: "submitReportActionAtomic(ctx, activity, event);"},
		{anchor: "export const submitStationSighting = spacetimedb.reducer", want: "submitReportActionAtomic(ctx, activity, event);"},
		{anchor: "export const voteIncident = spacetimedb.reducer", want: "submitIncidentVoteAtomic(ctx,"},
		{anchor: "export const commentIncident = spacetimedb.reducer", want: "submitIncidentCommentAtomic(ctx,"},
		{anchor: "export const serviceSubmitReport = spacetimedb.reducer", want: "submitReportActionAtomic(ctx, activity,"},
		{anchor: "export const serviceSubmitStationSighting = spacetimedb.reducer", want: "submitReportActionAtomic(ctx, activity,"},
		{anchor: "export const serviceSubmitLocationReport = spacetimedb.reducer", want: "submitReportActionAtomic(ctx, activity,"},
		{anchor: "export const serviceSubmitIncidentVote = spacetimedb.reducer", want: "submitIncidentVoteAtomic(ctx, args);"},
		{anchor: "export const serviceSubmitIncidentComment = spacetimedb.reducer", want: "submitIncidentCommentAtomic(ctx, args);"},
	} {
		snippet := sourceSnippet(t, source, tc.anchor, 2600)
		if !strings.Contains(snippet, tc.want) {
			t.Fatalf("%s snippet missing shared atomic mutation %q in:\n%s", tc.anchor, tc.want, snippet)
		}
	}
	if strings.Contains(source, "service_record_incident_vote_event") {
		t.Fatalf("split service vote accounting reducer must not remain available")
	}
}

func TestActiveBundlePublishReprojectsCurrentPublicIncidentRows(t *testing.T) {
	t.Parallel()

	source := readSpacetimeSource(t)
	snippet := sourceSnippet(t, source, "export const serviceSetActiveBundle = spacetimedb.reducer", 1800)
	if !strings.Contains(snippet, "refreshAllPublicProjections(ctx, next.serviceDate);") {
		t.Fatalf("active bundle publish must deterministically rebuild current-day public projections:\n%s", snippet)
	}
	if !strings.Contains(snippet, "updatedAt: nowISO(ctx)") {
		t.Fatalf("active bundle publish must use the reducer timestamp deterministically:\n%s", snippet)
	}
	projection := sourceSnippet(t, source, "function refreshActivityProjection", 5200)
	for _, want := range []string{
		"lastActivityActor: summary.lastActivityActor",
		"lastReporter: summary.lastReporter",
		"nickname: PUBLIC_INCIDENT_ACTOR_LABEL",
	} {
		if !strings.Contains(projection, want) {
			t.Fatalf("public reprojection is missing anonymous actor write %q in:\n%s", want, projection)
		}
	}
}

func TestPersistedPublicProjectionSchemaRemainsBackwardCompatible(t *testing.T) {
	t.Parallel()

	source := readSpacetimeSource(t)
	assertOrdered := func(name string, snippet string, fields ...string) {
		t.Helper()
		previous := -1
		for _, field := range fields {
			position := strings.Index(snippet, field)
			if position < 0 {
				t.Fatalf("%s missing persisted field %q in:\n%s", name, field, snippet)
			}
			if position <= previous {
				t.Fatalf("%s persisted field %q is out of order in:\n%s", name, field, snippet)
			}
			previous = position
		}
	}

	timelineDoc := sourceSnippet(t, source, "const timelineBucketDoc = t.object", 360)
	assertOrdered("timelineBucketDoc", timelineDoc, "at: t.string()", "signal: t.string()", "count: t.u32()")
	if strings.Contains(timelineDoc, "eventLabel: t.string()") {
		t.Fatalf("persisted timeline object must retain its production signal field:\n%s", timelineDoc)
	}

	timelineTable := sourceSnippet(t, source, "const trainbot_trip_timeline_bucket = table", 720)
	assertOrdered(
		"trainbot_trip_timeline_bucket",
		timelineTable,
		"id: t.string().primaryKey()",
		"trainId: t.string().index()",
		"serviceDate: t.string().index()",
		"at: t.string()",
		"signal: t.string()",
		"count: t.u32()",
	)

	incidentEvent := sourceSnippet(t, source, "const trainbot_incident_event = table", 780)
	assertOrdered(
		"trainbot_incident_event",
		incidentEvent,
		"id: t.string().primaryKey()",
		"incidentId: t.string().index()",
		"serviceDate: t.string().index()",
		"kind: t.string()",
		"name: t.string()",
		"detail: t.string()",
		"nickname: t.string()",
		"createdAt: t.string()",
		"signal: t.string()",
	)

	tripPublic := sourceSnippet(t, source, "const trainbot_trip_public = table", 1400)
	assertOrdered(
		"trainbot_trip_public",
		tripPublic,
		"id: t.string().primaryKey()",
		"serviceDate: t.string().index()",
		"fromStationId: t.string()",
		"fromStationName: t.string()",
		"toStationId: t.string()",
		"toStationName: t.string()",
		"departureAt: t.string()",
		"arrivalAt: t.string()",
		"sourceVersion: t.string()",
		"state: t.string()",
		"confidence: t.string()",
		"uniqueReporters: t.u32()",
		"riders: t.u32()",
		"lastReportAt: t.string()",
		"updatedAt: t.string()",
		"recentTimeline: t.array(timelineBucketDoc)",
	)

	tripProjection := sourceSnippet(t, source, "function refreshTripProjection", 3300)
	for _, want := range []string{"signal: publicBucket.signal", "sourceVersion: ''"} {
		if !strings.Contains(tripProjection, want) {
			t.Fatalf("public trip projection must retain a safe legacy field write %q in:\n%s", want, tripProjection)
		}
	}
	if strings.Contains(tripProjection, "sourceVersion: trip.sourceVersion") {
		t.Fatalf("public trip projection must not expose the private source version:\n%s", tripProjection)
	}

	incidentProjection := sourceSnippet(t, source, "function refreshActivityProjection", 5200)
	if strings.Count(incidentProjection, "signal: ''") < 2 {
		t.Fatalf("all public incident event variants must blank the legacy signal field:\n%s", incidentProjection)
	}
	apiTimeline := sourceSnippet(t, source, "function publicTimelinePayload", 420)
	if !strings.Contains(apiTimeline, "eventLabel: publicTimelineEventLabel(bucket)") {
		t.Fatalf("public API timeline must keep the eventLabel presentation field:\n%s", apiTimeline)
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

func sourceSnippet(t *testing.T, source string, anchor string, length int) string {
	t.Helper()
	start := strings.Index(source, anchor)
	if start < 0 {
		t.Fatalf("%s anchor not found", anchor)
	}
	return source[start:min(start+length, len(source))]
}

func min(left int, right int) int {
	if left < right {
		return left
	}
	return right
}
