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
	if !telegram.hasMessageContaining("waiting") {
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
	if !telegram.hasMessageContaining("5 digits") {
		t.Fatalf("invalid input guidance missing: %#v", telegram.messages)
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
	if !telegram.hasMessageContaining("cancelled") {
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
	mu       sync.Mutex
	messages []string
	photos   []sentPhoto
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
	t.messages = append(t.messages, text)
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
