package bot

import (
	"context"
	"strconv"
	"testing"
	"time"
)

func TestServiceRequiresAllowedUserBeforeQueueing(t *testing.T) {
	access := NewMemoryAccessManager(AccessConfig{DefaultOpen: false, Clock: fixedAccessClock(t)})
	broker := &fakeBroker{createJob: QRJob{ID: "job-1", UserID: "42", ChatID: "42", Status: JobWaiting}}
	telegram := &fakeTelegram{}
	service := NewService(ServiceConfig{PollInterval: time.Millisecond, PollTimeout: 20 * time.Millisecond, Access: access}, telegram, broker)

	if err := service.HandleMessage(context.Background(), Message{ChatID: 42, ChatType: "private", UserID: 42, Text: "12345"}); err != nil {
		t.Fatal(err)
	}
	if broker.createCount != 0 || broker.createdCode != "" {
		t.Fatalf("unauthorized user created job count=%d code=%q", broker.createCount, broker.createdCode)
	}
	if !telegram.hasMessageContaining("not allowed") {
		t.Fatalf("access denial message missing: %#v", telegram.messages)
	}
}

func TestAdminCanAllowUserIntoQuotaGroupAndQuotaIsEnforced(t *testing.T) {
	access := NewMemoryAccessManager(AccessConfig{AdminUserIDs: []string{"7"}, DefaultOpen: false, Clock: fixedAccessClock(t)})
	broker := &fakeBroker{
		createJob: QRJob{ID: "job-1", UserID: "42", ChatID: "42", Status: JobWaiting},
		job:       QRJob{ID: "job-1", UserID: "42", ChatID: "42", Status: JobSucceeded},
		image:     []byte("png image"),
		mime:      "image/png",
	}
	telegram := &fakeTelegram{}
	service := NewService(ServiceConfig{PollInterval: time.Millisecond, PollTimeout: time.Second, Access: access}, telegram, broker)

	admin := Message{ChatID: 7, ChatType: "private", UserID: 7}
	if err := service.HandleMessage(context.Background(), withText(admin, "/set_group riders 1")); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleMessage(context.Background(), withText(admin, "/allow_user 42 0 riders")); err != nil {
		t.Fatal(err)
	}
	if !telegram.hasMessageContaining("group riders") || !telegram.hasMessageContaining("allowed user 42") {
		t.Fatalf("admin confirmations missing: %#v", telegram.messages)
	}

	user := Message{ChatID: 42, ChatType: "private", UserID: 42}
	if err := service.HandleMessage(context.Background(), withText(user, "12345")); err != nil {
		t.Fatal(err)
	}
	if broker.createCount != 1 || broker.createdCode != "12345" {
		t.Fatalf("first authorized request not queued: count=%d code=%q", broker.createCount, broker.createdCode)
	}

	if err := service.HandleMessage(context.Background(), withText(user, "23456")); err != nil {
		t.Fatal(err)
	}
	if broker.createCount != 1 {
		t.Fatalf("quota-exceeded request created a broker job: count=%d", broker.createCount)
	}
	if !telegram.hasMessageContaining("daily limit") {
		t.Fatalf("quota denial message missing: %#v", telegram.messages)
	}
}

func TestAdminCanAddUserByIDUnderAdminWithDefaultLimitTwenty(t *testing.T) {
	access := NewMemoryAccessManager(AccessConfig{AdminUserIDs: []string{"7"}, DefaultOpen: false, Clock: fixedAccessClock(t)})
	service := NewService(ServiceConfig{PollInterval: time.Millisecond, PollTimeout: time.Second, Access: access}, &fakeTelegram{}, &fakeBroker{})

	if err := service.HandleMessage(context.Background(), Message{ChatID: 7, ChatType: "private", UserID: 7, Text: "/admin add 42"}); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 20; i++ {
		decision, err := access.AuthorizeAndConsume(context.Background(), AccessRequest{ChatID: "42", ChatType: "private", UserID: "42", Now: fixedAccessClock(t)()})
		if err != nil {
			t.Fatal(err)
		}
		if !decision.Allowed {
			t.Fatalf("request %d was denied: %#v", i+1, decision)
		}
	}
	decision, err := access.AuthorizeAndConsume(context.Background(), AccessRequest{ChatID: "42", ChatType: "private", UserID: "42", Now: fixedAccessClock(t)()})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || decision.Reason != "user_daily_limit" {
		t.Fatalf("21st request decision = %#v, want user_daily_limit denial", decision)
	}
}

