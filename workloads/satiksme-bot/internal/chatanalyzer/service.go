package chatanalyzer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"satiksmebot/internal/model"
	"satiksmebot/internal/reports"
	"satiksmebot/internal/runtime"
	"satiksmebot/internal/store"
)

type Service struct {
	settings     Settings
	store        store.ChatAnalyzerStore
	collector    Collector
	analyzer     BatchAnalyzer
	catalog      CatalogProvider
	reports      *reports.Service
	dump         ReportDumper
	liveFetcher  LiveVehicleFetcher
	incidents    ActiveIncidentFetcher
	now          func() time.Time
	runtimeState *runtime.State

	consecutiveModelFailures int
	modelCircuitOpenUntil    time.Time
	lastStaleRecoveryAt      time.Time
	staleBatchesRecovered    int
}

type ServiceOptions struct {
	Settings     Settings
	Store        store.ChatAnalyzerStore
	Collector    Collector
	Analyzer     BatchAnalyzer
	Catalog      CatalogProvider
	Reports      *reports.Service
	Dump         ReportDumper
	LiveFetcher  LiveVehicleFetcher
	Incidents    ActiveIncidentFetcher
	Now          func() time.Time
	RuntimeState *runtime.State
}

type RunOnceResult struct {
	Collected int
	Processed bool
	RetryAt   time.Time
	Batch     *model.ChatAnalyzerBatch
}

func NewService(opts ServiceOptions) *Service {
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{
		settings:     opts.Settings.withDefaults(),
		store:        opts.Store,
		collector:    opts.Collector,
		analyzer:     opts.Analyzer,
		catalog:      opts.Catalog,
		reports:      opts.Reports,
		dump:         opts.Dump,
		liveFetcher:  opts.LiveFetcher,
		incidents:    opts.Incidents,
		now:          now,
		runtimeState: opts.RuntimeState,
	}
}

func (s *Service) Run(ctx context.Context) error {
	if s == nil || !s.settings.Enabled {
		return nil
	}
	if s.store == nil || s.collector == nil || s.analyzer == nil || s.catalog == nil || s.reports == nil {
		return fmt.Errorf("chat analyzer is enabled but not fully configured")
	}
	if err := s.recoverStaleBatches(ctx); err != nil {
		log.Printf("satiksme chat analyzer initial stale batch recovery failed: %v", err)
	}
	nextCollect := time.Time{}
	nextProcess := nextScheduledProcessAt(s.now().UTC(), s.settings)
	lastProcessAt := time.Time{}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			now := s.now().UTC()
			if err := s.recoverStaleBatches(ctx); err != nil {
				log.Printf("satiksme chat analyzer stale batch recovery failed: %v", err)
			}
			if !now.Before(nextCollect) {
				collected, err := s.collectNewMessages(ctx)
				if err != nil {
					log.Printf("satiksme chat analyzer collect failed: %v", err)
				} else if collected > 0 {
					if eventProcessAt := s.throttledProcessAt(now, lastProcessAt); nextProcess.IsZero() || eventProcessAt.Before(nextProcess) {
						nextProcess = eventProcessAt
					}
				}
				nextCollect = now.Add(s.settings.PollInterval)
			}
			var nextRetry time.Time
			if !now.Before(nextProcess) {
				processed, retryAt, _, err := s.processPendingBatch(ctx)
				if err != nil {
					log.Printf("satiksme chat analyzer pass failed: %v", err)
				}
				if processed {
					lastProcessAt = now
				}
				nextProcess = s.throttledProcessAt(nextScheduledProcessAfter(now, s.settings), lastProcessAt)
				if !retryAt.IsZero() {
					retryProcessAt := s.throttledProcessAt(retryAt, lastProcessAt)
					nextRetry = retryProcessAt
					if nextProcess.IsZero() || retryProcessAt.Before(nextProcess) {
						nextProcess = retryProcessAt
					}
				}
			}
			timer.Reset(s.nextDelay(now, nextCollect, nextProcess, nextRetry))
		}
	}
}

func (s *Service) RunOnce(ctx context.Context) error {
	_, err := s.RunOnceWithResult(ctx)
	return err
}

func (s *Service) RunOnceWithResult(ctx context.Context) (RunOnceResult, error) {
	if err := s.recoverStaleBatches(ctx); err != nil {
		return RunOnceResult{}, err
	}
	collected, err := s.collectNewMessages(ctx)
	if err != nil {
		return RunOnceResult{}, err
	}
	processed, retryAt, batch, err := s.processPendingBatch(ctx)
	return RunOnceResult{
		Collected: collected,
		Processed: processed,
		RetryAt:   retryAt,
		Batch:     batch,
	}, err
}

func (s *Service) collectNewMessages(ctx context.Context) (int, error) {
	attemptAt := s.now().UTC()
	result, err := s.collector.Collect(ctx)
	if err != nil {
		if s.runtimeState != nil {
			s.runtimeState.RecordChatAnalyzerCollection(attemptAt, 0, 0, collectorSessionState(err), analyzerErrorCode(err))
		}
		return 0, fmt.Errorf("collect telegram chat: %w", err)
	}
	checkpointMax := make(map[string]int64, len(result.CheckpointMessageIDs))
	for chatID, messageID := range result.CheckpointMessageIDs {
		if cleanChatID := strings.TrimSpace(chatID); cleanChatID != "" && messageID > 0 {
			checkpointMax[cleanChatID] = messageID
		}
	}
	for _, item := range result.Messages {
		if _, err := s.store.EnqueueChatAnalyzerMessage(ctx, item); err != nil {
			if s.runtimeState != nil {
				s.runtimeState.RecordChatAnalyzerCollection(attemptAt, 0, 0, "authorized", "storage_failed")
			}
			return 0, fmt.Errorf("enqueue Telegram chat message: %w", err)
		}
		if item.MessageID > checkpointMax[item.ChatID] {
			checkpointMax[item.ChatID] = item.MessageID
		}
	}
	for chatID, messageID := range checkpointMax {
		if err := s.store.SetChatAnalyzerCheckpoint(ctx, chatID, messageID, s.now().UTC()); err != nil {
			if s.runtimeState != nil {
				s.runtimeState.RecordChatAnalyzerCollection(attemptAt, 0, 0, "authorized", "storage_failed")
			}
			return 0, fmt.Errorf("advance Telegram chat checkpoint: %w", err)
		}
	}
	if result.SkippedStale > 0 {
		log.Printf("satiksme chat analyzer skipped %d stale Telegram message(s)", result.SkippedStale)
	}
	if s.runtimeState != nil {
		s.runtimeState.RecordChatAnalyzerCollection(attemptAt, len(result.Messages), result.SkippedStale, "authorized", "")
	}
	return len(result.Messages), nil
}

func (s *Service) processPendingBatch(ctx context.Context) (bool, time.Time, *model.ChatAnalyzerBatch, error) {
	now := s.now().UTC()
	if s.settings.MaxMessageAge > 0 {
		analysisJSON := batchOutcomeJSON("", "ignored", "message exceeded processing age limit", nil)
		expired, expiryComplete, err := s.expireStalePendingMessages(ctx, now.Add(-s.settings.MaxMessageAge), now, analysisJSON)
		if err != nil {
			s.recordMaintenance(now, "pending_expiry_failed")
			return false, time.Time{}, nil, fmt.Errorf("expire stale pending telegram chat messages: %w", err)
		}
		if s.runtimeState != nil {
			s.runtimeState.RecordChatAnalyzerExpiredPending(expired)
		}
		if expired > 0 {
			log.Printf("satiksme chat analyzer expired %d stale pending message(s)", expired)
		}
		if !expiryComplete {
			// The portable store returned a full maintenance page, so an older
			// row may still be hidden behind it. Continue cleanup on the next
			// pass instead of allowing that row to reach the model now.
			s.recordMaintenance(now, "")
			return false, time.Time{}, nil, nil
		}
	}
	pending, err := s.store.ListPendingChatAnalyzerMessages(ctx, s.settings.BatchLimit)
	if err != nil {
		s.recordMaintenance(now, "pending_list_failed")
		return false, time.Time{}, nil, fmt.Errorf("list pending telegram chat messages: %w", err)
	}
	pending, reconciled, _, err := s.reconcileCommittedActionClaims(ctx, pending, now)
	if err != nil {
		s.recordMaintenance(now, "action_reconciliation_failed")
		return reconciled > 0, time.Time{}, nil, fmt.Errorf("reconcile committed telegram chat actions: %w", err)
	}
	if len(pending) == 0 {
		s.recordMaintenance(now, "")
		return reconciled > 0, time.Time{}, nil, nil
	}

	if s.modelCircuitOpenUntil.After(now) {
		s.recordMaintenance(now, "")
		return reconciled > 0, s.modelCircuitOpenUntil, nil, nil
	}
	ready := make([]model.ChatAnalyzerMessage, 0, len(pending))
	var nextRetry time.Time
	for i := range pending {
		item := pending[i]
		if s.messageReadyForRetry(item, now) {
			ready = append(ready, pending[i])
			continue
		}
		retryAt := item.ProcessedAt.Add(s.retryDelay(messageProcessingAttempts(item)))
		if nextRetry.IsZero() || retryAt.Before(nextRetry) {
			nextRetry = retryAt
		}
	}
	if len(ready) == 0 {
		s.recordMaintenance(now, "")
		return reconciled > 0, nextRetry, nil, nil
	}

	catalog := s.catalog.Current()
	vehicles := s.fetchLiveVehicles(ctx, catalog, now)
	incidents, err := s.activeIncidents(ctx, catalog, now)
	if err != nil {
		s.recordMaintenance(now, "incident_load_failed")
		return false, time.Time{}, nil, fmt.Errorf("load incident candidates: %w", err)
	}
	s.recordMaintenance(now, "")
	batch, err := s.processBatch(ctx, catalog, vehicles, incidents, ready, now)
	if err != nil {
		if s.runtimeState != nil {
			s.runtimeState.RecordChatAnalyzerProcess(s.now().UTC(), batch.ID, "failed", batch.SelectedModel, analyzerErrorCode(err), s.modelCircuitOpenUntil)
		}
		return false, time.Time{}, nil, err
	}
	if s.runtimeState != nil {
		errorCode := ""
		if batch.Status == model.ChatAnalyzerBatchFailed {
			errorCode = analyzerErrorCodeText(batch.Error)
		}
		s.runtimeState.RecordChatAnalyzerProcess(batch.FinishedAt, batch.ID, string(batch.Status), batch.SelectedModel, errorCode, s.modelCircuitOpenUntil)
	}
	return true, time.Time{}, &batch, nil
}

