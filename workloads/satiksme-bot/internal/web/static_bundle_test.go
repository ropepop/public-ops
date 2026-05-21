package web

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"satiksmebot/internal/model"
	"satiksmebot/internal/spacetime"
)

type nilSafeBundleSync struct{}

func (n *nilSafeBundleSync) PublishCatalogBundle(context.Context, spacetime.BundleSnapshot) error {
	if n == nil {
		panic("nil syncer should not be called")
	}
	return nil
}

type captureBundleSync struct {
	snapshot spacetime.BundleSnapshot
}

func (c *captureBundleSync) PublishCatalogBundle(_ context.Context, snapshot spacetime.BundleSnapshot) error {
	c.snapshot = snapshot
	return nil
}

func TestStaticBundlePublisherIgnoresTypedNilSyncer(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "public-bundles")
	var syncer *nilSafeBundleSync
	publisher := NewStaticBundlePublisher(dir, syncer)
	catalog := &model.Catalog{
		GeneratedAt: time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC),
		Stops: []model.Stop{{
			ID:        "3012",
			Name:      "Centrāltirgus",
			Latitude:  56.94,
			Longitude: 24.12,
		}},
	}

	manifest, err := publisher.PublishCatalog(context.Background(), catalog, catalog.GeneratedAt)
	if err != nil {
		t.Fatalf("PublishCatalog() error = %v", err)
	}
	if manifest == nil || manifest.Version == "" {
		t.Fatalf("manifest = %#v", manifest)
	}
	if _, err := os.Stat(filepath.Join(dir, "active.json")); err != nil {
		t.Fatalf("active bundle stat error = %v", err)
	}
}

func TestStaticBundlePublisherWritesPublicCatalogAllowlist(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "public-bundles")
	syncer := &captureBundleSync{}
	publisher := NewStaticBundlePublisher(dir, syncer)
	catalog := &model.Catalog{
		GeneratedAt: time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC),
		Stops: []model.Stop{{
			ID:            "3012",
			LiveID:        "live-3012",
			Name:          "Centraltirgus",
			Latitude:      56.94,
			Longitude:     24.12,
			Modes:         []string{"tram"},
			RouteLabels:   []string{"1"},
			NearbyStopIDs: []string{"3013"},
		}},
		Routes: []model.Route{{
			Label:   "1",
			Mode:    "tram",
			Name:    "Imanta",
			StopIDs: []string{"3012"},
		}},
	}

	manifest, err := publisher.PublishCatalog(context.Background(), catalog, catalog.GeneratedAt)
	if err != nil {
		t.Fatalf("PublishCatalog() error = %v", err)
	}
	if manifest == nil || manifest.Version == "" {
		t.Fatalf("manifest = %#v", manifest)
	}

	var stops []map[string]any
	stopsBody, err := os.ReadFile(filepath.Join(dir, "bundles", manifest.Version, "stops.json"))
	if err != nil {
		t.Fatalf("read stops bundle: %v", err)
	}
	if err := json.Unmarshal(stopsBody, &stops); err != nil {
		t.Fatalf("decode stops bundle: %v", err)
	}
	if len(stops) != 1 {
		t.Fatalf("stops bundle length = %d, want 1", len(stops))
	}
	assertJSONKeys(t, stops[0], []string{"id", "name", "latitude", "longitude", "modes", "routeLabels"})

	var routes []map[string]any
	routesBody, err := os.ReadFile(filepath.Join(dir, "bundles", manifest.Version, "routes.json"))
	if err != nil {
		t.Fatalf("read routes bundle: %v", err)
	}
	if err := json.Unmarshal(routesBody, &routes); err != nil {
		t.Fatalf("decode routes bundle: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("routes bundle length = %d, want 1", len(routes))
	}
	assertJSONKeys(t, routes[0], []string{"label", "mode", "name", "stopIds"})

	if len(syncer.snapshot.Stops) != 1 {
		t.Fatalf("sync snapshot stops = %+v", syncer.snapshot.Stops)
	}
	if got := syncer.snapshot.Stops[0].LiveID; got != "live-3012" {
		t.Fatalf("sync snapshot LiveID = %q, want live-3012", got)
	}
	if got := syncer.snapshot.Stops[0].NearbyStopIDs; len(got) != 1 || got[0] != "3013" {
		t.Fatalf("sync snapshot NearbyStopIDs = %+v, want [3013]", got)
	}
}

func TestStaticBundlePublisherRemovesOldPublicBundleVersions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "public-bundles")
	oldVersionDir := filepath.Join(dir, "bundles", "old-public-version")
	if err := os.MkdirAll(oldVersionDir, 0o755); err != nil {
		t.Fatalf("create old bundle dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oldVersionDir, "manifest.json"), []byte(`{"sourceVersion":"stale"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write old manifest: %v", err)
	}

	publisher := NewStaticBundlePublisher(dir, nil)
	catalog := &model.Catalog{
		GeneratedAt: time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC),
		Stops:       []model.Stop{{ID: "3012", Name: "Centraltirgus"}},
	}

	manifest, err := publisher.PublishCatalog(context.Background(), catalog, catalog.GeneratedAt)
	if err != nil {
		t.Fatalf("PublishCatalog() error = %v", err)
	}
	if manifest == nil || manifest.Version == "" {
		t.Fatalf("manifest = %#v", manifest)
	}
	if _, err := os.Stat(oldVersionDir); !os.IsNotExist(err) {
		t.Fatalf("old public bundle dir still exists, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bundles", manifest.Version, "manifest.json")); err != nil {
		t.Fatalf("active bundle manifest missing after cleanup: %v", err)
	}
}

func assertJSONKeys(t *testing.T, item map[string]any, want []string) {
	t.Helper()

	if len(item) != len(want) {
		t.Fatalf("keys = %v, want %v", item, want)
	}
	for _, key := range want {
		if _, ok := item[key]; !ok {
			t.Fatalf("missing key %q in %v", key, item)
		}
	}
}
