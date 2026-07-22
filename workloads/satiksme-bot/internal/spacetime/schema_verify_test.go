package spacetime

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestVerifyExpectedSchemaSuccess(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Method, http.MethodPost; got != want {
			t.Fatalf("request method = %q, want %q", got, want)
		}
		if got, want := r.URL.Path, "/v1/database/live-db/call/"+schemaInfoProcedureName; got != want {
			t.Fatalf("request path = %q, want %q", got, want)
		}
		if got, want := strings.TrimSpace(r.Header.Get("Content-Type")), "application/json"; got != want {
			t.Fatalf("content type = %q, want %q", got, want)
		}
		_, _ = w.Write([]byte(fmt.Sprintf("{\"module\":\"%s\",\"schemaVersion\":\"%s\"}", ExpectedSchemaModule, ExpectedSchemaVersion)))
	}))
	defer server.Close()

	err := VerifyExpectedSchema(context.Background(), server.Client(), SchemaTarget{
		Host:     server.URL,
		Database: "live-db",
	})
	if err != nil {
		t.Fatalf("VerifyExpectedSchema() error = %v, want nil", err)
	}
}

func TestVerifyExpectedSchemaMissingProcedure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`External attempt to call nonexistent reducer "satiksmebot_schema_info" failed.`))
	}))
	defer server.Close()

	err := VerifyExpectedSchema(context.Background(), server.Client(), SchemaTarget{
		Host:     server.URL,
		Database: "live-db",
	})
	if err == nil {
		t.Fatalf("VerifyExpectedSchema() error = nil, want missing procedure error")
	}
	if !strings.Contains(err.Error(), schemaInfoProcedureName) {
		t.Fatalf("VerifyExpectedSchema() error = %q, want mention of %q", err, schemaInfoProcedureName)
	}
}

func TestVerifyExpectedSchemaVersionMismatch(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"module":"satiksme-bot","schemaVersion":"old-version"}`))
	}))
	defer server.Close()

	err := VerifyExpectedSchema(context.Background(), server.Client(), SchemaTarget{
		Host:     server.URL,
		Database: "live-db",
	})
	if err == nil {
		t.Fatalf("VerifyExpectedSchema() error = nil, want mismatch error")
	}
	if !strings.Contains(err.Error(), `schemaVersion="old-version"`) {
		t.Fatalf("VerifyExpectedSchema() error = %q, want observed version", err)
	}
}

func TestVerifyExpectedSchemaMalformedPayload(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"module":"satiksme-bot"}`))
	}))
	defer server.Close()

	err := VerifyExpectedSchema(context.Background(), server.Client(), SchemaTarget{
		Host:     server.URL,
		Database: "live-db",
	})
	if err == nil {
		t.Fatalf("VerifyExpectedSchema() error = nil, want malformed payload error")
	}
	if !strings.Contains(err.Error(), "missing module or schemaVersion") {
		t.Fatalf("VerifyExpectedSchema() error = %q, want malformed payload message", err)
	}
}

func TestExpectedSchemaVersionMatchesSpacetimeModule(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(spacetimeModuleIndexPath(t))
	if err != nil {
		t.Fatalf("read Spacetime module: %v", err)
	}
	text := string(body)
	moduleMatch := regexp.MustCompile(`const SATIKSMEBOT_SCHEMA_MODULE = '([^']+)'`).FindStringSubmatch(text)
	if len(moduleMatch) != 2 {
		t.Fatalf("Spacetime module is missing SATIKSMEBOT_SCHEMA_MODULE")
	}
	versionMatch := regexp.MustCompile(`const SATIKSMEBOT_SCHEMA_VERSION = '([^']+)'`).FindStringSubmatch(text)
	if len(versionMatch) != 2 {
		t.Fatalf("Spacetime module is missing SATIKSMEBOT_SCHEMA_VERSION")
	}
	if got, want := moduleMatch[1], ExpectedSchemaModule; got != want {
		t.Fatalf("Spacetime module schema module = %q, want %q", got, want)
	}
	if got, want := versionMatch[1], ExpectedSchemaVersion; got != want {
		t.Fatalf("Spacetime module schema version = %q, want %q", got, want)
	}
}