func (s *Service) reconcileCommittedActionClaims(ctx context.Context, pending []model.ChatAnalyzerMessage, now time.Time) ([]model.ChatAnalyzerMessage, int, *model.ChatAnalyzerBatch, error) {
	actionIDs := make([]string, 0, len(pending))
	seen := make(map[string]struct{}, len(pending))
	var since time.Time
	for _, item := range pending {
		actionID := strings.TrimSpace(item.AppliedActionID)
		if !strings.HasPrefix(actionID, chatAnalyzerActionIDPrefix) {
			continue
		}
		if _, ok := seen[actionID]; !ok {
			seen[actionID] = struct{}{}
			actionIDs = append(actionIDs, actionID)
		}
		// MessageDate is remote-controlled and may be clock-skewed. ReceivedAt
		// and the claim's ProcessedAt are local timestamps, so consider all
		// three and retain the earliest safe event-scan boundary. This remains
		// correct even if recovery happens after the remote skew window passes.
		for _, timestamp := range []time.Time{item.MessageDate, item.ReceivedAt, item.ProcessedAt} {
			candidate := timestamp.UTC()
			if !candidate.IsZero() && (since.IsZero() || candidate.Before(since)) {
				since = candidate
			}
		}
	}
	if len(actionIDs) == 0 {
		return pending, 0, nil, nil
	}
	// Telegram message dates are supplied by the remote service and can be a
	// little ahead of this process's clock. A future lower bound would hide the
	// action event committed locally just before a crash, allowing the same
	// source message to be modeled and applied again.
	if since.After(now.UTC()) {
		since = time.Unix(0, 0).UTC()
	}
	committed, err := s.reports.CommittedActionIDs(ctx, actionIDs, since)
	if err != nil {
		return pending, 0, nil, err
	}
	remaining := make([]model.ChatAnalyzerMessage, 0, len(pending))
	reconciled := 0
	reconciliationBatchID := chatAnalyzerBatchID(now) + "-reconcile"
	for index, item := range pending {
		actionID := strings.TrimSpace(item.AppliedActionID)
		if _, ok := committed[actionID]; !ok {
			remaining = append(remaining, item)
			continue
		}
		analysisJSON := strings.TrimSpace(item.AnalysisJSON)
		if analysisJSON == "" {
			analysisJSON = batchOutcomeJSON(item.BatchID, "reconciled", "action was committed before message finalization", nil)
		}
		if err := s.mark(
			ctx,
			item.ID,
			model.ChatAnalyzerMessageApplied,
			analysisJSON,
			actionID,
			item.AppliedTargetKey,
			reconciliationBatchID,
			"",
			now,
		); err != nil {
			remaining = append(remaining, item)
			remaining = append(remaining, pending[index+1:]...)
			return remaining, reconciled, nil, err
		}
		reconciled++
	}
	if reconciled > 0 {
		log.Printf("satiksme chat analyzer reconciled %d committed action claim(s)", reconciled)
	}
	if reconciled == 0 {
		return remaining, 0, nil, nil
	}
	batch := model.ChatAnalyzerBatch{
		ID:           reconciliationBatchID,
		Status:       model.ChatAnalyzerBatchCompleted,
		StartedAt:    now,
		FinishedAt:   now,
		MessageCount: reconciled,
		AppliedCount: reconciled,
		Model:        s.modelName(),
		ResultJSON:   batchOutcomeJSON(reconciliationBatchID, "reconciled", "actions committed before message finalization", nil),
	}
	if err := s.store.SaveChatAnalyzerBatch(ctx, batch); err != nil {
		return remaining, reconciled, &batch, err
	}
	return remaining, reconciled, &batch, nil
}

func (s *Service) fetchLiveVehicles(ctx context.Context, catalog *model.Catalog, now time.Time) []model.LiveVehicle {
	if s.liveFetcher == nil {
		return nil
	}
	fetchCtx, cancel := context.WithTimeout(ctx, s.settings.LiveVehicleFetchTimeout)
	defer cancel()
	vehicles, err := s.liveFetcher(fetchCtx, catalog, now)
	if err != nil {
		log.Printf("satiksme chat analyzer live vehicle candidates unavailable: %v", err)
		return nil
	}
	return vehicles
}

func (s *Service) activeIncidents(ctx context.Context, catalog *model.Catalog, now time.Time) ([]model.IncidentSummary, error) {
	if s.incidents != nil {
		return s.incidents(ctx, catalog, now)
	}
	return s.reports.ListActiveIncidents(ctx, catalog, now, 0, 50)
}

type batchMessageOutcome struct {
	status           model.ChatAnalyzerMessageStatus
	analysisJSON     string
	appliedActionID  string
	appliedTargetKey string
	lastError        string
}

func (s *Service) processBatch(ctx context.Context, catalog *model.Catalog, vehicles []model.LiveVehicle, incidents []model.IncidentSummary, messages []model.ChatAnalyzerMessage, now time.Time) (model.ChatAnalyzerBatch, error) {
	if s.modelCircuitOpenUntil.After(now) {
		return model.ChatAnalyzerBatch{}, fmt.Errorf("model circuit is open until %s", s.modelCircuitOpenUntil.Format(time.RFC3339))
	}
	if len(messages) == 0 {
		return model.ChatAnalyzerBatch{}, nil
	}
	batchID := chatAnalyzerBatchID(now)
	batch := model.ChatAnalyzerBatch{
		ID:           batchID,
		Status:       model.ChatAnalyzerBatchRunning,
		DryRun:       s.settings.DryRun,
		StartedAt:    now,
		MessageCount: len(messages),
		Model:        s.modelName(),
	}
	_, canRecoverRunningBatch := s.store.(store.ChatAnalyzerBatchRecoveryStore)
	if canRecoverRunningBatch {
		if err := s.store.SaveChatAnalyzerBatch(ctx, batch); err != nil {
			return model.ChatAnalyzerBatch{}, fmt.Errorf("save chat analyzer batch start: %w", err)
		}
	}
	terminalSaved := false
	defer func() {
		if terminalSaved {
			return
		}
		batch.Status = model.ChatAnalyzerBatchFailed
		batch.FinishedAt = s.now().UTC()
		if batch.Error == "" {
			batch.Error = "interrupted before completion"
		}
		if batch.ErrorCount == 0 {
			batch.ErrorCount = len(messages)
		}
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		_ = s.store.SaveChatAnalyzerBatch(saveCtx, batch)
	}()

	stopDirectory := BuildStopDirectory(catalog)
	items := make([]BatchItem, 0, len(messages))
	for _, item := range messages {
		items = append(items, BatchItem{
			Message:       item,
			Candidates:    BuildCandidateContext(catalog, vehicles, incidents, item.Text),
			StopDirectory: stopDirectory,
		})
	}
	decision, raw, selectedModel, err := s.analyzer.AnalyzeBatch(ctx, items, incidents)
	batch.SelectedModel = strings.TrimSpace(selectedModel)
	if err != nil {
		s.recordModelFailure(now)
		batch.Status = model.ChatAnalyzerBatchFailed
		batch.FinishedAt = s.now().UTC()
		batch.Error = err.Error()
		batch.ErrorCount = len(messages)
		messageOutcomes := make([]store.ChatAnalyzerMessageOutcome, 0, len(messages))
		for _, item := range messages {
			messageOutcomes = append(messageOutcomes, store.ChatAnalyzerMessageOutcome{
				ID:               item.ID,
				Status:           model.ChatAnalyzerMessagePending,
				AnalysisJSON:     analysisJSONWithProcessingAttempt(batchOutcomeJSON(batchID, "model_error", err.Error(), nil), nextMessageProcessingAttempt(item)),
				AppliedActionID:  item.AppliedActionID,
				AppliedTargetKey: item.AppliedTargetKey,
				BatchID:          batchID,
				LastError:        err.Error(),
				ProcessedAt:      batch.FinishedAt,
			})
		}
		if finalizeErr := s.finalizeBatch(ctx, batch, messageOutcomes); finalizeErr != nil {
			return batch, fmt.Errorf("finalize failed chat analyzer batch: %w", finalizeErr)
		}
		terminalSaved = true
		return batch, nil
	}
	s.resetModelFailures()
	if batch.SelectedModel == "" {
		batch.SelectedModel = strings.TrimSpace(decision.ModelMeta.SelectedModel)
	}
	batch.ReportCount = len(decision.Reports)
	batch.VoteCount = len(decision.Votes)
	batch.IgnoredCount = len(decision.Ignored)
	batch.ResultJSON = raw

	initialSourcesValid := validateUniqueBatchDecisionSources(items, decision) == nil
	if reasoner, ok := s.analyzer.(LocationReasoningAnalyzer); ok && initialSourcesValid {
		reasoningItems := items
		if freshVehicles := s.fetchLiveVehicles(ctx, catalog, s.now().UTC()); len(freshVehicles) > 0 {
			reasoningItems = rebuildBatchItemsWithVehicles(catalog, freshVehicles, incidents, items, stopDirectory)
		}
		reasoningItems, recheckIDs := locationReasoningItems(reasoningItems, decision)
		if len(recheckIDs) > 0 {
			reasoned, reasonedRaw, reasonedModel, reasonErr := reasoner.DeduceLocations(ctx, reasoningItems, incidents, decision, recheckIDs)
			if reasonErr != nil {
				log.Printf("satiksme chat analyzer location reasoning failed: %v", reasonErr)
			} else {
				decision = mergeLocationReasoningDecision(decision, reasoned, recheckIDs)
				items = reasoningItems
				if strings.TrimSpace(reasonedModel) != "" {
					batch.SelectedModel = strings.TrimSpace(reasonedModel)
				}
				batch.ReportCount = len(decision.Reports)
				batch.VoteCount = len(decision.Votes)
				batch.IgnoredCount = len(decision.Ignored)
				batch.ResultJSON = combinedBatchResultJSON(raw, reasonedRaw)
			}
		}
	}

	var outcomes map[int64]batchMessageOutcome
	var stats batchDecisionStats
	if sourceErr := validateUniqueBatchDecisionSources(items, decision); sourceErr != nil {
		outcomes = make(map[int64]batchMessageOutcome, len(items))
		stats.errors = len(items)
		for _, item := range items {
			outcomes[item.Message.MessageID] = batchMessageOutcome{
				status:       model.ChatAnalyzerMessageUncertain,
				analysisJSON: batchOutcomeJSON(batchID, "invalid_model_output", sourceErr.Error(), nil),
				lastError:    sourceErr.Error(),
			}
		}
	} else {
		outcomes, stats = s.evaluateBatchDecisions(ctx, catalog, incidents, items, decision, batchID, now)
		s.addFallbackOmittedSignalOutcomes(ctx, catalog, items, outcomes, &stats, batchID, now)
	}
	batch.WouldApply = stats.wouldApply
	batch.AppliedCount = stats.applied
	batch.ErrorCount = stats.errors
	batch.Status = model.ChatAnalyzerBatchCompleted
	batch.FinishedAt = s.now().UTC()
	messageOutcomes := make([]store.ChatAnalyzerMessageOutcome, 0, len(messages))
	for _, item := range messages {
		outcome, ok := outcomes[item.MessageID]
		if !ok {
			outcome = batchMessageOutcome{
				status:       model.ChatAnalyzerMessageIgnored,
				analysisJSON: batchOutcomeJSON(batchID, "ignored", "model returned no decision", nil),
				lastError:    "model returned no decision",
			}
		}
		messageOutcomes = append(messageOutcomes, store.ChatAnalyzerMessageOutcome{
			ID:               item.ID,
			Status:           outcome.status,
			AnalysisJSON:     analysisJSONWithProcessingAttempt(outcome.analysisJSON, nextMessageProcessingAttempt(item)),
			AppliedActionID:  outcome.appliedActionID,
			AppliedTargetKey: outcome.appliedTargetKey,
			BatchID:          batchID,
			LastError:        outcome.lastError,
			ProcessedAt:      batch.FinishedAt,
		})
	}
	if err := s.finalizeBatch(ctx, batch, messageOutcomes); err != nil {
		return batch, fmt.Errorf("finalize completed chat analyzer batch: %w", err)
	}
	terminalSaved = true
	return batch, nil
}

func (s *Service) finalizeBatch(ctx context.Context, batch model.ChatAnalyzerBatch, outcomes []store.ChatAnalyzerMessageOutcome) error {
	if finalizer, ok := s.store.(store.ChatAnalyzerBatchFinalizer); ok {
		return finalizer.FinalizeChatAnalyzerBatch(ctx, batch, outcomes)
	}
	// Older stores do not expose a multi-row transaction. Persist every message
	// outcome first, then the terminal batch record. This ordering can leave a
	// failed or absent batch after interruption, but never a completed batch
	// that still advertises pending messages.
	for _, outcome := range outcomes {
		if err := s.mark(ctx, outcome.ID, outcome.Status, outcome.AnalysisJSON, outcome.AppliedActionID, outcome.AppliedTargetKey, outcome.BatchID, outcome.LastError, outcome.ProcessedAt); err != nil {
			return err
		}
	}
	return s.store.SaveChatAnalyzerBatch(ctx, batch)
}

