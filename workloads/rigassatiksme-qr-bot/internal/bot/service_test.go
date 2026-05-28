package bot

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestServiceDefaultsUseLowLatencyJobPolling(t *testing.T) {
	service := NewService(ServiceConfig{}, &fakeTelegram{}, &fakeBroker{})
	if service.cfg.PollInterval != 250*time.Millisecond {
		t.Fatalf("PollInterval = %s, want 250ms", service.cfg.PollInterval)
	}
}

func TestServiceQueuesValidCodeAndSendsCompletedQRImage(t *testing.T) {
	broker := &fakeBroker{
		createJob: QRJob{ID: "job-1", UserID: "42", ChatID: "1001", Status: JobWaiting},
		job:       QRJob{ID: "job-1", UserID: "42", ChatID: "1001", Status: JobSucceeded},
		image:     []byte("png image"),
		mime:      "image/png",
	}
	telegram := &fakeTelegram{}
	service := NewService(ServiceConfig{PollInterval: time.Millisecond, PollTimeout: time.Second}, telegram, broker)

	if err := service.HandleMessage(context.Background(), Message{ChatID: 1001, UserID: 42, Text: "12345"}); err != nil {
		t.Fatal(err)
	}

	if got := broker.createdCode; got != "12345" {
		t.Fatalf("created code = %q, want 12345", got)
	}
	if !telegram.hasMessageContaining("Pieprasījums gaida") {
		t.Fatalf("queue message missing: %#v", telegram.messages)
	}
	photo := telegram.waitForPhoto(t)
	if !bytes.Equal(photo.bytes, []byte("png image")) {
		t.Fatalf("photo bytes = %q", string(photo.bytes))
	}
	if strings.Contains(photo.caption, "12345") {
		t.Fatalf("photo caption leaked code: %q", photo.caption)
	}
	if photo.caption != "" {
		t.Fatalf("photo caption = %q, want empty QR-only delivery", photo.caption)
	}
}

func TestServiceSendsBrokerGeneratedScreenshotWithoutAdditionalCropping(t *testing.T) {
	input := stripedPNGWithSystemBars(t, 9, 120, 6, 6)
	broker := &fakeBroker{
		createJob: QRJob{ID: "job-1", UserID: "42", ChatID: "1001", Status: JobWaiting},
		job:       QRJob{ID: "job-1", UserID: "42", ChatID: "1001", Status: JobSucceeded},
		image:     input,
		mime:      "image/png",
	}
	telegram := &fakeTelegram{}
	service := NewService(ServiceConfig{PollInterval: time.Millisecond, PollTimeout: time.Second}, telegram, broker)

	if err := service.HandleMessage(context.Background(), Message{ChatID: 1001, UserID: 42, Text: "12345"}); err != nil {
		t.Fatal(err)
	}

	photo := telegram.waitForPhoto(t)
	if !bytes.Equal(photo.bytes, input) {
		t.Fatalf("bot must not crop broker-generated screenshot a second time: sent %d bytes, broker had %d bytes", len(photo.bytes), len(input))
	}
}

func TestServiceQueuesCodeCommandForGroupPrivacyMode(t *testing.T) {
	broker := &fakeBroker{
		createJob: QRJob{ID: "job-1", UserID: "42", ChatID: "-1001", Status: JobWaiting},
		job:       QRJob{ID: "job-1", UserID: "42", ChatID: "-1001", Status: JobSucceeded},
		image:     []byte("png image"),
		mime:      "image/png",
	}
	telegram := &fakeTelegram{}
	service := NewService(ServiceConfig{PollInterval: time.Millisecond, PollTimeout: time.Second}, telegram, broker)

	if err := service.HandleMessage(context.Background(), Message{ChatID: -1001, ChatType: "group", UserID: 42, Text: "/qr 23456"}); err != nil {
		t.Fatal(err)
	}

	if got := broker.createdCode; got != "23456" {
		t.Fatalf("created code = %q, want 23456", got)
	}
	if broker.createCount != 1 {
		t.Fatalf("create count = %d, want 1", broker.createCount)
	}
}