func TestServiceAndImportActionsRequireServiceRole(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(spacetimeModuleIndexPath(t))
	if err != nil {
		t.Fatalf("read Spacetime module: %v", err)
	}
	source := string(body)
	for _, name := range []string{
		"beginBundleImport",
		"appendBundleChunk",
		"commitBundleImport",
		"abortBundleImport",
		"serviceSyncBundle",
		"serviceImportStateSnapshot",
		"serviceUpsertLiveSnapshotState",
		"serviceCountLiveViewers",
		"serviceCleanupLiveViewers",
		"servicePutStopSighting",
		"serviceRecordStopSightingWithVote",
		"serviceGetLastStopSighting",
		"serviceListStopSightingsSince",
		"servicePutVehicleSighting",
		"serviceRecordVehicleSightingWithVote",
		"serviceGetLastVehicleSighting",
		"serviceListVehicleSightingsSince",
		"servicePutAreaReport",
		"serviceRecordAreaReportWithVote",
		"serviceGetLastAreaReport",
		"serviceListAreaReportsSince",
		"serviceUpsertIncidentVote",
		"serviceRecordIncidentVote",
		"serviceListIncidentVotes",
		"serviceListIncidentVoteEvents",
		"serviceCountMapReportsByUserSince",
		"serviceCountIncidentVoteEventsByUserSince",
		"serviceCountIncidentCommentsByUserSince",
		"serviceCountIncidentCommentsByIncidentSince",
		"servicePutIncidentComment",
		"serviceListIncidentComments",
		"serviceEnqueueReportDump",
		"serviceNextReportDump",
		"servicePeekReportDump",
		"serviceDeleteReportDump",
		"serviceUpdateReportDumpFailure",
		"servicePendingReportDumpCount",
		"serviceGetChatAnalyzerCheckpoint",
		"serviceSetChatAnalyzerCheckpoint",
		"serviceEnqueueChatAnalyzerMessage",
		"serviceListPendingChatAnalyzerMessages",
		"serviceMarkChatAnalyzerMessageProcessed",
		"serviceSaveChatAnalyzerBatch",
		"serviceCountChatAnalyzerMessagesBySenderSince",
		"serviceCountChatAnalyzerAppliedByTargetSince",
		"serviceCleanupExpiredState",
	} {
		anchor := "export const " + name + " ="
		start := strings.Index(source, anchor)
		if start < 0 {
			t.Fatalf("missing %s", anchor)
		}
		rest := source[start+len(anchor):]
		end := strings.Index(rest, "\n\nexport const ")
		if end < 0 {
			end = len(rest)
		}
		block := rest[:end]
		if !strings.Contains(block, "requireServiceRole(tx)") {
			t.Fatalf("%s must call requireServiceRole(tx)", name)
		}
	}
}

func TestReporterActionsRequireTelegramViewerRole(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(spacetimeModuleIndexPath(t))
	if err != nil {
		t.Fatalf("read Spacetime module: %v", err)
	}
	source := string(body)

	sessionBlock := sourceBlock(t, source, "function requireReporterSession", "function requireServiceRole")
	for _, want := range []string{
		"roles.includes('satiksme_viewer')",
		"stableId.startsWith('telegram:')",
	} {
		if !strings.Contains(sessionBlock, want) {
			t.Fatalf("reporter session guard must require a Telegram-backed satiksme_viewer session; missing %q in:\n%s", want, sessionBlock)
		}
	}

	ensureBlock := sourceBlock(t, source, "function ensureReporter", "function stableIdFromServiceItem")
	if !strings.Contains(ensureBlock, "requireReporterSession(tx)") {
		t.Fatalf("ensureReporter must reject anonymous Spacetime identities before creating reporter rows:\n%s", ensureBlock)
	}

	viewerBlock := sourceBlock(t, source, "function optionalViewerStableId", "function requireServiceRole")
	if !strings.Contains(viewerBlock, "requireReporterSession(tx)") {
		t.Fatalf("optionalViewerStableId must ignore anonymous/non-viewer Spacetime identities:\n%s", viewerBlock)
	}

	for _, name := range []string{
		"bootstrapMe",
		"listRecentReports",
		"submitStopReport",
		"submitVehicleReport",
		"submitAreaReport",
		"voteIncident",
		"commentIncident",
	} {
		anchor := "export const " + name + " ="
		start := strings.Index(source, anchor)
		if start < 0 {
			t.Fatalf("missing %s", anchor)
		}
		rest := source[start+len(anchor):]
		end := strings.Index(rest, "\n\nexport const ")
		if end < 0 {
			end = len(rest)
		}
		block := rest[:end]
		if !strings.Contains(block, "ensureReporter") &&
			!strings.Contains(block, "requireReporterSession") &&
			!strings.Contains(block, "submitStopReportImpl") &&
			!strings.Contains(block, "submitVehicleReportImpl") &&
			!strings.Contains(block, "submitAreaReportImpl") {
			t.Fatalf("%s must flow through the reporter guard before user state changes:\n%s", name, block)
		}
	}
}