func (s *Service) recoverStaleBatches(ctx context.Context) error {
	if s == nil {
		return nil
	}
	now := s.now().UTC()
	interval := s.settings.BatchStaleAfter / 2
	if interval <= 0 || interval > 5*time.Minute {
		interval = 5 * time.Minute
	}
	if !s.lastStaleRecoveryAt.IsZero() && now.Sub(s.lastStaleRecoveryAt) < interval {
		return nil
	}
	recoveryStore, ok := s.store.(store.ChatAnalyzerBatchRecoveryStore)
	if !ok {
		// Spacetime production does not yet expose a batch-list or bulk-recovery
		// procedure. processBatch therefore stores terminal records only, so a
		// hard process interruption cannot create a new stale running record.
		s.lastStaleRecoveryAt = now
		s.recordMaintenance(now, "")
		return nil
	}
	updated, err := recoveryStore.FailStaleChatAnalyzerBatches(ctx, now.Add(-s.settings.BatchStaleAfter), now, "interrupted before completion")
	if err != nil {
		s.recordMaintenance(now, "stale_batch_recovery_failed")
		return fmt.Errorf("recover stale chat analyzer batches: %w", err)
	}
	s.recordMaintenance(now, "")
	s.lastStaleRecoveryAt = now
	s.staleBatchesRecovered += updated
	if s.runtimeState != nil {
		s.runtimeState.RecordChatAnalyzerStaleRecovery(updated)
	}
	if updated > 0 {
		log.Printf("satiksme chat analyzer recovered %d interrupted batch record(s)", updated)
	}
	return nil
}

const portableExpiryLimit = 5000

func (s *Service) expireStalePendingMessages(ctx context.Context, messageDateBefore, processedAt time.Time, analysisJSON string) (int, bool, error) {
	if expiryStore, ok := s.store.(store.ChatAnalyzerMessageExpiryStore); ok {
		expired, err := expiryStore.ExpireStaleChatAnalyzerMessages(ctx, messageDateBefore, processedAt, analysisJSON)
		return expired, true, err
	}

	// The deployed Spacetime module already provides these two operations. A
	// generous bounded read avoids requiring a schema publish merely to expire
	// old pending records. Any overflow remains pending and is revisited after
	// normal queue progress exposes it in a later maintenance pass.
	pending, err := s.store.ListPendingChatAnalyzerMessages(ctx, portableExpiryLimit)
	if err != nil {
		return 0, false, err
	}
	updated := 0
	for _, item := range pending {
		if item.MessageDate.IsZero() || !item.MessageDate.Before(messageDateBefore) {
			continue
		}
		if err := s.store.MarkChatAnalyzerMessageProcessed(
			ctx,
			item.ID,
			model.ChatAnalyzerMessageIgnored,
			strings.TrimSpace(analysisJSON),
			"",
			"",
			"",
			processedAt,
		); err != nil {
			return updated, false, err
		}
		updated++
	}
	return updated, len(pending) < portableExpiryLimit, nil
}

func (s *Service) recordMaintenance(at time.Time, errorCode string) {
	if s != nil && s.runtimeState != nil {
		s.runtimeState.RecordChatAnalyzerMaintenance(at, errorCode)
	}
}

func collectorSessionState(err error) string {
	if errors.Is(err, ErrMTProtoSessionUnauthorized) {
		return "unauthorized"
	}
	return "error"
}

func analyzerErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrMTProtoSessionUnauthorized) {
		return "session_unauthorized"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var statusErr *modelHTTPError
	if errors.As(err, &statusErr) {
		return fmt.Sprintf("model_http_%d", statusErr.StatusCode)
	}
	var outputErr *modelOutputError
	if errors.As(err, &outputErr) {
		return "model_output_invalid"
	}
	return analyzerErrorCodeText(err.Error())
}

func analyzerErrorCodeText(message string) string {
	clean := strings.ToLower(strings.TrimSpace(message))
	switch {
	case strings.Contains(clean, "not authorized"):
		return "session_unauthorized"
	case strings.Contains(clean, "429"):
		return "model_rate_limited"
	case strings.Contains(clean, "model endpoint returned 5"):
		return "model_unavailable"
	case strings.Contains(clean, "invalid batch decision json") ||
		strings.Contains(clean, "decode model response") ||
		strings.Contains(clean, "model response had no choices"):
		return "model_output_invalid"
	case strings.Contains(clean, "deadline") || strings.Contains(clean, "timeout"):
		return "timeout"
	case strings.Contains(clean, "circuit"):
		return "model_circuit_open"
	case strings.Contains(clean, "model"):
		return "model_request_failed"
	default:
		return "processing_failed"
	}
}

type appliedDecision struct {
	actionID   string
	targetKey  string
	incidentID string
}

func (s *Service) applyDecision(ctx context.Context, catalog *model.Catalog, item model.ChatAnalyzerMessage, decision Decision, target validatedTarget, now time.Time) (appliedDecision, error) {
	return s.applyDecisionWithActionID(ctx, catalog, item, decision, target, "", item.MessageDate, now)
}

func (s *Service) applyDecisionWithActionID(ctx context.Context, catalog *model.Catalog, item model.ChatAnalyzerMessage, decision Decision, target validatedTarget, actionID string, idempotencySince, now time.Time) (appliedDecision, error) {
	planned := appliedDecision{
		actionID:   strings.TrimSpace(actionID),
		targetKey:  target.dedupeKey,
		incidentID: target.incidentID,
	}
	userID, err := reporterUserID(item)
	if err != nil {
		return planned, err
	}
	if idempotencySince.IsZero() {
		idempotencySince = item.MessageDate
	}
	switch {
	case decision.TargetType == TargetStop && (decision.Action == ActionSighting || decision.Action == ActionNotice || decision.Action == ActionConfirmation):
		result, sighting, err := s.reports.SubmitStopSightingWithOptions(ctx, userID, target.stop.ID, now, reports.SubmitOptions{
			Source:           model.IncidentVoteSourceTelegramChat,
			IdempotencyID:    planned.actionID,
			IdempotencyKey:   item.ID,
			IdempotencySince: idempotencySince,
		})
		if err != nil {
			return planned, err
		}
		if !result.Accepted {
			return planned, reportResultError(result)
		}
		if sighting == nil {
			return planned, fmt.Errorf("accepted stop report did not return a sighting")
		}
		if !result.Reconciled {
			s.enqueueDumpForStop(target.stop, sighting)
		}
		return appliedDecision{actionID: sighting.ID, targetKey: "sighting:stop:" + strings.TrimSpace(sighting.StopID), incidentID: result.IncidentID}, nil
	case decision.TargetType == TargetVehicle && (decision.Action == ActionSighting || decision.Action == ActionNotice || decision.Action == ActionConfirmation):
		result, sighting, err := s.reports.SubmitVehicleSightingWithOptions(ctx, userID, model.VehicleReportInput{
			Mode:             target.vehicle.Mode,
			RouteLabel:       target.vehicle.RouteLabel,
			Direction:        target.vehicle.Direction,
			Destination:      target.vehicle.Destination,
			DepartureSeconds: target.vehicle.DepartureSeconds,
			LiveRowID:        target.vehicle.LiveRowID,
		}, now, reports.SubmitOptions{
			Source:           model.IncidentVoteSourceTelegramChat,
			IdempotencyID:    planned.actionID,
			IdempotencyKey:   item.ID,
			IdempotencySince: idempotencySince,
		})
		if err != nil {
			return planned, err
		}
		if !result.Accepted {
			return planned, reportResultError(result)
		}
		if sighting == nil {
			return planned, fmt.Errorf("accepted vehicle report did not return a sighting")
		}
		if !result.Reconciled {
			s.enqueueDumpForVehicle(sighting)
		}
		return appliedDecision{actionID: sighting.ID, targetKey: "sighting:vehicle:" + vehicleSightingTargetScope(sighting), incidentID: result.IncidentID}, nil
	case decision.TargetType == TargetArea && (decision.Action == ActionSighting || decision.Action == ActionNotice || decision.Action == ActionConfirmation):
		result, areaReport, err := s.reports.SubmitAreaReportWithOptions(ctx, userID, model.AreaReportInput{
			Latitude:     target.area.Latitude,
			Longitude:    target.area.Longitude,
			RadiusMeters: areaRadiusForConfidence(target.area.RadiusMeters, decision.Confidence),
			Description:  areaPublicDescription(target.area),
		}, now, reports.SubmitOptions{
			Source:           model.IncidentVoteSourceTelegramChat,
			IdempotencyID:    planned.actionID,
			IdempotencyKey:   item.ID,
			IdempotencySince: idempotencySince,
		})
		if err != nil {
			return planned, err
		}
		if !result.Accepted {
			return planned, reportResultError(result)
		}
		if areaReport == nil {
			return planned, fmt.Errorf("accepted area report did not return a report")
		}
		return appliedDecision{actionID: areaReport.ID, targetKey: "sighting:area:" + areaReportTargetScope(areaReport), incidentID: result.IncidentID}, nil
	case decision.Action == ActionConfirmation:
		if planned.actionID == "" {
			planned.actionID = item.ID
		}
		_, err := s.reports.RecordIncidentVoteFromSource(ctx, catalog, target.incidentID, userID, model.IncidentVoteOngoing, model.IncidentVoteSourceTelegramChat, planned.actionID, now)
		return planned, err
	case decision.Action == ActionDenial || decision.Action == ActionCleared:
		if planned.actionID == "" {
			planned.actionID = item.ID
		}
		_, err := s.reports.RecordIncidentVoteFromSource(ctx, catalog, target.incidentID, userID, model.IncidentVoteCleared, model.IncidentVoteSourceTelegramChat, planned.actionID, now)
		return planned, err
	default:
		return planned, fmt.Errorf("unsupported validated action %q for target %q", decision.Action, decision.TargetType)
	}
}

const chatAnalyzerActionIDPrefix = "chat-action-"

func (s *Service) applyClaimedDecision(ctx context.Context, catalog *model.Catalog, sources []BatchItem, decision Decision, target validatedTarget, batchID, analysisJSON string, now time.Time) (appliedDecision, error) {
	if len(sources) == 0 {
		return appliedDecision{}, fmt.Errorf("source messages are required")
	}
	actionID := chatAnalyzerActionID(batchID, decision, target, sources)
	planned := appliedDecision{
		actionID:   actionID,
		targetKey:  target.dedupeKey,
		incidentID: target.incidentID,
	}
	for _, source := range sources {
		if strings.TrimSpace(source.Message.ID) == "" {
			return planned, fmt.Errorf("source message identity is required")
		}
		if err := s.mark(
			ctx,
			source.Message.ID,
			model.ChatAnalyzerMessagePending,
			analysisJSONWithProcessingAttempt(analysisJSON, nextMessageProcessingAttempt(source.Message)),
			actionID,
			target.dedupeKey,
			batchID,
			"",
			now,
		); err != nil {
			return planned, fmt.Errorf("persist report action claim: %w", err)
		}
	}
	return s.applyDecisionWithActionID(ctx, catalog, sources[0].Message, decision, target, actionID, earliestSourceMessageDate(sources), now)
}

func chatAnalyzerActionID(batchID string, decision Decision, target validatedTarget, sources []BatchItem) string {
	identities := make([]string, 0, len(sources))
	for _, source := range sources {
		identity := strings.TrimSpace(source.Message.ID)
		if identity == "" {
			identity = fmt.Sprintf("%s:%d", strings.TrimSpace(source.Message.ChatID), source.Message.MessageID)
		}
		identities = append(identities, identity)
	}
	sort.Strings(identities)
	payload := strings.Join([]string{
		"satiksme-chat-action-v1",
		strings.TrimSpace(batchID),
		strings.TrimSpace(decision.Action),
		strings.TrimSpace(decision.TargetType),
		strings.TrimSpace(decision.TargetID),
		strings.TrimSpace(target.incidentID),
		strings.TrimSpace(target.dedupeKey),
		strings.Join(identities, "\x00"),
	}, "\x00")
	sum := sha256.Sum256([]byte(payload))
	return chatAnalyzerActionIDPrefix + hex.EncodeToString(sum[:12])
}

