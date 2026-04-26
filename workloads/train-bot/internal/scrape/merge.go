package scrape

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"telegramtrainapp/internal/domain"
)

func BuildSnapshotFile(serviceDate time.Time, schedules []RawSchedule) (SnapshotFile, Stats, error) {
	stats := Stats{ProvidersTried: len(schedules)}
	if len(schedules) == 0 {
		return SnapshotFile{}, stats, fmt.Errorf("no schedules provided")
	}
	targetDate := serviceDate.Format("2006-01-02")
	type mergedTrain struct {
		train     RawTrain
		stopsByID map[string]RawStop
	}
	merged := map[string]*mergedTrain{}
	sourceNames := make([]string, 0, len(schedules))

	for idx, sched := range schedules {
		if len(sched.Trains) == 0 {
			continue
		}
		stats.ProvidersSucceeded++
		sourceNames = append(sourceNames, sched.SourceName)
		for _, t := range sched.Trains {
			if t.ServiceDate != targetDate {
				stats.TrainsDropped++
				continue
			}
			if t.DepartureAt.IsZero() || t.ArrivalAt.IsZero() || strings.TrimSpace(t.FromStation) == "" || strings.TrimSpace(t.ToStation) == "" {
				stats.TrainsDropped++
				continue
			}
			key := mergeKey(t)
			cur, ok := merged[key]
			if !ok {
				cur = &mergedTrain{
					train: RawTrain{
						ID:          trainID(t),
						TrainNumber: strings.TrimSpace(t.TrainNumber),
						ServiceDate: t.ServiceDate,
						FromStation: t.FromStation,
						ToStation:   t.ToStation,
						DepartureAt: t.DepartureAt,
						ArrivalAt:   t.ArrivalAt,
					},
					stopsByID: map[string]RawStop{},
				}
				merged[key] = cur
			} else if idx == 0 {
				if !cur.train.DepartureAt.Equal(t.DepartureAt) || !cur.train.ArrivalAt.Equal(t.ArrivalAt) {
					stats.ConflictsResolved++
				}
				cur.train.DepartureAt = t.DepartureAt
				cur.train.ArrivalAt = t.ArrivalAt
				if strings.TrimSpace(t.TrainNumber) != "" {
					cur.train.TrainNumber = strings.TrimSpace(t.TrainNumber)
				}
				cur.train.ID = trainID(cur.train)
				if strings.TrimSpace(t.FromStation) != "" {
					cur.train.FromStation = t.FromStation
				}
				if strings.TrimSpace(t.ToStation) != "" {
					cur.train.ToStation = t.ToStation
				}
			}

			for _, stop := range t.Stops {
				k := stopKey(stop)
				if k == "" {
					continue
				}
				existing, exists := cur.stopsByID[k]
				if !exists {
					cur.stopsByID[k] = stop
					if idx > 0 {
						stats.StopsFilledFromB++
					}
					continue
				}
				if existing.ArrivalAt == nil && stop.ArrivalAt != nil {
					existing.ArrivalAt = stop.ArrivalAt
					if idx > 0 {
						stats.StopsFilledFromB++
					}
				}
				if existing.DepartureAt == nil && stop.DepartureAt != nil {
					existing.DepartureAt = stop.DepartureAt
					if idx > 0 {
						stats.StopsFilledFromB++
					}
				}
				if existing.Latitude == nil && stop.Latitude != nil {
					existing.Latitude = stop.Latitude
				}
				if existing.Longitude == nil && stop.Longitude != nil {
					existing.Longitude = stop.Longitude
				}
				if existing.Seq == 0 && stop.Seq > 0 {
					existing.Seq = stop.Seq
				}
				cur.stopsByID[k] = existing
			}
		}
	}

	out := SnapshotFile{
		SourceVersion: fmt.Sprintf("agg-%s-%s", targetDate, strings.Join(sourceNames, "+")),
		Trains:        make([]SnapshotTrain, 0, len(merged)),
	}
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		m := merged[k]
		train := SnapshotTrain{
			ID:          m.train.ID,
			ServiceDate: m.train.ServiceDate,
			FromStation: m.train.FromStation,
			ToStation:   m.train.ToStation,
			DepartureAt: m.train.DepartureAt.Format(time.RFC3339),
			ArrivalAt:   m.train.ArrivalAt.Format(time.RFC3339),
		}
		if len(m.stopsByID) > 0 {
			stopKeys := make([]string, 0, len(m.stopsByID))
			for sk := range m.stopsByID {
				stopKeys = append(stopKeys, sk)
			}
			sort.Strings(stopKeys)
			stops := make([]SnapshotStop, 0, len(stopKeys))
			for _, sk := range stopKeys {
				rs := m.stopsByID[sk]
				ss := SnapshotStop{StationName: rs.StationName, Seq: rs.Seq}
				if rs.ArrivalAt != nil {
					ss.ArrivalAt = rs.ArrivalAt.Format(time.RFC3339)
				}
				if rs.DepartureAt != nil {
					ss.DepartureAt = rs.DepartureAt.Format(time.RFC3339)
				}
				ss.Latitude = rs.Latitude
				ss.Longitude = rs.Longitude
				stops = append(stops, ss)
			}
			train.Stops = stops
		}
		out.Trains = append(out.Trains, train)
	}
	stats.TrainsMerged = len(out.Trains)
	if len(out.Trains) == 0 {
		return SnapshotFile{}, stats, fmt.Errorf("no trains produced by merge")
	}
	return out, stats, nil
}

