package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	unlimitedQuota        = -1
	defaultUserDailyLimit = 20
)

type AccessController interface {
	RecordUser(ctx context.Context, req AccessRequest) (string, error)
	IsAdmin(ctx context.Context, req AccessRequest) (bool, error)
	AnnouncementRecipients(ctx context.Context) ([]AnnouncementRecipient, error)
	AuthorizeAndConsume(ctx context.Context, req AccessRequest) (AccessDecision, error)
	AuthorizeAndReserve(ctx context.Context, req AccessRequest, reservationID string) (AccessDecision, error)
	CommitReservation(ctx context.Context, reservationID string) error
	ReleaseReservation(ctx context.Context, reservationID string) error
	Refund(ctx context.Context, req AccessRequest) error
	HandleAdminCommand(ctx context.Context, req AccessRequest, command string, args []string) (string, bool, error)
	AccessStatus(ctx context.Context, req AccessRequest) (string, error)
}

type AccessConfig struct {
	AdminUserIDs          []string
	DefaultOpen           bool
	DefaultUserDailyLimit int
	StatePath             string
	Remote                AccessRemoteStore
	UsernameResolver      UsernameResolver
	Clock                 func() time.Time
}

type UsernameResolver interface {
	ResolveUsername(ctx context.Context, username string) (ResolvedUsername, bool, error)
}

type ResolvedUsername struct {
	UserID   string
	Username string
}

type AccessRequest struct {
	ChatID         string
	ChatType       string
	UserID         string
	Username       string
	Language       string
	MentionedUsers []AccessMentionedUser
	Now            time.Time
}

type AccessMentionedUser struct {
	UserID   string
	Username string
}

type AnnouncementRecipient struct {
	UserID   string
	Username string
}

type AccessDecision struct {
	Allowed   bool
	Reason    string
	Remaining int
}

type AccessManager struct {
	mu                    sync.Mutex
	state                 AccessState
	statePath             string
	remote                AccessRemoteStore
	defaultOpen           bool
	defaultUserDailyLimit int
	usernameResolver      UsernameResolver
	clock                 func() time.Time
}

type AccessState struct {
	Version               int                               `json:"version"`
	DefaultUserDailyLimit *int                              `json:"defaultUserDailyLimit,omitempty"`
	Admins                map[string]bool                   `json:"admins,omitempty"`
	Users                 map[string]AccessUser             `json:"users,omitempty"`
	Groups                map[string]AccessGroup            `json:"groups,omitempty"`
	Chats                 map[string]AccessChat             `json:"chats,omitempty"`
	Usage                 map[string]DailyUsage             `json:"usage,omitempty"`
	Reservations          map[string]AccessReservation      `json:"reservations,omitempty"`
	KnownUsers            map[string]KnownAccessUser        `json:"knownUsers,omitempty"`
	PendingUserGrants     map[string]PendingAccessUserGrant `json:"pendingUserGrants,omitempty"`
	UserLanguages         map[string]string                 `json:"userLanguages,omitempty"`
	UpdatedAt             string                            `json:"updatedAt,omitempty"`
}

type AccessUser struct {
	UserID     string `json:"userId"`
	Username   string `json:"username,omitempty"`
	Active     bool   `json:"active"`
	DailyLimit int    `json:"dailyLimit"`
	Group      string `json:"group,omitempty"`
	CreatedAt  string `json:"createdAt,omitempty"`
	UpdatedAt  string `json:"updatedAt,omitempty"`
}

type AccessGroup struct {
	Name       string `json:"name"`
	Active     bool   `json:"active"`
	DailyLimit int    `json:"dailyLimit"`
	CreatedAt  string `json:"createdAt,omitempty"`
	UpdatedAt  string `json:"updatedAt,omitempty"`
}

type AccessChat struct {
	ChatID     string `json:"chatId"`
	ChatType   string `json:"chatType,omitempty"`
	Active     bool   `json:"active"`
	DailyLimit int    `json:"dailyLimit"`
	CreatedAt  string `json:"createdAt,omitempty"`
	UpdatedAt  string `json:"updatedAt,omitempty"`
}

type DailyUsage struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

type AccessReservation struct {
	ID        string                   `json:"id"`
	Date      string                   `json:"date"`
	Scopes    []AccessReservationScope `json:"scopes,omitempty"`
	CreatedAt string                   `json:"createdAt,omitempty"`
}

type AccessReservationScope struct {
	Key   string `json:"key"`
	Label string `json:"label,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

type KnownAccessUser struct {
	UserID   string `json:"userId"`
	Username string `json:"username"`
	SeenAt   string `json:"seenAt,omitempty"`
}

type PendingAccessUserGrant struct {
	Username    string `json:"username"`
	DailyLimit  int    `json:"dailyLimit"`
	Group       string `json:"group,omitempty"`
	RequestedBy string `json:"requestedBy,omitempty"`
	CreatedAt   string `json:"createdAt,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
}

type quotaScope struct {
	key   string
	label string
	limit int
}

func NewMemoryAccessManager(cfg AccessConfig) *AccessManager {
	manager, err := NewAccessManager(cfg)
	if err != nil {
		panic(err)
	}
	return manager
}