func earliestSourceMessageDate(sources []BatchItem) time.Time {
	var earliest time.Time
	for _, source := range sources {
		candidate := source.Message.MessageDate.UTC()
		if candidate.IsZero() {
			candidate = source.Message.ReceivedAt.UTC()
		}
		if candidate.IsZero() {
			continue
		}
		if earliest.IsZero() || candidate.Before(earliest) {
			earliest = candidate
		}
	}
	return earliest
}

func vehicleSightingTargetScope(sighting *model.VehicleSighting) string {
	if sighting == nil {
		return ""
	}
	// Stored scope keys can be v1-compatible opaque aliases. Reconstruct the
	// logical private vehicle scope so analyzer dedupe remains stable.
	return reports.VehicleScopeKey(model.VehicleReportInput{
		Mode:             sighting.Mode,
		RouteLabel:       sighting.RouteLabel,
		Direction:        sighting.Direction,
		Destination:      sighting.Destination,
		DepartureSeconds: sighting.DepartureSeconds,
		LiveRowID:        sighting.LiveRowID,
	})
}

func areaReportTargetScope(areaReport *model.AreaReport) string {
	if areaReport == nil {
		return ""
	}
	scopeKey := strings.ToLower(strings.TrimSpace(areaReport.ScopeKey))
	opaqueScope := false
	if len(scopeKey) == len("pub-")+8 && strings.HasPrefix(scopeKey, "pub-") {
		_, decodeErr := hex.DecodeString(strings.TrimPrefix(scopeKey, "pub-"))
		opaqueScope = decodeErr == nil
	}
	if scopeKey != "" && !opaqueScope {
		return scopeKey
	}
	return reports.AreaScopeKey(model.AreaReportInput{
		Latitude:     areaReport.Latitude,
		Longitude:    areaReport.Longitude,
		RadiusMeters: areaReport.RadiusMeters,
		Description:  areaReport.Description,
	})
}

func rebuildBatchItemsWithVehicles(catalog *model.Catalog, vehicles []model.LiveVehicle, incidents []model.IncidentSummary, items []BatchItem, stopDirectory []StopCandidate) []BatchItem {
	out := make([]BatchItem, 0, len(items))
	for _, item := range items {
		out = append(out, BatchItem{
			Message:       item.Message,
			Candidates:    BuildCandidateContext(catalog, vehicles, incidents, item.Message.Text),
			StopDirectory: stopDirectory,
		})
	}
	return out
}

func (s *Service) enqueueDumpForStop(stop StopCandidate, sighting *model.StopSighting) {
	if s == nil || s.dump == nil || sighting == nil || sighting.Hidden {
		return
	}
	s.dump.EnqueueStop(model.Stop{
		ID:          strings.TrimSpace(stop.ID),
		Name:        strings.TrimSpace(stop.Name),
		Modes:       append([]string(nil), stop.Modes...),
		RouteLabels: append([]string(nil), stop.RouteLabels...),
	}, sighting)
}

func (s *Service) enqueueDumpForVehicle(sighting *model.VehicleSighting) {
	if s == nil || s.dump == nil || sighting == nil || sighting.Hidden {
		return
	}
	s.dump.EnqueueVehicle(sighting)
}

type reportResultApplicationError struct {
	message string
}

func (e *reportResultApplicationError) Error() string {
	return e.message
}

func reportResultError(result model.ReportResult) error {
	switch {
	case result.Deduped:
		return &reportResultApplicationError{message: "duplicate report"}
	case result.RateLimited:
		return &reportResultApplicationError{message: fmt.Sprintf("rate limited: %s", result.Reason)}
	default:
		return &reportResultApplicationError{message: "report was not accepted"}
	}
}

func claimedActionFailureOutcome(applied appliedDecision, analysisJSON string, err error) batchMessageOutcome {
	outcome := batchMessageOutcome{
		status:           model.ChatAnalyzerMessagePending,
		analysisJSON:     analysisJSON,
		appliedActionID:  applied.actionID,
		appliedTargetKey: applied.targetKey,
	}
	if err != nil {
		outcome.lastError = err.Error()
	}
	var rejected *reportResultApplicationError
	var rateLimited *reports.RateLimitError
	if errors.As(err, &rejected) || errors.As(err, &rateLimited) {
		outcome.status = model.ChatAnalyzerMessageUncertain
		outcome.appliedActionID = ""
		outcome.appliedTargetKey = ""
	}
	return outcome
}

func areaRadiusForConfidence(base int, confidence float64) int {
	radius := base
	if radius <= 0 {
		radius = 250
	}
	if confidence < 0.88 && radius < 500 {
		radius = 500
	} else if confidence < 0.94 && radius < 250 {
		radius = 250
	}
	if radius > 500 {
		radius = 500
	}
	return radius
}

func areaReportDescription(text, fallback string) string {
	clean := strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if clean == "" {
		clean = strings.Join(strings.Fields(strings.TrimSpace(fallback)), " ")
	}
	if clean == "" {
		return "approximate inspection area"
	}
	runes := []rune(clean)
	if len(runes) <= 160 {
		return clean
	}
	return string(runes[:157]) + "..."
}

func areaPublicDescription(area AreaCandidate) string {
	label := strings.Join(strings.Fields(strings.TrimSpace(area.Label)), " ")
	if label == "" && len(area.Anchors) > 0 {
		anchors := make([]string, 0, len(area.Anchors))
		for _, anchor := range area.Anchors {
			if clean := strings.Join(strings.Fields(strings.TrimSpace(anchor)), " "); clean != "" {
				anchors = append(anchors, clean)
			}
		}
		if len(anchors) > 0 {
			label = "near " + strings.Join(anchors, " / ")
		}
	}
	return areaReportDescription(label, "approximate inspection area")
}

const clearActionMinConfidence = 0.94
const sameReportClearMinConfidence = 0.90
const areaReportMinConfidence = 0.80

func confidenceThresholdForAction(action string, minConfidence float64) float64 {
	if minConfidence <= 0 {
		minConfidence = 0.82
	}
	if isClearAction(action) {
		if minConfidence > clearActionMinConfidence {
			return minConfidence
		}
		return clearActionMinConfidence
	}
	return minConfidence
}

func confidenceThresholdForReport(decision Decision, minConfidence float64) float64 {
	threshold := confidenceThresholdForAction(decision.Action, minConfidence)
	if strings.ToLower(strings.TrimSpace(decision.TargetType)) == TargetArea && threshold > areaReportMinConfidence {
		return areaReportMinConfidence
	}
	return threshold
}

func confidenceThresholdForVote(decision Decision, minConfidence float64, cleanStatus bool) float64 {
	threshold := confidenceThresholdForAction(decision.Action, minConfidence)
	if cleanStatus &&
		isClearAction(decision.Action) &&
		strings.ToLower(strings.TrimSpace(decision.TargetType)) == "report" &&
		threshold > sameReportClearMinConfidence {
		return sameReportClearMinConfidence
	}
	return threshold
}

func isClearAction(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case ActionCleared, ActionDenial:
		return true
	default:
		return false
	}
}

func sourcesContainNoInspectionStatus(sources []BatchItem) bool {
	for _, source := range sources {
		if looksLikeNoInspectionStatus(source.Message.Text) {
			return true
		}
	}
	return false
}

func looksLikeNoInspectionStatus(text string) bool {
	clean := normalizeText(text)
	if clean == "" {
		return false
	}
	tokens := strings.Fields(clean)
	dirty := map[string]struct{}{
		"dirty":   {},
		"griazno": {},
		"grjazno": {},
		"netirs":  {},
		"netira":  {},
		"netiru":  {},
		"netiri":  {},
		"netiras": {},
	}
	for _, token := range tokens {
		if _, ok := dirty[token]; ok {
			return false
		}
	}
	for _, token := range tokens {
		switch token {
		case "clean", "tirs", "tira", "tiri", "tiras", "chisto", "cisto", "ok", "good", "gud":
			return true
		case "gone", "prom", "aizbrauca", "aizbrauc", "aizgaja", "aiziet", "izkapa", "izkap", "visli", "vysli", "uehal", "uehali", "usli":
			return true
		case "empty", "pusto", "pusts", "tukss", "tuksa":
			return true
		}
		if strings.HasPrefix(token, "cist") {
			return true
		}
	}
	return noControlPhrase(tokens)
}

func noControlPhrase(tokens []string) bool {
	hasAbsent := false
	hasControl := false
	for _, token := range tokens {
		switch token {
		case "no", "not", "nav", "bez", "net", "netu":
			hasAbsent = true
		}
		if strings.HasPrefix(token, "kontrol") ||
			strings.HasPrefix(token, "controller") ||
			strings.HasPrefix(token, "inspection") ||
			strings.HasPrefix(token, "parbaud") {
			hasControl = true
		}
	}
	return hasAbsent && hasControl
}

func cleanVoteHasGroundedTarget(sources []BatchItem, decision Decision, incidents []model.IncidentSummary, reportRefs map[string]batchReportRef) bool {
	switch strings.ToLower(strings.TrimSpace(decision.TargetType)) {
	case TargetIncident:
		_, err := validateActiveIncident(decision.TargetID, incidents, decision.Action)
		return err == nil
	case "report":
		ref, ok := reportRefs[strings.TrimSpace(decision.TargetID)]
		if !ok {
			return false
		}
		return cleanVoteReferencesReport(sources, ref)
	default:
		return false
	}
}

func cleanVoteReferencesReport(sources []BatchItem, ref batchReportRef) bool {
	reportSourceIDs := make(map[int64]struct{}, len(ref.sourceMessageIDs))
	for _, id := range ref.sourceMessageIDs {
		reportSourceIDs[id] = struct{}{}
	}
	for _, source := range sources {
		if source.Message.ReplyToMessageID != 0 {
			if _, ok := reportSourceIDs[source.Message.ReplyToMessageID]; ok {
				return true
			}
		}
		if candidateContextsOverlap(source.Candidates, ref.sourceCandidates) {
			return true
		}
	}
	return false
}

func sourceCandidateContext(sources []BatchItem) CandidateContext {
	var candidates CandidateContext
	for _, source := range sources {
		candidates.Stops = append(candidates.Stops, source.Candidates.Stops...)
		candidates.Vehicles = append(candidates.Vehicles, source.Candidates.Vehicles...)
		candidates.Areas = append(candidates.Areas, source.Candidates.Areas...)
		candidates.Incidents = append(candidates.Incidents, source.Candidates.Incidents...)
	}
	return dedupeCandidates(candidates)
}

func candidateContextsOverlap(left, right CandidateContext) bool {
	if stopCandidatesOverlap(left.Stops, right.Stops) {
		return true
	}
	if vehicleCandidatesOverlap(left.Vehicles, right.Vehicles) {
		return true
	}
	if areaCandidatesOverlap(left.Areas, right.Areas) {
		return true
	}
	if incidentCandidatesOverlap(left.Incidents, right.Incidents) {
		return true
	}
	return false
}

func stopCandidatesOverlap(left, right []StopCandidate) bool {
	seen := make(map[string]struct{}, len(left))
	for _, item := range left {
		if item.Score <= 0 {
			continue
		}
		if id := strings.TrimSpace(item.ID); id != "" {
			seen[id] = struct{}{}
		}
	}
	for _, item := range right {
		if item.Score <= 0 {
			continue
		}
		if _, ok := seen[strings.TrimSpace(item.ID)]; ok {
			return true
		}
	}
	return false
}

func vehicleCandidatesOverlap(left, right []VehicleCandidate) bool {
	seen := make(map[string]struct{}, len(left))
	for _, item := range left {
		if item.Score <= 0 {
			continue
		}
		if id := strings.TrimSpace(item.ID); id != "" {
			seen[id] = struct{}{}
		}
	}
	for _, item := range right {
		if item.Score <= 0 {
			continue
		}
		if _, ok := seen[strings.TrimSpace(item.ID)]; ok {
			return true
		}
	}
	return false
}

