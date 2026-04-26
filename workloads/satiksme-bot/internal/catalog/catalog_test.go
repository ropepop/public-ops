package catalog

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"satiksmebot/internal/model"
	"satiksmebot/internal/runtime"
)

func TestBuildCatalogUsesLiveStopsAndGTFSFallback(t *testing.T) {
	stopsRaw := []byte("ID;SiriID;Direction;Lat;Lng;Stops;Name\n3012;15014;;5694595;2410565;5003,3009,2054;\n")
	routesRaw := []byte("RouteNum;Authority;City;Transport;Operator;ValidityPeriods;SpecialDates;RouteTag;RouteType;Commercial;RouteName;Weekdays;Streets;RouteStops;RouteStopsPlatforms;TripIds;Pikas\n22;Rīga;riga;bus;X;;;a-b;A;22;Lidosta virziens;1234567;;;3012,2054;;\n")
	gtfsRaw := testGTFSZip(t, "stops.txt", "stop_id,stop_name,stop_lat,stop_lon\n3012,Centrāltirgus,56.94012,24.12123\n")

	catalog, err := BuildCatalog(stopsRaw, routesRaw, gtfsRaw)
	if err != nil {
		t.Fatalf("BuildCatalog() error = %v", err)
	}
	if len(catalog.Stops) != 1 {
		t.Fatalf("len(catalog.Stops) = %d, want 1", len(catalog.Stops))
	}
	stop := catalog.Stops[0]
	if stop.Name != "Centrāltirgus" {
		t.Fatalf("stop.Name = %q", stop.Name)
	}
	if stop.LiveID != "15014" {
		t.Fatalf("stop.LiveID = %q", stop.LiveID)
	}
	if len(stop.RouteLabels) != 1 || stop.RouteLabels[0] != "22" {
		t.Fatalf("stop.RouteLabels = %#v", stop.RouteLabels)
	}
	if len(stop.Modes) != 1 || stop.Modes[0] != "bus" {
		t.Fatalf("stop.Modes = %#v", stop.Modes)
	}
	if stop.Latitude == 0 || stop.Longitude == 0 {
		t.Fatalf("expected GTFS coordinates, got %+v", stop)
	}
}

func testGTFSZip(t *testing.T, fileName, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fh, err := zw.Create(fileName)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := fmt.Fprint(fh, content); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return buf.Bytes()
}

func TestManagerCachesCatalogLookupsAndPayload(t *testing.T) {
	manager := NewManager(Settings{})
	catalog := &model.Catalog{
		GeneratedAt: time.Date(2026, 3, 10, 15, 0, 0, 0, time.UTC),
		Stops:       []model.Stop{{ID: "3012", Name: "Centrāltirgus"}},
		Routes:      []model.Route{{Label: "1", Mode: "tram", Name: "Imanta"}},
	}
	status := runtime.CatalogStatus{
		Loaded:      true,
		GeneratedAt: catalog.GeneratedAt,
		StopCount:   1,
		RouteCount:  1,
	}
	if err := manager.useCatalog(catalog, status); err != nil {
		t.Fatalf("useCatalog() error = %v", err)
	}

	stop, ok := manager.FindStop("3012")
	if !ok {
		t.Fatalf("FindStop() did not find stop")
	}
	if stop.Name != "Centrāltirgus" {
		t.Fatalf("stop.Name = %q", stop.Name)
	}
	if got := manager.CatalogETag(); got == "" {
		t.Fatalf("CatalogETag() = empty")
	}

	cachedJSON := manager.CatalogJSON()
	if len(cachedJSON) == 0 {
		t.Fatalf("CatalogJSON() = empty")
	}
	var decoded model.Catalog
	if err := json.Unmarshal(cachedJSON, &decoded); err != nil {
		t.Fatalf("Unmarshal(CatalogJSON()) error = %v", err)
	}
	if len(decoded.Stops) != 1 || decoded.Stops[0].ID != "3012" {
		t.Fatalf("decoded = %+v", decoded)
	}
}