func TestPublicIncidentActorsAreRedactedInSpacetimeModule(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(spacetimeModuleIndexPath(t))
	if err != nil {
		t.Fatalf("read Spacetime module: %v", err)
	}
	source := string(body)
	if !strings.Contains(source, "const PUBLIC_INCIDENT_ACTOR_LABEL = 'anonīmi'") {
		t.Fatalf("Spacetime module must define a fixed public incident actor label")
	}

	refreshBlock := sourceBlock(t, source, "function refreshPublicProjections", "function incidentSummaryPayload")
	for _, forbidden := range []string{
		"current.lastReporter = event.nickname",
		"incident.lastReporter = asString(latestEvent.nickname).trim()",
		"nickname: asString(event.nickname).trim()",
		"nickname: event.nickname",
		"nickname: asString(comment.nickname).trim()",
	} {
		if strings.Contains(refreshBlock, forbidden) {
			t.Fatalf("refreshPublicProjections exposes private nickname with %q", forbidden)
		}
	}

	summaryBlock := sourceBlock(t, source, "function incidentSummaryPayload", "function visibleSightingsPayload")
	if strings.Contains(summaryBlock, "lastReporter: asString(row.lastReporter).trim()") {
		t.Fatalf("incidentSummaryPayload must not expose stored public row nicknames")
	}

	detailBlock := sourceBlock(t, source, "function publicIncidentDetailPayload", "function upsertLiveSnapshotStatePayload")
	for _, forbidden := range []string{
		"nickname: asString(item.nickname).trim()",
	} {
		if strings.Contains(detailBlock, forbidden) {
			t.Fatalf("publicIncidentDetailPayload exposes stored nickname with %q", forbidden)
		}
	}

	commentBlock := sourceBlock(t, source, "export const commentIncident", "export const beginBundleImport")
	if strings.Contains(commentBlock, "nickname: next.nickname") {
		t.Fatalf("commentIncident response must not return the private stored nickname")
	}
	if !strings.Contains(commentBlock, "nickname: publicIncidentNickname()") {
		t.Fatalf("commentIncident response must return the public incident actor label")
	}
}

func TestPublicAreaIncidentsAreCoarsenedInSpacetimeModule(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(spacetimeModuleIndexPath(t))
	if err != nil {
		t.Fatalf("read Spacetime module: %v", err)
	}
	source := string(body)
	for _, required := range []string{
		"function publicAreaContext",
		"function publicAreaReportPayload",
		"function publicAreaIncidentID",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("Spacetime module must define %s", required)
		}
	}
	if strings.Contains(source, "function areaIncidentID(scopeKey: string): string {\n  return `area:${sanitizeIncidentKey(scopeKey)}`;") {
		t.Fatalf("areaIncidentID must not expose the raw location/description scope key")
	}
	areaIncidentIDBlock := sourceBlock(t, source, "function publicAreaIncidentID", "function publicStopSightingID")
	for _, required := range []string{"/^pub-[0-9a-f]{8}$/.test(clean)", "return `area:${clean}`", "fnv1a32Hex(scopeKey)"} {
		if !strings.Contains(areaIncidentIDBlock, required) {
			t.Fatalf("publicAreaIncidentID must preserve validated compatibility aliases and hash every other scope with %q:\n%s", required, areaIncidentIDBlock)
		}
	}

	areaDocBlock := sourceBlock(t, source, "const areaContextDoc = ", "const satiksmebot_active_bundle")
	if !strings.Contains(areaDocBlock, "scopeKey: t.string()") {
		t.Fatalf("public area context schema must retain the v1 scopeKey column for non-destructive compatibility:\n%s", areaDocBlock)
	}

	publicAreaContextBlock := sourceBlock(t, source, "function publicAreaContext", "function publicVehicleContext")
	if !strings.Contains(publicAreaContextBlock, "scopeKey: ''") {
		t.Fatalf("publicAreaContext must populate the retained v1 scopeKey with an empty value:\n%s", publicAreaContextBlock)
	}
	if strings.Contains(publicAreaContextBlock, "scopeKey: asString") {
		t.Fatalf("publicAreaContext must not populate the compatibility scopeKey from private data:\n%s", publicAreaContextBlock)
	}
	scrubBlock := sourceBlock(t, source, "function scrubLegacyPublicCompatibilityFields", "function upsertLiveViewerPayload")
	if !strings.Contains(scrubBlock, "? { ...row.area, scopeKey: '' }") {
		t.Fatalf("startup compatibility scrub must clear retained public area scope keys:\n%s", scrubBlock)
	}

	refreshAreaBlock := sourceBlock(t, source, "for (const row of rowsFrom(tx.db.satiksmebot_area_report.iter()))", "for (const incident of Array.from(incidents.values()))")
	for _, forbidden := range []string{
		"subjectId: scopeKey",
		"scopeKey,",
		"latitude: Number(row.latitude) || 0",
		"longitude: Number(row.longitude) || 0",
		"radiusMeters: Number(row.radiusMeters) || 0",
		"area: incident.area || undefined",
	} {
		if strings.Contains(refreshAreaBlock, forbidden) {
			t.Fatalf("refreshPublicProjections exposes raw public area detail with %q", forbidden)
		}
	}

	visibleBlock := sourceBlock(t, source, "let areaReports = cleanStopId ? []", "if (limit > 0)")
	for _, forbidden := range []string{
		"id: asString(row.id).trim()",
		"latitude: Number(row.latitude) || 0",
		"longitude: Number(row.longitude) || 0",
		"radiusMeters: Number(row.radiusMeters) || 0",
	} {
		if strings.Contains(visibleBlock, forbidden) {
			t.Fatalf("visibleSightingsPayload exposes raw public area detail with %q", forbidden)
		}
	}
}