func areaCandidatesOverlap(left, right []AreaCandidate) bool {
	seen := make(map[string]struct{}, len(left))
	for _, item := range left {
		if item.Score <= 0 {
			continue
		}
		if id := strings.TrimSpace(item.ID); id != "" {
			seen[id] = struct{}{}
		}
	}
	for _, item := range right {
		if item.Score <= 0 {
			continue
		}
		if _, ok := seen[strings.TrimSpace(item.ID)]; ok {
			return true
		}
	}
	return false
}

func incidentCandidatesOverlap(left, right []IncidentCandidate) bool {
	seen := make(map[string]struct{}, len(left))
	for _, item := range left {
		if item.Score <= 0 {
			continue
		}
		if id := strings.TrimSpace(item.ID); id != "" {
			seen[id] = struct{}{}
		}
	}
	for _, item := range right {
		if item.Score <= 0 {
			continue
		}
		if _, ok := seen[strings.TrimSpace(item.ID)]; ok {
			return true
		}
	}
	return false
}

func fallbackOmittedSignalDecision(item BatchItem) (Decision, bool) {
	text := item.Message.Text
	if !looksLikeTransportSignal(text) || looksLikeNoInspectionStatus(text) {
		return Decision{}, false
	}
	if len(item.Candidates.Vehicles) > 0 && item.Candidates.Vehicles[0].Score >= 20 {
		return Decision{
			Action:     ActionSighting,
			TargetType: TargetVehicle,
			TargetID:   item.Candidates.Vehicles[0].ID,
			Confidence: 0.90,
			Reason:     "fallback for omitted transport signal",
		}, true
	}
	if len(item.Candidates.Areas) > 0 && item.Candidates.Areas[0].Score >= 20 {
		return Decision{
			Action:     ActionSighting,
			TargetType: TargetArea,
			TargetID:   item.Candidates.Areas[0].ID,
			Confidence: 0.90,
			Reason:     "fallback for omitted transport signal",
		}, true
	}
	if len(item.Candidates.Stops) > 0 && item.Candidates.Stops[0].Score >= 20 {
		stop := item.Candidates.Stops[0]
		if ambiguousStopReportNeedsArea(text, stop, item.Candidates) {
			for _, area := range item.Candidates.Areas {
				if area.Score >= 20 {
					return Decision{
						Action:     ActionSighting,
						TargetType: TargetArea,
						TargetID:   area.ID,
						Confidence: 0.90,
						Reason:     "fallback for omitted transport signal",
					}, true
				}
			}
			return Decision{}, false
		}
		return Decision{
			Action:     ActionSighting,
			TargetType: TargetStop,
			TargetID:   stop.ID,
			Confidence: 0.90,
			Reason:     "fallback for omitted transport signal",
		}, true
	}
	return Decision{}, false
}

type batchDecisionStats struct {
	wouldApply int
	applied    int
	errors     int
}

type batchReportRef struct {
	incidentID       string
	dedupeKey        string
	sourceMessageIDs []int64
	sourceCandidates CandidateContext
}

