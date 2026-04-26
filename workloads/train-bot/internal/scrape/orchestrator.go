package scrape

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type Orchestrator struct {
	providers []Provider
	outDir    string
	minTrains int
}

type RunResult struct {
	Snapshot   SnapshotFile
	OutputPath string
	Stats      Stats
}

func NewOrchestrator(providers []Provider, outDir string, minTrains int) *Orchestrator {
	if minTrains < 1 {
		minTrains = 1
	}
	return &Orchestrator{
		providers: providers,
		outDir:    outDir,
		minTrains: minTrains,
	}
}

func (o *Orchestrator) Run(ctx context.Context, serviceDate time.Time) (RunResult, error) {
	if len(o.providers) == 0 {
		return RunResult{}, fmt.Errorf("no providers configured")
	}

	rawSchedules := make([]RawSchedule, 0, len(o.providers))
	failed := make([]string, 0)
	for _, provider := range o.providers {
		select {
		case <-ctx.Done():
			return RunResult{}, ctx.Err()
		default:
		}
		raw, err := provider.Fetch(ctx, serviceDate)
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", provider.Name(), err))
			continue
		}
		rawSchedules = append(rawSchedules, raw)
	}
	if len(rawSchedules) == 0 {
		return RunResult{}, fmt.Errorf("all providers failed: %s", strings.Join(failed, "; "))
	}

	snapshot, stats, err := BuildSnapshotFile(serviceDate, rawSchedules)
	if err != nil {
		if len(failed) > 0 {
			return RunResult{}, fmt.Errorf("build snapshot: %w (provider failures: %s)", err, strings.Join(failed, "; "))
		}
		return RunResult{}, fmt.Errorf("build snapshot: %w", err)
	}
	if stats.TrainsMerged < o.minTrains {
		return RunResult{}, fmt.Errorf("merged %d trains below minimum %d", stats.TrainsMerged, o.minTrains)
	}
	path := ""
	if strings.TrimSpace(o.outDir) != "" {
		var err error
		path, err = WriteSnapshotAtomically(o.outDir, serviceDate, snapshot)
		if err != nil {
			return RunResult{}, fmt.Errorf("write snapshot: %w", err)
		}
	}
	return RunResult{
		Snapshot:   snapshot,
		OutputPath: path,
		Stats:      stats,
	}, nil
}
