package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"telegramtrainapp/internal/scrape"
)

func main() {
	dateFlag := flag.String("date", "", "service date in YYYY-MM-DD (default: today in Europe/Riga)")
	outDirFlag := flag.String("out-dir", envOr("SCRAPER_OUTPUT_DIR", "./data/schedules"), "optional output directory for debug snapshot files")
	minTrainsFlag := flag.Int("min-trains", envOrInt("SCRAPER_MIN_TRAINS", 1), "minimum merged trains required for success")
	timeoutFlag := flag.Int("timeout-sec", 20, "HTTP timeout seconds per provider")
	viviPageURLFlag := flag.String("vivi-page-url", envOr("SCRAPER_VIVI_PAGE_URL", "https://www.vivi.lv/lv/informacija-pasazieriem/"), "Vivi passenger info page URL")
	viviGTFSURLFlag := flag.String("vivi-gtfs-url", envOr("SCRAPER_VIVI_GTFS_URL", "https://www.vivi.lv/uploads/GTFS.zip"), "Vivi GTFS zip URL")
	flag.Parse()

	loc, err := time.LoadLocation(envOr("TZ", "Europe/Riga"))
	if err != nil {
		log.Fatalf("invalid TZ: %v", err)
	}
	serviceDate, err := parseDate(*dateFlag, loc)
	if err != nil {
		log.Fatalf("invalid date: %v", err)
	}

	viviPageURL := strings.TrimSpace(*viviPageURLFlag)
	viviGTFSURL := strings.TrimSpace(*viviGTFSURLFlag)
	if viviPageURL == "" {
		log.Fatalf("SCRAPER_VIVI_PAGE_URL is required")
	}
	if viviGTFSURL == "" {
		log.Fatalf("SCRAPER_VIVI_GTFS_URL is required")
	}
	ua := envOr("SCRAPER_USER_AGENT", "telegram-train-bot-scraper/1.0")
	timeout := time.Duration(*timeoutFlag) * time.Second

	providers := []scrape.Provider{
		scrape.NewViviGTFSProvider("vivi_gtfs", viviGTFSURL, ua, timeout),
		scrape.NewViviPDFProvider("vivi_pdf", viviPageURL, ua, timeout),
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	orchestrator := scrape.NewOrchestrator(providers, *outDirFlag, *minTrainsFlag)
	result, err := orchestrator.Run(ctx, serviceDate)
	if err != nil {
		log.Fatalf("scrape run failed: %v", err)
	}
	metrics := map[string]any{
		"service_date": serviceDate.Format("2006-01-02"),
		"output_path":  result.OutputPath,
		"stats":        result.Stats,
	}
	b, _ := json.MarshalIndent(metrics, "", "  ")
	fmt.Println(string(b))
}

func parseDate(raw string, loc *time.Location) (time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		now := time.Now().In(loc)
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc), nil
	}
	t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(raw), loc)
	if err != nil {
		return time.Time{}, err
	}
	return t, nil
}

func envOr(key string, fallback string) string {
	if v := os.Getenv(key); strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

func envOrInt(key string, fallback int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			return parsed
		}
	}
	return fallback
}