func TestPublicIncidentCommentsUseOpaquePublicIDs(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(spacetimeModuleIndexPath(t))
	if err != nil {
		t.Fatalf("read Spacetime module: %v", err)
	}
	source := string(body)
	for _, required := range []string{
		"function publicIncidentCommentID",
		"incident-comment:pub-${fnv1a32Hex(seed)}",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("Spacetime module must define opaque public comment ID marker %q", required)
		}
	}

	refreshBlock := sourceBlock(t, source, "for (const comment of comments) {", "function incidentSummaryPayload")
	if strings.Contains(refreshBlock, "id: asString(comment.id).trim()") {
		t.Fatalf("public incident comments expose raw private comment IDs:\n%s", refreshBlock)
	}
	required := "id: publicIncidentCommentID(incident.id, asString(comment.id).trim(), publicTimestamp(comment.createdAt))"
	if !strings.Contains(refreshBlock, required) {
		t.Fatalf("public incident comments must derive opaque public IDs with %q:\n%s", required, refreshBlock)
	}
}

func TestPublicVehicleIncidentsHideInternalLiveRowIDs(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(spacetimeModuleIndexPath(t))
	if err != nil {
		t.Fatalf("read Spacetime module: %v", err)
	}
	source := string(body)

	for _, tc := range []struct {
		name  string
		start string
		end   string
	}{
		{name: "public vehicle context", start: "const vehicleContextDoc = ", end: "const areaContextDoc = "},
		{name: "public vehicle sighting table", start: "const satiksmebot_public_vehicle_sighting = table", end: "const satiksmebot_public_area_report = table"},
		{name: "public vehicle projection", start: "const storedScopeKey = asString(row.scopeKey).trim() || vehicleScopeKey(row);", end: "for (const row of rowsFrom(tx.db.satiksmebot_area_report.iter()))"},
		{name: "visible public vehicle sightings", start: "let vehicleSightings = rowsFrom(tx.db.satiksmebot_public_vehicle_sighting.iter())", end: "let areaReports = cleanStopId ? []"},
	} {
		block := sourceBlock(t, source, tc.start, tc.end)
		switch tc.name {
		case "public vehicle context":
			for _, compatibilityField := range []string{"scopeKey: t.string()", "liveRowId: t.string()"} {
				if !strings.Contains(block, compatibilityField) {
					t.Fatalf("%s must retain v1 compatibility field %q:\n%s", tc.name, compatibilityField, block)
				}
			}
		case "public vehicle sighting table":
			if !strings.Contains(block, "liveRowId: t.string()") {
				t.Fatalf("%s must retain the v1 liveRowId compatibility field:\n%s", tc.name, block)
			}
		case "visible public vehicle sightings":
			if strings.Contains(block, "liveRowId") {
				t.Fatalf("%s must omit the inert compatibility field from procedure responses:\n%s", tc.name, block)
			}
		case "public vehicle projection":
			for _, forbidden := range []string{
				"subjectId: storedScopeKey",
				"scopeKey: storedScopeKey",
				"liveRowId: asString(row.liveRowId)",
			} {
				if strings.Contains(block, forbidden) {
					t.Fatalf("%s must not expose reusable vehicle scope keys with %q:\n%s", tc.name, forbidden, block)
				}
			}
			if !strings.Contains(block, "subjectId: ''") {
				t.Fatalf("%s must blank public vehicle subject IDs:\n%s", tc.name, block)
			}
			if !strings.Contains(block, "liveRowId: ''") {
				t.Fatalf("%s must blank the retained public vehicle liveRowId:\n%s", tc.name, block)
			}
		}
	}
	publicContextBlock := sourceBlock(t, source, "function publicVehicleContext", "function publicAreaReportPayload")
	for _, required := range []string{"scopeKey: ''", "liveRowId: ''"} {
		if !strings.Contains(publicContextBlock, required) {
			t.Fatalf("publicVehicleContext must blank compatibility field with %q:\n%s", required, publicContextBlock)
		}
	}
	for _, forbidden := range []string{"scopeKey: asString", "liveRowId: asString"} {
		if strings.Contains(publicContextBlock, forbidden) {
			t.Fatalf("publicVehicleContext must not populate compatibility fields from private data with %q:\n%s", forbidden, publicContextBlock)
		}
	}
	scrubBlock := sourceBlock(t, source, "function scrubLegacyPublicCompatibilityFields", "function upsertLiveViewerPayload")
	for _, inertValue := range []string{"? { ...row.vehicle, scopeKey: '', liveRowId: '' }", "liveRowId: ''"} {
		if !strings.Contains(scrubBlock, inertValue) {
			t.Fatalf("startup compatibility scrub must clear retained public vehicle field with %q:\n%s", inertValue, scrubBlock)
		}
	}

	scopeBlock := sourceBlock(t, source, "function vehicleScopeKey", "function normalizeAreaRadius")
	if !strings.Contains(scopeBlock, "return `live:${mode}:${routeLabel}:${direction}:${liveRowId}`") {
		t.Fatalf("private vehicleScopeKey must stay compatible with v1 and the Go service:\n%s", scopeBlock)
	}
	if strings.Contains(scopeBlock, "fnv1a32Hex(liveRowId)") {
		t.Fatalf("private vehicleScopeKey must not rewrite stored live row identity:\n%s", scopeBlock)
	}
	incidentIDBlock := sourceBlock(t, source, "function vehicleIncidentID", "function fnv1a32Hex")
	for _, required := range []string{"/^pub-[0-9a-f]{8}$/.test(clean)", "return `vehicle:${clean}`", "vehicle:pub-${fnv1a32Hex(scopeKey)}"} {
		if !strings.Contains(incidentIDBlock, required) {
			t.Fatalf("vehicleIncidentID must preserve validated opaque aliases and hash logical scopes with %q:\n%s", required, incidentIDBlock)
		}
	}
	sanitizeBlock := sourceBlock(t, source, "function sanitizeVehicleSighting", "function sanitizeAreaReport")
	if !strings.Contains(sanitizeBlock, "payload.scopeKey = payload.scopeKey || opaqueVehicleScopeKey(vehicleScopeKey(payload))") {
		t.Fatalf("sanitizeVehicleSighting must preserve every supplied nonempty scope and derive an opaque fallback:\n%s", sanitizeBlock)
	}
	if strings.Contains(sanitizeBlock, "payload.liveRowId ? vehicleScopeKey(payload)") {
		t.Fatalf("sanitizeVehicleSighting must not overwrite a supplied opaque scope when liveRowId is present:\n%s", sanitizeBlock)
	}
	projectionScopeBlock := sourceBlock(t, source, "const storedScopeKey = asString(row.scopeKey).trim() || vehicleScopeKey(row);", "const event = {")
	if !strings.Contains(projectionScopeBlock, "vehicleIncidentID(storedScopeKey)") {
		t.Fatalf("public vehicle projection must prefer stored scope identity and only recompute when it is missing:\n%s", projectionScopeBlock)
	}
	collisionBlock := sourceBlock(t, source, "function ensureVehiclePublicIDAvailable", "function normalizeAreaRadius")
	for _, required := range []string{
		"satiksmebot_vehicle_sighting.iter()",
		"vehicleLogicalScopeKey(row)",
		"opaqueVehicleScopeKey(existingLogicalScopeKey) === opaqueScopeKey",
		"existingLogicalScopeKey !== cleanLogicalScopeKey",
		"throw new SenderError('opaque vehicle incident collision')",
	} {
		if !strings.Contains(collisionBlock, required) {
			t.Fatalf("vehicle opaque-ID collision guard must fail closed with %q:\n%s", required, collisionBlock)
		}
	}
	directSubmitBlock := sourceBlock(t, source, "function submitVehicleReportImpl", "function submitAreaReportImpl")
	for _, required := range []string{
		"const logicalScopeKey = vehicleScopeKey(payload)",
		"ensureVehiclePublicIDAvailable(tx, logicalScopeKey)",
		"const scopeKey = opaqueVehicleScopeKey(logicalScopeKey)",
		"const incidentId = vehicleIncidentID(scopeKey)",
		"direction: asString(payload?.direction).trim()",
	} {
		if !strings.Contains(directSubmitBlock, required) {
			t.Fatalf("direct vehicle submit must derive and store one opaque scope without direction drift; missing %q:\n%s", required, directSubmitBlock)
		}
	}
}

