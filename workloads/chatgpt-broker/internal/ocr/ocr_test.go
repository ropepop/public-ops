package ocr

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestExtractChatGPTAnswerFromEnvImage(t *testing.T) {
	path := os.Getenv("CHATGPT_OCR_TEST_IMAGE")
	if path == "" {
		t.Skip("CHATGPT_OCR_TEST_IMAGE is not set")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	text, err := (Extractor{TesseractPath: os.Getenv("CHATGPT_TESSERACT_PATH")}).ExtractChatGPTAnswer(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(text) == "" {
		t.Fatal("empty OCR text")
	}
	t.Logf("ocr text: %q", text)
}
