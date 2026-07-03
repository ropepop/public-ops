package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"ticketremote/internal/state"
)

const (
	streamCommandBridgeFastPollInterval   = 100 * time.Millisecond
	streamCommandBridgeActivePollInterval = 100 * time.Millisecond
	streamCommandBridgeQuietPollInterval  = 2 * time.Second
	streamCommandBridgeIdlePollInterval   = 1 * time.Second
	streamCommandBridgePurgeInterval      = 30 * time.Second
	streamCommandBridgeReadTimeout        = 4 * time.Second
	streamCommandBridgeWriteTimeout       = 8 * time.Second
	streamCommandBridgePhoneStartTimeout  = 75 * time.Second
	streamCommandBridgeLimit              = 20
	streamCommandBridgeBackoffMax         = 5 * time.Second
	controlCodePhoneHealthPollInterval    = 100 * time.Millisecond
	controlCodePhoneHealthRequestTimeout  = 1500 * time.Millisecond
)

type controlCodeBridgeObservation int

const (
	controlCodeBridgeIgnored controlCodeBridgeObservation = iota
	controlCodeBridgeResultObserved
	controlCodeBridgeCleanupObserved
)

type pendingStreamCommandReader interface {
	PendingStreamCommands(ctx context.Context, ticketID string, backendID string, limit uint32, now time.Time) ([]state.StreamCommand, error)
}

type streamCommandSignalReader interface {
	StreamCommandSignal(ctx context.Context, ticketID string, backendID string) (state.StreamCommandSignal, bool, error)
}

type expiredStreamCommandPurger interface {
	PurgeExpiredStreamCommands(ctx context.Context, ticketID string, now time.Time, batchSize uint32) error
}

type streamCommandBridgeAttempt struct {
	failures   int
	next       time.Time
	dispatched bool
}

func (s *Server) startStreamCommandBridge() {
	reader, ok := s.store.(pendingStreamCommandReader)
	if !ok || s.store == nil {
		return
	}
	if s.streamCommandBridgeStop == nil {
		s.streamCommandBridgeStop = make(chan struct{})
	}
	go s.streamCommandBridgeLoop(reader, s.streamCommandBridgeStop)
}

func (s *Server) streamCommandBridgeLoop(reader pendingStreamCommandReader, stop <-chan struct{}) {
	attempts := map[string]streamCommandBridgeAttempt{}
	lastSignals := map[string]string{}
	lastExpiredPurge := time.Time{}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-stop:
			return
		case <-timer.C:
		}
		hadCommands := s.pollPendingStreamCommands(reader, attempts, lastSignals, &lastExpiredPurge)
		timer.Reset(s.nextStreamCommandBridgePollDelay(hadCommands, attempts))
	}
}

