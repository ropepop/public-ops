package web

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"ticketremote/internal/auth"
	"ticketremote/internal/state"
)

type ticketPush struct {
	PublicKey  string `json:"publicKey"`
	PrivateKey string `json:"privateKey"`
	client     *http.Client
	cancel     context.CancelFunc
	done       chan struct{}
}

func (s *Server) handleNotificationWorker(w http.ResponseWriter, r *http.Request) {
	writeNoStoreHeaders(w)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, err := staticFS.ReadFile("pwa/ticket-notifications-sw.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Service-Worker-Allowed", "/")
	if r.Method == http.MethodGet {
		_, _ = w.Write(body)
	}
}

func readNotificationJSON(w http.ResponseWriter, r *http.Request, input any) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		http.Error(w, "invalid_request", http.StatusBadRequest)
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	d.DisallowUnknownFields()
	if err := d.Decode(input); err != nil || d.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid_request", http.StatusBadRequest)
		return false
	}
	return true
}

func notificationStoreError(w http.ResponseWriter, err error) {
	code := http.StatusServiceUnavailable
	if errors.Is(err, state.ErrForbidden) || errors.Is(err, state.ErrNotMember) {
		code = http.StatusForbidden
	}
	// Store errors may contain endpoint/key material; never return or log them.
	http.Error(w, "notifications_unavailable", code)
}

func (s *Server) handleMonitoring(w http.ResponseWriter, r *http.Request, id auth.Identity, _ string, snapshot state.Snapshot) {
	store, ok := s.store.(state.MonitoringStore)
	if !ok {
		http.Error(w, "monitoring_unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	member, _ := snapshot.Member(id.Email)
	if r.Method == http.MethodPost {
		if member.Role != state.RoleOwner {
			http.Error(w, "owner_required", http.StatusForbidden)
			return
		}
		var input struct {
			Enabled *bool `json:"enabled"`
		}
		if !readNotificationJSON(w, r, &input) {
			return
		}
		if input.Enabled == nil {
			http.Error(w, "enabled_required", http.StatusBadRequest)
			return
		}
		if err := store.SetMonitoring(ctx, s.cfg.TicketID, id.Email, *input.Enabled); err != nil {
			notificationStoreError(w, err)
			return
		}
	} else if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	value, err := store.Monitoring(ctx, s.cfg.TicketID, id.Email)
	if err != nil {
		notificationStoreError(w, err)
		return
	}
	key := ""
	if s.push != nil {
		key = s.push.PublicKey
	}
	writeJSON(w, http.StatusOK, struct {
		state.MonitoringState
		CanManageMonitoring bool   `json:"canManageMonitoring"`
		VAPIDPublicKey      string `json:"vapidPublicKey"`
		PushAvailable       bool   `json:"pushAvailable"`
	}{value, member.Role == state.RoleOwner, key, key != ""})
}

func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request, id auth.Identity, _ string, _ state.Snapshot) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	store, ok := s.store.(state.MonitoringStore)
	if !ok {
		http.Error(w, "notifications_unavailable", http.StatusServiceUnavailable)
		return
	}
	var input struct {
		Action       string `json:"action"`
		Endpoint     string `json:"endpoint"`
		Subscription *struct {
			webpush.Subscription
			ExpirationTime *float64 `json:"expirationTime"`
		} `json:"subscription"`
	}
	if !readNotificationJSON(w, r, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	switch input.Action {
	case "status":
		if input.Endpoint == "" {
			writeJSON(w, http.StatusOK, map[string]bool{"subscribed": false})
			return
		}
		if !validPushEndpoint(input.Endpoint) {
			http.Error(w, "invalid_subscription", http.StatusBadRequest)
			return
		}
		subscribed, err := store.PushSubscribed(ctx, s.cfg.TicketID, id.Email, input.Endpoint)
		if err != nil {
			notificationStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"subscribed": subscribed})
	case "subscribe", "unsubscribe":
		value := state.PushSubscriptionInput{TicketID: s.cfg.TicketID, Email: id.Email, Endpoint: input.Endpoint}
		if input.Action == "subscribe" {
			if s.push == nil {
				http.Error(w, "push_not_configured", http.StatusServiceUnavailable)
				return
			}
			if input.Subscription == nil || !validPushSubscription(input.Subscription.Subscription) {
				http.Error(w, "invalid_subscription", http.StatusBadRequest)
				return
			}
			value.Endpoint, value.P256dh, value.Auth, value.Enabled = input.Subscription.Endpoint, input.Subscription.Keys.P256dh, input.Subscription.Keys.Auth, true
		}
		if !validPushEndpoint(value.Endpoint) {
			http.Error(w, "invalid_subscription", http.StatusBadRequest)
			return
		}
		if err := store.SetPushSubscription(ctx, value); err != nil {
			notificationStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"subscribed": value.Enabled})
	default:
		http.Error(w, "invalid_action", http.StatusBadRequest)
	}
}

func validPushEndpoint(raw string) bool {
	if len(raw) > 2048 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "fcm.googleapis.com" || host == "updates.push.services.mozilla.com" || host == "push.services.mozilla.com" || host == "web.push.apple.com" || strings.HasSuffix(host, ".push.apple.com") || strings.HasSuffix(host, ".notify.windows.com")
}

