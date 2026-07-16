package web

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	. "ticketremote/internal/state"
	"time"
)

const (
	PresenceTTL = 45 * time.Second
)

type AuditEvent struct {
	ID, TicketID, ActorEmail, Event, PayloadJSON, CreatedAt string
}

type MemoryStore struct {
	mu      sync.Mutex
	tickets map[string]*memoryTicket
}

type memoryTicket struct {
	ticket   Ticket
	members  map[string]Member
	presence map[string]Viewer
	phone    *PhoneBackend
	audit    []AuditEvent
	safeLogs []SafeOperationalLogInput
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{tickets: map[string]*memoryTicket{}}
}

func (s *MemoryStore) Backend() string {
	return "memory"
}

func (s *MemoryStore) Bootstrap(_ context.Context, input BootstrapInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := iso(time.Now())
	ticketID := stringsNonEmpty(input.TicketID, "vivi-default")
	ticket := s.ensureTicketLocked(ticketID, input.DisplayName, now)
	adminEmail := normalizeEmail(input.AdminEmail)
	if adminEmail != "" {
		if _, ok := ticket.members[adminEmail]; !ok {
			ticket.members[adminEmail] = Member{Email: adminEmail, PublicID: accountPublicID(adminEmail), Role: RoleOwner, Active: true, UpdatedAt: now}
		}
	}
	if input.PhoneBackendID != "" {
		ticket.phone = &PhoneBackend{
			ID:           input.PhoneBackendID,
			AttachName:   stringsNonEmpty(input.PhoneAttachName, input.PhoneBackendID),
			BaseURL:      input.PhoneBaseURL,
			DesiredState: "idle",
			LastSeenAt:   now,
		}
	}
	return nil
}

func (s *MemoryStore) Snapshot(_ context.Context, ticketID string, now time.Time) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ticket := s.ensureTicketLocked(ticketID, "", iso(now))
	s.cleanupLocked(ticket, now)
	return s.snapshotLocked(ticket, now), nil
}

func (s *MemoryStore) UpsertMember(_ context.Context, ticketID string, actorEmail string, email string, role string) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	ticket := s.ensureTicketLocked(ticketID, "", iso(now))
	s.cleanupLocked(ticket, now)
	if !ticket.isAdmin(actorEmail) {
		return s.snapshotLocked(ticket, now), ErrForbidden
	}
	clean := normalizeEmail(email)
	if clean == "" {
		return s.snapshotLocked(ticket, now), fmt.Errorf("email is required")
	}
	ticket.members[clean] = Member{Email: clean, PublicID: accountPublicID(clean), Role: normalizeRole(role), Active: true, UpdatedAt: iso(now)}
	ticket.audit = append(ticket.audit, auditEvent(ticketID, actorEmail, "member_upserted", map[string]any{"email": clean, "role": normalizeRole(role)}, now))
	return s.snapshotLocked(ticket, now), nil
}

func (s *MemoryStore) RemoveMember(_ context.Context, ticketID string, actorEmail string, email string) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	ticket := s.ensureTicketLocked(ticketID, "", iso(now))
	s.cleanupLocked(ticket, now)
	if !ticket.isAdmin(actorEmail) {
		return s.snapshotLocked(ticket, now), ErrForbidden
	}
	clean := normalizeEmail(email)
	member := ticket.members[clean]
	member.Active = false
	member.UpdatedAt = iso(now)
	ticket.members[clean] = member
	ticket.audit = append(ticket.audit, auditEvent(ticketID, actorEmail, "member_removed", map[string]any{"email": clean}, now))
	return s.snapshotLocked(ticket, now), nil
}

func (s *MemoryStore) UpdatePhone(_ context.Context, input PhoneInput) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}
	ticket := s.ensureTicketLocked(input.TicketID, "", iso(now))
	phoneID := stringsNonEmpty(input.BackendID, "pixel")
	ticket.phone = &PhoneBackend{
		ID:           phoneID,
		AttachName:   stringsNonEmpty(input.AttachName, phoneID),
		BaseURL:      input.BaseURL,
		DesiredState: stringsNonEmpty(input.DesiredState, "idle"),
		HealthJSON:   input.HealthJSON,
		LastError:    input.LastError,
		LastSeenAt:   iso(now),
	}
	s.cleanupLocked(ticket, now)
	return s.snapshotLocked(ticket, now), nil
}

func (s *MemoryStore) UpdatePhoneStatus(ctx context.Context, input PhoneInput) error {
	_, err := s.UpdatePhone(ctx, input)
	return err
}

func (s *MemoryStore) SetStreamDesiredState(_ context.Context, _ StreamDesiredStateInput) error {
	return nil
}

func (s *MemoryStore) AppendStreamCommand(_ context.Context, _ StreamCommandInput) error {
	return nil
}

func (s *MemoryStore) UpdatePhoneCurrentReport(_ context.Context, _ PhoneCurrentReportInput) error {
	return nil
}

func (s *MemoryStore) UpdateRelayCurrentReport(_ context.Context, _ RelayCurrentReportInput) error {
	return nil
}