func SnapshotToDomain(snapshot SnapshotFile) ([]domain.TrainInstance, map[string][]domain.TrainStop, error) {
	trains := make([]domain.TrainInstance, 0, len(snapshot.Trains))
	stopsByTrain := make(map[string][]domain.TrainStop, len(snapshot.Trains))
	for _, t := range snapshot.Trains {
		dep, err := time.Parse(time.RFC3339, t.DepartureAt)
		if err != nil {
			return nil, nil, fmt.Errorf("parse departure for %s: %w", t.ID, err)
		}
		arr, err := time.Parse(time.RFC3339, t.ArrivalAt)
		if err != nil {
			return nil, nil, fmt.Errorf("parse arrival for %s: %w", t.ID, err)
		}
		if strings.TrimSpace(t.ID) == "" || strings.TrimSpace(t.ServiceDate) == "" {
			return nil, nil, fmt.Errorf("invalid train with empty id/service_date")
		}
		trains = append(trains, domain.TrainInstance{
			ID:            strings.TrimSpace(t.ID),
			ServiceDate:   strings.TrimSpace(t.ServiceDate),
			FromStation:   strings.TrimSpace(t.FromStation),
			ToStation:     strings.TrimSpace(t.ToStation),
			DepartureAt:   dep,
			ArrivalAt:     arr,
			SourceVersion: strings.TrimSpace(snapshot.SourceVersion),
		})
		trainStops := make([]domain.TrainStop, 0, len(t.Stops))
		for _, s := range t.Stops {
			if strings.TrimSpace(s.StationName) == "" {
				continue
			}
			stop := domain.TrainStop{
				TrainInstanceID: strings.TrimSpace(t.ID),
				StationName:     strings.TrimSpace(s.StationName),
				Seq:             s.Seq,
				Latitude:        s.Latitude,
				Longitude:       s.Longitude,
			}
			if s.ArrivalAt != "" {
				at, err := time.Parse(time.RFC3339, s.ArrivalAt)
				if err != nil {
					return nil, nil, fmt.Errorf("parse stop arrival for %s: %w", t.ID, err)
				}
				stop.ArrivalAt = &at
			}
			if s.DepartureAt != "" {
				dt, err := time.Parse(time.RFC3339, s.DepartureAt)
				if err != nil {
					return nil, nil, fmt.Errorf("parse stop departure for %s: %w", t.ID, err)
				}
				stop.DepartureAt = &dt
			}
			trainStops = append(trainStops, stop)
		}
		stopsByTrain[stopTrainKey(t.ID)] = trainStops
	}
	return trains, stopsByTrain, nil
}

func mergeKey(t RawTrain) string {
	if strings.TrimSpace(t.ServiceDate) == "" {
		return trainID(t)
	}
	if strings.TrimSpace(t.TrainNumber) != "" {
		return fmt.Sprintf("%s|%s", strings.TrimSpace(t.ServiceDate), strings.ToLower(strings.TrimSpace(t.TrainNumber)))
	}
	return trainID(t)
}

func WriteSnapshotAtomically(outDir string, serviceDate time.Time, snapshot SnapshotFile) (string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	finalPath := filepath.Join(outDir, serviceDate.Format("2006-01-02")+".json")
	tmpPath := finalPath + ".tmp"
	b, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(tmpPath, append(b, '\n'), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return "", err
	}
	return finalPath, nil
}

func trainID(t RawTrain) string {
	if strings.TrimSpace(t.ID) != "" {
		return strings.TrimSpace(t.ID)
	}
	if strings.TrimSpace(t.TrainNumber) != "" {
		return fmt.Sprintf("%s-train-%s", t.ServiceDate, slugPart(t.TrainNumber))
	}
	from := slugPart(t.FromStation)
	to := slugPart(t.ToStation)
	return fmt.Sprintf("%s-%s-%s-%s", t.ServiceDate, from, to, t.DepartureAt.UTC().Format("1504"))
}

func slugPart(v string) string {
	s := strings.ToLower(strings.TrimSpace(v))
	s = strings.ReplaceAll(s, "-", " ")
	s = strings.Join(strings.Fields(s), "-")
	if s == "" {
		return "x"
	}
	return s
}

func stopKey(s RawStop) string {
	if strings.TrimSpace(s.StationName) == "" {
		return ""
	}
	if s.Seq > 0 {
		return fmt.Sprintf("%04d:%s", s.Seq, strings.ToLower(strings.TrimSpace(s.StationName)))
	}
	return strings.ToLower(strings.TrimSpace(s.StationName))
}

func stopTrainKey(trainID string) string {
	return strings.TrimSpace(trainID)
}