func NewAccessManager(cfg AccessConfig) (*AccessManager, error) {
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	manager := &AccessManager{
		statePath:             strings.TrimSpace(cfg.StatePath),
		remote:                cfg.Remote,
		defaultOpen:           cfg.DefaultOpen,
		defaultUserDailyLimit: configuredDefaultUserDailyLimit(cfg.DefaultUserDailyLimit),
		usernameResolver:      cfg.UsernameResolver,
		clock:                 clock,
		state: AccessState{
			Version:           1,
			Admins:            map[string]bool{},
			Users:             map[string]AccessUser{},
			Groups:            map[string]AccessGroup{},
			Chats:             map[string]AccessChat{},
			Usage:             map[string]DailyUsage{},
			Reservations:      map[string]AccessReservation{},
			KnownUsers:        map[string]KnownAccessUser{},
			PendingUserGrants: map[string]PendingAccessUserGrant{},
			UserLanguages:     map[string]string{},
		},
	}
	if manager.statePath != "" {
		if err := manager.load(); err != nil {
			return nil, err
		}
	}
	if manager.remote != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		remoteState, ok, err := manager.remote.Load(ctx)
		cancel()
		if err != nil {
			return nil, err
		}
		if ok {
			manager.state = remoteState
		}
	}
	manager.ensureMapsLocked()
	for _, id := range cfg.AdminUserIDs {
		clean := cleanID(id)
		if clean != "" {
			manager.state.Admins[clean] = true
		}
	}
	manager.touchLocked(clock())
	if err := manager.saveLocked(); err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *AccessManager) RecordUser(_ context.Context, req AccessRequest) (string, error) {
	now := m.requestTime(req)
	userID := cleanID(req.UserID)
	username := cleanUsername(req.Username)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureMapsLocked()

	changed := false
	notices := []string{}
	if userID != "" && username != "" {
		if m.recordKnownUserLocked(userID, username, now) {
			changed = true
		}
		if user, ok := m.state.Users[userID]; ok && user.Username != username {
			user.Username = username
			user.UpdatedAt = nowText(now)
			m.state.Users[userID] = user
			changed = true
		}
		key := usernameKey(username)
		if grant, ok := m.state.PendingUserGrants[key]; ok {
			m.grantUserLocked(userID, username, grant.DailyLimit, grant.Group, now)
			delete(m.state.PendingUserGrants, key)
			changed = true
			notices = append(notices, pendingGrantActivatedTextForLanguage(username, grant.DailyLimit, grant.Group, m.responseLanguageLocked(req)))
		}
	}
	for _, mentioned := range req.MentionedUsers {
		mentionedUserID := cleanID(mentioned.UserID)
		mentionedUsername := cleanUsername(mentioned.Username)
		if m.recordKnownUserLocked(mentionedUserID, mentionedUsername, now) {
			changed = true
		}
		key := usernameKey(mentionedUsername)
		if mentionedUserID != "" && key != "" {
			if grant, ok := m.state.PendingUserGrants[key]; ok {
				m.grantUserLocked(mentionedUserID, mentionedUsername, grant.DailyLimit, grant.Group, now)
				delete(m.state.PendingUserGrants, key)
				changed = true
				notices = append(notices, pendingGrantActivatedTextForLanguage(mentionedUsername, grant.DailyLimit, grant.Group, m.responseLanguageLocked(req)))
			}
		}
	}
	if !changed {
		return "", nil
	}
	m.touchLocked(now)
	if err := m.saveLocked(); err != nil {
		return "", err
	}
	return strings.Join(notices, "\n"), nil
}