func TestPublicStopAndVehicleSightingsUseOpaquePublicIDs(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(spacetimeModuleIndexPath(t))
	if err != nil {
		t.Fatalf("read Spacetime module: %v", err)
	}
	source := string(body)
	for _, required := range []string{
		"function publicStopSightingID",
		"function publicVehicleSightingID",
		"stop-report:pub-${fnv1a32Hex(reportId)}",
		"vehicle-report:pub-${fnv1a32Hex(reportId)}",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("Spacetime module must define opaque public sighting ID marker %q", required)
		}
	}
	for _, tc := range []struct {
		name     string
		start    string
		end      string
		required string
	}{
		{
			name:     "public stop projection",
			start:    "tx.db.satiksmebot_public_stop_sighting.insert({",
			end:      "for (const row of rowsFrom(tx.db.satiksmebot_vehicle_sighting.iter())) {\n    const sourceCreatedAt = asString(row.createdAt).trim();",
			required: "id: publicStopSightingID(asString(row.id).trim())",
		},
		{
			name:     "public vehicle projection",
			start:    "tx.db.satiksmebot_public_vehicle_sighting.insert({",
			end:      "for (const row of rowsFrom(tx.db.satiksmebot_area_report.iter())) {\n    const sourceCreatedAt = asString(row.createdAt).trim();",
			required: "id: publicVehicleSightingID(asString(row.id).trim())",
		},
	} {
		block := sourceBlock(t, source, tc.start, tc.end)
		if strings.Contains(block, "id: asString(row.id).trim()") {
			t.Fatalf("%s exposes raw report row IDs:\n%s", tc.name, block)
		}
		if !strings.Contains(block, tc.required) {
			t.Fatalf("%s must derive opaque public IDs with %q:\n%s", tc.name, tc.required, block)
		}
	}
}

