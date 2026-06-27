package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"nhooyr.io/websocket"

	"ticketremote/internal/state"
)

const (
	streamCommandBridgeFastPollInterval   = 500 * time.Millisecond
	streamCommandBridgeActivePollInterval = time.Second
	streamCommandBridgeQuietPollInterval  = 2 * time.Second
	streamCommandBridgeIdlePollInterval   = 10 * time.Second
	streamCommandBridgeReadTimeout        = 4 * time.Second
	streamCommandBridgeWriteTimeout       = 8 * time.Second
	streamCommandBridgeLimit              = 20
	streamCommandBridgeBackoffMax         = 5 * time.Second
)

type pendingStreamCommandReader interface {
	PendingStreamCommands(ctx context.Context, ticketID string, backendID string, limit uint32, now time.Time) ([]state.StreamCommand, error)
}

type streamCommandSignalReader interface {
	StreamCommandSignal(ctx context.Context, ticketID string, backendID string) (state.StreamCommandSignal, bool, error)
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
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-stop:
			return
		case <-timer.C:
		}
		hadCommands := s.pollPendingStreamCommands(reader, attempts, lastSignals)
		timer.Reset(s.nextStreamCommandBridgePollDelay(hadCommands, attempts))
	}
}

func (s *Server) pollPendingStreamCommands(reader pendingStreamCommandReader, attempts map[string]streamCommandBridgeAttempt, lastSignals map[string]string) bool {
	now := time.Now()
	backend := s.activePhoneBackend()
	if signalReader, ok := reader.(streamCommandSignalReader); ok && len(attempts) == 0 {
		signalCtx, signalCancel := context.WithTimeout(context.Background(), streamCommandBridgeReadTimeout)
		signal, found, err := signalReader.StreamCommandSignal(signalCtx, s.cfg.TicketID, backend.ID)
		signalCancel()
		if err != nil {
			log.Printf("ticket stream command bridge signal read failed: %v", err)
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
		log.Printf("ticket stream command bridge read failed: %v", err)
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
		attempt := attempts[command.ID]
		if !attempt.next.IsZero() && now.Before(attempt.next) {
			continue
		}
		if !attempt.dispatched {
			ctx, cancel := context.WithTimeout(context.Background(), streamCommandBridgeWriteTimeout)
			err := s.dispatchStreamCommandToPhone(ctx, command)
			cancel()
			if err != nil {
				attempt.failures++
				attempt.next = now.Add(streamCommandBridgeBackoff(attempt.failures))
				attempts[command.ID] = attempt
				s.appendStreamCommandBridgeLogAsync("warn", "stream_command_dispatch_failed", command, err)
				continue
			}
			attempt.dispatched = true
			attempt.failures = 0
			attempt.next = time.Time{}
			attempts[command.ID] = attempt
		}
		if err := s.ackDispatchedStreamCommand(command); err != nil {
			attempt.failures++
			attempt.next = now.Add(streamCommandBridgeBackoff(attempt.failures))
			attempts[command.ID] = attempt
			log.Printf("ticket stream command bridge ack failed command=%s type=%s: %v", command.ID, command.CommandType, err)
			continue
		}
		delete(attempts, command.ID)
	}
	pruneStreamCommandBridgeAttempts(attempts, now)
	return hadCommands
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
		s.relay.EnsureActive("spacetime_command_start")
		startErr := s.postPhoneSessionCommand(ctx, "/api/v1/session/start")
		sendErr := s.sendPhoneSessionCommand(ctx, payload)
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
		s.direct.recordKeyframeRequested()
		return s.sendPhoneSessionCommand(ctx, payload)
	case "recover_stream":
		_ = s.postPhoneSessionCommand(ctx, "/api/v1/session/start")
		s.relay.Reconnect("spacetime_command_recover_stream")
		return s.sendPhoneSessionCommand(ctx, payload)
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
	go func() {
		detail := map[string]any{
			"commandId":   command.ID,
			"commandType": cleanStreamControlText(command.CommandType, "command"),
			"backendId":   cleanStreamControlText(command.BackendID, "pixel"),
		}
		if err != nil {
			detail["error"] = safeStreamCommandBridgeError(err)
		}
		body, marshalErr := json.Marshal(detail)
		if marshalErr != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), streamControlWriteTimeout)
		defer cancel()
		_ = s.store.AppendSafeOperationalLog(ctx, state.SafeOperationalLogInput{
			TicketID:      s.cfg.TicketID,
			Source:        "ticket_remote",
			Level:         cleanStreamControlText(level, "info"),
			Event:         cleanStreamControlText(event, "stream_command_bridge"),
			CorrelationID: command.ID,
			DetailJSON:    string(body),
			Now:           time.Now(),
		})
	}()
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
	go s.readPhoneControlCodeResult(command.ID, requestID, conn)
	return nil
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

func (s *Server) readPhoneControlCodeResult(commandID string, requestID string, conn *websocket.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), controlCodePhoneResultWait+5*time.Second)
	defer cancel()
	defer conn.Close(websocket.StatusNormalClosure, "control code result read complete")
	for {
		msgType, body, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				s.updateSpacetimeControlCodeRequestAsync(requestID, controlCodeFailed, "phone_timeout", "phone_timeout", 0, 0, 0, 0, 0, "", "", false)
				s.appendStreamCommandBridgeLogAsync("warn", "control_code_result_timeout", state.StreamCommand{ID: commandID, CommandType: "generate_control_code", BackendID: s.activePhoneBackend().ID}, err)
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
		if s.handlePhoneControlCodeBridgeMessage(requestID, msg) {
			return
		}
	}
}

func (s *Server) handlePhoneControlCodeBridgeMessage(requestID string, msg map[string]any) bool {
	if event, ok := pixelTicketEventFromMessage(msg); ok {
		s.handlePixelTicketStateEvent(msg)
		if event.RequestID == requestID && event.TicketState == "generated_result" {
			return true
		}
		return false
	}
	msgType, _ := msg["type"].(string)
	msgRequestID, _ := msg["requestId"].(string)
	if strings.TrimSpace(msgRequestID) != requestID {
		return false
	}
	if s.handleControlCodePhoneResult(msg) {
		switch strings.TrimSpace(msgType) {
		case "control_code_frame_ready", "control_code_result", "control_code_cleanup_complete":
			return true
		}
	}
	return false
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