func (s *Server) pollPendingStreamCommands(reader pendingStreamCommandReader, attempts map[string]streamCommandBridgeAttempt, lastSignals map[string]string, lastExpiredPurge *time.Time) bool {
	now := time.Now()
	backend := s.activePhoneBackend()
	s.purgeExpiredStreamCommandsIfDue(reader, now, lastExpiredPurge)
	if signalReader, ok := reader.(streamCommandSignalReader); ok && len(attempts) == 0 {
		signalCtx, signalCancel := context.WithTimeout(context.Background(), streamCommandBridgeReadTimeout)
		signal, found, err := signalReader.StreamCommandSignal(signalCtx, s.cfg.TicketID, backend.ID)
		signalCancel()
		if err != nil {
			s.recordRuntimeErrorAsync("stream_command_signal_read_failed", backend.ID, err, map[string]any{"backendId": backend.ID})
			return false
		}
		key := backend.ID
		signalKey := strings.TrimSpace(signal.Revision) + "|" + strings.TrimSpace(signal.UpdatedAt)
		if !found || signal.PendingCount == 0 || signalKey == "" || lastSignals[key] == signalKey {
			return false
		}
		lastSignals[key] = signalKey
	}
	ctx, cancel := context.WithTimeout(context.Background(), streamCommandBridgeReadTimeout)
	commands, err := reader.PendingStreamCommands(ctx, s.cfg.TicketID, backend.ID, streamCommandBridgeLimit, now)
	cancel()
	if err != nil {
		s.recordRuntimeErrorAsync("stream_command_read_failed", backend.ID, err, map[string]any{"backendId": backend.ID})
		return false
	}
	hadCommands := len(commands) > 0
	for _, command := range commands {
		command.ID = strings.TrimSpace(command.ID)
		if command.ID == "" {
			continue
		}
		if expiredStreamCommand(command, now) {
			delete(attempts, command.ID)
			continue
		}
		requestID := streamCommandRequestIDFromJSON(command.PayloadJSON)
		attempt := attempts[command.ID]
		if !attempt.next.IsZero() && now.Before(attempt.next) {
			continue
		}
		if !attempt.dispatched {
			ctx, cancel := context.WithTimeout(context.Background(), streamCommandDispatchTimeout(command))
			err := s.dispatchStreamCommandToPhone(ctx, command)
			cancel()
			if err != nil {
				if cleanStreamControlText(command.CommandType, "command") == "recover_stream" {
					s.noteStreamAutoRecoveryResult("failed", command.Reason, command.ID, err)
				}
				s.recordProductEventAsync(productEventInput{
					Source:        "ticket_remote_bridge",
					Category:      "command",
					Action:        "dispatch_failed",
					Status:        "failed",
					Reason:        safeStreamCommandBridgeError(err),
					CommandID:     command.ID,
					RequestID:     requestID,
					BackendID:     command.BackendID,
					CorrelationID: command.ID,
					SafeState: map[string]any{
						"commandType": cleanStreamControlText(command.CommandType, "command"),
					},
				})
				attempt.failures++
				attempt.next = now.Add(streamCommandBridgeBackoff(attempt.failures))
				attempts[command.ID] = attempt
				continue
			}
			s.recordProductEventAsync(productEventInput{
				Source:        "ticket_remote_bridge",
				Category:      "command",
				Action:        "dispatched",
				Status:        "ok",
				Reason:        command.Reason,
				CommandID:     command.ID,
				RequestID:     requestID,
				BackendID:     command.BackendID,
				CorrelationID: command.ID,
				SafeState: map[string]any{
					"commandType": cleanStreamControlText(command.CommandType, "command"),
				},
			})
			attempt.dispatched = true
			attempt.failures = 0
			attempt.next = time.Time{}
			attempts[command.ID] = attempt
		}
		if err := s.ackDispatchedStreamCommand(command); err != nil {
			attempt.failures++
			attempt.next = now.Add(streamCommandBridgeBackoff(attempt.failures))
			attempts[command.ID] = attempt
			s.recordProductEventAsync(productEventInput{
				Source:        "ticket_remote_bridge",
				Category:      "command",
				Action:        "ack_failed",
				Status:        "failed",
				Reason:        safeRuntimeLogError(err),
				CommandID:     command.ID,
				RequestID:     requestID,
				BackendID:     command.BackendID,
				CorrelationID: command.ID,
				SafeState: map[string]any{
					"commandType": cleanStreamControlText(command.CommandType, "command"),
				},
			})
			s.recordRuntimeErrorAsync("stream_command_ack_failed", command.ID, err, map[string]any{
				"commandId":   command.ID,
				"commandType": cleanStreamControlText(command.CommandType, "command"),
			})
			continue
		}
		s.recordProductEventAsync(productEventInput{
			Source:        "ticket_remote_bridge",
			Category:      "command",
			Action:        "acknowledged",
			Status:        "ok",
			Reason:        "dispatched_by_ticket_remote_bridge",
			CommandID:     command.ID,
			RequestID:     requestID,
			BackendID:     command.BackendID,
			CorrelationID: command.ID,
			SafeState: map[string]any{
				"commandType": cleanStreamControlText(command.CommandType, "command"),
			},
		})
		if cleanStreamControlText(command.CommandType, "command") == "recover_stream" {
			s.noteStreamAutoRecoveryResult("succeeded", command.Reason, command.ID, nil)
		}
		delete(attempts, command.ID)
	}
	pruneStreamCommandBridgeAttempts(attempts, now)
	return hadCommands
}