func TestServiceStartShowsLatvianHelpWithRussianButton(t *testing.T) {
	telegram := &fakeTelegram{}
	service := NewService(ServiceConfig{}, telegram, &fakeBroker{})

	if err := service.HandleMessage(context.Background(), Message{ChatID: 1001, UserID: 42, Text: "/start"}); err != nil {
		t.Fatal(err)
	}

	if !telegram.hasMessageContaining("rs biļete") || !telegram.hasMessageContaining("5 ciparu") {
		t.Fatalf("latvian start help missing: %#v", telegram.messages)
	}
	if !telegram.hasButton("Русский", "lang:ru") {
		t.Fatalf("russian language button missing: %#v", telegram.buttonMessages)
	}
}

func TestServiceLanguageCallbackStoresPreferenceAndHelpUsesIt(t *testing.T) {
	access := NewMemoryAccessManager(AccessConfig{DefaultOpen: true, Clock: fixedAccessClock(t)})
	telegram := &fakeTelegram{}
	service := NewService(ServiceConfig{Access: access}, telegram, &fakeBroker{})

	if err := service.HandleCallback(context.Background(), Callback{ChatID: 1001, UserID: 42, Username: "target", Data: "lang:ru"}); err != nil {
		t.Fatal(err)
	}
	if !telegram.hasMessageContaining("rs biļete") || !telegram.hasMessageContaining("5-знач") {
		t.Fatalf("russian help missing after callback: %#v", telegram.messages)
	}
	if !telegram.hasButton("Latviski", "lang:lv") {
		t.Fatalf("latvian language button missing: %#v", telegram.buttonMessages)
	}

	if err := service.HandleMessage(context.Background(), Message{ChatID: 1001, UserID: 42, Text: "/help"}); err != nil {
		t.Fatal(err)
	}
	if !telegram.hasMessageContaining("5-знач") {
		t.Fatalf("saved russian preference not used for /help: %#v", telegram.messages)
	}
}

func TestAnnouncementCommandRejectsNonAdmin(t *testing.T) {
	access := NewMemoryAccessManager(AccessConfig{AdminUserIDs: []string{"7"}, DefaultOpen: false, Clock: fixedAccessClock(t)})
	telegram := &fakeTelegram{}
	service := NewService(ServiceConfig{Access: access}, telegram, &fakeBroker{})

	if err := service.HandleMessage(context.Background(), Message{MessageID: 10, ChatID: 42, ChatType: "private", UserID: 42, Text: "/admin announce"}); err != nil {
		t.Fatal(err)
	}

	if !telegram.hasMessageContaining("Tikai administratoram") {
		t.Fatalf("non-admin announcement command should get admin-only response: %#v", telegram.messages)
	}
}

func TestAnnouncementCommandPromptsAdminToReply(t *testing.T) {
	access := NewMemoryAccessManager(AccessConfig{AdminUserIDs: []string{"7"}, DefaultOpen: false, Clock: fixedAccessClock(t)})
	telegram := &fakeTelegram{}
	service := NewService(ServiceConfig{Access: access}, telegram, &fakeBroker{})

	if err := service.HandleMessage(context.Background(), Message{MessageID: 10, ChatID: 7, ChatType: "private", UserID: 7, Text: "/admin announce"}); err != nil {
		t.Fatal(err)
	}

	if !telegram.hasMessageContaining("Reply to this command") {
		t.Fatalf("announcement reply prompt missing: %#v", telegram.messages)
	}
}