func (s *MemoryStore) UpdateControlCodeRequest(_ context.Context, _ ControlCodeRequestUpdateInput) error {
	return nil
}

func (s *MemoryStore) AppendSafeOperationalLog(_ context.Context, input SafeOperationalLogInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}
	ticketID := stringsNonEmpty(input.TicketID, DefaultTicketID)
	ticket := s.ensureTicketLocked(ticketID, "", iso(now))
	ticket.safeLogs = append(ticket.safeLogs, SafeOperationalLogInput{
		ID:            stringsNonEmpty(input.ID, NewSafeOperationalLogID(input.Source, input.Event, input.CorrelationID, now)),
		TicketID:      ticketID,
		Source:        stringsNonEmpty(input.Source, "memory"),
		Level:         stringsNonEmpty(input.Level, "info"),
		Event:         stringsNonEmpty(input.Event, "event"),
		CorrelationID: input.CorrelationID,
		DetailJSON:    ClampSafeOperationalLogDetail(input.DetailJSON),
		Now:           now,
	})
	return nil
}

func (s *MemoryStore) Audit(_ context.Context, ticketID string, actorEmail string, event string, payload map[string]any, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ticket := s.ensureTicketLocked(ticketID, "", iso(now))
	ticket.audit = append(ticket.audit, auditEvent(ticketID, actorEmail, event, payload, now))
	return nil
}

func (s *MemoryStore) ensureTicketLocked(ticketID string, displayName string, nowISO string) *memoryTicket {
	if ticketID == "" {
		ticketID = DefaultTicketID
	}
	if existing := s.tickets[ticketID]; existing != nil {
		if displayName != "" {
			existing.ticket.DisplayName = displayName
			existing.ticket.UpdatedAt = nowISO
		}
		return existing
	}
	ticket := &memoryTicket{
		ticket: Ticket{
			ID:          ticketID,
			DisplayName: stringsNonEmpty(displayName, DefaultTicketName),
			UpdatedAt:   nowISO,
		},
		members:  map[string]Member{},
		presence: map[string]Viewer{},
	}
	s.tickets[ticketID] = ticket
	return ticket
}

func (s *MemoryStore) cleanupLocked(ticket *memoryTicket, now time.Time) {
	for sessionID, viewer := range ticket.presence {
		lastSeenAt, err := time.Parse(time.RFC3339, viewer.LastSeenAt)
		if err == nil && now.Sub(lastSeenAt) > PresenceTTL {
			delete(ticket.presence, sessionID)
		}
	}
}

func (s *MemoryStore) snapshotLocked(ticket *memoryTicket, now time.Time) Snapshot {
	if now.IsZero() {
		now = time.Now()
	}
	members := make([]Member, 0, len(ticket.members))
	for _, member := range ticket.members {
		member.PublicID = stringsNonEmpty(member.PublicID, accountPublicID(member.Email))
		members = append(members, member)
	}
	sort.Slice(members, func(i, j int) bool {
		return members[i].Email < members[j].Email
	})
	viewers := make([]Viewer, 0, len(ticket.presence))
	for _, viewer := range ticket.presence {
		if viewer.Connected {
			viewers = append(viewers, viewer)
		}
	}
	sort.Slice(viewers, func(i, j int) bool {
		return viewers[i].Email < viewers[j].Email
	})
	var phone *PhoneBackend
	if ticket.phone != nil {
		copy := *ticket.phone
		phone = &copy
	}
	return Snapshot{
		Ticket:       ticket.ticket,
		Members:      members,
		Viewers:      viewers,
		Phone:        phone,
		ServerTime:   iso(now),
		StateBackend: s.Backend(),
	}
}

func (t *memoryTicket) isAdmin(email string) bool {
	member, ok := t.members[normalizeEmail(email)]
	return ok && member.Active && (member.Role == RoleOwner || member.Role == RoleAdmin)
}

func auditEvent(ticketID string, actorEmail string, event string, payload map[string]any, now time.Time) AuditEvent {
	raw, _ := json.Marshal(payload)
	return AuditEvent{
		ID:          fmt.Sprintf("%d", now.UnixNano()),
		TicketID:    ticketID,
		ActorEmail:  normalizeEmail(actorEmail),
		Event:       event,
		PayloadJSON: string(raw),
		CreatedAt:   iso(now),
	}
}

func stringsNonEmpty(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func normalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeRole(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case RoleOwner:
		return RoleOwner
	case RoleAdmin:
		return RoleAdmin
	default:
		return RoleMember
	}
}

func iso(value time.Time) string {
	if value.IsZero() {
		value = time.Now()
	}
	return value.UTC().Format(time.RFC3339)
}

func accountPublicID(email string) string {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	var hash uint32 = 2166136261
	for _, value := range []byte(normalizeEmail(email)) {
		hash ^= uint32(value)
		hash *= 16777619
	}
	value := hash % (36 * 36 * 36 * 36)
	var out [4]byte
	for index := len(out) - 1; index >= 0; index-- {
		out[index] = alphabet[value%36]
		value /= 36
	}
	return string(out[:])
}