func TestPublicStopCatalogCompatibilityFieldsAreInert(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(spacetimeModuleIndexPath(t))
	if err != nil {
		t.Fatalf("read Spacetime module: %v", err)
	}
	source := string(body)
	tableBlock := sourceBlock(t, source, "const satiksmebot_stop_catalog = table", "const satiksmebot_route_catalog = table")
	for _, compatibilityField := range []string{"liveId: t.string()", "nearbyStopIds: t.array(t.string())"} {
		if !strings.Contains(tableBlock, compatibilityField) {
			t.Fatalf("public stop catalog must retain v1 compatibility field %q:\n%s", compatibilityField, tableBlock)
		}
	}
	sanitizerBlock := sourceBlock(t, source, "function sanitizeStopCatalogRow", "function sanitizeRouteCatalogRow")
	for _, inertValue := range []string{"liveId: ''", "nearbyStopIds: []"} {
		if !strings.Contains(sanitizerBlock, inertValue) {
			t.Fatalf("public stop catalog sanitizer must write inert compatibility value %q:\n%s", inertValue, sanitizerBlock)
		}
	}
	for _, forbidden := range []string{"item?.liveId", "item?.nearbyStopIds"} {
		if strings.Contains(sanitizerBlock, forbidden) {
			t.Fatalf("public stop catalog sanitizer must not copy private compatibility input %q:\n%s", forbidden, sanitizerBlock)
		}
	}
	scrubBlock := sourceBlock(t, source, "function scrubLegacyPublicCompatibilityFields", "function upsertLiveViewerPayload")
	for _, inertValue := range []string{"liveId: ''", "nearbyStopIds: []"} {
		if !strings.Contains(scrubBlock, inertValue) {
			t.Fatalf("startup compatibility scrub must clear retained stop catalog field with %q:\n%s", inertValue, scrubBlock)
		}
	}
}

func TestPublicBrowserClientHidesInternalLiveRowIDs(t *testing.T) {
	t.Parallel()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller(0) failed")
	}
	root := filepath.Join(filepath.Dir(filename), "..", "..")
	for _, tc := range []struct {
		name string
		path string
	}{
		{name: "web client source", path: filepath.Join(root, "web-client", "src", "index.ts")},
		{name: "generated browser asset", path: filepath.Join(root, "internal", "web", "static", "live-client.js")},
	} {
		body, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatalf("read %s: %v", tc.name, err)
		}
		source := string(body)
		for _, block := range []struct {
			name  string
			start string
			end   string
		}{
			{name: "vehicle context normalizer", start: "function normalizeVehicleContext", end: "function normalizeAreaContext"},
			{name: "vehicle sighting normalizer", start: "function normalizeVehicleSighting", end: "function normalizeAreaReport"},
			{name: "area context normalizer", start: "function normalizeAreaContext", end: "function normalizeIncidentVotes"},
			{name: "snapshot state normalizer", start: "function normalizeSnapshotState", end: "function normalizeVehicleContext"},
		} {
			snippet := sourceBlock(t, source, block.start, block.end)
			if strings.Contains(snippet, "liveRowId") {
				t.Fatalf("%s %s must not expose internal live row IDs:\n%s", tc.name, block.name, snippet)
			}
			if (block.name == "vehicle context normalizer" || block.name == "area context normalizer") && strings.Contains(snippet, "scopeKey") {
				t.Fatalf("%s %s must not expose reusable scope keys:\n%s", tc.name, block.name, snippet)
			}
			if block.name == "snapshot state normalizer" {
				for _, forbidden := range []string{
					"hash",
					"publishedAt",
					"lastSuccessAt",
					"lastAttemptAt",
					"status",
					"consecutiveFailures",
					"vehicleCount",
				} {
					if strings.Contains(snippet, forbidden) {
						t.Fatalf("%s %s must not expose live snapshot diagnostic field %q:\n%s", tc.name, block.name, forbidden, snippet)
					}
				}
			}
		}
		if tc.name == "generated browser asset" {
			for _, forbidden := range []string{
				"// src/generated/types.ts",
				"SatiksmebotReporterIdentity",
				"SatiksmebotReportDump",
				"SatiksmebotChatAnalyzerMessage",
				"SatiksmebotStopSighting = t.object",
				"SatiksmebotVehicleSighting = t.object",
				"scopeKey",
				"liveRowId",
				"stableId",
				"userId",
				"payloadJson",
				"publishedAt",
				"lastSuccessAt",
				"lastAttemptAt",
				"consecutiveFailures",
				"vehicleCount",
			} {
				if strings.Contains(source, forbidden) {
					t.Fatalf("%s exposes private generated Spacetime schema marker %q", tc.name, forbidden)
				}
			}
		}
	}
}

