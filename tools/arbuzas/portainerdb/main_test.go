package main

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func TestRepairDBFixesStaleAgentEndpoint(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	srcPath := filepath.Join(tempDir, "src.db")
	dstPath := filepath.Join(tempDir, "dst.db")

	if err := seedTestDB(srcPath); err != nil {
		t.Fatalf("seed db: %v", err)
	}

	before, err := inspectDB(srcPath)
	if err != nil {
		t.Fatalf("inspect before repair: %v", err)
	}
	if before.Healthy {
		t.Fatalf("expected test db to start unhealthy")
	}
	if len(before.StaleEndpointIDs) != 1 || before.StaleEndpointIDs[0] != 1 {
		t.Fatalf("unexpected stale endpoints before repair: %#v", before.StaleEndpointIDs)
	}

	after, err := repairDB(srcPath, dstPath)
	if err != nil {
		t.Fatalf("repair db: %v", err)
	}
	if !after.Healthy {
		t.Fatalf("expected repaired db to be healthy: %#v", after)
	}
	if after.RawContainsAgentEndpoint {
		t.Fatalf("expected compacted db to drop stale raw bytes")
	}
	if len(after.StaleEndpointIDs) != 0 {
		t.Fatalf("expected no stale endpoints after repair: %#v", after.StaleEndpointIDs)
	}
	if len(after.LocalEndpointIDs) != 1 || after.LocalEndpointIDs[0] != 1 {
		t.Fatalf("unexpected local endpoints after repair: %#v", after.LocalEndpointIDs)
	}
	if len(after.SnapshotIDs) != 0 {
		t.Fatalf("expected repaired db to delete stale snapshot: %#v", after.SnapshotIDs)
	}
}

func seedTestDB(dbPath string) error {
	_ = os.Remove(dbPath)

	db, err := bolt.Open(dbPath, 0o600, nil)
	if err != nil {
		return err
	}
	defer db.Close()

	return db.Update(func(tx *bolt.Tx) error {
		endpoints, err := tx.CreateBucketIfNotExists([]byte(endpointsBucket))
		if err != nil {
			return err
		}
		snapshots, err := tx.CreateBucketIfNotExists([]byte(snapshotsBucket))
		if err != nil {
			return err
		}

		key := make([]byte, 8)
		binary.BigEndian.PutUint64(key, 1)

		endpoint := map[string]any{
			"Id":       1,
			"Name":     "local",
			"Type":     2,
			"URL":      portainerAgentEndpoint,
			"PublicURL": "",
			"TLSConfig": map[string]any{
				"TLS":           true,
				"TLSSkipVerify": true,
			},
			"Agent": map[string]any{
				"Version": "2.0.0",
			},
		}
		encodedEndpoint, err := json.Marshal(endpoint)
		if err != nil {
			return err
		}
		if err := endpoints.Put(key, encodedEndpoint); err != nil {
			return err
		}

		snapshot := map[string]any{"DockerSnapshotRaw": map[string]any{"Swarm": true}}
		encodedSnapshot, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		return snapshots.Put(key, encodedSnapshot)
	})
}
