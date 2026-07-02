package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	ticketViewerPhoneLeaseTTL = 60 * time.Second
)

type ticketPhoneLeaseRequest struct {
	LeaseID   string `json:"leaseId"`
	RequestID string `json:"requestId,omitempty"`
	Reason    string `json:"reason,omitempty"`
	TTLMillis int64  `json:"ttlMillis,omitempty"`
}

func viewerTicketPhoneLeaseID(sessionID string) (string, bool) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || strings.HasPrefix(sessionID, "control-code:") {
		return "", false
	}
	return "viewer:" + sessionID, true
}

func (s *Server) acquireTicketPhoneLeaseAsync(leaseID string, requestID string, reason string, ttl time.Duration) {
	leaseID = strings.TrimSpace(leaseID)
	if leaseID == "" {
		return
	}
	request := ticketPhoneLeaseRequest{
		LeaseID:   leaseID,
		RequestID: strings.TrimSpace(requestID),
		Reason:    strings.TrimSpace(reason),
		TTLMillis: int64(ttl / time.Millisecond),
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), phoneBrokerPresenceTimeout)
		defer cancel()
		if err := s.postTicketPhoneLease(ctx, "/api/v1/phone/leases/ticket", request); err != nil {
			s.recordRuntimeErrorAsync("ticket_phone_lease_acquire_failed", request.LeaseID, err, map[string]any{
				"leaseId":   request.LeaseID,
				"requestId": request.RequestID,
				"reason":    request.Reason,
			})
		}
	}()
}

func (s *Server) releaseTicketPhoneLeaseAsync(leaseID string, requestID string) {
	leaseID = strings.TrimSpace(leaseID)
	if leaseID == "" {
		return
	}
	request := ticketPhoneLeaseRequest{
		LeaseID:   leaseID,
		RequestID: strings.TrimSpace(requestID),
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), phoneBrokerPresenceTimeout)
		defer cancel()
		if err := s.postTicketPhoneLease(ctx, "/api/v1/phone/leases/ticket/release", request); err != nil {
			s.recordRuntimeErrorAsync("ticket_phone_lease_release_failed", request.LeaseID, err, map[string]any{
				"leaseId":   request.LeaseID,
				"requestId": request.RequestID,
			})
		}
	}()
}

func (s *Server) postTicketPhoneLease(ctx context.Context, path string, request ticketPhoneLeaseRequest) error {
	brokerURL := strings.TrimRight(strings.TrimSpace(s.cfg.Phone.BrokerBaseURL), "/")
	if brokerURL == "" {
		return nil
	}
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, brokerURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	client := s.phoneBrokerHTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpRequest)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s returned HTTP %d", path, resp.StatusCode)
	}
	return nil
}