func validateUniqueBatchDecisionSources(items []BatchItem, decision BatchDecision) error {
	batchIDs := make(map[int64]struct{}, len(items))
	for _, item := range items {
		messageID := item.Message.MessageID
		if _, exists := batchIDs[messageID]; exists {
			return fmt.Errorf("batch contains duplicate source message %d", messageID)
		}
		batchIDs[messageID] = struct{}{}
	}

	claimedBy := make(map[int64]string, len(items))
	claim := func(messageID int64, location string) error {
		if _, exists := batchIDs[messageID]; !exists {
			return fmt.Errorf("%s references source message %d outside the batch", location, messageID)
		}
		if previous, exists := claimedBy[messageID]; exists {
			return fmt.Errorf("source message %d appears more than once (%s and %s)", messageID, previous, location)
		}
		claimedBy[messageID] = location
		return nil
	}

	for reportIndex, report := range decision.Reports {
		if len(report.SourceMessageIDs) == 0 {
			return fmt.Errorf("report %d has no source messages", reportIndex)
		}
		for sourceIndex, messageID := range report.SourceMessageIDs {
			if err := claim(messageID, fmt.Sprintf("report %d source %d", reportIndex, sourceIndex)); err != nil {
				return err
			}
		}
	}
	for voteIndex, vote := range decision.Votes {
		if len(vote.SourceMessageIDs) == 0 {
			return fmt.Errorf("vote %d has no source messages", voteIndex)
		}
		for sourceIndex, messageID := range vote.SourceMessageIDs {
			if err := claim(messageID, fmt.Sprintf("vote %d source %d", voteIndex, sourceIndex)); err != nil {
				return err
			}
		}
	}
	for ignoredIndex, ignored := range decision.Ignored {
		if err := claim(ignored.MessageID, fmt.Sprintf("ignored %d", ignoredIndex)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) evaluateBatchDecisions(ctx context.Context, catalog *model.Catalog, incidents []model.IncidentSummary, items []BatchItem, decision BatchDecision, batchID string, now time.Time) (map[int64]batchMessageOutcome, batchDecisionStats) {
	outcomes := make(map[int64]batchMessageOutcome)
	stats := batchDecisionStats{}
	byMessageID := make(map[int64]BatchItem, len(items))
	for _, item := range items {
		byMessageID[item.Message.MessageID] = item
	}
	reportRefs := make(map[string]batchReportRef)
	for _, report := range decision.Reports {
		sources, candidates, err := batchSourcesAndCandidates(byMessageID, report.SourceMessageIDs)
		if err != nil {
			stats.errors++
			continue
		}
		normalized, err := normalizeDecision(Decision{
			Action:     report.Action,
			TargetType: report.TargetType,
			TargetID:   report.TargetID,
			Confidence: report.Confidence,
			Language:   report.Language,
			Reason:     report.Reason,
		})
		if err == nil && normalized.Action != ActionSighting && normalized.Action != ActionNotice && normalized.Action != ActionConfirmation {
			err = fmt.Errorf("report action %q is not publishable as an ongoing incident signal", normalized.Action)
		}
		if err == nil && normalized.Confidence < confidenceThresholdForReport(normalized, s.settings.MinConfidence) {
			err = fmt.Errorf("low confidence")
		}
		if err == nil && normalized.Action == ActionConfirmation {
			target, activeErr := validateActiveIncident(normalized.TargetID, incidents, normalized.Action)
			if activeErr == nil {
				analysis := batchOutcomeJSON(batchID, "vote", "incident confirmation returned as report", report)
				if _, err = reporterUserID(sources[0].Message); err == nil && target.dedupeKey != "" {
					var applied int
					applied, err = s.store.CountChatAnalyzerAppliedByTargetSince(ctx, target.dedupeKey, now.Add(-s.settings.TargetDedupeWindow))
					if err == nil && applied > 0 {
						err = fmt.Errorf("target duplicate window")
					}
				}
				if err != nil {
					stats.errors++
					markSources(outcomes, sources, batchMessageOutcome{
						status:       model.ChatAnalyzerMessageUncertain,
						analysisJSON: analysis,
						lastError:    err.Error(),
					})
					continue
				}
				status := model.ChatAnalyzerMessageDryRun
				actionID := ""
				if s.settings.DryRun {
					stats.wouldApply++
				} else {
					normalized.TargetType = TargetIncident
					normalized.TargetID = target.incidentID
					var applyErr error
					applied, applyErr := s.applyClaimedDecision(ctx, catalog, sources, normalized, target, batchID, analysis, now)
					if applyErr != nil {
						stats.errors++
						markSources(outcomes, sources, claimedActionFailureOutcome(applied, analysis, applyErr))
						continue
					}
					actionID = applied.actionID
					target.dedupeKey = applied.targetKey
					target.incidentID = applied.incidentID
					status = model.ChatAnalyzerMessageApplied
					stats.applied++
				}
				markSources(outcomes, sources, batchMessageOutcome{
					status:           status,
					analysisJSON:     analysis,
					appliedActionID:  actionID,
					appliedTargetKey: target.dedupeKey,
				})
				continue
			}
		}
		var target validatedTarget
		if err == nil {
			target, err = validateTarget(normalized, candidates)
		}
		if err == nil && normalized.TargetType == TargetStop && ambiguousStopReportNeedsArea(sources[0].Message.Text, target.stop, candidates) {
			err = fmt.Errorf("ambiguous stop name should use area target")
		}
		if err == nil {
			_, err = reporterUserID(sources[0].Message)
		}
		if err == nil && target.dedupeKey != "" {
			var applied int
			applied, err = s.store.CountChatAnalyzerAppliedByTargetSince(ctx, target.dedupeKey, now.Add(-s.settings.TargetDedupeWindow))
			if err == nil && applied > 0 {
				err = fmt.Errorf("target duplicate window")
			}
		}
		analysis := batchOutcomeJSON(batchID, "report", "", report)
		if err != nil {
			stats.errors++
			markSources(outcomes, sources, batchMessageOutcome{
				status:       model.ChatAnalyzerMessageUncertain,
				analysisJSON: analysis,
				lastError:    err.Error(),
			})
			continue
		}
		status := model.ChatAnalyzerMessageDryRun
		actionID := ""
		if s.settings.DryRun {
			stats.wouldApply++
		} else {
			var applyErr error
			applied, applyErr := s.applyClaimedDecision(ctx, catalog, sources, normalized, target, batchID, analysis, now)
			if applyErr != nil {
				stats.errors++
				markSources(outcomes, sources, claimedActionFailureOutcome(applied, analysis, applyErr))
				continue
			}
			actionID = applied.actionID
			target.dedupeKey = applied.targetKey
			target.incidentID = applied.incidentID
			status = model.ChatAnalyzerMessageApplied
			stats.applied++
		}
		if strings.TrimSpace(report.ID) != "" {
			reportRefs[strings.TrimSpace(report.ID)] = batchReportRef{
				incidentID:       target.incidentID,
				dedupeKey:        target.dedupeKey,
				sourceMessageIDs: append([]int64(nil), report.SourceMessageIDs...),
				sourceCandidates: sourceCandidateContext(sources),
			}
		}
		markSources(outcomes, sources, batchMessageOutcome{
			status:           status,
			analysisJSON:     analysis,
			appliedActionID:  actionID,
			appliedTargetKey: target.dedupeKey,
		})
	}
	for _, vote := range decision.Votes {
		sources, candidates, err := batchSourcesAndCandidates(byMessageID, vote.SourceMessageIDs)
		if err != nil {
			stats.errors++
			continue
		}
		normalized := Decision{
			Action:     strings.ToLower(strings.TrimSpace(vote.Action)),
			TargetType: strings.ToLower(strings.TrimSpace(vote.TargetType)),
			TargetID:   strings.TrimSpace(vote.TargetID),
			Confidence: vote.Confidence,
			Language:   vote.Language,
			Reason:     vote.Reason,
		}
		cleanStatus := sourcesContainNoInspectionStatus(sources)
		if cleanStatus && normalized.Action == ActionConfirmation {
			normalized.Action = ActionCleared
		}
		if normalized.Action != ActionConfirmation && normalized.Action != ActionDenial && normalized.Action != ActionCleared {
			stats.errors++
			err := fmt.Errorf("unsupported vote action %q", normalized.Action)
			markSources(outcomes, sources, batchMessageOutcome{
				status:       model.ChatAnalyzerMessageUncertain,
				analysisJSON: batchOutcomeJSON(batchID, "vote", "", vote),
				lastError:    err.Error(),
			})
			continue
		}
		if cleanStatus && isClearAction(normalized.Action) && !cleanVoteHasGroundedTarget(sources, normalized, incidents, reportRefs) {
			markSources(outcomes, sources, batchMessageOutcome{
				status:       model.ChatAnalyzerMessageIgnored,
				analysisJSON: batchOutcomeJSON(batchID, "ignored", "clean message without matching active incident", vote),
				lastError:    "clean message without matching active incident",
			})
			continue
		}
		if normalized.Confidence < confidenceThresholdForVote(normalized, s.settings.MinConfidence, cleanStatus) {
			stats.errors++
			markSources(outcomes, sources, batchMessageOutcome{
				status:       model.ChatAnalyzerMessageUncertain,
				analysisJSON: batchOutcomeJSON(batchID, "vote", "", vote),
				lastError:    "low confidence",
			})
			continue
		}
		var target validatedTarget
		switch normalized.TargetType {
		case TargetIncident:
			target, err = validateActiveIncident(normalized.TargetID, incidents, normalized.Action)
		case "report":
			ref, ok := reportRefs[normalized.TargetID]
			if !ok {
				err = fmt.Errorf("referenced report was not validated")
			} else {
				target = validatedTarget{incidentID: ref.incidentID, dedupeKey: ongoingVoteDedupeKey(ref.incidentID)}
			}
		default:
			normalized.TargetType = TargetIncident
			target, err = validateTarget(normalized, candidates)
		}
		analysis := batchOutcomeJSON(batchID, "vote", "", vote)
		if err == nil {
			_, err = reporterUserID(sources[0].Message)
		}
		if err == nil && target.dedupeKey != "" {
			var applied int
			applied, err = s.store.CountChatAnalyzerAppliedByTargetSince(ctx, target.dedupeKey, now.Add(-s.settings.TargetDedupeWindow))
			if err == nil && applied > 0 {
				err = fmt.Errorf("target duplicate window")
			}
		}
		if err != nil {
			stats.errors++
			markSources(outcomes, sources, batchMessageOutcome{
				status:       model.ChatAnalyzerMessageUncertain,
				analysisJSON: analysis,
				lastError:    err.Error(),
			})
			continue
		}
		status := model.ChatAnalyzerMessageDryRun
		actionID := ""
		if s.settings.DryRun {
			stats.wouldApply++
		} else {
			normalized.TargetType = TargetIncident
			normalized.TargetID = target.incidentID
			var applyErr error
			applied, applyErr := s.applyClaimedDecision(ctx, catalog, sources, normalized, target, batchID, analysis, now)
			if applyErr != nil {
				stats.errors++
				markSources(outcomes, sources, claimedActionFailureOutcome(applied, analysis, applyErr))
				continue
			}
			actionID = applied.actionID
			target.dedupeKey = applied.targetKey
			target.incidentID = applied.incidentID
			status = model.ChatAnalyzerMessageApplied
			stats.applied++
		}
		markSources(outcomes, sources, batchMessageOutcome{
			status:           status,
			analysisJSON:     analysis,
			appliedActionID:  actionID,
			appliedTargetKey: target.dedupeKey,
		})
	}
	for _, ignored := range decision.Ignored {
		if _, exists := outcomes[ignored.MessageID]; exists {
			continue
		}
		if _, ok := byMessageID[ignored.MessageID]; !ok {
			continue
		}
		outcomes[ignored.MessageID] = batchMessageOutcome{
			status:       model.ChatAnalyzerMessageIgnored,
			analysisJSON: batchOutcomeJSON(batchID, "ignored", ignored.Reason, ignored),
			lastError:    strings.TrimSpace(ignored.Reason),
		}
	}
	return outcomes, stats
}

func (s *Service) addFallbackOmittedSignalOutcomes(ctx context.Context, catalog *model.Catalog, items []BatchItem, outcomes map[int64]batchMessageOutcome, stats *batchDecisionStats, batchID string, now time.Time) {
	if stats == nil {
		return
	}
	for _, item := range items {
		if _, exists := outcomes[item.Message.MessageID]; exists {
			continue
		}
		if omittedSignalNearDecidedOutcome(item, items, outcomes) {
			continue
		}
		decision, ok := fallbackOmittedSignalDecision(item)
		if !ok {
			continue
		}
		target, err := validateTarget(decision, item.Candidates)
		if err == nil {
			_, err = reporterUserID(item.Message)
		}
		if err == nil && target.dedupeKey != "" {
			var applied int
			applied, err = s.store.CountChatAnalyzerAppliedByTargetSince(ctx, target.dedupeKey, now.Add(-s.settings.TargetDedupeWindow))
			if err == nil && applied > 0 {
				err = fmt.Errorf("target duplicate window")
			}
		}
		report := BatchReportDecision{
			ID:               "fallback-" + fmt.Sprint(item.Message.MessageID),
			Action:           decision.Action,
			TargetType:       decision.TargetType,
			TargetID:         decision.TargetID,
			Confidence:       decision.Confidence,
			Reason:           decision.Reason,
			Language:         "",
			SourceMessageIDs: []int64{item.Message.MessageID},
		}
		analysis := batchOutcomeJSON(batchID, "report", "fallback for omitted transport signal", report)
		if err != nil {
			stats.errors++
			outcomes[item.Message.MessageID] = batchMessageOutcome{
				status:       model.ChatAnalyzerMessageUncertain,
				analysisJSON: analysis,
				lastError:    err.Error(),
			}
			continue
		}
		status := model.ChatAnalyzerMessageDryRun
		actionID := ""
		if s.settings.DryRun {
			stats.wouldApply++
		} else {
			var applyErr error
			applied, applyErr := s.applyClaimedDecision(ctx, catalog, []BatchItem{item}, decision, target, batchID, analysis, now)
			if applyErr != nil {
				stats.errors++
				outcomes[item.Message.MessageID] = claimedActionFailureOutcome(applied, analysis, applyErr)
				continue
			}
			actionID = applied.actionID
			target.dedupeKey = applied.targetKey
			target.incidentID = applied.incidentID
			status = model.ChatAnalyzerMessageApplied
			stats.applied++
		}
		outcomes[item.Message.MessageID] = batchMessageOutcome{
			status:           status,
			analysisJSON:     analysis,
			appliedActionID:  actionID,
			appliedTargetKey: target.dedupeKey,
		}
	}
}

func omittedSignalNearDecidedOutcome(target BatchItem, items []BatchItem, outcomes map[int64]batchMessageOutcome) bool {
	for _, item := range items {
		if item.Message.MessageID == target.Message.MessageID {
			continue
		}
		outcome, ok := outcomes[item.Message.MessageID]
		if !ok {
			continue
		}
		if outcome.status != model.ChatAnalyzerMessageApplied && outcome.status != model.ChatAnalyzerMessageDryRun {
			continue
		}
		if nearbyForLocationReasoning(target.Message, item.Message) {
			return true
		}
	}
	return false
}

func locationReasoningItems(items []BatchItem, decision BatchDecision) ([]BatchItem, []int64) {
	if len(items) == 0 {
		return nil, nil
	}
	recheck := locationReasoningMessageIDs(items, decision)
	if len(recheck) == 0 {
		return items, nil
	}
	out := append([]BatchItem(nil), items...)
	for i := range out {
		if _, ok := recheck[out[i].Message.MessageID]; !ok {
			continue
		}
		out[i].Candidates = locationReasoningCandidates(out, i)
	}
	ids := make([]int64, 0, len(recheck))
	for _, item := range out {
		if _, ok := recheck[item.Message.MessageID]; ok {
			ids = append(ids, item.Message.MessageID)
		}
	}
	return out, ids
}

func locationReasoningMessageIDs(items []BatchItem, decision BatchDecision) map[int64]struct{} {
	decided := make(map[int64]struct{})
	for _, report := range decision.Reports {
		for _, id := range report.SourceMessageIDs {
			if shouldRecheckReportLocation(items, id, report) {
				continue
			}
			decided[id] = struct{}{}
		}
	}
	for _, vote := range decision.Votes {
		for _, id := range vote.SourceMessageIDs {
			decided[id] = struct{}{}
		}
	}
	recheck := make(map[int64]struct{})
	for _, ignored := range decision.Ignored {
		reason := strings.ToLower(strings.TrimSpace(ignored.Reason))
		if strings.Contains(reason, "vague") ||
			strings.Contains(reason, "location") ||
			strings.Contains(reason, "ambiguous") ||
			strings.Contains(reason, "unclear") ||
			strings.Contains(reason, "target") ||
			strings.Contains(reason, "place") {
			recheck[ignored.MessageID] = struct{}{}
		}
		decided[ignored.MessageID] = struct{}{}
	}
	for _, item := range items {
		if _, ok := decided[item.Message.MessageID]; ok {
			continue
		}
		if looksLikeTransportSignal(item.Message.Text) {
			recheck[item.Message.MessageID] = struct{}{}
		}
	}
	return recheck
}

func shouldRecheckReportLocation(items []BatchItem, messageID int64, report BatchReportDecision) bool {
	targetType := strings.ToLower(strings.TrimSpace(report.TargetType))
	if targetType == TargetArea {
		return false
	}
	if report.Confidence > 0 && report.Confidence < 0.88 {
		return true
	}
	reason := strings.ToLower(strings.TrimSpace(report.Reason))
	if strings.Contains(reason, "vague") ||
		strings.Contains(reason, "approx") ||
		strings.Contains(reason, "between") ||
		strings.Contains(reason, "ambiguous") ||
		strings.Contains(reason, "unclear") {
		return true
	}
	for _, item := range items {
		if item.Message.MessageID != messageID {
			continue
		}
		if targetType != TargetStop {
			return false
		}
		stop, ok := findStopCandidate(report.TargetID, item.Candidates.Stops)
		if !ok {
			return false
		}
		return (looksLikeApproximateAreaText(item.Message.Text) && len(item.Candidates.Areas) > 0) ||
			ambiguousStopReportNeedsArea(item.Message.Text, stop, item.Candidates)
	}
	return false
}

func ambiguousStopReportNeedsArea(text string, stop StopCandidate, candidates CandidateContext) bool {
	group := sameNameStopCandidates(stop, candidates.Stops)
	if len(group) < 2 {
		return false
	}
	if !hasSameNameAreaCandidate(stop, candidates.Areas) {
		return false
	}
	return !stopReportDisambiguatedByText(text, stop, group)
}

func findStopCandidate(id string, stops []StopCandidate) (StopCandidate, bool) {
	clean := strings.TrimSpace(id)
	for _, stop := range stops {
		if strings.TrimSpace(stop.ID) == clean {
			return stop, true
		}
	}
	return StopCandidate{}, false
}

func sameNameStopCandidates(stop StopCandidate, stops []StopCandidate) []StopCandidate {
	key := stopNameKey(stop.Name)
	if key == "" {
		return nil
	}
	out := make([]StopCandidate, 0, len(stops))
	for _, candidate := range stops {
		if stopNameKey(candidate.Name) == key {
			out = append(out, candidate)
		}
	}
	return out
}

func hasSameNameAreaCandidate(stop StopCandidate, areas []AreaCandidate) bool {
	id := "name:" + strings.ReplaceAll(stopNameKey(stop.Name), " ", "-")
	for _, area := range areas {
		if strings.TrimSpace(area.ID) == id {
			return true
		}
	}
	return false
}

func stopReportDisambiguatedByText(text string, stop StopCandidate, group []StopCandidate) bool {
	normalized := normalizeText(text)
	routes := routeLabelsFromText(normalized)
	for route := range routes {
		matches := 0
		selected := false
		for _, candidate := range group {
			if stopHasRoute(candidate, route) {
				matches++
				if candidate.ID == stop.ID {
					selected = true
				}
			}
		}
		if matches == 1 && selected {
			return true
		}
	}
	for mode := range mentionedModes(normalized) {
		matches := 0
		selected := false
		for _, candidate := range group {
			if stopHasMode(candidate, mode) {
				matches++
				if candidate.ID == stop.ID {
					selected = true
				}
			}
		}
		if matches == 1 && selected {
			return true
		}
	}
	return false
}

func stopHasRoute(stop StopCandidate, route string) bool {
	for _, label := range stop.RouteLabels {
		if normalizeRouteLabel(label) == route {
			return true
		}
	}
	return false
}

func mentionedModes(normalizedText string) map[string]struct{} {
	out := make(map[string]struct{})
	if strings.Contains(normalizedText, "tram") || strings.Contains(normalizedText, "tramv") {
		out["tram"] = struct{}{}
	}
	if strings.Contains(normalizedText, "trol") {
		out["trol"] = struct{}{}
	}
	if strings.Contains(normalizedText, "bus") || strings.Contains(normalizedText, "autobus") || strings.Contains(normalizedText, "avtobus") {
		out["bus"] = struct{}{}
	}
	return out
}

func stopHasMode(stop StopCandidate, mode string) bool {
	for _, candidate := range stop.Modes {
		if strings.Contains(strings.ToLower(strings.TrimSpace(candidate)), mode) {
			return true
		}
	}
	return false
}

func locationReasoningCandidates(items []BatchItem, index int) CandidateContext {
	merged := copyCandidateContext(items[index].Candidates)
	for i := range items {
		if i == index {
			continue
		}
		if !nearbyForLocationReasoning(items[index].Message, items[i].Message) {
			continue
		}
		merged.Stops = append(merged.Stops, items[i].Candidates.Stops...)
		merged.Vehicles = append(merged.Vehicles, items[i].Candidates.Vehicles...)
		merged.Areas = append(merged.Areas, items[i].Candidates.Areas...)
		merged.Incidents = append(merged.Incidents, items[i].Candidates.Incidents...)
	}
	merged = dedupeCandidates(merged)
	if len(merged.Stops) > maxStopCandidates+4 {
		merged.Stops = merged.Stops[:maxStopCandidates+4]
	}
	if len(merged.Vehicles) > maxVehicleCandidates+4 {
		merged.Vehicles = merged.Vehicles[:maxVehicleCandidates+4]
	}
	if len(merged.Areas) > maxAreaCandidates+4 {
		merged.Areas = merged.Areas[:maxAreaCandidates+4]
	}
	if len(merged.Incidents) > maxIncidentCandidates+4 {
		merged.Incidents = merged.Incidents[:maxIncidentCandidates+4]
	}
	return merged
}

func copyCandidateContext(candidates CandidateContext) CandidateContext {
	return CandidateContext{
		Stops:     append([]StopCandidate(nil), candidates.Stops...),
		Vehicles:  append([]VehicleCandidate(nil), candidates.Vehicles...),
		Areas:     append([]AreaCandidate(nil), candidates.Areas...),
		Incidents: append([]IncidentCandidate(nil), candidates.Incidents...),
	}
}

func nearbyForLocationReasoning(target, context model.ChatAnalyzerMessage) bool {
	if target.ReplyToMessageID != 0 && target.ReplyToMessageID == context.MessageID {
		return true
	}
	if context.ReplyToMessageID != 0 && context.ReplyToMessageID == target.MessageID {
		return true
	}
	if !target.MessageDate.IsZero() && !context.MessageDate.IsZero() {
		delta := target.MessageDate.Sub(context.MessageDate)
		if delta < 0 {
			delta = -delta
		}
		return delta <= 15*time.Minute
	}
	delta := target.MessageID - context.MessageID
	if delta < 0 {
		delta = -delta
	}
	return delta <= 5
}

func looksLikeTransportSignal(text string) bool {
	clean := normalizeText(text)
	if clean == "" {
		return false
	}
	needles := []string{
		"kontrole", "kontrol", "controller", "inspection", "ticket", "parbaude", "sods",
		"menti", "ment", "policija", "municipal", "rpp", "reid", "raid", "iekap", "iekapa", "izkap", "izkapa", "stav", "brauc",
		"gaid", "sed", "sez", "divi", "dvoe", "dvoje", "2oe", "2e", "two", "waiting", "standing", "sitting",
		"есть", "контрол", "провер", "штраф", "полици",
		"зашли", "зашел", "сели", "сел", "вошли", "вошел", "вышли", "вышел", "едут", "ехали", "стоят", "стоит", "ждут", "сидят",
	}
	for _, needle := range needles {
		if strings.Contains(clean, normalizeText(needle)) {
			return true
		}
	}
	return false
}

func mergeLocationReasoningDecision(initial BatchDecision, reasoned BatchDecision, recheckMessageIDs []int64) BatchDecision {
	recheck := make(map[int64]struct{}, len(recheckMessageIDs))
	for _, id := range recheckMessageIDs {
		recheck[id] = struct{}{}
	}
	reasonedIDs := make(map[int64]struct{})
	reasonedReports := make([]BatchReportDecision, 0, len(reasoned.Reports))
	reasonedVotes := make([]BatchVoteDecision, 0, len(reasoned.Votes))
	reasonedIgnored := make(map[int64]BatchIgnoredDecision)
	for _, report := range reasoned.Reports {
		report.SourceMessageIDs = onlyRecheckSourceIDs(report.SourceMessageIDs, recheck)
		if len(report.SourceMessageIDs) == 0 {
			continue
		}
		report.Reason = locationReasoningReason(report.Reason)
		reasonedReports = append(reasonedReports, report)
		for _, id := range report.SourceMessageIDs {
			reasonedIDs[id] = struct{}{}
		}
	}
	for _, vote := range reasoned.Votes {
		vote.SourceMessageIDs = onlyRecheckSourceIDs(vote.SourceMessageIDs, recheck)
		if len(vote.SourceMessageIDs) == 0 {
			continue
		}
		vote.Reason = locationReasoningReason(vote.Reason)
		reasonedVotes = append(reasonedVotes, vote)
		for _, id := range vote.SourceMessageIDs {
			reasonedIDs[id] = struct{}{}
		}
	}
	for _, item := range reasoned.Ignored {
		if _, ok := recheck[item.MessageID]; !ok {
			continue
		}
		item.Reason = locationReasoningReason(item.Reason)
		reasonedIgnored[item.MessageID] = item
		reasonedIDs[item.MessageID] = struct{}{}
	}
	out := BatchDecision{ModelMeta: initial.ModelMeta}
	for _, report := range initial.Reports {
		if anySourceIDIn(report.SourceMessageIDs, reasonedIDs) {
			continue
		}
		out.Reports = append(out.Reports, report)
	}
	for _, vote := range initial.Votes {
		if anySourceIDIn(vote.SourceMessageIDs, reasonedIDs) {
			continue
		}
		out.Votes = append(out.Votes, vote)
	}
	ignored := make([]BatchIgnoredDecision, 0, len(initial.Ignored)+len(reasoned.Ignored))
	for _, item := range initial.Ignored {
		if _, ok := reasonedIDs[item.MessageID]; ok {
			continue
		}
		if next, ok := reasonedIgnored[item.MessageID]; ok {
			ignored = append(ignored, next)
			delete(reasonedIgnored, item.MessageID)
			continue
		}
		ignored = append(ignored, item)
	}
	for _, item := range reasonedIgnored {
		ignored = append(ignored, item)
	}
	out.Reports = append(out.Reports, reasonedReports...)
	out.Votes = append(out.Votes, reasonedVotes...)
	out.Ignored = ignored
	return out
}

func anySourceIDIn(ids []int64, targets map[int64]struct{}) bool {
	for _, id := range ids {
		if _, ok := targets[id]; ok {
			return true
		}
	}
	return false
}

func onlyRecheckSourceIDs(ids []int64, recheck map[int64]struct{}) []int64 {
	out := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := recheck[id]; !ok {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func locationReasoningReason(reason string) string {
	clean := strings.TrimSpace(reason)
	if clean == "" {
		return "location deduction"
	}
	if strings.Contains(strings.ToLower(clean), "deduc") {
		return clean
	}
	return "location deduction: " + clean
}

func combinedBatchResultJSON(initialRaw, reasoningRaw string) string {
	body, err := json.Marshal(struct {
		Initial           json.RawMessage `json:"initial,omitempty"`
		LocationReasoning json.RawMessage `json:"locationReasoning,omitempty"`
	}{
		Initial:           rawJSONOrString(initialRaw),
		LocationReasoning: rawJSONOrString(reasoningRaw),
	})
	if err != nil {
		return initialRaw
	}
	return string(body)
}

func rawJSONOrString(raw string) json.RawMessage {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return nil
	}
	if json.Valid([]byte(clean)) {
		return json.RawMessage(clean)
	}
	body, err := json.Marshal(clean)
	if err != nil {
		return nil
	}
	return json.RawMessage(body)
}

func reporterUserID(item model.ChatAnalyzerMessage) (int64, error) {
	if userID, ok := model.ChatAnalyzerReporterUserID(item.SenderID); ok {
		return userID, nil
	}
	return 0, fmt.Errorf("telegram user id is required")
}

func batchSourcesAndCandidates(byMessageID map[int64]BatchItem, sourceIDs []int64) ([]BatchItem, CandidateContext, error) {
	if len(sourceIDs) == 0 {
		return nil, CandidateContext{}, fmt.Errorf("sourceMessageIds is required")
	}
	sources := make([]BatchItem, 0, len(sourceIDs))
	seen := make(map[int64]struct{}, len(sourceIDs))
	var candidates CandidateContext
	for _, messageID := range sourceIDs {
		if _, ok := seen[messageID]; ok {
			continue
		}
		item, ok := byMessageID[messageID]
		if !ok {
			return nil, CandidateContext{}, fmt.Errorf("source message %d was not in the batch", messageID)
		}
		seen[messageID] = struct{}{}
		sources = append(sources, item)
		candidates.Stops = append(candidates.Stops, item.Candidates.Stops...)
		candidates.Stops = append(candidates.Stops, item.StopDirectory...)
		candidates.Vehicles = append(candidates.Vehicles, item.Candidates.Vehicles...)
		candidates.Areas = append(candidates.Areas, item.Candidates.Areas...)
		candidates.Incidents = append(candidates.Incidents, item.Candidates.Incidents...)
	}
	return sources, dedupeCandidates(candidates), nil
}

func dedupeCandidates(candidates CandidateContext) CandidateContext {
	stopSeen := make(map[string]struct{}, len(candidates.Stops))
	stops := candidates.Stops[:0]
	for _, item := range candidates.Stops {
		if _, ok := stopSeen[item.ID]; ok {
			continue
		}
		stopSeen[item.ID] = struct{}{}
		stops = append(stops, item)
	}
	vehicleSeen := make(map[string]struct{}, len(candidates.Vehicles))
	vehicles := candidates.Vehicles[:0]
	for _, item := range candidates.Vehicles {
		if _, ok := vehicleSeen[item.ID]; ok {
			continue
		}
		vehicleSeen[item.ID] = struct{}{}
		vehicles = append(vehicles, item)
	}
	areaSeen := make(map[string]struct{}, len(candidates.Areas))
	areas := candidates.Areas[:0]
	for _, item := range candidates.Areas {
		if _, ok := areaSeen[item.ID]; ok {
			continue
		}
		areaSeen[item.ID] = struct{}{}
		areas = append(areas, item)
	}
	incidentSeen := make(map[string]struct{}, len(candidates.Incidents))
	incidents := candidates.Incidents[:0]
	for _, item := range candidates.Incidents {
		if _, ok := incidentSeen[item.ID]; ok {
			continue
		}
		incidentSeen[item.ID] = struct{}{}
		incidents = append(incidents, item)
	}
	return CandidateContext{Stops: stops, Vehicles: vehicles, Areas: areas, Incidents: incidents}
}

func validateActiveIncident(incidentID string, incidents []model.IncidentSummary, action string) (validatedTarget, error) {
	clean := strings.TrimSpace(incidentID)
	for _, incident := range incidents {
		if strings.TrimSpace(incident.ID) == clean {
			cleanAction := strings.ToLower(strings.TrimSpace(action))
			dedupeKey := "vote:" + clean + ":" + cleanAction
			if cleanAction == ActionConfirmation {
				dedupeKey = ongoingVoteDedupeKey(clean)
			}
			return validatedTarget{incidentID: clean, dedupeKey: dedupeKey}, nil
		}
	}
	return validatedTarget{}, fmt.Errorf("incident was not active")
}

func ongoingVoteDedupeKey(incidentID string) string {
	return "vote:" + strings.TrimSpace(incidentID) + ":" + ActionSighting
}

func markSources(outcomes map[int64]batchMessageOutcome, sources []BatchItem, outcome batchMessageOutcome) {
	for _, source := range sources {
		outcomes[source.Message.MessageID] = outcome
	}
}

func batchOutcomeJSON(batchID, kind, note string, payload any) string {
	body, err := json.Marshal(struct {
		BatchID string `json:"batchId"`
		Kind    string `json:"kind"`
		Note    string `json:"note,omitempty"`
		Payload any    `json:"payload,omitempty"`
	}{
		BatchID: batchID,
		Kind:    strings.TrimSpace(kind),
		Note:    strings.TrimSpace(note),
		Payload: payload,
	})
	if err != nil {
		return ""
	}
	return string(body)
}

func analysisJSONWithProcessingAttempt(raw string, attempt int) string {
	if attempt <= 0 {
		return strings.TrimSpace(raw)
	}
	body := make(map[string]any)
	if clean := strings.TrimSpace(raw); clean != "" {
		if err := json.Unmarshal([]byte(clean), &body); err != nil {
			return clean
		}
	}
	body["processingAttempt"] = attempt
	encoded, err := json.Marshal(body)
	if err != nil {
		return strings.TrimSpace(raw)
	}
	return string(encoded)
}

func messageProcessingAttempts(item model.ChatAnalyzerMessage) int {
	var marker struct {
		ProcessingAttempt int `json:"processingAttempt"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(item.AnalysisJSON)), &marker); err == nil && marker.ProcessingAttempt > 0 {
		return marker.ProcessingAttempt
	}
	attempts := item.Attempts
	if attempts > 1 && strings.HasPrefix(strings.TrimSpace(item.AppliedActionID), chatAnalyzerActionIDPrefix) {
		// Rows created before processingAttempt was persisted count both the
		// durable action claim and its outcome. Treat that pair as one logical
		// attempt so legacy pending work also retains the intended backoff.
		return (attempts + 1) / 2
	}
	return attempts
}

func nextMessageProcessingAttempt(item model.ChatAnalyzerMessage) int {
	return messageProcessingAttempts(item) + 1
}

func chatAnalyzerBatchID(now time.Time) string {
	return fmt.Sprintf("chat-batch-%s-%d", now.UTC().Format("20060102T150405Z"), now.UnixNano())
}

func (s *Service) modelName() string {
	return strings.TrimSpace(s.settings.ModelName)
}

func (s *Service) mark(ctx context.Context, id string, status model.ChatAnalyzerMessageStatus, analysisJSON, appliedActionID, appliedTargetKey, batchID, lastError string, processedAt time.Time) error {
	return s.store.MarkChatAnalyzerMessageProcessedInBatch(ctx, id, status, analysisJSON, appliedActionID, appliedTargetKey, batchID, lastError, processedAt)
}

func (s *Service) recordModelFailure(now time.Time) {
	s.consecutiveModelFailures++
	if s.consecutiveModelFailures >= s.settings.ModelFailureLimit {
		s.modelCircuitOpenUntil = now.Add(s.settings.ModelCircuitOpen)
		log.Printf("satiksme chat analyzer model circuit open until %s after %d failures", s.modelCircuitOpenUntil.Format(time.RFC3339), s.consecutiveModelFailures)
	}
}

func (s *Service) resetModelFailures() {
	s.consecutiveModelFailures = 0
	s.modelCircuitOpenUntil = time.Time{}
}

func (s *Service) nextDelay(now, nextCollect, nextProcess, nextRetry time.Time) time.Duration {
	nextWake := nextCollect
	if !nextProcess.IsZero() && (nextWake.IsZero() || nextProcess.Before(nextWake)) {
		nextWake = nextProcess
	}
	if !nextRetry.IsZero() && (nextWake.IsZero() || nextRetry.Before(nextWake)) {
		nextWake = nextRetry
	}
	delay := nextWake.Sub(now)
	if delay <= 0 {
		return time.Second
	}
	if delay > s.settings.PollInterval {
		return s.settings.PollInterval
	}
	return delay
}

func (s *Service) throttledProcessAt(candidate, lastProcessAt time.Time) time.Time {
	if candidate.IsZero() {
		return time.Time{}
	}
	next := candidate.UTC()
	if !lastProcessAt.IsZero() {
		minimum := lastProcessAt.UTC().Add(s.settings.ProcessInterval)
		if next.Before(minimum) {
			next = minimum
		}
	}
	window := nextProcessWindowOpenAt(next, s.settings)
	if next.Before(window) {
		return window
	}
	return next
}

func (s *Service) messageReadyForRetry(item model.ChatAnalyzerMessage, now time.Time) bool {
	attempts := messageProcessingAttempts(item)
	if attempts <= 0 || item.ProcessedAt.IsZero() {
		return true
	}
	return !now.Before(item.ProcessedAt.Add(s.retryDelay(attempts)))
}

func (s *Service) retryDelay(attempts int) time.Duration {
	if attempts <= 0 {
		return 0
	}
	delay := s.settings.RetryBaseDelay
	for i := 1; i < attempts; i++ {
		delay *= 2
		if delay >= s.settings.RetryMaxDelay {
			return s.settings.RetryMaxDelay
		}
	}
	if delay > s.settings.RetryMaxDelay {
		return s.settings.RetryMaxDelay
	}
	return delay
}

func nextScheduledProcessAt(now time.Time, settings Settings) time.Time {
	local := now.In(settings.Location)
	start := localMidnight(local).Add(time.Duration(settings.ProcessStartMinute) * time.Minute)
	end := localMidnight(local).Add(time.Duration(settings.ProcessEndMinute) * time.Minute)

	if settings.ProcessEndMinute > settings.ProcessStartMinute {
		if local.Before(start) {
			return start.In(time.UTC)
		}
		if candidate, ok := scheduledProcessCandidate(local, start, end, settings.ProcessInterval); ok {
			return candidate.In(time.UTC)
		}
		return start.AddDate(0, 0, 1).In(time.UTC)
	}

	if !local.After(end) {
		previousStart := start.AddDate(0, 0, -1)
		if candidate, ok := scheduledProcessCandidate(local, previousStart, end, settings.ProcessInterval); ok {
			return candidate.In(time.UTC)
		}
	}
	if local.Before(start) {
		return start.In(time.UTC)
	}
	nextEnd := end.AddDate(0, 0, 1)
	if candidate, ok := scheduledProcessCandidate(local, start, nextEnd, settings.ProcessInterval); ok {
		return candidate.In(time.UTC)
	}
	return start.AddDate(0, 0, 1).In(time.UTC)
}

func nextScheduledProcessAfter(now time.Time, settings Settings) time.Time {
	return nextScheduledProcessAt(now.Add(time.Second), settings)
}

func nextProcessWindowOpenAt(now time.Time, settings Settings) time.Time {
	local := now.In(settings.Location)
	start := localMidnight(local).Add(time.Duration(settings.ProcessStartMinute) * time.Minute)
	end := localMidnight(local).Add(time.Duration(settings.ProcessEndMinute) * time.Minute)

	if settings.ProcessEndMinute > settings.ProcessStartMinute {
		if local.Before(start) {
			return start.In(time.UTC)
		}
		if !local.After(end) {
			return now.UTC()
		}
		return start.AddDate(0, 0, 1).In(time.UTC)
	}

	if !local.After(end) {
		return now.UTC()
	}
	if local.Before(start) {
		return start.In(time.UTC)
	}
	return now.UTC()
}

func scheduledProcessCandidate(local, start, end time.Time, interval time.Duration) (time.Time, bool) {
	if local.Before(start) || local.After(end) {
		return time.Time{}, false
	}
	elapsed := local.Sub(start)
	slots := int64(elapsed / interval)
	candidate := start.Add(time.Duration(slots) * interval)
	if candidate.Before(local) {
		candidate = candidate.Add(interval)
	}
	if candidate.After(end) {
		return time.Time{}, false
	}
	return candidate, true
}

func localMidnight(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

type validatedTarget struct {
	stop       StopCandidate
	vehicle    VehicleCandidate
	area       AreaCandidate
	incidentID string
	dedupeKey  string
}

func normalizeDecision(decision Decision) (Decision, error) {
	decision.Action = strings.ToLower(strings.TrimSpace(decision.Action))
	decision.TargetType = strings.ToLower(strings.TrimSpace(decision.TargetType))
	decision.TargetID = strings.TrimSpace(decision.TargetID)
	switch decision.Action {
	case ActionSighting, ActionNotice, ActionCleared, ActionConfirmation, ActionDenial, ActionIgnore:
	default:
		return Decision{}, fmt.Errorf("unsupported action %q", decision.Action)
	}
	if decision.Action == ActionIgnore {
		decision.TargetType = TargetNone
		return decision, nil
	}
	switch decision.TargetType {
	case TargetStop, TargetVehicle, TargetArea, TargetIncident:
		if decision.TargetID == "" {
			return Decision{}, fmt.Errorf("missing target id")
		}
	case TargetNone, "":
		return Decision{}, fmt.Errorf("missing target type")
	default:
		return Decision{}, fmt.Errorf("unsupported target type %q", decision.TargetType)
	}
	return decision, nil
}

func validateTarget(decision Decision, candidates CandidateContext) (validatedTarget, error) {
	switch decision.TargetType {
	case TargetStop:
		for _, stop := range candidates.Stops {
			if stop.ID == decision.TargetID {
				incidentID := reports.StopIncidentID(stop.ID)
				if decision.Action == ActionDenial || decision.Action == ActionCleared {
					return validatedTarget{stop: stop, incidentID: incidentID, dedupeKey: "vote:" + incidentID + ":" + decision.Action}, nil
				}
				return validatedTarget{stop: stop, incidentID: incidentID, dedupeKey: "sighting:stop:" + stop.ID}, nil
			}
		}
	case TargetVehicle:
		for _, vehicle := range candidates.Vehicles {
			if vehicle.ID == decision.TargetID {
				incidentID := reports.VehicleIncidentID(vehicle.ID)
				if decision.Action == ActionDenial || decision.Action == ActionCleared {
					return validatedTarget{vehicle: vehicle, incidentID: incidentID, dedupeKey: "vote:" + incidentID + ":" + decision.Action}, nil
				}
				return validatedTarget{vehicle: vehicle, incidentID: incidentID, dedupeKey: "sighting:vehicle:" + vehicle.ID}, nil
			}
		}
	case TargetArea:
		for _, area := range candidates.Areas {
			if area.ID == decision.TargetID {
				area.RadiusMeters = areaRadiusForConfidence(area.RadiusMeters, decision.Confidence)
				scopeKey := reports.AreaScopeKey(model.AreaReportInput{
					Latitude:     area.Latitude,
					Longitude:    area.Longitude,
					RadiusMeters: area.RadiusMeters,
					Description:  areaPublicDescription(area),
				})
				incidentID := reports.AreaIncidentID(scopeKey)
				if decision.Action == ActionDenial || decision.Action == ActionCleared {
					return validatedTarget{area: area, incidentID: incidentID, dedupeKey: "vote:" + incidentID + ":" + decision.Action}, nil
				}
				return validatedTarget{area: area, incidentID: incidentID, dedupeKey: "sighting:area:" + scopeKey}, nil
			}
		}
	case TargetIncident:
		for _, incident := range candidates.Incidents {
			if incident.ID == decision.TargetID {
				return validatedTarget{incidentID: incident.ID, dedupeKey: "vote:" + incident.ID + ":" + decision.Action}, nil
			}
		}
	}
	return validatedTarget{}, fmt.Errorf("target was not in validated candidates")
}

func decisionJSON(decision Decision) string {
	body, err := json.Marshal(decision)
	if err != nil {
		return ""
	}
	return string(body)
}