func (m *AccessManager) IsAdmin(_ context.Context, req AccessRequest) (bool, error) {
	userID := cleanID(req.UserID)
	if userID == "" {
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureMapsLocked()
	return m.state.Admins[userID], nil
}

func (m *AccessManager) AnnouncementRecipients(_ context.Context) ([]AnnouncementRecipient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureMapsLocked()
	recipients := make([]AnnouncementRecipient, 0, len(m.state.Users))
	for _, user := range m.state.Users {
		userID := cleanID(user.UserID)
		if userID == "" || !user.Active {
			continue
		}
		recipients = append(recipients, AnnouncementRecipient{UserID: userID, Username: cleanUsername(user.Username)})
	}
	sort.Slice(recipients, func(i, j int) bool {
		return recipients[i].UserID < recipients[j].UserID
	})
	return recipients, nil
}

func (m *AccessManager) AuthorizeAndConsume(_ context.Context, req AccessRequest) (AccessDecision, error) {
	now := m.requestTime(req)
	day := usageDay(now)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureMapsLocked()
	decision, scopes := m.authorizeScopesLocked(req, day, true)
	if !decision.Allowed {
		return decision, nil
	}
	for _, scope := range scopes {
		if scope.limit < 0 {
			continue
		}
		usage := m.state.Usage[scope.key]
		if usage.Date != day {
			usage = DailyUsage{Date: day}
		}
		usage.Count++
		m.state.Usage[scope.key] = usage
	}
	m.touchLocked(now)
	if err := m.saveLocked(); err != nil {
		return AccessDecision{}, err
	}
	return decision, nil
}

func (m *AccessManager) AuthorizeAndReserve(_ context.Context, req AccessRequest, reservationID string) (AccessDecision, error) {
	now := m.requestTime(req)
	day := usageDay(now)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureMapsLocked()
	decision, scopes := m.authorizeScopesLocked(req, day, true)
	if !decision.Allowed {
		return decision, nil
	}
	if len(scopes) > 0 {
		reservationID = strings.TrimSpace(reservationID)
		if reservationID == "" {
			return AccessDecision{}, fmt.Errorf("reservation ID is required")
		}
		m.state.Reservations[reservationID] = AccessReservation{
			ID:        reservationID,
			Date:      day,
			Scopes:    reservationScopes(scopes),
			CreatedAt: nowText(now),
		}
	}
	m.touchLocked(now)
	if err := m.saveLocked(); err != nil {
		return AccessDecision{}, err
	}
	return decision, nil
}

func (m *AccessManager) CommitReservation(_ context.Context, reservationID string) error {
	reservationID = strings.TrimSpace(reservationID)
	if reservationID == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureMapsLocked()
	reservation, ok := m.state.Reservations[reservationID]
	if !ok {
		return nil
	}
	day := strings.TrimSpace(reservation.Date)
	if day == "" {
		day = usageDay(m.requestTime(AccessRequest{}))
	}
	for _, reserved := range reservation.Scopes {
		key := strings.TrimSpace(reserved.Key)
		if key == "" || reserved.Limit < 0 {
			continue
		}
		usage := m.state.Usage[key]
		if usage.Date != day {
			usage = DailyUsage{Date: day}
		}
		usage.Count++
		m.state.Usage[key] = usage
	}
	delete(m.state.Reservations, reservationID)
	m.touchLocked(m.requestTime(AccessRequest{}))
	return m.saveLocked()
}

func (m *AccessManager) ReleaseReservation(_ context.Context, reservationID string) error {
	reservationID = strings.TrimSpace(reservationID)
	if reservationID == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureMapsLocked()
	if _, ok := m.state.Reservations[reservationID]; !ok {
		return nil
	}
	delete(m.state.Reservations, reservationID)
	m.touchLocked(m.requestTime(AccessRequest{}))
	return m.saveLocked()
}

func (m *AccessManager) Refund(_ context.Context, req AccessRequest) error {
	now := m.requestTime(req)
	day := usageDay(now)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureMapsLocked()
	userID := cleanID(req.UserID)
	chatID := cleanID(req.ChatID)
	user := m.state.Users[userID]
	var group AccessGroup
	groupOK := false
	if user.Group != "" {
		group, groupOK = m.state.Groups[cleanGroupName(user.Group)]
	}
	chat := m.state.Chats[chatID]
	for _, scope := range m.quotaScopesLocked(user, group, groupOK, chat, day) {
		usage := m.state.Usage[scope.key]
		if usage.Date == day && usage.Count > 0 {
			usage.Count--
			m.state.Usage[scope.key] = usage
		}
	}
	m.touchLocked(now)
	return m.saveLocked()
}

func (m *AccessManager) HandleAdminCommand(ctx context.Context, req AccessRequest, command string, args []string) (string, bool, error) {
	rawCommand := cleanCommand(command)
	cmd := canonicalAdminCommand(rawCommand)
	underAdmin := rawCommand == "admin"
	if underAdmin {
		if len(args) == 0 {
			cmd = "admin_help"
		} else {
			cmd = canonicalAdminCommand(args[0])
			args = args[1:]
		}
	}
	if !underAdmin && !isAdminCommand(cmd) {
		return "", false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureMapsLocked()
	actor := cleanID(req.UserID)
	if !m.state.Admins[actor] {
		return botText(req.Language, "admin_only"), true, nil
	}
	if !isAdminCommand(cmd) {
		return botText("en", "unknown_admin"), true, nil
	}
	now := m.requestTime(req)
	message, err := m.applyAdminCommandLocked(ctx, cmd, args, now)
	if err != nil {
		return err.Error(), true, nil
	}
	m.touchLocked(now)
	if err := m.saveLocked(); err != nil {
		return "", true, err
	}
	return message, true, nil
}

func (m *AccessManager) AccessStatus(_ context.Context, req AccessRequest) (string, error) {
	now := m.requestTime(req)
	day := usageDay(now)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureMapsLocked()
	userID := cleanID(req.UserID)
	if m.state.Admins[userID] {
		return fmt.Sprintf("Access: admin. Users: %d. Groups: %d. Allowed chats: %d.", len(m.state.Users), len(m.state.Groups), len(m.state.Chats)), nil
	}
	language := normalizePublicBotLanguage(req.Language)
	user, ok := m.state.Users[userID]
	if !ok || !user.Active {
		if m.defaultOpen {
			return accessStatusText(language, "default_allowed"), nil
		}
		return accessStatusText(language, "not_allowed"), nil
	}
	parts := []string{accessStatusText(language, "allowed")}
	if user.Group != "" {
		parts = append(parts, fmt.Sprintf(accessStatusText(language, "group"), user.Group))
	}
	var group AccessGroup
	groupOK := false
	if user.Group != "" {
		group, groupOK = m.state.Groups[cleanGroupName(user.Group)]
		groupOK = groupOK && group.Active
	}
	var chat AccessChat
	if isGroupChat(req.ChatType, cleanID(req.ChatID)) {
		chat = m.state.Chats[cleanID(req.ChatID)]
	}
	for _, scope := range m.quotaScopesLocked(user, group, groupOK, chat, day) {
		if scope.limit <= 0 {
			continue
		}
		usage := m.state.Usage[scope.key]
		count := 0
		if usage.Date == day {
			count = usage.Count
		}
		parts = append(parts, fmt.Sprintf(accessStatusText(language, "quota"), quotaLabel(language, scope.label), count, scope.limit))
		if pending := m.reservedCountLocked(scope.key, day); pending > 0 {
			parts = append(parts, fmt.Sprintf(accessStatusText(language, "pending"), quotaLabel(language, scope.label), pending))
		}
	}
	return strings.Join(parts, ". ") + ".", nil
}

func (m *AccessManager) responseLanguageLocked(req AccessRequest) string {
	if m.state.Admins[cleanID(req.UserID)] {
		return "en"
	}
	return normalizePublicBotLanguage(req.Language)
}

func (m *AccessManager) UserLanguage(_ context.Context, req AccessRequest) (string, error) {
	userID := cleanID(req.UserID)
	if userID == "" || userID == "0" {
		return "", nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureMapsLocked()
	return normalizePublicBotLanguage(m.state.UserLanguages[userID]), nil
}

func (m *AccessManager) SetUserLanguage(_ context.Context, req AccessRequest, language string) error {
	userID := cleanID(req.UserID)
	if userID == "" || userID == "0" {
		return nil
	}
	language = normalizePublicBotLanguage(language)
	now := m.requestTime(req)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureMapsLocked()
	m.state.UserLanguages[userID] = language
	if username := cleanUsername(req.Username); username != "" {
		m.recordKnownUserLocked(userID, username, now)
	}
	m.touchLocked(now)
	return m.saveLocked()
}

func (m *AccessManager) applyAdminCommandLocked(ctx context.Context, cmd string, args []string, now time.Time) (string, error) {
	switch cmd {
	case "admin_help":
		return m.adminHelpTextLocked(), nil
	case "allow_user":
		defaultLimit := m.currentDefaultUserDailyLimitLocked()
		if len(args) < 1 {
			return "", fmt.Errorf("Usage: /admin add_user <telegram_user_id|@username> [daily_limit=%s] [group]", limitText(defaultLimit))
		}
		if allUsernameReferences(args) {
			messages := make([]string, 0, len(args))
			for _, arg := range args {
				message, err := m.allowUsernameLocked(ctx, arg, defaultLimit, "", cleanID(""), now)
				if err != nil {
					return "", err
				}
				messages = append(messages, message)
			}
			return strings.Join(messages, "\n"), nil
		}
		limit := defaultLimit
		group := ""
		if len(args) >= 2 {
			if parsed, err := strconv.Atoi(args[1]); err == nil {
				limit = parsed
				if len(args) >= 3 {
					group = cleanGroupName(args[2])
				}
			} else {
				group = cleanGroupName(args[1])
			}
		}
		if group != "" {
			if existing, ok := m.state.Groups[group]; !ok || !existing.Active {
				return "", fmt.Errorf("Group %s does not exist. Create it with /admin set_group %s <daily_limit>.", group, group)
			}
		}
		if isUsernameReference(args[0]) {
			return m.allowUsernameLocked(ctx, args[0], limit, group, cleanID(""), now)
		}
		userID := cleanID(args[0])
		if userID == "" {
			return "", fmt.Errorf("User ID is required")
		}
		m.grantUserLocked(userID, "", limit, group, now)
		if group != "" {
			return fmt.Sprintf("Allowed user %s with daily limit %s in group %s.", userID, limitText(limit), group), nil
		}
		return fmt.Sprintf("Allowed user %s with daily limit %s.", userID, limitText(limit)), nil
	case "remove_user":
		if len(args) != 1 {
			return "", fmt.Errorf("Usage: /admin remove_user <telegram_user_id>")
		}
		userID := cleanID(args[0])
		if userID == "" {
			return "", fmt.Errorf("User ID is required")
		}
		delete(m.state.Users, userID)
		return fmt.Sprintf("Removed user %s.", userID), nil
	case "deny_user":
		if len(args) != 1 {
			return "", fmt.Errorf("Usage: /admin deny_user <telegram_user_id>")
		}
		userID := cleanID(args[0])
		user := m.state.Users[userID]
		user.UserID = userID
		user.Active = false
		user.UpdatedAt = nowText(now)
		m.state.Users[userID] = user
		return fmt.Sprintf("Denied user %s.", userID), nil
	case "set_default_user_limit":
		if len(args) != 1 {
			return "", fmt.Errorf("Usage: /admin set_default_limit <daily_limit>")
		}
		limit, err := strconv.Atoi(args[0])
		if err != nil {
			return "", fmt.Errorf("Daily limit must be a number")
		}
		normalized := normalizeLimit(limit)
		m.state.DefaultUserDailyLimit = &normalized
		return fmt.Sprintf("Set default user daily limit to %s.", limitText(normalized)), nil
	case "set_user_limit":
		if len(args) != 2 {
			return "", fmt.Errorf("Usage: /admin set_user_limit <telegram_user_id> <daily_limit>")
		}
		userID := cleanID(args[0])
		limit, err := strconv.Atoi(args[1])
		if err != nil {
			return "", fmt.Errorf("Daily limit must be a number")
		}
		user := m.state.Users[userID]
		user.UserID = userID
		user.Active = true
		user.DailyLimit = normalizeLimit(limit)
		user.UpdatedAt = nowText(now)
		if user.CreatedAt == "" {
			user.CreatedAt = user.UpdatedAt
		}
		m.state.Users[userID] = user
		return fmt.Sprintf("Set user %s daily limit to %s.", userID, limitText(limit)), nil
	case "set_group":
		if len(args) != 2 {
			return "", fmt.Errorf("Usage: /admin set_group <name> <daily_limit>")
		}
		name := cleanGroupName(args[0])
		if name == "" {
			return "", fmt.Errorf("Group name is required")
		}
		limit, err := strconv.Atoi(args[1])
		if err != nil {
			return "", fmt.Errorf("Daily limit must be a number")
		}
		created := nowText(now)
		if existing, ok := m.state.Groups[name]; ok && existing.CreatedAt != "" {
			created = existing.CreatedAt
		}
		m.state.Groups[name] = AccessGroup{Name: name, Active: true, DailyLimit: normalizeLimit(limit), CreatedAt: created, UpdatedAt: nowText(now)}
		return fmt.Sprintf("Configured group %s with %s tickets/day.", name, limitText(limit)), nil
	case "add_to_group":
		if len(args) != 2 {
			return "", fmt.Errorf("Usage: /admin add_to_group <telegram_user_id> <group>")
		}
		userID := cleanID(args[0])
		group := cleanGroupName(args[1])
		if existing, ok := m.state.Groups[group]; !ok || !existing.Active {
			return "", fmt.Errorf("Group %s does not exist. Create it with /admin set_group %s <daily_limit>.", group, group)
		}
		user := m.state.Users[userID]
		user.UserID = userID
		user.Active = true
		user.Group = group
		user.UpdatedAt = nowText(now)
		if user.CreatedAt == "" {
			user.CreatedAt = user.UpdatedAt
		}
		m.state.Users[userID] = user
		return fmt.Sprintf("Added user %s to group %s.", userID, group), nil
	case "allow_chat":
		if len(args) < 1 {
			return "", fmt.Errorf("Usage: /admin allow_chat <telegram_chat_id> [daily_limit]")
		}
		chatID := cleanID(args[0])
		limit := 0
		if len(args) >= 2 {
			parsed, err := strconv.Atoi(args[1])
			if err != nil {
				return "", fmt.Errorf("Daily limit must be a number")
			}
			limit = parsed
		}
		created := nowText(now)
		if existing, ok := m.state.Chats[chatID]; ok && existing.CreatedAt != "" {
			created = existing.CreatedAt
		}
		m.state.Chats[chatID] = AccessChat{ChatID: chatID, ChatType: "group", Active: true, DailyLimit: normalizeLimit(limit), CreatedAt: created, UpdatedAt: nowText(now)}
		return fmt.Sprintf("Allowed chat %s with daily limit %s.", chatID, limitText(limit)), nil
	case "deny_chat":
		if len(args) != 1 {
			return "", fmt.Errorf("Usage: /admin deny_chat <telegram_chat_id>")
		}
		chatID := cleanID(args[0])
		chat := m.state.Chats[chatID]
		chat.ChatID = chatID
		chat.Active = false
		chat.UpdatedAt = nowText(now)
		m.state.Chats[chatID] = chat
		return fmt.Sprintf("Denied chat %s.", chatID), nil
	case "list_access":
		return m.adminSummaryLocked(), nil
	default:
		return "", fmt.Errorf("Unknown admin command. Use /admin for help.")
	}
}

func (m *AccessManager) recordKnownUserLocked(userID string, username string, now time.Time) bool {
	userID = cleanID(userID)
	username = cleanUsername(username)
	if userID == "" || username == "" {
		return false
	}
	key := usernameKey(username)
	seenAt := nowText(now)
	known := m.state.KnownUsers[key]
	if known.UserID == userID && known.Username == username {
		return false
	}
	m.state.KnownUsers[key] = KnownAccessUser{UserID: userID, Username: username, SeenAt: seenAt}
	return true
}

func (m *AccessManager) grantUserLocked(userID string, username string, limit int, group string, now time.Time) AccessUser {
	username = cleanUsername(username)
	created := nowText(now)
	existing, ok := m.state.Users[userID]
	if ok {
		if existing.CreatedAt != "" {
			created = existing.CreatedAt
		}
		if username == "" {
			username = existing.Username
		}
	}
	user := AccessUser{UserID: userID, Username: username, Active: true, DailyLimit: normalizeLimit(limit), Group: group, CreatedAt: created, UpdatedAt: nowText(now)}
	m.state.Users[userID] = user
	if username != "" {
		m.state.KnownUsers[usernameKey(username)] = KnownAccessUser{UserID: userID, Username: username, SeenAt: nowText(now)}
	}
	return user
}

func (m *AccessManager) allowUsernameLocked(ctx context.Context, usernameValue string, limit int, group string, requestedBy string, now time.Time) (string, error) {
	username := cleanUsername(usernameValue)
	if username == "" {
		return "", fmt.Errorf("Username is required")
	}
	if known, ok := m.knownUserByUsernameLocked(username); ok {
		m.grantUserLocked(known.UserID, known.Username, limit, group, now)
		if group != "" {
			return fmt.Sprintf("Allowed user %s with daily limit %s in group %s (@%s).", known.UserID, limitText(limit), group, known.Username), nil
		}
		return fmt.Sprintf("Allowed user %s with daily limit %s (@%s).", known.UserID, limitText(limit), known.Username), nil
	}
	lookupNote := ""
	if known, ok, err := m.resolveUsernameLocked(ctx, username, now); ok {
		m.grantUserLocked(known.UserID, known.Username, limit, group, now)
		if group != "" {
			return fmt.Sprintf("Allowed user %s with daily limit %s in group %s (@%s).", known.UserID, limitText(limit), group, known.Username), nil
		}
		return fmt.Sprintf("Allowed user %s with daily limit %s (@%s).", known.UserID, limitText(limit), known.Username), nil
	} else if err != nil {
		lookupNote = " I tried to look up the numeric Telegram user ID now, but lookup failed."
	} else if m.usernameResolver != nil {
		lookupNote = " I tried to look up the numeric Telegram user ID now, but Telegram did not return one."
	}
	key := usernameKey(username)
	created := nowText(now)
	if existing, ok := m.state.PendingUserGrants[key]; ok && existing.CreatedAt != "" {
		created = existing.CreatedAt
	}
	m.state.PendingUserGrants[key] = PendingAccessUserGrant{Username: username, DailyLimit: normalizeLimit(limit), Group: group, RequestedBy: requestedBy, CreatedAt: created, UpdatedAt: nowText(now)}
	activationNote := " It will activate when the user sends /start or any message such as /access to this bot, or when Telegram exposes their numeric ID in a native mention."
	if group != "" {
		return fmt.Sprintf("Pending @%s with daily limit %s in group %s.%s%s", username, limitText(limit), group, lookupNote, activationNote), nil
	}
	return fmt.Sprintf("Pending @%s with daily limit %s.%s%s", username, limitText(limit), lookupNote, activationNote), nil
}

func (m *AccessManager) resolveUsernameLocked(ctx context.Context, username string, now time.Time) (KnownAccessUser, bool, error) {
	if m.usernameResolver == nil {
		return KnownAccessUser{}, false, nil
	}
	resolved, ok, err := m.usernameResolver.ResolveUsername(ctx, cleanUsername(username))
	if err != nil || !ok {
		return KnownAccessUser{}, false, err
	}
	userID := cleanID(resolved.UserID)
	resolvedUsername := cleanUsername(resolved.Username)
	if resolvedUsername == "" {
		resolvedUsername = cleanUsername(username)
	}
	if userID == "" || resolvedUsername == "" {
		return KnownAccessUser{}, false, nil
	}
	m.recordKnownUserLocked(userID, resolvedUsername, now)
	return KnownAccessUser{UserID: userID, Username: resolvedUsername, SeenAt: nowText(now)}, true, nil
}

func (m *AccessManager) knownUserByUsernameLocked(username string) (KnownAccessUser, bool) {
	key := usernameKey(username)
	if known, ok := m.state.KnownUsers[key]; ok && cleanID(known.UserID) != "" {
		if known.Username == "" {
			known.Username = cleanUsername(username)
		}
		return known, true
	}
	for _, user := range m.state.Users {
		if usernameKey(user.Username) == key && cleanID(user.UserID) != "" {
			return KnownAccessUser{UserID: user.UserID, Username: user.Username, SeenAt: user.UpdatedAt}, true
		}
	}
	return KnownAccessUser{}, false
}

func (m *AccessManager) authorizeScopesLocked(req AccessRequest, day string, includeReservations bool) (AccessDecision, []quotaScope) {
	userID := cleanID(req.UserID)
	chatID := cleanID(req.ChatID)
	if userID == "" {
		return AccessDecision{Allowed: false, Reason: "not_allowed"}, nil
	}
	if m.state.Admins[userID] {
		return AccessDecision{Allowed: true, Remaining: unlimitedQuota}, nil
	}
	user, userOK := m.state.Users[userID]
	if !userOK || !user.Active {
		if !m.defaultOpen {
			return AccessDecision{Allowed: false, Reason: "not_allowed"}, nil
		}
		user = AccessUser{UserID: userID, Active: true, DailyLimit: 0}
	}
	if req.Username != "" && userOK {
		user.Username = cleanUsername(req.Username)
		m.state.Users[userID] = user
	}
	var group AccessGroup
	groupOK := false
	if user.Group != "" {
		group, groupOK = m.state.Groups[cleanGroupName(user.Group)]
		if !groupOK || !group.Active {
			return AccessDecision{Allowed: false, Reason: "group_not_allowed"}, nil
		}
	}
	var chat AccessChat
	if isGroupChat(req.ChatType, chatID) {
		var chatOK bool
		chat, chatOK = m.state.Chats[chatID]
		if !chatOK || !chat.Active {
			return AccessDecision{Allowed: false, Reason: "chat_not_allowed"}, nil
		}
	}
	scopes := m.quotaScopesLocked(user, group, groupOK, chat, day)
	remaining := unlimitedQuota
	for _, scope := range scopes {
		if scope.limit < 0 {
			continue
		}
		usage := m.state.Usage[scope.key]
		if usage.Date != day {
			usage = DailyUsage{Date: day}
		}
		pending := 0
		if includeReservations {
			pending = m.reservedCountLocked(scope.key, day)
		}
		left := scope.limit - usage.Count - pending
		if left <= 0 {
			return AccessDecision{Allowed: false, Reason: scope.label + "_daily_limit", Remaining: 0}, nil
		}
		if remaining == unlimitedQuota || left < remaining {
			remaining = left
		}
	}
	if remaining > 0 {
		remaining--
	}
	return AccessDecision{Allowed: true, Remaining: remaining}, scopes
}

func reservationScopes(scopes []quotaScope) []AccessReservationScope {
	out := make([]AccessReservationScope, 0, len(scopes))
	for _, scope := range scopes {
		if scope.key == "" {
			continue
		}
		out = append(out, AccessReservationScope{Key: scope.key, Label: scope.label, Limit: scope.limit})
	}
	return out
}

func (m *AccessManager) reservedCountLocked(scopeKey string, day string) int {
	count := 0
	for _, reservation := range m.state.Reservations {
		if reservation.Date != day {
			continue
		}
		for _, scope := range reservation.Scopes {
			if scope.Key == scopeKey {
				count++
				break
			}
		}
	}
	return count
}

func (m *AccessManager) quotaScopesLocked(user AccessUser, group AccessGroup, groupOK bool, chat AccessChat, day string) []quotaScope {
	_ = day
	scopes := []quotaScope{}
	if user.UserID != "" && user.DailyLimit > 0 {
		scopes = append(scopes, quotaScope{key: "user:" + user.UserID, label: "user", limit: user.DailyLimit})
	}
	if groupOK && group.DailyLimit > 0 {
		scopes = append(scopes, quotaScope{key: "group:" + group.Name, label: "group", limit: group.DailyLimit})
	}
	if chat.ChatID != "" && chat.Active && chat.DailyLimit > 0 {
		scopes = append(scopes, quotaScope{key: "chat:" + chat.ChatID, label: "chat", limit: chat.DailyLimit})
	}
	return scopes
}

func (m *AccessManager) requestTime(req AccessRequest) time.Time {
	if !req.Now.IsZero() {
		return req.Now
	}
	if m.clock != nil {
		return m.clock()
	}
	return time.Now()
}

func (m *AccessManager) ensureMapsLocked() {
	if m.state.Version == 0 {
		m.state.Version = 1
	}
	if m.state.Admins == nil {
		m.state.Admins = map[string]bool{}
	}
	if m.state.Users == nil {
		m.state.Users = map[string]AccessUser{}
	}
	if m.state.Groups == nil {
		m.state.Groups = map[string]AccessGroup{}
	}
	if m.state.Chats == nil {
		m.state.Chats = map[string]AccessChat{}
	}
	if m.state.Usage == nil {
		m.state.Usage = map[string]DailyUsage{}
	}
	if m.state.Reservations == nil {
		m.state.Reservations = map[string]AccessReservation{}
	}
	if m.state.KnownUsers == nil {
		m.state.KnownUsers = map[string]KnownAccessUser{}
	}
	if m.state.PendingUserGrants == nil {
		m.state.PendingUserGrants = map[string]PendingAccessUserGrant{}
	}
	if m.state.UserLanguages == nil {
		m.state.UserLanguages = map[string]string{}
	}
}

func (m *AccessManager) touchLocked(now time.Time) {
	m.state.UpdatedAt = nowText(now)
}

func (m *AccessManager) load() error {
	data, err := os.ReadFile(m.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read access state: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, &m.state); err != nil {
		return fmt.Errorf("decode access state: %w", err)
	}
	return nil
}

func (m *AccessManager) saveLocked() error {
	if m.statePath != "" {
		if err := os.MkdirAll(filepath.Dir(m.statePath), 0o700); err != nil {
			return fmt.Errorf("create access state directory: %w", err)
		}
		data, err := json.MarshalIndent(m.state, "", "  ")
		if err != nil {
			return fmt.Errorf("encode access state: %w", err)
		}
		tmp := m.statePath + ".tmp"
		if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
			return fmt.Errorf("write access state: %w", err)
		}
		if err := os.Rename(tmp, m.statePath); err != nil {
			return fmt.Errorf("replace access state: %w", err)
		}
	}
	if m.remote != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := m.remote.Save(ctx, m.state); err != nil {
			return err
		}
	}
	return nil
}

func (m *AccessManager) adminSummaryLocked() string {
	users := sortedKeysUser(m.state.Users)
	groups := sortedKeysGroup(m.state.Groups)
	chats := sortedKeysChat(m.state.Chats)
	lines := []string{"Access summary:"}
	lines = append(lines, fmt.Sprintf("Default user daily limit: %s", limitText(m.currentDefaultUserDailyLimitLocked())))
	lines = append(lines, fmt.Sprintf("Admins: %d", len(m.state.Admins)))
	lines = append(lines, fmt.Sprintf("Users: %s", strings.Join(users, ", ")))
	lines = append(lines, fmt.Sprintf("Groups: %s", strings.Join(groups, ", ")))
	lines = append(lines, fmt.Sprintf("Chats: %s", strings.Join(chats, ", ")))
	return strings.Join(lines, "\n")
}

func sortedKeysUser(values map[string]AccessUser) []string {
	out := make([]string, 0, len(values))
	for key, value := range values {
		status := "disabled"
		if value.Active {
			status = "active"
		}
		label := fmt.Sprintf("%s(%s, limit=%s", key, status, limitText(value.DailyLimit))
		if value.Group != "" {
			label += ", group=" + value.Group
		}
		label += ")"
		out = append(out, label)
	}
	sort.Strings(out)
	return out
}

func sortedKeysGroup(values map[string]AccessGroup) []string {
	out := make([]string, 0, len(values))
	for key, value := range values {
		status := "disabled"
		if value.Active {
			status = "active"
		}
		out = append(out, fmt.Sprintf("%s(%s, limit=%s)", key, status, limitText(value.DailyLimit)))
	}
	sort.Strings(out)
	return out
}

func sortedKeysChat(values map[string]AccessChat) []string {
	out := make([]string, 0, len(values))
	for key, value := range values {
		status := "disabled"
		if value.Active {
			status = "active"
		}
		out = append(out, fmt.Sprintf("%s(%s, limit=%s)", key, status, limitText(value.DailyLimit)))
	}
	sort.Strings(out)
	return out
}

func isAdminCommand(command string) bool {
	switch canonicalAdminCommand(command) {
	case "admin_help", "allow_user", "remove_user", "deny_user", "set_default_user_limit", "set_user_limit", "set_group", "add_to_group", "allow_chat", "deny_chat", "list_access":
		return true
	default:
		return false
	}
}

func canonicalAdminCommand(value string) string {
	command := strings.ReplaceAll(cleanCommand(value), "-", "_")
	switch command {
	case "help", "admin_help":
		return "admin_help"
	case "add", "add_user", "allow", "allow_user", "user_add":
		return "allow_user"
	case "remove", "remove_user", "rm", "delete", "delete_user", "del_user":
		return "remove_user"
	case "deny", "deny_user", "disable", "disable_user", "block", "block_user":
		return "deny_user"
	case "set_default_limit", "default_limit", "set_default_user_limit", "default_user_limit", "set_user_default_limit":
		return "set_default_user_limit"
	case "set_limit", "limit", "set_user_limit", "user_limit":
		return "set_user_limit"
	case "set_group", "group":
		return "set_group"
	case "add_to_group", "add_group", "user_group":
		return "add_to_group"
	case "allow_chat", "add_chat", "chat":
		return "allow_chat"
	case "deny_chat", "remove_chat", "delete_chat":
		return "deny_chat"
	case "list", "list_access", "users", "summary":
		return "list_access"
	default:
		return command
	}
}

func (m *AccessManager) adminHelpTextLocked() string {
	defaultLimit := m.currentDefaultUserDailyLimitLocked()
	return strings.Join([]string{
		"Admin commands:",
		fmt.Sprintf("/admin add_user <user_id|@username> [daily_limit=%s] [group] (alias: /admin add)", limitText(defaultLimit)),
		fmt.Sprintf("/admin add @user1 @user2 ... resolves usernames now when possible, otherwise queues grants with the default %s tickets/day limit", limitText(defaultLimit)),
		"/admin set_default_limit <daily_limit>",
		"/admin remove_user <user_id> (alias: /admin remove)",
		"/admin deny_user <user_id>",
		"/admin set_user_limit <user_id> <daily_limit> (alias: /admin set_limit)",
		"/admin set_group <name> <daily_limit>",
		"/admin add_to_group <user_id> <group>",
		"/admin allow_chat <chat_id> [daily_limit]",
		"/admin deny_chat <chat_id>",
		"/admin list_access (alias: /admin list)",
		"/admin announce",
	}, "\n")
}

func accessRequestFromMessage(msg Message) AccessRequest {
	return AccessRequest{
		ChatID:         strconv.FormatInt(msg.ChatID, 10),
		ChatType:       strings.TrimSpace(msg.ChatType),
		UserID:         strconv.FormatInt(msg.UserID, 10),
		Username:       cleanUsername(msg.Username),
		Language:       "",
		MentionedUsers: accessMentionedUsersFromMessage(msg.MentionedUsers),
	}
}

func accessRequestFromCallback(callback Callback) AccessRequest {
	return AccessRequest{
		ChatID:   strconv.FormatInt(callback.ChatID, 10),
		ChatType: strings.TrimSpace(callback.ChatType),
		UserID:   strconv.FormatInt(callback.UserID, 10),
		Username: cleanUsername(callback.Username),
		Language: "",
	}
}

func accessMentionedUsersFromMessage(users []MentionedUser) []AccessMentionedUser {
	if len(users) == 0 {
		return nil
	}
	out := make([]AccessMentionedUser, 0, len(users))
	for _, user := range users {
		userID := strconv.FormatInt(user.UserID, 10)
		username := cleanUsername(user.Username)
		if userID == "0" || username == "" {
			continue
		}
		out = append(out, AccessMentionedUser{UserID: userID, Username: username})
	}
	return out
}

func configuredDefaultUserDailyLimit(limit int) int {
	if limit == 0 {
		return defaultUserDailyLimit
	}
	return normalizeLimit(limit)
}

func (m *AccessManager) currentDefaultUserDailyLimitLocked() int {
	if m.state.DefaultUserDailyLimit != nil {
		return normalizeLimit(*m.state.DefaultUserDailyLimit)
	}
	return configuredDefaultUserDailyLimit(m.defaultUserDailyLimit)
}

func accessDenialText(decision AccessDecision) string {
	return accessDenialTextForLanguage(decision, "en")
}

func accessDenialTextForLanguage(decision AccessDecision, language string) string {
	language = normalizeBotLanguage(language)
	switch decision.Reason {
	case "chat_not_allowed":
		return localizedAccessDenial(language, "chat_not_allowed")
	case "chat_daily_limit":
		return localizedAccessDenial(language, "chat_daily_limit")
	case "user_daily_limit":
		return localizedAccessDenial(language, "user_daily_limit")
	case "group_daily_limit":
		return localizedAccessDenial(language, "group_daily_limit")
	case "group_not_allowed":
		return localizedAccessDenial(language, "group_not_allowed")
	default:
		return localizedAccessDenial(language, "not_allowed")
	}
}

func cleanCommand(value string) string {
	value = strings.TrimSpace(strings.TrimPrefix(value, "/"))
	if idx := strings.Index(value, "@"); idx >= 0 {
		value = value[:idx]
	}
	return strings.ToLower(value)
}

func cleanID(value string) string {
	return strings.TrimSpace(value)
}

func isUsernameReference(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "@") && cleanUsername(value) != ""
}