func TestAdminCanChangeDefaultUserDailyLimit(t *testing.T) {
	access := NewMemoryAccessManager(AccessConfig{AdminUserIDs: []string{"7"}, DefaultOpen: false, Clock: fixedAccessClock(t)})
	telegram := &fakeTelegram{}
	service := NewService(ServiceConfig{PollInterval: time.Millisecond, PollTimeout: time.Second, Access: access}, telegram, &fakeBroker{})

	admin := Message{ChatID: 7, ChatType: "private", UserID: 7}
	if err := service.HandleMessage(context.Background(), withText(admin, "/admin set_default_limit 3")); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleMessage(context.Background(), withText(admin, "/admin add 42")); err != nil {
		t.Fatal(err)
	}

	if !telegram.hasMessageContaining("default user daily limit to 3") {
		t.Fatalf("default-limit confirmation missing: %#v", telegram.messages)
	}
	if !telegram.hasMessageContaining("allowed user 42 with daily limit 3") {
		t.Fatalf("add confirmation did not use configured default: %#v", telegram.messages)
	}
	for i := 0; i < 3; i++ {
		decision, err := access.AuthorizeAndConsume(context.Background(), AccessRequest{ChatID: "42", ChatType: "private", UserID: "42", Now: fixedAccessClock(t)()})
		if err != nil {
			t.Fatal(err)
		}
		if !decision.Allowed {
			t.Fatalf("request %d was denied: %#v", i+1, decision)
		}
	}
	decision, err := access.AuthorizeAndConsume(context.Background(), AccessRequest{ChatID: "42", ChatType: "private", UserID: "42", Now: fixedAccessClock(t)()})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || decision.Reason != "user_daily_limit" {
		t.Fatalf("4th request decision = %#v, want user_daily_limit denial", decision)
	}
}

func TestConfiguredDefaultUserDailyLimitAppliesToAdminAdd(t *testing.T) {
	access := NewMemoryAccessManager(AccessConfig{AdminUserIDs: []string{"7"}, DefaultOpen: false, DefaultUserDailyLimit: 4, Clock: fixedAccessClock(t)})
	telegram := &fakeTelegram{}
	service := NewService(ServiceConfig{PollInterval: time.Millisecond, PollTimeout: time.Second, Access: access}, telegram, &fakeBroker{})

	if err := service.HandleMessage(context.Background(), Message{ChatID: 7, ChatType: "private", UserID: 7, Text: "/admin add 42"}); err != nil {
		t.Fatal(err)
	}

	if !telegram.hasMessageContaining("allowed user 42 with daily limit 4") {
		t.Fatalf("add confirmation did not use configured default: %#v", telegram.messages)
	}
	access.mu.Lock()
	user := access.state.Users["42"]
	access.mu.Unlock()
	if user.DailyLimit != 4 {
		t.Fatalf("user daily limit = %d, want configured default 4", user.DailyLimit)
	}
}

func TestAdminAddUsernameLooksUpUserIDImmediately(t *testing.T) {
	resolver := &fakeUsernameResolver{users: map[string]ResolvedUsername{
		"darja_smm_prod": {UserID: "42", Username: "darja_smm_prod"},
	}}
	access := NewMemoryAccessManager(AccessConfig{AdminUserIDs: []string{"7"}, DefaultOpen: false, UsernameResolver: resolver, Clock: fixedAccessClock(t)})
	telegram := &fakeTelegram{}
	service := NewService(ServiceConfig{PollInterval: time.Millisecond, PollTimeout: time.Second, Access: access}, telegram, &fakeBroker{})

	if err := service.HandleMessage(context.Background(), Message{ChatID: 7, ChatType: "private", UserID: 7, Text: "/admin add @darja_smm_prod"}); err != nil {
		t.Fatal(err)
	}

	if resolver.calls != 1 || resolver.lastUsername != "darja_smm_prod" {
		t.Fatalf("resolver calls=%d last=%q, want one lookup for darja_smm_prod", resolver.calls, resolver.lastUsername)
	}
	if !telegram.hasMessageContaining("allowed user 42 with daily limit 20") {
		t.Fatalf("username lookup add confirmation missing numeric user ID: %#v", telegram.messages)
	}
	access.mu.Lock()
	_, admin := access.state.Admins["42"]
	user, exists := access.state.Users["42"]
	pending := len(access.state.PendingUserGrants)
	access.mu.Unlock()
	if admin {
		t.Fatalf("username lookup incorrectly made user 42 an admin")
	}
	if !exists || !user.Active || user.DailyLimit != 20 || user.Username != "darja_smm_prod" {
		t.Fatalf("user 42 = %#v, exists=%v; want active regular user with username and limit 20", user, exists)
	}
	if pending != 0 {
		t.Fatalf("resolved username should not remain pending, pending grants=%d", pending)
	}
}

