package web

import (
	"context"
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