func allUsernameReferences(values []string) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if !isUsernameReference(value) {
			return false
		}
	}
	return true
}

func cleanUsername(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), "@")
}

func usernameKey(value string) string {
	return strings.ToLower(cleanUsername(value))
}

func pendingGrantActivatedText(username string, limit int, group string) string {
	return pendingGrantActivatedTextForLanguage(username, limit, group, "en")
}

func pendingGrantActivatedTextForLanguage(username string, limit int, group string, language string) string {
	language = normalizeBotLanguage(language)
	limitValue := localizedLimitText(limit, language)
	if group != "" {
		switch language {
		case "ru":
			return fmt.Sprintf("Доступ для @%s включён с дневным лимитом %s в группе %s.", cleanUsername(username), limitValue, group)
		case "lv":
			return fmt.Sprintf("Piekļuve @%s ieslēgta ar dienas limitu %s grupā %s.", cleanUsername(username), limitValue, group)
		default:
			return fmt.Sprintf("Access enabled for @%s with daily limit %s in group %s.", cleanUsername(username), limitValue, group)
		}
	}
	switch language {
	case "ru":
		return fmt.Sprintf("Доступ для @%s включён с дневным лимитом %s.", cleanUsername(username), limitValue)
	case "lv":
		return fmt.Sprintf("Piekļuve @%s ieslēgta ar dienas limitu %s.", cleanUsername(username), limitValue)
	default:
		return fmt.Sprintf("Access enabled for @%s with daily limit %s.", cleanUsername(username), limitValue)
	}
}