func TestAdminCanRemoveUserUnderAdmin(t *testing.T) {
	access := NewMemoryAccessManager(AccessConfig{AdminUserIDs: []string{"7"}, DefaultOpen: false, Clock: fixedAccessClock(t)})
	telegram := &fakeTelegram{}
	service := NewService(ServiceConfig{PollInterval: time.Millisecond, PollTimeout: time.Second, Access: access}, telegram, &fakeBroker{})
	admin := Message{ChatID: 7, ChatType: "private", UserID: 7}

	if err := service.HandleMessage(context.Background(), withText(admin, "/admin add_user 42 20")); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleMessage(context.Background(), withText(admin, "/admin remove 42")); err != nil {
		t.Fatal(err)
	}

	if !telegram.hasMessageContaining("removed user 42") {
		t.Fatalf("remove confirmation missing: %#v", telegram.messages)
	}
	access.mu.Lock()
	_, exists := access.state.Users["42"]
	access.mu.Unlock()
	if exists {
		t.Fatalf("removed user still present in access state")
	}
	decision, err := access.AuthorizeAndConsume(context.Background(), AccessRequest{ChatID: "42", ChatType: "private", UserID: "42", Now: fixedAccessClock(t)()})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || decision.Reason != "not_allowed" {
		t.Fatalf("removed user decision = %#v, want not_allowed", decision)
	}
}

func TestAdminCanAddPreviouslySeenUsernameAsRegularUser(t *testing.T) {
	access := NewMemoryAccessManager(AccessConfig{AdminUserIDs: []string{"7"}, DefaultOpen: false, Clock: fixedAccessClock(t)})
	telegram := &fakeTelegram{}
	service := NewService(ServiceConfig{PollInterval: time.Millisecond, PollTimeout: time.Second, Access: access}, telegram, &fakeBroker{})

	if err := service.HandleMessage(context.Background(), Message{ChatID: 42, ChatType: "private", UserID: 42, Username: "darja_smm_prod", Text: "/access"}); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleMessage(context.Background(), Message{ChatID: 7, ChatType: "private", UserID: 7, Text: "/admin add @darja_smm_prod"}); err != nil {
		t.Fatal(err)
	}

	if !telegram.hasMessageContaining("allowed user 42 with daily limit 20") {
		t.Fatalf("username add confirmation missing numeric user ID: %#v", telegram.messages)
	}
	access.mu.Lock()
	_, admin := access.state.Admins["42"]
	user, exists := access.state.Users["42"]
	_, bogusAtKey := access.state.Users["@darja_smm_prod"]
	access.mu.Unlock()
	if admin {
		t.Fatalf("username add incorrectly made user 42 an admin")
	}
	if !exists || !user.Active || user.DailyLimit != 20 || user.Username != "darja_smm_prod" {
		t.Fatalf("user 42 = %#v, exists=%v; want active regular user with username and limit 20", user, exists)
	}
	if bogusAtKey {
		t.Fatalf("username add wrote an @username key instead of numeric Telegram ID")
	}
}