func TestAnnouncementReplyCreatesPreviewWithoutSending(t *testing.T) {
	access := NewMemoryAccessManager(AccessConfig{AdminUserIDs: []string{"7"}, DefaultOpen: false, Clock: fixedAccessClock(t)})
	seedAnnouncementUser(access, "42", true)
	telegram := &fakeTelegram{}
	service := NewService(ServiceConfig{Access: access}, telegram, &fakeBroker{})
	text := seasonalAnnouncementTestText()

	if err := service.HandleMessage(context.Background(), Message{MessageID: 10, ChatID: 7, ChatType: "private", UserID: 7, Text: "/admin announce"}); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleMessage(context.Background(), Message{MessageID: 11, ReplyToMessageID: 10, ChatID: 7, ChatType: "private", UserID: 7, Text: text}); err != nil {
		t.Fatal(err)
	}

	if !telegram.hasMessageContaining("Recipients: 1") || !telegram.hasMessageContaining("rs biļete bots ir atjaunināts") {
		t.Fatalf("announcement preview missing count or text: %#v", telegram.messages)
	}
	if !telegram.hasButton("Send", "announce:send:7-7-10") || !telegram.hasButton("Cancel", "announce:cancel:7-7-10") {
		t.Fatalf("announcement preview buttons missing: %#v", telegram.buttonMessages)
	}
	if telegram.hasTextMessageTo(42, text) {
		t.Fatalf("announcement should not send before confirmation: %#v", telegram.textMessages)
	}
}

func TestAnnouncementConfirmSendsOnlyActiveAllowedUsersAndReportsFailures(t *testing.T) {
	access := NewMemoryAccessManager(AccessConfig{AdminUserIDs: []string{"7"}, DefaultOpen: false, Clock: fixedAccessClock(t)})
	seedAnnouncementUser(access, "42", true)
	seedAnnouncementUser(access, "43", true)
	seedAnnouncementUser(access, "44", false)
	seedPendingAnnouncementGrant(access, "pending_user")
	telegram := &fakeTelegram{failSendFor: map[int64]error{43: errors.New("blocked")}}
	service := NewService(ServiceConfig{Access: access}, telegram, &fakeBroker{})
	text := seasonalAnnouncementTestText()

	if err := service.HandleMessage(context.Background(), Message{MessageID: 10, ChatID: 7, ChatType: "private", UserID: 7, Text: "/admin announce"}); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleMessage(context.Background(), Message{MessageID: 11, ReplyToMessageID: 10, ChatID: 7, ChatType: "private", UserID: 7, Text: text}); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleCallback(context.Background(), Callback{ChatID: 7, ChatType: "private", UserID: 7, Data: "announce:send:7-7-10"}); err != nil {
		t.Fatal(err)
	}

	telegram.waitForTextMessageTo(t, 42, text)
	telegram.waitForMessageContaining(t, "Announcement sent to 1 users; failed for 1.")
	if telegram.hasTextMessageTo(44, text) {
		t.Fatalf("disabled user received announcement: %#v", telegram.textMessages)
	}
}

func TestAnnouncementCancelDeletesDraftAndSendsNothing(t *testing.T) {
	access := NewMemoryAccessManager(AccessConfig{AdminUserIDs: []string{"7"}, DefaultOpen: false, Clock: fixedAccessClock(t)})
	seedAnnouncementUser(access, "42", true)
	telegram := &fakeTelegram{}
	service := NewService(ServiceConfig{Access: access}, telegram, &fakeBroker{})
	text := seasonalAnnouncementTestText()

	if err := service.HandleMessage(context.Background(), Message{MessageID: 10, ChatID: 7, ChatType: "private", UserID: 7, Text: "/admin announce"}); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleMessage(context.Background(), Message{MessageID: 11, ReplyToMessageID: 10, ChatID: 7, ChatType: "private", UserID: 7, Text: text}); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleCallback(context.Background(), Callback{ChatID: 7, ChatType: "private", UserID: 7, Data: "announce:cancel:7-7-10"}); err != nil {
		t.Fatal(err)
	}

	if !telegram.hasMessageContaining("Announcement cancelled") {
		t.Fatalf("cancel confirmation missing: %#v", telegram.messages)
	}
	if telegram.hasTextMessageTo(42, text) {
		t.Fatalf("cancelled announcement was sent: %#v", telegram.textMessages)
	}
}

