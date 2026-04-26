package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"satiksmebot/internal/model"
	"satiksmebot/internal/spacetime"
)

const staticBundleTransformVersion = "satiksme-static-v1"

type staticBundleSync interface {
	PublishCatalogBundle(ctx context.Context, snapshot spacetime.BundleSnapshot) error
}

type staticBundleManifest struct {
	Version          string               `json:"version"`
	GeneratedAt      string               `json:"generatedAt"`
	TransformVersion string               `json:"transformVersion"`
	Counts           staticBundleCounts   `json:"counts"`
	Slices           staticBundleSliceSet `json:"slices"`
}

type staticBundleCounts struct {
	Stops  int `json:"stops"`
	Routes int `json:"routes"`
}

type staticBundleSliceSet struct {
	Stops  string `json:"stops"`
	Routes string `json:"routes"`
}

type staticBundleActiveState struct {
	Version          string `json:"version"`
	GeneratedAt      string `json:"generatedAt"`
	TransformVersion string `json:"transformVersion"`
	ManifestPath     string `json:"manifestPath"`
}

type StaticBundlePublisher struct {
	dir    string
	syncer staticBundleSync
}

type staticBundleStore struct {
	dir string

	mu     sync.RWMutex
	active *staticBundleActiveState
}

func NewStaticBundlePublisher(dir string, syncer staticBundleSync) *StaticBundlePublisher {
	if isNilStaticBundleSync(syncer) {
		syncer = nil
	}
	return &StaticBundlePublisher{
		dir:    strings.TrimSpace(dir),
		syncer: syncer,
	}
}

func isNilStaticBundleSync(syncer staticBundleSync) bool {
	if syncer == nil {
		return true
	}
	value := reflect.ValueOf(syncer)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func newStaticBundleStore(dir string) *staticBundleStore {
	return &staticBundleStore{dir: strings.TrimSpace(dir)}
}

func (p *StaticBundlePublisher) Enabled() bool {
	return p != nil && strings.TrimSpace(p.dir) != ""
}

func (p *StaticBundlePublisher) PublishCatalog(ctx context.Context, catalog *model.Catalog, now time.Time) (*staticBundleManifest, error) {
	if !p.Enabled() || catalog == nil {
		return nil, nil
	}
	stopsBody, err := json.Marshal(catalog.Stops)
	if err != nil {
		return nil, fmt.Errorf("marshal bundle stops: %w", err)
	}
	routesBody, err := json.Marshal(catalog.Routes)
	if err != nil {
		return nil, fmt.Errorf("marshal bundle routes: %w", err)
	}

	hasher := sha256.New()
	hasher.Write([]byte(staticBundleTransformVersion))
	hasher.Write([]byte{0})
	hasher.Write([]byte(catalog.GeneratedAt.UTC().Format(time.RFC3339)))
	hasher.Write([]byte{0})
	hasher.Write(stopsBody)
	hasher.Write([]byte{0})
	hasher.Write(routesBody)
	version := hex.EncodeToString(hasher.Sum(nil))[:12]

	manifest := &staticBundleManifest{
		Version:          version,
		GeneratedAt:      catalog.GeneratedAt.UTC().Format(time.RFC3339),
		TransformVersion: staticBundleTransformVersion,
		Counts: staticBundleCounts{
			Stops:  len(catalog.Stops),
			Routes: len(catalog.Routes),
		},
		Slices: staticBundleSliceSet{
			Stops:  "stops.json",
			Routes: "routes.json",
		},
	}
	if err := os.MkdirAll(filepath.Join(p.dir, "bundles", version), 0o755); err != nil {
		return nil, fmt.Errorf("create bundle dir: %w", err)
	}
	versionDir := filepath.Join(p.dir, "bundles", version)
	if err := writeJSONFile(filepath.Join(versionDir, "stops.json"), append(stopsBody, '\n')); err != nil {
		return nil, err
	}
	if err := writeJSONFile(filepath.Join(versionDir, "routes.json"), append(routesBody, '\n')); err != nil {
		return nil, err
	}
	manifestBody, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal bundle manifest: %w", err)
	}
	if err := writeJSONFile(filepath.Join(versionDir, "manifest.json"), append(manifestBody, '\n')); err != nil {
		return nil, err
	}
	active := staticBundleActiveState{
		Version:          manifest.Version,
		GeneratedAt:      manifest.GeneratedAt,
		TransformVersion: manifest.TransformVersion,
		ManifestPath:     filepath.ToSlash(filepath.Join("bundles", manifest.Version, "manifest.json")),
	}
	activeBody, err := json.MarshalIndent(active, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal bundle active state: %w", err)
	}
	if err := writeJSONFile(filepath.Join(p.dir, "active.json"), append(activeBody, '\n')); err != nil {
		return nil, err
	}
	if p.syncer != nil {
		if err := p.syncer.PublishCatalogBundle(ctx, spacetime.BundleSnapshot{
			Version:     manifest.Version,
			GeneratedAt: manifest.GeneratedAt,
			Stops:       catalog.Stops,
			Routes:      catalog.Routes,
		}); err != nil {
			return nil, err
		}
	}
	return manifest, nil
}

func (s *staticBundleStore) activeState() (*staticBundleActiveState, error) {
	if s == nil || strings.TrimSpace(s.dir) == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(filepath.Join(s.dir, "active.json"))
	if err != nil {
		return nil, err
	}
	var active staticBundleActiveState
	if err := json.Unmarshal(raw, &active); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.active = &active
	s.mu.Unlock()
	return &active, nil
}

func (s *staticBundleStore) bundleMetadata() (map[string]any, error) {
	active, err := s.activeState()
	if err != nil || active == nil {
		return nil, err
	}
	return map[string]any{
		"version":          active.Version,
		"generatedAt":      active.GeneratedAt,
		"transformVersion": active.TransformVersion,
		"manifestPath":     active.ManifestPath,
	}, nil
}

func (s *staticBundleStore) invalidate() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = nil
}

func writeJSONFile(path string, body []byte) error {
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
