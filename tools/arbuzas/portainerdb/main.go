package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	bolt "go.etcd.io/bbolt"
)

const (
	portainerAgentEndpoint = "tcp://tasks.agent:9001"
	portainerLocalEndpoint = "unix:///var/run/docker.sock"
	endpointsBucket        = "endpoints"
	snapshotsBucket        = "snapshots"
)

type endpointSummary struct {
	ID   uint64 `json:"id"`
	Type int    `json:"type"`
	URL  string `json:"url"`
}

type inspectionReport struct {
	Healthy                  bool              `json:"healthy"`
	EndpointCount            int               `json:"endpoint_count"`
	Endpoints                []endpointSummary `json:"endpoints"`
	LocalEndpointIDs         []uint64          `json:"local_endpoint_ids"`
	StaleEndpointIDs         []uint64          `json:"stale_endpoint_ids"`
	SnapshotIDs              []uint64          `json:"snapshot_ids"`
	RawContainsAgentEndpoint bool              `json:"raw_contains_agent_endpoint"`
}

func main() {
	if len(os.Args) < 3 {
		usage()
	}

	command := os.Args[1]
	switch command {
	case "inspect":
		if len(os.Args) != 3 {
			usage()
		}
		report, err := inspectDB(os.Args[2])
		exitWithReport(report, err, false)
	case "check":
		if len(os.Args) != 3 {
			usage()
		}
		report, err := inspectDB(os.Args[2])
		exitWithReport(report, err, true)
	case "repair":
		if len(os.Args) != 4 {
			usage()
		}
		report, err := repairDB(os.Args[2], os.Args[3])
		exitWithReport(report, err, true)
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage:\n  %s inspect <portainer.db>\n  %s check <portainer.db>\n  %s repair <src.db> <dst.db>\n", filepath.Base(os.Args[0]), filepath.Base(os.Args[0]), filepath.Base(os.Args[0]))
	os.Exit(2)
}

func exitWithReport(report inspectionReport, err error, enforceHealthy bool) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "portainerdb: %v\n", err)
		os.Exit(1)
	}
	output, marshalErr := json.Marshal(report)
	if marshalErr != nil {
		fmt.Fprintf(os.Stderr, "portainerdb: marshal report: %v\n", marshalErr)
		os.Exit(1)
	}
	fmt.Printf("%s\n", output)
	if enforceHealthy && !report.Healthy {
		os.Exit(1)
	}
}

func inspectDB(dbPath string) (inspectionReport, error) {
	report := inspectionReport{}

	raw, err := os.ReadFile(dbPath)
	if err != nil {
		return report, fmt.Errorf("read %s: %w", dbPath, err)
	}
	report.RawContainsAgentEndpoint = bytes.Contains(raw, []byte(portainerAgentEndpoint))

	db, err := bolt.Open(dbPath, 0o600, &bolt.Options{ReadOnly: true})
	if err != nil {
		return report, fmt.Errorf("open %s: %w", dbPath, err)
	}
	defer db.Close()

	err = db.View(func(tx *bolt.Tx) error {
		endpoints := tx.Bucket([]byte(endpointsBucket))
		if endpoints == nil {
			return fmt.Errorf("missing bucket %q", endpointsBucket)
		}

		cursor := endpoints.Cursor()
		for key, value := cursor.First(); key != nil; key, value = cursor.Next() {
			id := decodeKey(key)

			endpoint := map[string]any{}
			if err := json.Unmarshal(value, &endpoint); err != nil {
				return fmt.Errorf("decode endpoint %d: %w", id, err)
			}

			summary := endpointSummary{
				ID:   id,
				Type: jsonInt(endpoint["Type"]),
				URL:  jsonString(endpoint["URL"]),
			}
			report.EndpointCount++
			report.Endpoints = append(report.Endpoints, summary)

			switch {
			case summary.URL == portainerAgentEndpoint:
				report.StaleEndpointIDs = append(report.StaleEndpointIDs, id)
			case summary.URL == portainerLocalEndpoint && summary.Type == 1:
				report.LocalEndpointIDs = append(report.LocalEndpointIDs, id)
			}
		}

		if snapshots := tx.Bucket([]byte(snapshotsBucket)); snapshots != nil {
			cursor := snapshots.Cursor()
			for key, _ := cursor.First(); key != nil; key, _ = cursor.Next() {
				report.SnapshotIDs = append(report.SnapshotIDs, decodeKey(key))
			}
		}

		return nil
	})
	if err != nil {
		return report, err
	}

	slices.Sort(report.LocalEndpointIDs)
	slices.Sort(report.StaleEndpointIDs)
	slices.Sort(report.SnapshotIDs)
	slices.SortFunc(report.Endpoints, func(a, b endpointSummary) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})

	report.Healthy = len(report.StaleEndpointIDs) == 0 &&
		len(report.LocalEndpointIDs) > 0 &&
		!report.RawContainsAgentEndpoint

	return report, nil
}