func TestAnnouncementIgnoresWrongReply(t *testing.T) {
	access := NewMemoryAccessManager(AccessConfig{AdminUserIDs: []string{"7"}, DefaultOpen: false, Clock: fixedAccessClock(t)})
	seedAnnouncementUser(access, "42", true)
	telegram := &fakeTelegram{}
	service := NewService(ServiceConfig{Access: access}, telegram, &fakeBroker{})

	if err := service.HandleMessage(context.Background(), Message{MessageID: 10, ChatID: 7, ChatType: "private", UserID: 7, Text: "/admin announce"}); err != nil {
		t.Fatal(err)
	}
	if err := service.HandleMessage(context.Background(), Message{MessageID: 11, ReplyToMessageID: 999, ChatID: 7, ChatType: "private", UserID: 7, Text: seasonalAnnouncementTestText()}); err != nil {
		t.Fatal(err)
	}

	if telegram.hasButton("Send", "announce:send:7-7-10") {
		t.Fatalf("wrong reply should not create announcement preview: %#v", telegram.buttonMessages)
	}
}

func TestServiceRejectsInvalidCode(t *testing.T) {
	broker := &fakeBroker{}
	telegram := &fakeTelegram{}
	service := NewService(ServiceConfig{PollInterval: time.Millisecond, PollTimeout: time.Second}, telegram, broker)

	if err := service.HandleMessage(context.Background(), Message{ChatID: 1001, UserID: 42, Text: "1234"}); err != nil {
		t.Fatal(err)
	}
	if broker.createdCode != "" {
		t.Fatalf("invalid code should not create a job")
	}
	if !telegram.hasMessageContaining("5 ciparus") {
		t.Fatalf("invalid input guidance missing: %#v", telegram.messages)
	}
}

func TestServiceLocalizesLatvianNonAdminReplies(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  Message
		want string
	}{
		{name: "invalid qr command", msg: Message{ChatID: 1001, UserID: 42, Text: "/qr 1234"}, want: "Lietojums"},
		{name: "unknown command", msg: Message{ChatID: 1001, UserID: 42, Text: "/wat"}, want: "Nezināma komanda"},
		{name: "invalid text", msg: Message{ChatID: 1001, UserID: 42, Text: "hello"}, want: "Nosūti tieši 5 ciparus"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			telegram := &fakeTelegram{}
			service := NewService(ServiceConfig{}, telegram, &fakeBroker{})

			if err := service.HandleMessage(context.Background(), tc.msg); err != nil {
				t.Fatal(err)
			}
			if !telegram.hasMessageContaining(tc.want) {
				t.Fatalf("localized reply missing %q: %#v", tc.want, telegram.messages)
			}
			if telegram.hasMessageContaining("digits") || telegram.hasMessageContaining("unknown command") {
				t.Fatalf("english user reply leaked: %#v", telegram.messages)
			}
		})
	}
}

func TestServiceLocalizesRussianNonAdminReplies(t *testing.T) {
	access := NewMemoryAccessManager(AccessConfig{DefaultOpen: true, Clock: fixedAccessClock(t)})
	if err := access.SetUserLanguage(context.Background(), AccessRequest{UserID: "42", Now: fixedAccessClock(t)()}, "ru"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		msg  Message
		want string
	}{
		{name: "invalid qr command", msg: Message{ChatID: 1001, UserID: 42, Text: "/qr 1234"}, want: "Используй"},
		{name: "unknown command", msg: Message{ChatID: 1001, UserID: 42, Text: "/wat"}, want: "Неизвестная команда"},
		{name: "invalid text", msg: Message{ChatID: 1001, UserID: 42, Text: "hello"}, want: "Отправь ровно 5 цифр"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			telegram := &fakeTelegram{}
			service := NewService(ServiceConfig{Access: access}, telegram, &fakeBroker{})

			if err := service.HandleMessage(context.Background(), tc.msg); err != nil {
				t.Fatal(err)
			}
			if !telegram.hasMessageContaining(tc.want) {
				t.Fatalf("localized reply missing %q: %#v", tc.want, telegram.messages)
			}
			if telegram.hasMessageContaining("digits") || telegram.hasMessageContaining("unknown command") {
				t.Fatalf("english user reply leaked: %#v", telegram.messages)
			}
		})
	}
}