func (s *Server) purgeExpiredStreamCommandsIfDue(reader pendingStreamCommandReader, now time.Time, lastExpiredPurge *time.Time) {
	if lastExpiredPurge == nil {
		return
	}
	if !lastExpiredPurge.IsZero() && now.Sub(*lastExpiredPurge) < streamCommandBridgePurgeInterval {
		return
	}
	purger, ok := reader.(expiredStreamCommandPurger)
	if !ok {
		return
	}
	*lastExpiredPurge = now
	ctx, cancel := context.WithTimeout(context.Background(), streamCommandBridgeReadTimeout)
	err := purger.PurgeExpiredStreamCommands(ctx, s.cfg.TicketID, now, streamCommandBridgeLimit)
	cancel()
	if err != nil {
		s.recordRuntimeErrorAsync("stream_command_expired_purge_failed", s.cfg.TicketID, err, map[string]any{"ticketId": s.cfg.TicketID})
		return
	}
}

func streamCommandDispatchTimeout(command state.StreamCommand) time.Duration {
	switch cleanStreamControlText(command.CommandType, "command") {
	case "start", "prepare_control_code", "force_ticket_reselect":
		return streamCommandBridgePhoneStartTimeout
	default:
		return streamCommandBridgeWriteTimeout
	}
}

func (s *Server) nextStreamCommandBridgePollDelay(hadCommands bool, attempts map[string]streamCommandBridgeAttempt) time.Duration {
	if hadCommands || len(attempts) > 0 {
		return streamCommandBridgeFastPollInterval
	}
	if s.direct.activeVideoClientCount() > 0 {
		return streamCommandBridgeActivePollInterval
	}
	health := s.relay.Snapshot()
	if health.Desired || health.Viewers > 0 {
		return streamCommandBridgeQuietPollInterval
	}
	return streamCommandBridgeIdlePollInterval
}

func pruneStreamCommandBridgeAttempts(attempts map[string]streamCommandBridgeAttempt, now time.Time) {
	if len(attempts) <= 1000 {
		return
	}
	for id, attempt := range attempts {
		if attempt.next.IsZero() || now.Sub(attempt.next) > time.Minute {
			delete(attempts, id)
		}
		if len(attempts) <= 500 {
			return
		}
	}
}

func streamCommandBridgeBackoff(failures int) time.Duration {
	if failures <= 0 {
		return streamCommandBridgeFastPollInterval
	}
	delay := time.Duration(failures) * streamCommandBridgeFastPollInterval
	if delay > streamCommandBridgeBackoffMax {
		return streamCommandBridgeBackoffMax
	}
	return delay
}

func expiredStreamCommand(command state.StreamCommand, now time.Time) bool {
	expiresAt, err := time.Parse(time.RFC3339, strings.TrimSpace(command.ExpiresAt))
	if err != nil {
		expiresAt, err = time.Parse(time.RFC3339Nano, strings.TrimSpace(command.ExpiresAt))
	}
	return err == nil && !now.Before(expiresAt)
}

