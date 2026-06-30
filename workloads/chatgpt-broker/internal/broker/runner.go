package broker

import (
	"context"
	"encoding/base64"
	"log"
	"time"

	"chatgptbroker/internal/ocr"
	"chatgptbroker/internal/spacetime"
)

type RunnerConfig struct {
	Enabled      bool
	PollInterval time.Duration
	OCR          ocr.Extractor
}

type OCRQueue interface {
	OCRWork(ctx context.Context) ([]spacetime.OCRWork, error)
	MarkSucceeded(ctx context.Context, jobID, attemptID, resultText string) error
	MarkFailed(ctx context.Context, jobID, attemptID, failureCode string, retryable bool, publicStatus string) error
}

type Runner struct {
	queue OCRQueue
	cfg   RunnerConfig
}

func NewRunner(queue OCRQueue, cfg RunnerConfig) *Runner {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 3 * time.Second
	}
	return &Runner{queue: queue, cfg: cfg}
}

func (r *Runner) Run(ctx context.Context) {
	if !r.cfg.Enabled {
		log.Print("chatgpt OCR worker disabled")
		return
	}
	ticker := time.NewTicker(r.cfg.PollInterval)
	defer ticker.Stop()
	for {
		r.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Runner) tick(ctx context.Context) {
	if r.queue == nil {
		return
	}
	work, err := r.queue.OCRWork(ctx)
	if err != nil {
		log.Printf("chatgpt OCR work poll: %v", err)
		return
	}
	for _, item := range work {
		if ctx.Err() != nil {
			return
		}
		r.process(ctx, item)
	}
}

func (r *Runner) process(ctx context.Context, item spacetime.OCRWork) {
	png, err := base64.StdEncoding.DecodeString(item.ScreenshotPNGBase64)
	if err != nil {
		_ = r.queue.MarkFailed(ctx, item.JobID, item.AttemptID, "ocr_screenshot_decode_failed", true, "Could not read Pixel screenshot")
		return
	}
	text, err := r.cfg.OCR.ExtractChatGPTAnswer(ctx, png)
	if err != nil {
		_ = r.queue.MarkFailed(ctx, item.JobID, item.AttemptID, "ocr_failed", true, "Could not read ChatGPT response")
		return
	}
	if text == "" {
		_ = r.queue.MarkFailed(ctx, item.JobID, item.AttemptID, "ocr_empty", true, "Could not read ChatGPT response")
		return
	}
	if err := r.queue.MarkSucceeded(ctx, item.JobID, item.AttemptID, text); err != nil {
		log.Printf("chatgpt mark succeeded: %v", err)
	}
}