func TestServiceDoesNotUseEnglishForNonAdminStoredLanguage(t *testing.T) {
	access := NewMemoryAccessManager(AccessConfig{DefaultOpen: true, Clock: fixedAccessClock(t)})
	if err := access.SetUserLanguage(context.Background(), AccessRequest{UserID: "42", Now: fixedAccessClock(t)()}, "en"); err != nil {
		t.Fatal(err)
	}
	telegram := &fakeTelegram{}
	service := NewService(ServiceConfig{Access: access}, telegram, &fakeBroker{})

	if err := service.HandleMessage(context.Background(), Message{ChatID: 1001, UserID: 42, Text: "/status"}); err != nil {
		t.Fatal(err)
	}
	if !telegram.hasMessageContaining("Nav rindā") {
		t.Fatalf("latvian fallback missing: %#v", telegram.messages)
	}
	if telegram.hasMessageContaining("No QR request") {
		t.Fatalf("english fallback leaked to non-admin user: %#v", telegram.messages)
	}
}

func TestServiceLocalizesQrLifecycleReplies(t *testing.T) {
	for _, tc := range []struct {
		name      string
		language  string
		broker    *fakeBroker
		message   Message
		waitFor   string
		forbidden string
	}{
		{
			name:      "latvian queued",
			language:  "lv",
			broker:    &fakeBroker{createJob: QRJob{ID: "job-1", UserID: "42", ChatID: "1001", Status: JobWaiting}, job: QRJob{ID: "job-1", UserID: "42", ChatID: "1001", Status: JobRunning}},
			message:   Message{ChatID: 1001, UserID: 42, Text: "12345"},
			waitFor:   "Pieprasījums gaida",
			forbidden: "Your request",
		},
		{
			name:      "russian failed",
			language:  "ru",
			broker:    &fakeBroker{createJob: QRJob{ID: "job-1", UserID: "42", ChatID: "1001", Status: JobWaiting}, job: QRJob{ID: "job-1", UserID: "42", ChatID: "1001", Status: JobFailed, Reason: "rs_app_attention_required"}},
			message:   Message{ChatID: 1001, UserID: 42, Text: "12345"},
			waitFor:   "Приложению RS нужно внимание",
			forbidden: "QR request failed",
		},
		{
			name:      "latvian no status",
			language:  "lv",
			broker:    &fakeBroker{},
			message:   Message{ChatID: 1001, UserID: 42, Text: "/status"},
			waitFor:   "Nav rindā",
			forbidden: "No QR request",
		},
		{
			name:      "russian no cancel",
			language:  "ru",
			broker:    &fakeBroker{},
			message:   Message{ChatID: 1001, UserID: 42, Text: "/cancel"},
			waitFor:   "Нет QR-запроса",
			forbidden: "No QR request",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			access := NewMemoryAccessManager(AccessConfig{DefaultOpen: true, Clock: fixedAccessClock(t)})
			if err := access.SetUserLanguage(context.Background(), AccessRequest{UserID: "42", Now: fixedAccessClock(t)()}, tc.language); err != nil {
				t.Fatal(err)
			}
			telegram := &fakeTelegram{}
			service := NewService(ServiceConfig{PollInterval: time.Millisecond, PollTimeout: 20 * time.Millisecond, Access: access}, telegram, tc.broker)

			if err := service.HandleMessage(context.Background(), tc.message); err != nil {
				t.Fatal(err)
			}
			telegram.waitForMessageContaining(t, tc.waitFor)
			if tc.forbidden != "" && telegram.hasMessageContaining(tc.forbidden) {
				t.Fatalf("english user reply leaked: %#v", telegram.messages)
			}
		})
	}
}

func TestCleanReasonMapsRsAppAttentionToActionableText(t *testing.T) {
	if got := cleanReason("rs_app_attention_required"); got != "RS app needs attention. Open it once and retry." {
		t.Fatalf("cleanReason(rs_app_attention_required) = %q", got)
	}
	if got := cleanReason("rs_monthly_ticket_unknown_state"); got != "RS app needs attention. Open it once and retry." {
		t.Fatalf("cleanReason(rs_monthly_ticket_unknown_state) = %q", got)
	}
}