func (s *Server) dispatchStreamCommandToPhone(ctx context.Context, command state.StreamCommand) error {
	commandType := cleanStreamControlText(command.CommandType, "command")
	payload, err := streamCommandPhonePayload(command)
	if err != nil {
		return err
	}
	requestID := streamCommandRequestID(payload)
	switch commandType {
	case "start":
		s.direct.recordStartupPhase("spacetime_command_dispatch", fmt.Sprintf("type=start id=%s", command.ID))
		s.relay.EnsureActive("spacetime_command_start")
		startErr := s.postPhoneSessionCommand(ctx, "/api/v1/session/start")
		sendErr := s.sendPhoneSessionCommand(ctx, payload)
		if startErr == nil || sendErr == nil {
			s.direct.recordStartupPhase("phone_start_dispatched", fmt.Sprintf("http_ok=%t socket_ok=%t", startErr == nil, sendErr == nil))
		}
		if startErr != nil && sendErr != nil {
			return fmt.Errorf("start phone session: %w", startErr)
		}
		return nil
	case "prepare_control_code":
		s.relay.EnsureActive("spacetime_prepare_control_code")
		startErr := s.postPhoneSessionCommand(ctx, "/api/v1/session/start")
		sendErr := s.sendPhoneSessionCommand(ctx, payload)
		if startErr != nil && sendErr != nil {
			return fmt.Errorf("prepare control code phone session: %w", startErr)
		}
		return nil
	case "force_ticket_reselect":
		s.direct.recordStartupPhase("spacetime_command_dispatch", fmt.Sprintf("type=force_ticket_reselect id=%s", command.ID))
		s.relay.EnsureActive("spacetime_force_ticket_reselect")
		startErr := s.postPhoneSessionCommand(ctx, "/api/v1/session/start")
		sendErr := s.sendPhoneSessionCommand(ctx, payload)
		if startErr != nil && sendErr != nil {
			return fmt.Errorf("force ticket reselect phone session: %w", startErr)
		}
		return nil
	case "generate_control_code":
		if requestID != "" {
			s.retainControlCodeRelay(requestID)
		}
		if err := s.postPhoneSessionCommand(ctx, "/api/v1/session/start"); err != nil {
			return err
		}
		if !s.waitForPhoneRelayConnected("spacetime_generate_control_code", controlCodeRelayReadyWait) {
			return fmt.Errorf("phone relay did not connect for control code")
		}
		return s.sendPhoneSessionCommandAndReadControlCodeResult(ctx, command, payload, requestID)
	case "keyframe":
		s.direct.recordStartupPhase("spacetime_command_dispatch", fmt.Sprintf("type=keyframe id=%s", command.ID))
		s.direct.recordKeyframeRequested()
		return s.sendPhoneSessionCommand(ctx, payload)
	case "recover_stream":
		s.direct.recordStartupPhase("spacetime_command_dispatch", fmt.Sprintf("type=stream_recovery id=%s", command.ID))
		s.relay.Reconnect("spacetime_stream_recovery")
		s.direct.recordKeyframeRequested()
		if err := s.sendPhoneSessionCommand(ctx, payload); err != nil {
			s.direct.recordClientTelemetry("stream_recovery_command_socket_unavailable", command.Reason)
		}
		return nil
	case "control_code_browser_capture", "control_code_result_ack":
		err := s.sendPhoneSessionCommand(ctx, payload)
		if requestID != "" {
			s.releaseControlCodeRelay(requestID)
		}
		return err
	case "control_exit", "activity":
		return s.sendPhoneSessionCommand(ctx, payload)
	case "stop":
		sendErr := s.sendPhoneSessionCommand(ctx, payload)
		stopErr := s.postPhoneSessionCommand(ctx, "/api/v1/session/stop")
		if sendErr != nil && stopErr != nil {
			return fmt.Errorf("stop phone session: %w", stopErr)
		}
		return nil
	default:
		return s.sendPhoneSessionCommand(ctx, payload)
	}
}

func streamCommandPhonePayload(command state.StreamCommand) (map[string]any, error) {
	payload := map[string]any{}
	raw := strings.TrimSpace(command.PayloadJSON)
	if raw != "" && raw != "{}" {
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return nil, fmt.Errorf("decode stream command payload: %w", err)
		}
	}
	if _, ok := payload["type"]; !ok {
		payload["type"] = cleanStreamControlText(command.CommandType, "command")
	}
	if _, ok := payload["reason"]; !ok && strings.TrimSpace(command.Reason) != "" {
		payload["reason"] = command.Reason
	}
	payload["commandId"] = command.ID
	payload["revision"] = command.Revision
	payload["commandType"] = command.CommandType
	return payload, nil
}

func streamCommandRequestID(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	for _, key := range []string{"requestId", "requestID", "request_id"} {
		if value, ok := payload[key]; ok {
			return strings.TrimSpace(fmt.Sprint(value))
		}
	}
	return ""
}

func streamCommandRequestIDFromJSON(payloadJSON string) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return ""
	}
	return streamCommandRequestID(payload)
}

func (s *Server) ackDispatchedStreamCommand(command state.StreamCommand) error {
	now := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), streamControlWriteTimeout)
	defer cancel()
	if err := s.store.AckStreamCommand(ctx, state.StreamCommandAckInput{
		CommandID: command.ID,
		Status:    "acknowledged",
		Reason:    "dispatched_by_ticket_remote_bridge",
		Now:       now,
	}); err != nil {
		return err
	}
	return s.publishBridgePhoneCurrentReport(command, now)
}