func accessStatusText(language string, key string) string {
	switch normalizeBotLanguage(language) {
	case "ru":
		switch key {
		case "default_allowed":
			return "Доступ: разрешён по умолчанию."
		case "not_allowed":
			return "Доступ: не выдан. Попроси администратора добавить твой Telegram user ID."
		case "allowed":
			return "Доступ: разрешён"
		case "group":
			return "группа: %s"
		case "quota":
			return "лимит %s: %d/%d сегодня"
		case "pending":
			return "%s ожидает: %d"
		}
	case "lv":
		switch key {
		case "default_allowed":
			return "Piekļuve: atļauta pēc noklusējuma."
		case "not_allowed":
			return "Piekļuve: nav piešķirta. Palūdz administratoram pievienot tavu Telegram lietotāja ID."
		case "allowed":
			return "Piekļuve: atļauta"
		case "group":
			return "grupa: %s"
		case "quota":
			return "%s limits: %d/%d šodien"
		case "pending":
			return "%s gaida: %d"
		}
	}
	switch key {
	case "default_allowed":
		return "Access: allowed by default."
	case "not_allowed":
		return "Access: not allowed. Ask an admin to add your Telegram user ID."
	case "allowed":
		return "Access: allowed"
	case "group":
		return "group: %s"
	case "quota":
		return "%s quota: %d/%d today"
	case "pending":
		return "%s pending: %d"
	default:
		return ""
	}
}