func TestCleanReasonMapsStaleCodeToActionableText(t *testing.T) {
	if got := cleanReason("rs_monthly_ticket_stale_code"); got != "RS kept showing the previous QR after the new code was submitted. I did not send a stale image." {
		t.Fatalf("cleanReason(rs_monthly_ticket_stale_code) = %q", got)
	}
}

func TestServiceCancelsLatestJob(t *testing.T) {
	broker := &fakeBroker{
		cancelJob: QRJob{ID: "job-1", UserID: "42", ChatID: "1001", Status: JobCanceled},
		cancelOK:  true,
	}
	telegram := &fakeTelegram{}
	service := NewService(ServiceConfig{PollInterval: time.Millisecond, PollTimeout: time.Second}, telegram, broker)

	if err := service.HandleMessage(context.Background(), Message{ChatID: 1001, UserID: 42, Text: "/cancel"}); err != nil {
		t.Fatal(err)
	}
	if broker.cancelUserID != "42" {
		t.Fatalf("cancel user = %q, want 42", broker.cancelUserID)
	}
	if !telegram.hasMessageContaining("atcelts") {
		t.Fatalf("cancel confirmation missing: %#v", telegram.messages)
	}
}

func decodePNG(t *testing.T, data []byte) image.Image {
	t.Helper()
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	return decoded
}

func stripedPNGWithSystemBars(t *testing.T, width int, height int, topBar int, bottomBar int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		pixel := color.RGBA{B: 255, A: 255}
		switch {
		case y < topBar:
			pixel = color.RGBA{R: 255, A: 255}
		case y >= height-bottomBar:
			pixel = color.RGBA{G: 255, A: 255}
		}
		for x := 0; x < width; x++ {
			img.Set(x, y, pixel)
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return out.Bytes()
}

type fakeBroker struct {
	createJob    QRJob
	createErr    error
	job          QRJob
	image        []byte
	mime         string
	cancelJob    QRJob
	cancelOK     bool
	createdCode  string
	createCount  int
	cancelUserID string
}

func (b *fakeBroker) CreateQRJob(ctx context.Context, chatID string, userID string, code string) (QRJob, error) {
	b.createdCode = code
	b.createCount++
	if b.createErr != nil {
		return QRJob{}, b.createErr
	}
	return b.createJob, nil
}

func (b *fakeBroker) Job(ctx context.Context, id string) (QRJob, error) {
	if b.job.ID == "" {
		return QRJob{}, errors.New("missing")
	}
	return b.job, nil
}

func (b *fakeBroker) LatestJob(ctx context.Context, userID string) (QRJob, bool, error) {
	if b.job.ID == "" {
		return QRJob{}, false, nil
	}
	return b.job, true, nil
}

func (b *fakeBroker) CancelLatestJob(ctx context.Context, userID string) (QRJob, bool, error) {
	b.cancelUserID = userID
	return b.cancelJob, b.cancelOK, nil
}

func (b *fakeBroker) JobImage(ctx context.Context, id string) ([]byte, string, error) {
	if len(b.image) == 0 {
		return nil, "", errors.New("missing")
	}
	return append([]byte(nil), b.image...), b.mime, nil
}

type fakeTelegram struct {
	mu             sync.Mutex
	messages       []string
	textMessages   []sentTextMessage
	buttonMessages []sentButtonMessage
	photos         []sentPhoto
	failSendFor    map[int64]error
}

type sentTextMessage struct {
	chatID int64
	text   string
}

type sentButtonMessage struct {
	chatID  int64
	text    string
	buttons [][]InlineButton
}

type sentPhoto struct {
	chatID  int64
	bytes   []byte
	mime    string
	caption string
}

func (t *fakeTelegram) SendMessage(ctx context.Context, chatID int64, text string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.failSendFor[chatID]; err != nil {
		return err
	}
	t.messages = append(t.messages, text)
	t.textMessages = append(t.textMessages, sentTextMessage{chatID: chatID, text: text})
	return nil
}

func (t *fakeTelegram) SendMessageWithButtons(ctx context.Context, chatID int64, text string, buttons [][]InlineButton) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.failSendFor[chatID]; err != nil {
		return err
	}
	t.messages = append(t.messages, text)
	t.textMessages = append(t.textMessages, sentTextMessage{chatID: chatID, text: text})
	t.buttonMessages = append(t.buttonMessages, sentButtonMessage{chatID: chatID, text: text, buttons: buttons})
	return nil
}