func (s *Server) publishBridgePhoneCurrentReport(command state.StreamCommand, now time.Time) error {
	health := s.relay.Snapshot()
	status := map[string]any{
		"source":           "ticket_remote_spacetime_bridge",
		"commandType":      cleanStreamControlText(command.CommandType, "command"),
		"relayConnected":   health.Connected,
		"relayDesired":     health.Desired,
		"relayViewers":     health.Viewers,
		"relayStreamState": health.StreamState,
	}
	statusJSON, err := json.Marshal(status)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), streamControlWriteTimeout)
	defer cancel()
	return s.store.UpdatePhoneCurrentReport(ctx, state.PhoneCurrentReportInput{
		TicketID:            s.cfg.TicketID,
		BackendID:           s.activePhoneBackend().ID,
		StreamState:         cleanStreamControlText(health.StreamState, "unknown"),
		DesiredActive:       health.Desired,
		LastCommandID:       command.ID,
		LastCommandRevision: command.Revision,
		StatusJSON:          string(statusJSON),
		Now:                 now,
	})
}

func (s *Server) appendStreamCommandBridgeLogAsync(level string, event string, command state.StreamCommand, err error) {
	if s.store == nil {
		return
	}
	category := "command"
	if strings.Contains(event, "control_code") || cleanStreamControlText(command.CommandType, "command") == "generate_control_code" {
		category = "control_code"
	}
	status := "ok"
	if cleanStreamControlText(level, "info") == "warn" || err != nil || strings.Contains(event, "timeout") || strings.Contains(event, "failed") {
		status = "failed"
	}
	s.recordProductEventAsync(productEventInput{
		Source:        "ticket_remote_bridge",
		Category:      category,
		Action:        cleanStreamControlText(event, "bridge_event"),
		Status:        status,
		Reason:        safeStreamCommandBridgeError(err),
		CommandID:     command.ID,
		BackendID:     command.BackendID,
		CorrelationID: command.ID,
		SafeState: map[string]any{
			"commandType": cleanStreamControlText(command.CommandType, "command"),
		},
	})
}

func safeStreamCommandBridgeError(err error) string {
	if err == nil {
		return ""
	}
	text := strings.Join(strings.Fields(err.Error()), " ")
	if len(text) > 180 {
		return text[:180]
	}
	return text
}

func (s *Server) sendPhoneSessionCommand(ctx context.Context, payload map[string]any) error {
	conn, err := s.dialPhoneSessionCommand(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(websocket.StatusNormalClosure, "command dispatched")
	return writePhoneSessionCommand(ctx, conn, payload)
}

func (s *Server) sendPhoneSessionCommandAndReadControlCodeResult(ctx context.Context, command state.StreamCommand, payload map[string]any, requestID string) error {
	conn, err := s.dialPhoneSessionCommand(ctx)
	if err != nil {
		return err
	}
	if err := writePhoneSessionCommand(ctx, conn, payload); err != nil {
		_ = conn.Close(websocket.StatusInternalError, "command write failed")
		return err
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return conn.Close(websocket.StatusNormalClosure, "command dispatched")
	}
	s.updateSpacetimeControlCodeRequestAsync(requestID, controlCodeRunning, "dispatched_by_ticket_remote_bridge", "", 0, 0, 0, 0, 0, "", "", false)
	readCtx, releaseReadCtx := context.WithTimeout(context.Background(), controlCodePhoneResultWait+controlCodePhoneCleanupWait+5*time.Second)
	return s.readPhoneControlCodeResult(readCtx, releaseReadCtx, command.ID, requestID, conn)
}

func (s *Server) dialPhoneSessionCommand(ctx context.Context) (*websocket.Conn, error) {
	target, err := s.phoneSessionWebsocketURL()
	if err != nil {
		return nil, err
	}
	conn, _, err := websocket.Dial(ctx, target, &websocket.DialOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return nil, fmt.Errorf("dial phone command socket: %w", err)
	}
	return conn, nil
}

func writePhoneSessionCommand(ctx context.Context, conn *websocket.Conn, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, body)
}