func TestPublicLiveSnapshotCompatibilityFieldsAreInert(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(spacetimeModuleIndexPath(t))
	if err != nil {
		t.Fatalf("read Spacetime module: %v", err)
	}
	source := string(body)
	tableBlock := sourceBlock(t, source, "const satiksmebot_public_live_snapshot_state = table", "const satiksmebot_live_viewer_heartbeat = table")
	for _, compatibilityField := range []string{
		"hash: t.string()",
		"publishedAt: t.string()",
		"lastSuccessAt: t.string()",
		"lastAttemptAt: t.string()",
		"status: t.string()",
		"consecutiveFailures: t.u32()",
		"vehicleCount: t.u32()",
	} {
		if !strings.Contains(tableBlock, compatibilityField) {
			t.Fatalf("public live snapshot table must retain v1 compatibility field %q:\n%s", compatibilityField, tableBlock)
		}
	}
	upsertBlock := sourceBlock(t, source, "function upsertLiveSnapshotStatePayload", "function countLiveViewersPayload")
	for _, inertValue := range []string{
		"hash: ''",
		"publishedAt: ''",
		"lastSuccessAt: ''",
		"lastAttemptAt: ''",
		"status: ''",
		"consecutiveFailures: 0",
		"vehicleCount: 0",
	} {
		if !strings.Contains(upsertBlock, inertValue) {
			t.Fatalf("public live snapshot upsert must write inert compatibility value %q:\n%s", inertValue, upsertBlock)
		}
	}
	for _, forbidden := range []string{
		"item?.hash",
		"item?.publishedAt",
		"item?.lastSuccessAt",
		"item?.lastAttemptAt",
		"item?.status",
		"item?.consecutiveFailures",
		"item?.vehicleCount",
	} {
		if strings.Contains(upsertBlock, forbidden) {
			t.Fatalf("public live snapshot upsert must not copy private diagnostic input %q:\n%s", forbidden, upsertBlock)
		}
	}
	responseBlock := sourceBlock(t, source, "function liveSnapshotStateRowToJSON", "function latestVoteMap")
	for _, forbidden := range []string{"hash:", "publishedAt:", "lastSuccessAt:", "lastAttemptAt:", "status:", "consecutiveFailures:", "vehicleCount:"} {
		if strings.Contains(responseBlock, forbidden) {
			t.Fatalf("live snapshot procedure response must omit inert compatibility field %q:\n%s", forbidden, responseBlock)
		}
	}
	scrubBlock := sourceBlock(t, source, "function scrubLegacyPublicCompatibilityFields", "function upsertLiveViewerPayload")
	for _, inertValue := range []string{
		"hash: ''",
		"publishedAt: ''",
		"lastSuccessAt: ''",
		"lastAttemptAt: ''",
		"status: ''",
		"consecutiveFailures: 0",
		"vehicleCount: 0",
	} {
		if !strings.Contains(scrubBlock, inertValue) {
			t.Fatalf("startup compatibility scrub must clear retained live snapshot field with %q:\n%s", inertValue, scrubBlock)
		}
	}
}