func repairDB(srcPath, dstPath string) (inspectionReport, error) {
	if srcPath == dstPath {
		return inspectionReport{}, errors.New("source and destination paths must differ")
	}

	tempPath := dstPath + ".tmp"
	_ = os.Remove(tempPath)
	_ = os.Remove(dstPath)

	if err := copyFile(srcPath, tempPath); err != nil {
		return inspectionReport{}, fmt.Errorf("copy source db: %w", err)
	}
	defer os.Remove(tempPath)

	patchedEndpointIDs, err := patchTempDB(tempPath)
	if err != nil {
		return inspectionReport{}, err
	}

	srcDB, err := bolt.Open(tempPath, 0o600, &bolt.Options{ReadOnly: true})
	if err != nil {
		return inspectionReport{}, fmt.Errorf("open patched db: %w", err)
	}
	defer srcDB.Close()

	dstDB, err := bolt.Open(dstPath, 0o600, nil)
	if err != nil {
		return inspectionReport{}, fmt.Errorf("open compacted db: %w", err)
	}
	if err := bolt.Compact(dstDB, srcDB, 0); err != nil {
		dstDB.Close()
		return inspectionReport{}, fmt.Errorf("compact repaired db: %w", err)
	}
	if err := dstDB.Close(); err != nil {
		return inspectionReport{}, fmt.Errorf("close compacted db: %w", err)
	}

	report, err := inspectDB(dstPath)
	if err != nil {
		return inspectionReport{}, err
	}
	if len(patchedEndpointIDs) > 0 && !report.Healthy {
		return report, fmt.Errorf("repaired db still unhealthy after patching endpoints %v", patchedEndpointIDs)
	}
	return report, nil
}

func patchTempDB(dbPath string) ([]uint64, error) {
	db, err := bolt.Open(dbPath, 0o600, nil)
	if err != nil {
		return nil, fmt.Errorf("open temp db: %w", err)
	}
	defer db.Close()

	var patchedEndpointIDs []uint64
	err = db.Update(func(tx *bolt.Tx) error {
		endpoints := tx.Bucket([]byte(endpointsBucket))
		if endpoints == nil {
			return fmt.Errorf("missing bucket %q", endpointsBucket)
		}

		cursor := endpoints.Cursor()
		for key, value := cursor.First(); key != nil; key, value = cursor.Next() {
			id := decodeKey(key)
			endpoint := map[string]any{}
			if err := json.Unmarshal(value, &endpoint); err != nil {
				return fmt.Errorf("decode endpoint %d: %w", id, err)
			}
			if jsonString(endpoint["URL"]) != portainerAgentEndpoint {
				continue
			}

			endpoint["Type"] = 1
			endpoint["URL"] = portainerLocalEndpoint
			endpoint["TLSConfig"] = map[string]any{
				"TLS":           false,
				"TLSSkipVerify": false,
			}
			delete(endpoint, "Agent")

			encoded, err := json.Marshal(endpoint)
			if err != nil {
				return fmt.Errorf("encode endpoint %d: %w", id, err)
			}
			if err := endpoints.Put(key, encoded); err != nil {
				return fmt.Errorf("write endpoint %d: %w", id, err)
			}
			patchedEndpointIDs = append(patchedEndpointIDs, id)
		}

		if snapshots := tx.Bucket([]byte(snapshotsBucket)); snapshots != nil {
			for _, id := range patchedEndpointIDs {
				key := encodeKey(id)
				if err := snapshots.Delete(key); err != nil {
					return fmt.Errorf("delete snapshot %d: %w", id, err)
				}
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return patchedEndpointIDs, nil
}

func copyFile(srcPath, dstPath string) error {
	input, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	return os.WriteFile(dstPath, input, 0o600)
}

func encodeKey(id uint64) []byte {
	key := make([]byte, 8)
	binary.BigEndian.PutUint64(key, id)
	return key
}

func decodeKey(key []byte) uint64 {
	if len(key) < 8 {
		return 0
	}
	return binary.BigEndian.Uint64(key)
}

func jsonString(value any) string {
	text, _ := value.(string)
	return text
}

func jsonInt(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	default:
		return 0
	}
}