func (s *Server) readPhoneControlCodeResult(ctx context.Context, releaseReadCtx context.CancelFunc, commandID string, requestID string, conn *websocket.Conn) error {
	ctx, cancel := context.WithCancel(ctx)
	var completeOnce sync.Once
	completed := make(chan struct{})
	var resultOnce sync.Once
	resultObserved := make(chan struct{})
	markCompleted := func() {
		completeOnce.Do(func() {
			close(completed)
			cancel()
			if releaseReadCtx != nil {
				releaseReadCtx()
			}
			_ = conn.Close(websocket.StatusNormalClosure, "control code result observed")
		})
	}
	markResultObserved := func() {
		resultOnce.Do(func() {
			close(resultObserved)
			time.AfterFunc(controlCodePhoneCleanupWait, markCompleted)
		})
	}
	observeBridgeMessage := func(observation controlCodeBridgeObservation) {
		switch observation {
		case controlCodeBridgeCleanupObserved:
			markResultObserved()
			markCompleted()
		case controlCodeBridgeResultObserved:
			markResultObserved()
		}
	}
	readErr := make(chan error, 1)
	go s.pollPhoneControlCodeHealthForResult(ctx, requestID, observeBridgeMessage)
	go func() {
		for {
			msgType, body, err := conn.Read(ctx)
			if err != nil {
				if controlCodeBridgeCompleted(completed) || controlCodeBridgeCompleted(resultObserved) {
					return
				}
				select {
				case readErr <- err:
				default:
				}
				return
			}
			if msgType != websocket.MessageText {
				continue
			}
			var msg map[string]any
			if err := json.Unmarshal(body, &msg); err != nil {
				continue
			}
			observeBridgeMessage(s.handlePhoneControlCodeBridgeMessage(requestID, msg))
		}
	}()
	select {
	case <-resultObserved:
		return nil
	case <-completed:
		return nil
	case err := <-readErr:
		ctxErr := ctx.Err()
		markCompleted()
		if ctxErr != nil {
			s.updateSpacetimeControlCodeRequestAsync(requestID, controlCodeFailed, "phone_timeout", "phone_timeout", 0, 0, 0, 0, 0, "", "", false)
			s.appendStreamCommandBridgeLogAsync("warn", "control_code_result_timeout", state.StreamCommand{ID: commandID, CommandType: "generate_control_code", BackendID: s.activePhoneBackend().ID}, err)
			return fmt.Errorf("read control code result: %w", ctxErr)
		}
		s.updateSpacetimeControlCodeRequestAsync(requestID, controlCodeFailed, "phone_command_socket_closed", "phone_command_socket_closed", 0, 0, 0, 0, 0, "", "", false)
		s.appendStreamCommandBridgeLogAsync("warn", "control_code_result_socket_closed", state.StreamCommand{ID: commandID, CommandType: "generate_control_code", BackendID: s.activePhoneBackend().ID}, err)
		return fmt.Errorf("read control code result: %w", err)
	case <-ctx.Done():
		if controlCodeBridgeCompleted(resultObserved) {
			return nil
		}
		markCompleted()
		s.updateSpacetimeControlCodeRequestAsync(requestID, controlCodeFailed, "phone_timeout", "phone_timeout", 0, 0, 0, 0, 0, "", "", false)
		s.appendStreamCommandBridgeLogAsync("warn", "control_code_result_timeout", state.StreamCommand{ID: commandID, CommandType: "generate_control_code", BackendID: s.activePhoneBackend().ID}, ctx.Err())
		return fmt.Errorf("read control code result: %w", ctx.Err())
	}
}

func controlCodeBridgeCompleted(completed <-chan struct{}) bool {
	select {
	case <-completed:
		return true
	default:
		return false
	}
}

func (s *Server) pollPhoneControlCodeHealthForResult(ctx context.Context, requestID string, observe func(controlCodeBridgeObservation)) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return
	}
	if observation := s.reconcilePhoneControlCodeHealth(ctx, requestID); observation != controlCodeBridgeIgnored {
		observe(observation)
		if observation == controlCodeBridgeCleanupObserved {
			return
		}
	}
	ticker := time.NewTicker(controlCodePhoneHealthPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			observation := s.reconcilePhoneControlCodeHealth(ctx, requestID)
			if observation != controlCodeBridgeIgnored {
				observe(observation)
			}
			if observation == controlCodeBridgeCleanupObserved {
				return
			}
		}
	}
}