func TestAdminCanAddMentionedUsernameAsRegularUser(t *testing.T) {
	access := NewMemoryAccessManager(AccessConfig{AdminUserIDs: []string{"7"}, DefaultOpen: false, Clock: fixedAccessClock(t)})
	telegram := &fakeTelegram{}
	service := NewService(ServiceConfig{PollInterval: time.Millisecond, PollTimeout: time.Second, Access: access}, telegram, &fakeBroker{})

	if err := service.HandleMessage(context.Background(), Message{
		ChatID:   7,
		ChatType: "private",
		UserID:   7,
		Text:     "/admin add @darja_smm_prod",
		MentionedUsers: []MentionedUser{{
			UserID:   42,
			Username: "darja_smm_prod",
		}},
	}); err != nil {
		t.Fatal(err)
	}

	if !telegram.hasMessageContaining("allowed user 42 with daily limit 20") {
		t.Fatalf("mentioned username add confirmation missing numeric user ID: %#v", telegram.messages)
	}
	access.mu.Lock()
	_, admin := access.state.Admins["42"]
	user, exists := access.state.Users["42"]
	pending := len(access.state.PendingUserGrants)
	access.mu.Unlock()
	if admin {
		t.Fatalf("mentioned username add incorrectly made user 42 an admin")
	}
	if !exists || !user.Active || user.DailyLimit != 20 || user.Username != "darja_smm_prod" {
		t.Fatalf("user 42 = %#v, exists=%v; want active regular user with username and limit 20", user, exists)
	}
	if pending != 0 {
		t.Fatalf("mentioned username should resolve immediately, pending grants=%d", pending)
	}
}

func TestAdminUsernameGrantAppliesWhenUserLaterMessagesBot(t *testing.T) {
	access := NewMemoryAccessManager(AccessConfig{AdminUserIDs: []string{"7"}, DefaultOpen: false, Clock: fixedAccessClock(t)})
	telegram := &fakeTelegram{}
	service := NewService(ServiceConfig{PollInterval: time.Millisecond, PollTimeout: time.Second, Access: access}, telegram, &fakeBroker{})

	if err := service.HandleMessage(context.Background(), Message{ChatID: 7, ChatType: "private", UserID: 7, Text: "/admin add @darja_smm_prod"}); err != nil {
		t.Fatal(err)
	}
	if !telegram.hasMessageContaining("pending @darja_smm_prod") {
		t.Fatalf("pending username grant confirmation missing: %#v", telegram.messages)
	}
	if !telegram.hasMessageContaining("/start") {
		t.Fatalf("pending username grant confirmation should tell admins that /start activates it: %#v", telegram.messages)
	}
	decision, err := access.AuthorizeAndConsume(context.Background(), AccessRequest{ChatID: "42", ChatType: "private", UserID: "42", Now: fixedAccessClock(t)()})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed {
		t.Fatalf("unresolved username grant should not authorize numeric user yet: %#v", decision)
	}

	if err := service.HandleMessage(context.Background(), Message{ChatID: 42, ChatType: "private", UserID: 42, Username: "darja_smm_prod", Text: "/access"}); err != nil {
		t.Fatal(err)
	}

	assertPendingUsernameGrantActivated(t, access, telegram, "darja_smm_prod", "42", 20)
}

func TestPendingUsernameGrantAppliesWhenUserStartsBot(t *testing.T) {
	access := NewMemoryAccessManager(AccessConfig{AdminUserIDs: []string{"7"}, DefaultOpen: false, Clock: fixedAccessClock(t)})
	telegram := &fakeTelegram{}
	service := NewService(ServiceConfig{PollInterval: time.Millisecond, PollTimeout: time.Second, Access: access}, telegram, &fakeBroker{})

	if err := service.HandleMessage(context.Background(), Message{ChatID: 7, ChatType: "private", UserID: 7, Text: "/admin add @darja_smm_prod"}); err != nil {
		t.Fatal(err)
	}
	if !telegram.hasMessageContaining("pending @darja_smm_prod") {
		t.Fatalf("pending username grant confirmation missing: %#v", telegram.messages)
	}

	if err := service.HandleMessage(context.Background(), Message{ChatID: 42, ChatType: "private", UserID: 42, Username: "darja_smm_prod", Text: "/start"}); err != nil {
		t.Fatal(err)
	}

	assertPendingUsernameGrantActivated(t, access, telegram, "darja_smm_prod", "42", 20)
	if !telegram.hasMessageContaining("send one 5 digit code") {
		t.Fatalf("start help should still be sent after activating grant: %#v", telegram.messages)
	}
}

