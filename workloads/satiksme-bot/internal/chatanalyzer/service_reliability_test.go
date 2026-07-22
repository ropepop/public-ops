package chatanalyzer

import (
	"context"
	"errors"
	"testing"
	"time"

	"satiksmebot/internal/model"
	"satiksmebot/internal/reports"
	"satiksmebot/internal/runtime"
)

type collectorFunc func(context.Context) (CollectionResult, error)

func (f collectorFunc) Collect(ctx context.Context) (CollectionResult, error) {
	return f(ctx)
}

func TestServiceHealthIdentifiesUnauthorizedCollectorWithoutSensitiveDetails(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t, ctx)
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	state := runtime.New(now, true, "127.0.0.1:8080")
	state.ConfigureChatAnalyzer(true, true)
	svc := NewService(ServiceOptions{
		Settings: Settings{Enabled: true},
		Store:    st,
		Collector: collectorFunc(func(context.Context) (CollectionResult, error) {
			return CollectionResult{}, ErrMTProtoSessionUnauthorized
		}),
		Analyzer:     fakeAnalyzer{},
		Catalog:      fakeCatalog{catalog: &model.Catalog{}},
		Reports:      reports.NewService(st, time.Minute, time.Minute, time.Minute),
		Now:          func() time.Time { return now },
		RuntimeState: state,
	})
	if _, err := svc.RunOnceWithResult(ctx); !errors.Is(err, ErrMTProtoSessionUnauthorized) {
		t.Fatalf("RunOnceWithResult() error = %v", err)
	}
	status := state.ChatAnalyzerStatus()
	if status.SessionState != "unauthorized" || status.LastErrorCode != "session_unauthorized" || status.ConsecutiveCollectErrors != 1 {
		t.Fatalf("chat analyzer status = %+v", status)
	}
}

func TestServiceHealthRecordsFailedModelBatchAndCircuitState(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t, ctx)
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	state := runtime.New(now, true, "127.0.0.1:8080")
	state.ConfigureChatAnalyzer(true, true)
	svc := NewService(ServiceOptions{
		Settings: Settings{Enabled: true, ModelFailureLimit: 1, ModelCircuitOpen: 10 * time.Minute},
		Store:    st,
		Collector: fakeCollector{messages: []model.ChatAnalyzerMessage{
			testMessage("chat:1", 105, 4, "test"),
		}},
		Analyzer:     fakeAnalyzer{err: errors.New("model unavailable")},
		Catalog:      fakeCatalog{catalog: &model.Catalog{}},
		Reports:      reports.NewService(st, time.Minute, time.Minute, time.Minute),
		Now:          func() time.Time { return now },
		RuntimeState: state,
	})
	result, err := svc.RunOnceWithResult(ctx)
	if err != nil {
		t.Fatalf("RunOnceWithResult() error = %v", err)
	}
	if result.Batch == nil || result.Batch.Status != model.ChatAnalyzerBatchFailed {
		t.Fatalf("batch = %+v", result.Batch)
	}
	status := state.ChatAnalyzerStatus()
	if status.LastBatchStatus != "failed" || status.LastErrorCode != "model_request_failed" || !status.ModelCircuitOpenUntil.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("chat analyzer process status = %+v", status)
	}
}
