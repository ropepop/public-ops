package broker

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBrokerRuntimeDoesNotWriteLocalProcessLogs(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	files := []string{
		fileBody(t, filepath.Join(root, "cmd", "chatgpt-broker", "main.go")),
		fileBody(t, filepath.Join(root, "cmd", "chatgpt-bot", "main.go")),
		fileBody(t, filepath.Join(root, "internal", "broker", "server.go")),
	}
	for _, source := range files {
		for _, forbidden := range []string{`"log"`, "log.Print(", "log.Printf(", "log.Fatal(", "fmt.Print("} {
			if strings.Contains(source, forbidden) {
				t.Fatalf("ChatGPT broker runtime must not write local process logs, found %q", forbidden)
			}
		}
	}
}

func TestBrokerRuntimeDoesNotExposeActiveOCRPath(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	files := map[string]string{
		"cmd/chatgpt-broker/main.go":        fileBody(t, filepath.Join(root, "cmd", "chatgpt-broker", "main.go")),
		"cmd/chatgpt-bot/main.go":           fileBody(t, filepath.Join(root, "cmd", "chatgpt-bot", "main.go")),
		"internal/config/config.go":         fileBody(t, filepath.Join(root, "internal", "config", "config.go")),
		"internal/spacetime/client.go":      fileBody(t, filepath.Join(root, "internal", "spacetime", "client.go")),
		"spacetimedb/src/lib.rs":            fileBody(t, filepath.Join(root, "spacetimedb", "src", "lib.rs")),
		"internal/broker/server.go":         fileBody(t, filepath.Join(root, "internal", "broker", "server.go")),
		"internal/broker/server_test.go":    fileBody(t, filepath.Join(root, "internal", "broker", "server_test.go")),
		"cmd/chatgpt-bot/main_test.go":      fileBody(t, filepath.Join(root, "cmd", "chatgpt-bot", "main_test.go")),
		"cmd/chatgpt-broker/main_test.go":   maybeFileBody(t, filepath.Join(root, "cmd", "chatgpt-broker", "main_test.go")),
		"internal/config/config_test.go":    maybeFileBody(t, filepath.Join(root, "internal", "config", "config_test.go")),
		"internal/spacetime/client_test.go": maybeFileBody(t, filepath.Join(root, "internal", "spacetime", "client_test.go")),
	}
	for name, source := range files {
		for _, forbidden := range []string{
			"CHATGPT_OCR",
			"CHATGPT_TESSERACT",
			"TESSERACT",
			"chatgptbroker_ocr",
			"ocr_pending",
			"OCRWork",
			"mark_screenshot_ready",
			"screenshotPngBase64",
			"internal/ocr",
			"CHATGPT_RUNNER_ENABLED",
		} {
			if strings.Contains(source, forbidden) {
				t.Fatalf("%s must not contain active OCR/runtime fallback token %q", name, forbidden)
			}
		}
	}
}

func TestTelegramBotKeepsFreshChatQueueSilent(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	source := fileBody(t, filepath.Join(root, "cmd", "chatgpt-bot", "main.go"))

	for _, required := range []string{
		"CHATGPT_BROKER_CONTROL new=1;files=0",
		"Working on Pixel...",
		"Commands: /status, /cancel, /health, /privacy.",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("bot source missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"threadStore",
		"thread=",
		"Queued job",
		"Thread:",
		"Commands: /status, /cancel, /new, /thread",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("bot source must not contain old queue/thread behavior %q", forbidden)
		}
	}
}

func fileBody(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

func maybeFileBody(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}