func (s *Server) reconcilePhoneControlCodeHealth(ctx context.Context, requestID string) controlCodeBridgeObservation {
	data, err := s.fetchPhoneHealthData(ctx)
	if err != nil || len(data) == 0 {
		return controlCodeBridgeIgnored
	}
	controlCode, _ := data["controlCodeRequest"].(map[string]any)
	if controlCodeRequestID(controlCode) != requestID {
		controlCode = nil
	}
	for _, key := range []string{"pixelTicketStateEvent", "ticketStateEvent"} {
		eventRaw, _ := data[key].(map[string]any)
		if len(eventRaw) == 0 {
			continue
		}
		eventMsg := clonePhoneHealthMap(eventRaw)
		eventMsg["type"] = "ticket_state_event"
		if strings.TrimSpace(fmt.Sprint(eventMsg["requestId"])) != requestID {
			continue
		}
		if controlCode != nil {
			if _, ok := eventMsg["totalDurationMillis"]; !ok {
				eventMsg["totalDurationMillis"] = controlCode["totalDurationMillis"]
			}
			if _, ok := eventMsg["phases"]; !ok {
				eventMsg["phases"] = controlCode["phases"]
			}
		}
		observation := s.handlePhoneControlCodeBridgeMessage(requestID, eventMsg)
		if observation != controlCodeBridgeIgnored {
			return observation
		}
	}
	return controlCodeBridgeIgnored
}

func controlCodeRequestID(controlCode map[string]any) string {
	if len(controlCode) == 0 {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(controlCode["requestId"]))
}

func clonePhoneHealthMap(input map[string]any) map[string]any {
	output := make(map[string]any, len(input)+1)
	for key, value := range input {
		output[key] = value
	}
	return output
}

func (s *Server) fetchPhoneHealthData(ctx context.Context) (map[string]any, error) {
	base := s.phoneCommandBaseURL()
	if base == "" {
		return nil, fmt.Errorf("phone command base URL is empty")
	}
	requestCtx, cancel := context.WithTimeout(ctx, controlCodePhoneHealthRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, strings.TrimRight(base, "/")+"/api/v1/health", nil)
	if err != nil {
		return nil, err
	}
	client := s.phoneBrokerHTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("phone health returned HTTP %d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1024*1024)).Decode(&payload); err != nil {
		return nil, err
	}
	if data, ok := payload["data"].(map[string]any); ok {
		return data, nil
	}
	return payload, nil
}

func (s *Server) handlePhoneControlCodeBridgeMessage(requestID string, msg map[string]any) controlCodeBridgeObservation {
	if s.handlePixelTraceEvent(msg) {
		return controlCodeBridgeIgnored
	}
	if event, ok := pixelTicketEventFromMessage(msg); ok {
		s.handlePixelTicketStateEvent(msg)
		if event.RequestID != requestID {
			return controlCodeBridgeIgnored
		}
		switch event.TicketState {
		case "generated_result":
			return controlCodeBridgeResultObserved
		case "raw_ticket":
			return controlCodeBridgeCleanupObserved
		}
		return controlCodeBridgeIgnored
	}
	msgType, _ := msg["type"].(string)
	msgRequestID, _ := msg["requestId"].(string)
	if strings.TrimSpace(msgRequestID) != requestID {
		return controlCodeBridgeIgnored
	}
	if s.handleControlCodePhoneResult(msg) {
		switch strings.TrimSpace(msgType) {
		case "control_code_cleanup_complete":
			return controlCodeBridgeCleanupObserved
		case "control_code_frame_ready", "control_code_result":
			return controlCodeBridgeResultObserved
		}
	}
	return controlCodeBridgeIgnored
}

func (s *Server) postPhoneSessionCommand(ctx context.Context, path string) error {
	base := s.phoneCommandBaseURL()
	if base == "" {
		return fmt.Errorf("phone command base URL is empty")
	}
	requestURL := strings.TrimRight(base, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, nil)
	if err != nil {
		return err
	}
	client := s.phoneBrokerHTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s returned HTTP %d", path, resp.StatusCode)
	}
	return nil
}

func (s *Server) phoneCommandBaseURL() string {
	if brokerURL := strings.TrimRight(strings.TrimSpace(s.cfg.Phone.BrokerBaseURL), "/"); brokerURL != "" {
		return brokerURL
	}
	return strings.TrimRight(strings.TrimSpace(s.activePhoneBackend().BaseURL), "/")
}

func (s *Server) phoneSessionWebsocketURL() (string, error) {
	base := s.phoneCommandBaseURL()
	if base == "" {
		return "", fmt.Errorf("phone command base URL is empty")
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported phone command URL scheme %q", parsed.Scheme)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/api/v1/session"
	parsed.RawQuery = ""
	return parsed.String(), nil
}