func validPushSubscription(value webpush.Subscription) bool {
	if !validPushEndpoint(value.Endpoint) {
		return false
	}
	key, err := base64.RawURLEncoding.DecodeString(value.Keys.P256dh)
	if err != nil || len(key) != 65 {
		return false
	}
	if _, err = ecdh.P256().NewPublicKey(key); err != nil {
		return false
	}
	authKey, err := base64.RawURLEncoding.DecodeString(value.Keys.Auth)
	return err == nil && len(authKey) == 16
}

func (s *Server) startTicketPush() error {
	if s.cfg.WebPushKeyFile == "" {
		return nil
	}
	info, err := os.Lstat(s.cfg.WebPushKeyFile)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return errors.New("Web Push key file must be a private regular file")
	}
	data, err := os.ReadFile(s.cfg.WebPushKeyFile)
	if err != nil {
		return errors.New("cannot read Web Push key file")
	}
	var push ticketPush
	if len(data) > 1024 || json.Unmarshal(data, &push) != nil {
		return errors.New("invalid Web Push key file")
	}
	privateBytes, err := base64.RawURLEncoding.DecodeString(push.PrivateKey)
	if err != nil {
		return errors.New("invalid Web Push private key")
	}
	private, err := ecdh.P256().NewPrivateKey(privateBytes)
	if err != nil || base64.RawURLEncoding.EncodeToString(private.PublicKey().Bytes()) != push.PublicKey {
		return errors.New("Web Push key pair does not match")
	}
	store, ok := s.store.(state.MonitoringStore)
	if !ok {
		return errors.New("Web Push requires monitoring state")
	}
	push.client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ctx, cancel := context.WithCancel(context.Background())
	push.cancel, push.done = cancel, make(chan struct{})
	s.push = &push
	go s.ticketPushLoop(ctx, store)
	return nil
}

func (s *Server) ticketPushLoop(ctx context.Context, store state.MonitoringStore) {
	defer close(s.push.done)
	wake := make(chan struct{}, 1)
	signal := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		for ctx.Err() == nil {
			_ = store.WatchMonitoring(ctx, signal)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}()
	defer func() { <-watchDone }()
	signal()
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-timer.C:
		}
		next := s.deliverTicketPush(ctx, store)
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(next)
	}
}

func (s *Server) deliverTicketPush(ctx context.Context, store state.MonitoringStore) time.Duration {
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	deliveries, err := store.PushDeliveries(callCtx, s.cfg.TicketID)
	cancel()
	if err != nil {
		return 30 * time.Second
	}
	next := time.Hour
	for _, delivery := range deliveries {
		if ctx.Err() != nil {
			return next
		}
		if wait := time.Until(time.UnixMilli(delivery.NextAttemptAtMS)); wait > 0 {
			if wait < next {
				next = wait
			}
			continue
		}
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return time.Minute
		}
		claim := hex.EncodeToString(nonce[:])
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		current, err := store.ClaimPushDelivery(callCtx, s.cfg.TicketID, delivery.ID, claim)
		if err == nil && current != nil {
			outcome := s.sendTicketPush(callCtx, *current)
			err = store.FinishPushDelivery(callCtx, s.cfg.TicketID, delivery.ID, claim, outcome)
			if err == nil {
				s.recordRuntimeEventForSourceAsync("ticket_remote", "info", "ticket_notification_delivery", "", map[string]any{"status": outcome, "kind": current.Kind})
			}
		}
		cancel()
		// A lost receipt is recovered through the durable claim, never an immediate resend.
		if next > 30*time.Second {
			next = 30 * time.Second
		}
	}
	return next
}

func notificationBody(delivery state.PushDelivery) string {
	if delivery.Kind == "recovery" {
		return "Ticket is ready again."
	}
	if delivery.Reason == "capture_unavailable" || delivery.Reason == "observation_overdue" || delivery.Reason == "busy" || delivery.Reason == "unknown" {
		return "Ticket could not be checked twice in a row. Open Ticket to review it."
	}
	return "Two checks in a row found the same ticket problem. Open Ticket to check it."
}

func (s *Server) sendTicketPush(ctx context.Context, delivery state.PushDelivery) string {
	sub := webpush.Subscription{Endpoint: delivery.Endpoint, Keys: webpush.Keys{P256dh: delivery.P256dh, Auth: delivery.Auth}}
	if !validPushSubscription(sub) {
		return "invalid"
	}
	tagHash := sha256.Sum256([]byte(delivery.IncidentID))
	tag := "ticket-" + hex.EncodeToString(tagHash[:12])
	payload, _ := json.Marshal(map[string]string{"title": "Ticket", "body": notificationBody(delivery), "tag": tag})
	response, err := webpush.SendNotificationWithContext(ctx, payload, &sub, &webpush.Options{
		Subscriber: s.cfg.PublicBaseURL, VAPIDPublicKey: s.push.PublicKey, VAPIDPrivateKey: s.push.PrivateKey,
		TTL: 300, Topic: tag, Urgency: webpush.UrgencyNormal, HTTPClient: s.push.client,
	})
	if err != nil {
		return "retry"
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		return "sent"
	case response.StatusCode == 404 || response.StatusCode == 410:
		return "invalid"
	case response.StatusCode == 429 || response.StatusCode >= 500:
		return "retry"
	default:
		return "failed"
	}
}