func TestPendingUsernameGrantAppliesWhenMentionedUserIDArrives(t *testing.T) {
	access := NewMemoryAccessManager(AccessConfig{AdminUserIDs: []string{"7"}, DefaultOpen: false, Clock: fixedAccessClock(t)})
	telegram := &fakeTelegram{}
	service := NewService(ServiceConfig{PollInterval: time.Millisecond, PollTimeout: time.Second, Access: access}, telegram, &fakeBroker{})

	if err := service.HandleMessage(context.Background(), Message{ChatID: 7, ChatType: "private", UserID: 7, Text: "/admin add @darja_smm_prod"}); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleMessage(context.Background(), Message{
		ChatID:   7,
		ChatType: "private",
		UserID:   7,
		Text:     "native mention @darja_smm_prod",
		MentionedUsers: []MentionedUser{{
			UserID:   42,
			Username: "darja_smm_prod",
		}},
	}); err != nil {
		t.Fatal(err)
	}

	if !telegram.hasMessageContaining("access enabled for @darja_smm_prod with daily limit 20") {
		t.Fatalf("pending grant activation notice from mention missing: %#v", telegram.messages)
	}
	access.mu.Lock()
	_, admin := access.state.Admins["42"]
	user, exists := access.state.Users["42"]
	pending := len(access.state.PendingUserGrants)
	access.mu.Unlock()
	if admin {
		t.Fatalf("pending mention grant incorrectly made user 42 an admin")
	}
	if !exists || !user.Active || user.DailyLimit != 20 || user.Username != "darja_smm_prod" {
		t.Fatalf("user 42 = %#v, exists=%v; want active regular user with username and limit 20", user, exists)
	}
	if pending != 0 {
		t.Fatalf("pending mention grant should be consumed, pending grants=%d", pending)
	}
}

func TestGroupChatMustBeAllowedAndUsesChatQuota(t *testing.T) {
	access := NewMemoryAccessManager(AccessConfig{AdminUserIDs: []string{"7"}, DefaultOpen: false, Clock: fixedAccessClock(t)})
	broker := &fakeBroker{
		createJob: QRJob{ID: "job-1", UserID: "42", ChatID: "-1001", Status: JobWaiting},
		job:       QRJob{ID: "job-1", UserID: "42", ChatID: "-1001", Status: JobSucceeded},
		image:     []byte("png image"),
		mime:      "image/png",
	}
	telegram := &fakeTelegram{}
	service := NewService(ServiceConfig{PollInterval: time.Millisecond, PollTimeout: time.Second, Access: access}, telegram, broker)

	admin := Message{ChatID: 7, ChatType: "private", UserID: 7}
	for _, text := range []string{"/allow_user 42 5", "/allow_chat -1001 1"} {
		if err := service.HandleMessage(context.Background(), withText(admin, text)); err != nil {
			t.Fatal(err)
		}
	}

	blockedGroup := Message{ChatID: -2002, ChatType: "supergroup", UserID: 42, Text: "12345"}
	if err := service.HandleMessage(context.Background(), blockedGroup); err != nil {
		t.Fatal(err)
	}
	if broker.createCount != 0 {
		t.Fatalf("request in unallowed group created a broker job: count=%d", broker.createCount)
	}
	if !telegram.hasMessageContaining("chat is not allowed") {
		t.Fatalf("group denial message missing: %#v", telegram.messages)
	}

	allowedGroup := Message{ChatID: -1001, ChatType: "supergroup", UserID: 42}
	if err := service.HandleMessage(context.Background(), withText(allowedGroup, "12345")); err != nil {
		t.Fatal(err)
	}
	if broker.createCount != 1 {
		t.Fatalf("first allowed group request not queued: count=%d", broker.createCount)
	}
	if err := service.HandleMessage(context.Background(), withText(allowedGroup, "23456")); err != nil {
		t.Fatal(err)
	}
	if broker.createCount != 1 {
		t.Fatalf("chat-quota-exceeded request created a broker job: count=%d", broker.createCount)
	}
	if !telegram.hasMessageContaining("chat daily limit") {
		t.Fatalf("chat quota denial message missing: %#v", telegram.messages)
	}
}