func TestLegacyPublicLiveViewerHeartbeatIsEmptyAndServiceOnly(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(spacetimeModuleIndexPath(t))
	if err != nil {
		t.Fatalf("read Spacetime module: %v", err)
	}
	source := string(body)
	tableBlock := sourceBlock(t, source, "const satiksmebot_live_viewer_heartbeat = table(", "const satiksmebot_live_viewer_state = table(")
	if !strings.Contains(tableBlock, "public: true") {
		t.Fatalf("legacy live viewer heartbeat table must retain the v1 public flag for non-destructive compatibility")
	}
	if strings.Contains(source, "satiksmebot_live_viewer_heartbeat.insert") {
		t.Fatalf("legacy public heartbeat compatibility table must never receive new rows")
	}
	clearBlock := sourceBlock(t, source, "function clearLegacyPublicViewerHeartbeats", "function upsertLiveViewerPayload")
	for _, required := range []string{
		"satiksmebot_live_viewer_heartbeat.iter()",
		"satiksmebot_live_viewer_heartbeat.sessionId.delete",
	} {
		if !strings.Contains(clearBlock, required) {
			t.Fatalf("legacy heartbeat cleanup must contain %q:\n%s", required, clearBlock)
		}
	}
	upsertBlock := sourceBlock(t, source, "function upsertLiveViewerPayload", "function heartbeatLiveViewerPayload")
	for _, required := range []string{
		"clearLegacyPublicViewerHeartbeats(tx)",
		"satiksmebot_live_viewer_state.sessionId.delete",
		"satiksmebot_live_viewer_state.insert",
	} {
		if !strings.Contains(upsertBlock, required) {
			t.Fatalf("viewer updates must use private viewer state and clear the legacy table with %q:\n%s", required, upsertBlock)
		}
	}
	countBlock := sourceBlock(t, source, "function countLiveViewersPayload", "function listStopSightingsSincePayload")
	for _, required := range []string{
		"clearLegacyPublicViewerHeartbeats(tx)",
		"satiksmebot_live_viewer_state.iter()",
		"row.visible === true",
		"row.updatedAt",
	} {
		if !strings.Contains(countBlock, required) {
			t.Fatalf("viewer counts must use only private viewer state with %q:\n%s", required, countBlock)
		}
	}
	if strings.Contains(countBlock, "satiksmebot_live_viewer_heartbeat.iter()") {
		t.Fatalf("viewer counts must not read the legacy public heartbeat table:\n%s", countBlock)
	}
	schemaInfoBlock := sourceBlock(t, source, "export const schemaInfo =", "export const bootstrapMe =")
	if !strings.Contains(schemaInfoBlock, "scrubLegacyPublicCompatibilityFields(tx)") {
		t.Fatalf("schema verification must scrub retained v1 public compatibility data before readiness:\n%s", schemaInfoBlock)
	}
	cleanupBlock := sourceBlock(t, source, "export const serviceCleanupLiveViewers =", "export const servicePutStopSighting =")
	if !strings.Contains(cleanupBlock, "clearLegacyPublicViewerHeartbeats(tx)") || !strings.Contains(cleanupBlock, "satiksmebot_live_viewer_state.iter()") {
		t.Fatalf("viewer cleanup must empty the legacy table and clean private viewer state:\n%s", cleanupBlock)
	}
	for _, tc := range []struct {
		name  string
		start string
		end   string
	}{
		{name: "heartbeatLiveViewer", start: "export const heartbeatLiveViewer =", end: "export const setLiveViewerState ="},
		{name: "setLiveViewerState", start: "export const setLiveViewerState =", end: "export const listPublicIncidents ="},
	} {
		block := sourceBlock(t, source, tc.start, tc.end)
		if !strings.Contains(block, "requireServiceRole(tx)") {
			t.Fatalf("%s must require the service role", tc.name)
		}
	}
}

func TestServiceReportProceduresRejectExistingIDsBeforeMutation(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(spacetimeModuleIndexPath(t))
	if err != nil {
		t.Fatalf("read Spacetime module: %v", err)
	}
	source := string(body)
	for _, tc := range []struct {
		name  string
		start string
		end   string
		table string
		value string
	}{
		{
			name:  "stop report",
			start: "function recordServiceStopSightingWithVotePayload",
			end:   "function recordServiceVehicleSightingWithVotePayload",
			table: "satiksmebot_stop_sighting",
			value: "sighting",
		},
		{
			name:  "vehicle report",
			start: "function recordServiceVehicleSightingWithVotePayload",
			end:   "function recordServiceAreaReportWithVotePayload",
			table: "satiksmebot_vehicle_sighting",
			value: "sighting",
		},
		{
			name:  "area report",
			start: "function recordServiceAreaReportWithVotePayload",
			end:   "function validateServiceReportVotePair",
			table: "satiksmebot_area_report",
			value: "report",
		},
	} {
		block := sourceBlock(t, source, tc.start, tc.end)
		find := fmt.Sprintf("tx.db.%s.id.find(%s.id)", tc.table, tc.value)
		deleteCall := fmt.Sprintf("tx.db.%s.id.delete(%s.id)", tc.table, tc.value)
		findAt := strings.Index(block, find)
		deleteAt := strings.Index(block, deleteCall)
		if findAt < 0 || deleteAt < 0 || findAt > deleteAt {
			t.Fatalf("%s must reject an existing deterministic id before mutation", tc.name)
		}
		if !strings.Contains(block[findAt:deleteAt], "reason: 'idempotent_replay'") {
			t.Fatalf("%s existing-id guard must return idempotent_replay", tc.name)
		}
	}
}

func sourceBlock(t *testing.T, source, startAnchor, endAnchor string) string {
	t.Helper()
	start := strings.Index(source, startAnchor)
	if start < 0 {
		t.Fatalf("missing source anchor %q", startAnchor)
	}
	rest := source[start+len(startAnchor):]
	end := strings.Index(rest, endAnchor)
	if end < 0 {
		t.Fatalf("missing source end anchor %q after %q", endAnchor, startAnchor)
	}
	return rest[:end]
}

func spacetimeModuleIndexPath(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller(0) failed")
	}
	return filepath.Join(filepath.Dir(filename), "..", "..", "spacetimedb", "src", "index.ts")
}