func (t *fakeTelegram) SendPhoto(ctx context.Context, chatID int64, image []byte, mime string, caption string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.photos = append(t.photos, sentPhoto{chatID: chatID, bytes: append([]byte(nil), image...), mime: mime, caption: caption})
	return nil
}

func (t *fakeTelegram) hasMessageContaining(part string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, message := range t.messages {
		if strings.Contains(strings.ToLower(message), strings.ToLower(part)) {
			return true
		}
	}
	return false
}

func (t *fakeTelegram) hasTextMessageTo(chatID int64, text string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, message := range t.textMessages {
		if message.chatID == chatID && message.text == text {
			return true
		}
	}
	return false
}

func (t *fakeTelegram) hasButton(text string, data string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, message := range t.buttonMessages {
		for _, row := range message.buttons {
			for _, button := range row {
				if button.Text == text && button.Data == data {
					return true
				}
			}
		}
	}
	return false
}

func (t *fakeTelegram) waitForTextMessageTo(tst *testing.T, chatID int64, text string) {
	tst.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if t.hasTextMessageTo(chatID, text) {
			return
		}
		select {
		case <-deadline:
			tst.Fatalf("timed out waiting for message to %d: %#v", chatID, t.textMessages)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func (t *fakeTelegram) waitForPhoto(tst *testing.T) sentPhoto {
	tst.Helper()
	deadline := time.After(2 * time.Second)
	for {
		t.mu.Lock()
		if len(t.photos) > 0 {
			photo := t.photos[0]
			t.mu.Unlock()
			return photo
		}
		t.mu.Unlock()
		select {
		case <-deadline:
			tst.Fatalf("timed out waiting for photo")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func seedAnnouncementUser(access *AccessManager, userID string, active bool) {
	access.mu.Lock()
	defer access.mu.Unlock()
	access.ensureMapsLocked()
	access.state.Users[userID] = AccessUser{UserID: userID, Active: active, DailyLimit: 20, CreatedAt: nowText(fixedAnnouncementTestTime()), UpdatedAt: nowText(fixedAnnouncementTestTime())}
}

func seedPendingAnnouncementGrant(access *AccessManager, username string) {
	access.mu.Lock()
	defer access.mu.Unlock()
	access.ensureMapsLocked()
	access.state.PendingUserGrants[usernameKey(username)] = PendingAccessUserGrant{Username: username, DailyLimit: 20, CreatedAt: nowText(fixedAnnouncementTestTime()), UpdatedAt: nowText(fixedAnnouncementTestTime())}
}

func seasonalAnnouncementTestText() string {
	return "rs biļete bots ir atjaunināts un tagad darbojas labāk - lūdzu, pamēģini vēlreiz.\nБот rs biļete обновлён и теперь работает лучше - попробуй ещё раз, пожалуйста."
}

func fixedAnnouncementTestTime() time.Time {
	return time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
}

func (t *fakeTelegram) waitForMessageContaining(tst *testing.T, part string) string {
	tst.Helper()
	deadline := time.After(2 * time.Second)
	for {
		t.mu.Lock()
		for _, message := range t.messages {
			if strings.Contains(strings.ToLower(message), strings.ToLower(part)) {
				t.mu.Unlock()
				return message
			}
		}
		t.mu.Unlock()
		select {
		case <-deadline:
			tst.Fatalf("timed out waiting for message containing %q", part)
		case <-time.After(5 * time.Millisecond):
		}
	}
}
