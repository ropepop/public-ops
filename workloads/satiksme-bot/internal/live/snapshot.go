package live

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"satiksmebot/internal/model"
)

const (
	SnapshotPublicPrefix = "transport/live"
	snapshotActiveName   = "active.json"
	snapshotFileSuffix   = ".json.js"
)

type SnapshotActiveState struct {
	Version      string    `json:"version"`
	Path         string    `json:"path"`
	Hash         string    `json:"hash"`
	PublishedAt  time.Time `json:"publishedAt"`
	VehicleCount int       `json:"vehicleCount"`
}

type SnapshotPublishResult struct {
	Version      string
	Path         string
	Hash         string
	PublishedAt  time.Time
	VehicleCount int
	Changed      bool
}

type SnapshotPublisher struct {
	dir      string
	maxFiles int
}

type snapshotCanonicalVehicle struct {
	ID             string  `json:"id"`
	VehicleCode    string  `json:"vehicleCode,omitempty"`
	Mode           string  `json:"mode"`
	RouteLabel     string  `json:"routeLabel"`
	Direction      string  `json:"direction,omitempty"`
	Destination    string  `json:"destination,omitempty"`
	Latitude       float64 `json:"latitude"`
	Longitude      float64 `json:"longitude"`
	Heading        int     `json:"heading,omitempty"`
	StopID         string  `json:"stopId,omitempty"`
	StopName       string  `json:"stopName,omitempty"`
	ArrivalSeconds int     `json:"arrivalSeconds,omitempty"`
	LowFloor       bool    `json:"lowFloor,omitempty"`
	LiveRowID      string  `json:"liveRowId,omitempty"`
}

func NewSnapshotPublisher(dir string, maxFiles int) *SnapshotPublisher {
	return &SnapshotPublisher{
		dir:      strings.TrimSpace(dir),
		maxFiles: maxFiles,
	}
}

func (p *SnapshotPublisher) Enabled() bool {
	return p != nil && strings.TrimSpace(p.dir) != ""
}

func (p *SnapshotPublisher) ActiveState() (*SnapshotActiveState, error) {
	if !p.Enabled() {
		return nil, nil
	}
	return ReadSnapshotActiveState(p.dir)
}

func ReadSnapshotActiveState(dir string) (*SnapshotActiveState, error) {
	cleanDir := strings.TrimSpace(dir)
	if cleanDir == "" {
		return nil, nil
	}
	body, err := os.ReadFile(filepath.Join(cleanDir, snapshotActiveName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read live snapshot active state: %w", err)
	}
	var active SnapshotActiveState
	if err := json.Unmarshal(body, &active); err != nil {
		return nil, fmt.Errorf("decode live snapshot active state: %w", err)
	}
	return &active, nil
}

func (p *SnapshotPublisher) Publish(now time.Time, vehicles []model.LiveVehicle) (*SnapshotPublishResult, error) {
	if !p.Enabled() {
		return nil, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	canonicalVehicles := canonicalSnapshotVehicles(vehicles)
	canonicalBody, err := json.Marshal(canonicalVehicles)
	if err != nil {
		return nil, fmt.Errorf("marshal live snapshot hash payload: %w", err)
	}
	hashSum := sha256.Sum256(canonicalBody)
	hash := hex.EncodeToString(hashSum[:])

	active, err := p.ActiveState()
	if err != nil {
		return nil, err
	}
	if active != nil && strings.EqualFold(strings.TrimSpace(active.Hash), hash) {
		return &SnapshotPublishResult{
			Version:      active.Version,
			Path:         active.Path,
			Hash:         active.Hash,
			PublishedAt:  active.PublishedAt,
			VehicleCount: len(canonicalVehicles),
			Changed:      false,
		}, nil
	}

	version := now.UTC().Format("20060102T150405Z") + "-" + hash[:12]
	payloadVehicles := snapshotVehiclesForPayload(vehicles, now.UTC())
	payload := model.LiveTransportSnapshot{
		Version:     version,
		GeneratedAt: now.UTC(),
		Vehicles:    payloadVehicles,
	}
	payloadBody, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal live snapshot payload: %w", err)
	}
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return nil, fmt.Errorf("create live snapshot dir: %w", err)
	}
	fileName := version + snapshotFileSuffix
	if err := writeSnapshotFile(filepath.Join(p.dir, fileName), append(payloadBody, '\n')); err != nil {
		return nil, err
	}
	result := &SnapshotPublishResult{
		Version:      version,
		Path:         filepath.ToSlash(filepath.Join(SnapshotPublicPrefix, fileName)),
		Hash:         hash,
		PublishedAt:  now.UTC(),
		VehicleCount: len(canonicalVehicles),
		Changed:      true,
	}
	activeState := SnapshotActiveState{
		Version:      result.Version,
		Path:         result.Path,
		Hash:         result.Hash,
		PublishedAt:  result.PublishedAt,
		VehicleCount: result.VehicleCount,
	}
	activeBody, err := json.MarshalIndent(activeState, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal live snapshot active state: %w", err)
	}
	if err := writeSnapshotFile(filepath.Join(p.dir, snapshotActiveName), append(activeBody, '\n')); err != nil {
		return nil, err
	}
	if err := p.cleanupOldSnapshots(); err != nil {
		return nil, err
	}
	return result, nil
}