func TestNonAdminCannotRunAdminCommands(t *testing.T) {
	access := NewMemoryAccessManager(AccessConfig{AdminUserIDs: []string{"7"}, DefaultOpen: false, Clock: fixedAccessClock(t)})
	broker := &fakeBroker{}
	telegram := &fakeTelegram{}
	service := NewService(ServiceConfig{PollInterval: time.Millisecond, PollTimeout: time.Second, Access: access}, telegram, broker)

	if err := service.HandleMessage(context.Background(), Message{ChatID: 42, ChatType: "private", UserID: 42, Text: "/allow_user 42 5"}); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleMessage(context.Background(), Message{ChatID: 42, ChatType: "private", UserID: 42, Text: "/admin"}); err != nil {
		t.Fatal(err)
	}
	if !telegram.hasMessageContaining("admin only") {
		t.Fatalf("non-admin rejection missing: %#v", telegram.messages)
	}
	if telegram.hasMessageContaining("/allow_user") {
		t.Fatalf("non-admin /admin leaked admin help: %#v", telegram.messages)
	}

	if err := service.HandleMessage(context.Background(), Message{ChatID: 42, ChatType: "private", UserID: 42, Text: "12345"}); err != nil {
		t.Fatal(err)
	}
	if broker.createCount != 0 {
		t.Fatalf("non-admin grant unexpectedly changed access: count=%d", broker.createCount)
	}
}

func TestAccessStatusIncludesUserGroupAndChatQuota(t *testing.T) {
	access := NewMemoryAccessManager(AccessConfig{AdminUserIDs: []string{"7"}, DefaultOpen: false, Clock: fixedAccessClock(t)})
	broker := &fakeBroker{
		createJob: QRJob{ID: "job-1", UserID: "42", ChatID: "-1001", Status: JobWaiting},
		job:       QRJob{ID: "job-1", UserID: "42", ChatID: "-1001", Status: JobSucceeded},
		image:     []byte("png image"),
		mime:      "image/png",
	}
	telegram := &fakeTelegram{}
	service := NewService(ServiceConfig{PollInterval: time.Millisecond, PollTimeout: time.Second, Access: access}, telegram, broker)

	admin := Message{ChatID: 7, ChatType: "private", UserID: 7}
	for _, text := range []string{"/set_group riders 3", "/allow_user 42 5 riders", "/allow_chat -1001 2"} {
		if err := service.HandleMessage(context.Background(), withText(admin, text)); err != nil {
			t.Fatal(err)
		}
	}
	user := Message{ChatID: -1001, ChatType: "supergroup", UserID: 42}
	if err := service.HandleMessage(context.Background(), withText(user, "12345")); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleMessage(context.Background(), withText(user, "/access")); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"group: riders", "user quota: 1/5", "group quota: 1/3", "chat quota: 1/2"} {
		if !telegram.hasMessageContaining(want) {
			t.Fatalf("access status missing %q: %#v", want, telegram.messages)
		}
	}
}

func assertPendingUsernameGrantActivated(t *testing.T, access *AccessManager, telegram *fakeTelegram, username string, userID string, dailyLimit int) {
	t.Helper()
	if !telegram.hasMessageContaining("access enabled for @" + username + " with daily limit " + strconv.Itoa(dailyLimit)) {
		t.Fatalf("pending grant activation notice missing: %#v", telegram.messages)
	}
	access.mu.Lock()
	_, admin := access.state.Admins[userID]
	user, exists := access.state.Users[userID]
	_, bogusAtKey := access.state.Users["@"+username]
	_, pending := access.state.PendingUserGrants[usernameKey(username)]
	access.mu.Unlock()
	if admin {
		t.Fatalf("pending username grant incorrectly made user %s an admin", userID)
	}
	if !exists || !user.Active || user.DailyLimit != dailyLimit || user.Username != username {
		t.Fatalf("user %s = %#v, exists=%v; want active regular user with username and limit %d", userID, user, exists, dailyLimit)
	}
	if bogusAtKey {
		t.Fatalf("pending grant wrote an @username key instead of numeric Telegram ID")
	}
	if pending {
		t.Fatalf("pending grant for @%s should be consumed", username)
	}
}

type fakeUsernameResolver struct {
	users        map[string]ResolvedUsername
	calls        int
	lastUsername string
	err          error
}

func (r *fakeUsernameResolver) ResolveUsername(ctx context.Context, username string) (ResolvedUsername, bool, error) {
	r.calls++
	r.lastUsername = username
	if r.err != nil {
		return ResolvedUsername{}, false, r.err
	}
	user, ok := r.users[username]
	return user, ok, nil
}

func fixedAccessClock(t *testing.T) func() time.Time {
	t.Helper()
	fixed, err := time.Parse(time.RFC3339, "2026-05-18T07:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	return func() time.Time { return fixed }
}

func withText(msg Message, text string) Message {
	msg.Text = text
	return msg
}