func quotaLabel(language string, label string) string {
	switch normalizeBotLanguage(language) {
	case "ru":
		switch label {
		case "user":
			return "пользователя"
		case "group":
			return "группы"
		case "chat":
			return "чата"
		}
	case "lv":
		switch label {
		case "user":
			return "lietotāja"
		case "group":
			return "grupas"
		case "chat":
			return "čata"
		}
	}
	return label
}

func localizedAccessDenial(language string, key string) string {
	switch normalizeBotLanguage(language) {
	case "ru":
		switch key {
		case "chat_not_allowed":
			return "У этого чата ещё нет доступа для запросов билетов. Попроси администратора добавить этот Telegram чат."
		case "chat_daily_limit":
			return "Дневной лимит этого чата израсходован. Попробуй завтра или попроси администратора увеличить лимит чата."
		case "user_daily_limit":
			return "Твой дневной лимит израсходован. Попробуй завтра или попроси администратора увеличить лимит."
		case "group_daily_limit":
			return "Дневной лимит твоей группы израсходован. Попробуй завтра или попроси администратора увеличить лимит группы."
		case "group_not_allowed":
			return "У твоей группы доступа нет разрешения. Попроси администратора обновить доступ."
		default:
			return "Доступ ещё не выдан. Попроси администратора добавить твой Telegram user ID."
		}
	case "lv":
		switch key {
		case "chat_not_allowed":
			return "Šim čatam vēl nav piekļuves biļešu pieprasījumiem. Palūdz administratoram pievienot šo Telegram čatu."
		case "chat_daily_limit":
			return "Šī čata dienas limits ir iztērēts. Mēģini rīt vai palūdz administratoram lielāku čata limitu."
		case "user_daily_limit":
			return "Tavs dienas limits ir iztērēts. Mēģini rīt vai palūdz administratoram lielāku limitu."
		case "group_daily_limit":
			return "Tavas grupas dienas limits ir iztērēts. Mēģini rīt vai palūdz administratoram lielāku grupas limitu."
		case "group_not_allowed":
			return "Tavai piekļuves grupai nav atļaujas. Palūdz administratoram atjaunināt piekļuvi."
		default:
			return "Piekļuve vēl nav piešķirta. Palūdz administratoram pievienot tavu Telegram lietotāja ID."
		}
	}
	switch key {
	case "chat_not_allowed":
		return "This chat is not allowed to request tickets. Ask an admin to run /admin allow_chat for this Telegram chat ID."
	case "chat_daily_limit":
		return "This chat daily limit is used up. Try again tomorrow or ask an admin for a higher chat quota."
	case "user_daily_limit":
		return "Your daily limit is used up. Try again tomorrow or ask an admin for a higher quota."
	case "group_daily_limit":
		return "Your group daily limit is used up. Try again tomorrow or ask an admin for a higher group quota."
	case "group_not_allowed":
		return "Your access group is not allowed. Ask an admin to update your ticket access."
	default:
		return "You are not allowed to request tickets yet. Ask an admin to add your Telegram user ID."
	}
}

func localizedLimitText(limit int, language string) string {
	limit = normalizeLimit(limit)
	if limit > 0 {
		return strconv.Itoa(limit)
	}
	switch normalizeBotLanguage(language) {
	case "ru":
		if limit < 0 {
			return "безлимитно"
		}
		return "наследуется/безлимитно"
	case "lv":
		if limit < 0 {
			return "bez limita"
		}
		return "mantots/bez limita"
	default:
		return limitText(limit)
	}
}

func cleanGroupName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "-")
	return value
}

func isGroupChat(chatType string, chatID string) bool {
	chatType = strings.ToLower(strings.TrimSpace(chatType))
	return chatType == "group" || chatType == "supergroup" || strings.HasPrefix(chatID, "-")
}

func usageDay(now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	return now.UTC().Format("2006-01-02")
}

func nowText(now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	return now.UTC().Format(time.RFC3339Nano)
}

func normalizeLimit(limit int) int {
	if limit < 0 {
		return unlimitedQuota
	}
	return limit
}

func limitText(limit int) string {
	limit = normalizeLimit(limit)
	if limit < 0 {
		return "unlimited"
	}
	if limit == 0 {
		return "inherited/unlimited"
	}
	return strconv.Itoa(limit)
}