func canonicalSnapshotVehicles(vehicles []model.LiveVehicle) []snapshotCanonicalVehicle {
	out := make([]snapshotCanonicalVehicle, 0, len(vehicles))
	for _, vehicle := range vehicles {
		out = append(out, snapshotCanonicalVehicle{
			ID:             strings.TrimSpace(vehicle.ID),
			VehicleCode:    strings.TrimSpace(vehicle.VehicleCode),
			Mode:           strings.TrimSpace(vehicle.Mode),
			RouteLabel:     strings.TrimSpace(vehicle.RouteLabel),
			Direction:      strings.TrimSpace(vehicle.Direction),
			Destination:    strings.TrimSpace(vehicle.Destination),
			Latitude:       vehicle.Latitude,
			Longitude:      vehicle.Longitude,
			Heading:        vehicle.Heading,
			StopID:         strings.TrimSpace(vehicle.StopID),
			StopName:       strings.TrimSpace(vehicle.StopName),
			ArrivalSeconds: vehicle.ArrivalSeconds,
			LowFloor:       vehicle.LowFloor,
			LiveRowID:      strings.TrimSpace(vehicle.LiveRowID),
		})
	}
	return out
}

func snapshotVehiclesForPayload(vehicles []model.LiveVehicle, now time.Time) []model.LiveVehicle {
	out := make([]model.LiveVehicle, 0, len(vehicles))
	for _, vehicle := range vehicles {
		next := vehicle
		next.UpdatedAt = now
		next.SightingCount = 0
		next.Incidents = nil
		out = append(out, next)
	}
	return out
}

func (p *SnapshotPublisher) cleanupOldSnapshots() error {
	if !p.Enabled() || p.maxFiles <= 0 {
		return nil
	}
	entries, err := os.ReadDir(p.dir)
	if err != nil {
		return fmt.Errorf("list live snapshot dir: %w", err)
	}
	fileNames := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if name == "" || name == snapshotActiveName || !isVersionedSnapshotFile(name) {
			continue
		}
		fileNames = append(fileNames, name)
	}
	if len(fileNames) <= p.maxFiles {
		return nil
	}
	sort.Strings(fileNames)
	for _, name := range fileNames[:len(fileNames)-p.maxFiles] {
		if err := os.Remove(filepath.Join(p.dir, name)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove old live snapshot %s: %w", name, err)
		}
	}
	return nil
}

func writeSnapshotFile(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create live snapshot parent dir: %w", err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, body, 0o644); err != nil {
		return fmt.Errorf("write live snapshot %s: %w", path, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("commit live snapshot %s: %w", path, err)
	}
	return nil
}

func isVersionedSnapshotFile(name string) bool {
	clean := strings.TrimSpace(name)
	return strings.HasSuffix(clean, snapshotFileSuffix) || strings.HasSuffix(clean, ".json")
}
