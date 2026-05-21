//go:build live_smoke

package bot

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type liveSmokePhoto struct {
	chatID  int64
	image   []byte
	mime    string
	caption string
}

type liveSmokeTelegram struct {
	mu       sync.Mutex
	messages []string
	photos   chan liveSmokePhoto
	failures chan string
}

func newLiveSmokeTelegram() *liveSmokeTelegram {
	return &liveSmokeTelegram{photos: make(chan liveSmokePhoto, 1), failures: make(chan string, 1)}
}

func (t *liveSmokeTelegram) SendMessage(ctx context.Context, chatID int64, text string) error {
	t.mu.Lock()
	t.messages = append(t.messages, text)
	t.mu.Unlock()
	if strings.Contains(strings.ToLower(text), "failed") || strings.Contains(strings.ToLower(text), "expired") {
		select {
		case t.failures <- text:
		default:
		}
	}
	return nil
}

func (t *liveSmokeTelegram) SendPhoto(ctx context.Context, chatID int64, image []byte, mime string, caption string) error {
	photo := liveSmokePhoto{chatID: chatID, image: append([]byte(nil), image...), mime: mime, caption: caption}
	select {
	case t.photos <- photo:
	default:
	}
	return nil
}

func TestLiveBrokerTelegramDelivery(t *testing.T) {
	brokerURL := strings.TrimRight(strings.TrimSpace(os.Getenv("RS_QR_LIVE_BROKER_URL")), "/")
	if brokerURL == "" {
		t.Fatal("RS_QR_LIVE_BROKER_URL is required")
	}
	codePath := strings.TrimSpace(os.Getenv("RS_QR_LIVE_CODE_FILE"))
	if codePath == "" {
		t.Fatal("RS_QR_LIVE_CODE_FILE is required")
	}
	rawCode, err := os.ReadFile(codePath)
	if err != nil {
		t.Fatalf("read code file: %v", err)
	}
	code := strings.TrimSpace(string(rawCode))
	if !fiveDigitCodePattern.MatchString(code) {
		t.Fatal("code file must contain exactly five digits")
	}
	artifactPath := strings.TrimSpace(os.Getenv("RS_QR_LIVE_ARTIFACT"))
	if artifactPath == "" {
		artifactPath = filepath.Join(t.TempDir(), "rs-live-smoke.png")
	}

	telegram := newLiveSmokeTelegram()
	broker := NewHTTPBroker(brokerURL, 30*time.Second)
	service := NewService(ServiceConfig{PollInterval: 250 * time.Millisecond, PollTimeout: 4 * time.Minute}, telegram, broker)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := service.HandleMessage(ctx, Message{ChatID: -42424242, ChatType: "private", UserID: 900000001, Username: "live_smoke", Text: "/qr " + code}); err != nil {
		t.Fatalf("handle message: %v", err)
	}

	select {
	case photo := <-telegram.photos:
		if !strings.Contains(strings.ToLower(photo.mime), "png") {
			t.Fatalf("unexpected photo mime %q", photo.mime)
		}
		if len(photo.image) < 8 || !bytes.Equal(photo.image[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
			t.Fatalf("photo is not a PNG, bytes=%d", len(photo.image))
		}
		if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
			t.Fatalf("mkdir artifact: %v", err)
		}
		if err := os.WriteFile(artifactPath, photo.image, 0o600); err != nil {
			t.Fatalf("write artifact: %v", err)
		}
		t.Logf("live smoke delivered PNG bytes=%d artifact=%s", len(photo.image), artifactPath)
	case failure := <-telegram.failures:
		t.Fatalf("fake Telegram received failure message: %s", failure)
	case <-time.After(4 * time.Minute):
		t.Fatal("timed out waiting for fake Telegram SendPhoto")
	}
}
