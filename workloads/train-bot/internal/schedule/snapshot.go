package schedule

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"telegramtrainapp/internal/domain"
	"telegramtrainapp/internal/scrape"
)

func LoadSnapshotFile(path string, expectedServiceDate string) (string, []domain.TrainInstance, map[string][]domain.TrainStop, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", nil, nil, fmt.Errorf("read snapshot: %w", err)
	}
	var payload scrape.SnapshotFile
	if err := json.Unmarshal(b, &payload); err != nil {
		return "", nil, nil, fmt.Errorf("decode snapshot: %w", err)
	}
	if strings.Contains(payload.SourceVersion, "vivi_pdf") {
		return "", nil, nil, fmt.Errorf("schedule uses retired PDF source")
	}
	if payload.SourceVersion == "" {
		payload.SourceVersion = "snapshot-unknown"
	}
	for _, train := range payload.Trains {
		if expectedServiceDate != "" && train.ServiceDate != expectedServiceDate {
			return "", nil, nil, fmt.Errorf("train %s has service_date %s (expected %s)", train.ID, train.ServiceDate, expectedServiceDate)
		}
	}
	trains, stops, err := scrape.SnapshotToDomain(payload)
	return payload.SourceVersion, trains, stops, err
}
