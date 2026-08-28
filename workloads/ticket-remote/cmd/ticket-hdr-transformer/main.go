package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	transformerVersion = "ticket-hdr-transformer-iso-gainmap-v1"
	maxInputBytes      = 2 * 1024 * 1024
	maxOutputBytes     = 2 * 1024 * 1024
	maxWidth           = 720
	maxHeight          = 1800
	maxPixels          = 1_250_000
	linearIntentBoost  = float32(4.0)
)

type transformer struct {
	ffmpegBin   string
	ultrahdrBin string
	tempRoot    string
	timeout     time.Duration
	semaphore   chan struct{}

	metricsMu sync.Mutex
	metrics   transformMetrics
}

type transformMetrics struct {
	accepted             uint64
	completed            uint64
	busyRejected         uint64
	invalidRejected      uint64
	failures             uint64
	timeouts             uint64
	lastConversionMillis int64
	maxConversionMillis  int64
	lastOutputBytes      int
}

func main() {
	port := envInt("TICKET_HDR_TRANSFORMER_PORT", 9352)
	timeout := envDuration("TICKET_HDR_TRANSFORM_TIMEOUT", 1200*time.Millisecond)
	service := &transformer{
		ffmpegBin:   envString("TICKET_HDR_FFMPEG_BIN", "ffmpeg"),
		ultrahdrBin: envString("TICKET_HDR_ULTRAHDR_BIN", "ultrahdr_app"),
		tempRoot:    envString("TICKET_HDR_TMP_ROOT", "/tmp/ticket-hdr-transformer"),
		timeout:     timeout,
		semaphore:   make(chan struct{}, 1),
	}
	if timeout < 100*time.Millisecond || timeout > 5*time.Second {
		log.Fatal("TICKET_HDR_TRANSFORM_TIMEOUT must be between 100ms and 5s")
	}
	if err := os.MkdirAll(service.tempRoot, 0o700); err != nil {
		log.Fatalf("prepare in-memory workspace: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/livez", service.handleLive)
	mux.HandleFunc("/healthz", service.handleHealth)
	mux.HandleFunc("/v1/transform", service.handleTransform)
	server := &http.Server{
		Addr:              net.JoinHostPort(envString("TICKET_HDR_TRANSFORMER_BIND_ADDR", "0.0.0.0"), strconv.Itoa(port)),
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       timeout + time.Second,
		WriteTimeout:      timeout + time.Second,
		IdleTimeout:       10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() {
		log.Printf("ticket HDR transformer listening on %s", server.Addr)
		errCh <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("serve: %v", err)
		}
	}
}

func (t *transformer) handleLive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": transformerVersion})
}

func (t *transformer) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	t.metricsMu.Lock()
	metrics := t.metrics
	t.metricsMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                       true,
		"version":                  transformerVersion,
		"queueDepth":               len(t.semaphore),
		"queueCapacity":            cap(t.semaphore),
		"accepted":                 metrics.accepted,
		"completed":                metrics.completed,
		"busyRejected":             metrics.busyRejected,
		"invalidRejected":          metrics.invalidRejected,
		"failures":                 metrics.failures,
		"timeouts":                 metrics.timeouts,
		"lastConversionMillis":     metrics.lastConversionMillis,
		"maxConversionMillis":      metrics.maxConversionMillis,
		"lastOutputBytes":          metrics.lastOutputBytes,
		"maxInputBytes":            maxInputBytes,
		"maxOutputBytes":           maxOutputBytes,
		"maxConcurrentConversions": cap(t.semaphore),
		"storage":                  "request-scoped-tmpfs",
		"output":                   "jpeg-iso-21496-gainmap",
		"targetDisplayBoost":       linearIntentBoost,
	})
}

func (t *transformer) handleTransform(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	select {
	case t.semaphore <- struct{}{}:
		defer func() { <-t.semaphore }()
	default:
		t.recordBusy()
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"ok": false, "error": "transformer_busy"})
		return
	}
	width, widthErr := boundedDimension(r.Header.Get("X-Ticket-Width"), maxWidth)
	height, heightErr := boundedDimension(r.Header.Get("X-Ticket-Height"), maxHeight)
	if widthErr != nil || heightErr != nil || width*height > maxPixels || r.Header.Get("Content-Type") != "video/h264" {
		t.recordInvalid()
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid_transform_request"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxInputBytes+1))
	if err != nil || len(body) == 0 || len(body) > maxInputBytes {
		t.recordInvalid()
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"ok": false, "error": "invalid_transform_input"})
		return
	}
	t.metricsMu.Lock()
	t.metrics.accepted++
	t.metricsMu.Unlock()
	started := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), t.timeout)
	defer cancel()
	output, err := t.transform(ctx, body, width, height)
	duration := time.Since(started)
	if err != nil {
		t.recordFailure(err, duration)
		log.Printf("HDR transform failed duration_ms=%d input_bytes=%d dimensions=%dx%d reason=%s", duration.Milliseconds(), len(body), width, height, safeReason(err))
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "transform_failed"})
		return
	}
	t.recordCompleted(duration, len(output))
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-HDR-Format", "jpeg-iso-21496-gainmap")
	w.Header().Set("X-HDR-Target-Display-Boost", "4")
	w.Header().Set("X-Conversion-Millis", strconv.FormatInt(duration.Milliseconds(), 10))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(output)
}

func (t *transformer) transform(ctx context.Context, input []byte, width int, height int) ([]byte, error) {
	workDir, err := os.MkdirTemp(t.tempRoot, "request-")
	if err != nil {
		return nil, errors.New("create request workspace")
	}
	defer os.RemoveAll(workDir)
	inputPath := filepath.Join(workDir, "source.h264")
	sdrPath := filepath.Join(workDir, "sdr.rgba")
	hdrPath := filepath.Join(workDir, "hdr-linear.rgba16f")
	outputPath := filepath.Join(workDir, "gainmap.jpg")
	if err := os.WriteFile(inputPath, input, 0o600); err != nil {
		return nil, errors.New("stage source frame")
	}
	ffmpeg := exec.CommandContext(ctx, t.ffmpegBin,
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-f", "h264", "-i", inputPath,
		"-frames:v", "1", "-vf", fmt.Sprintf("scale=%d:%d:flags=fast_bilinear", width, height),
		"-pix_fmt", "rgba", "-f", "rawvideo", sdrPath,
	)
	if output, err := ffmpeg.CombinedOutput(); err != nil {
		return nil, commandError("decode source keyframe", err, output)
	}
	sdr, err := os.ReadFile(sdrPath)
	if err != nil || len(sdr) != width*height*4 {
		return nil, errors.New("decoded source dimensions mismatch")
	}
	hdr := expandSDRToLinearHDR(sdr)
	if len(hdr) != width*height*8 {
		return nil, errors.New("HDR intent dimensions mismatch")
	}
	if err := os.WriteFile(hdrPath, hdr, 0o600); err != nil {
		return nil, errors.New("stage HDR intent")
	}
	ultrahdr := exec.CommandContext(ctx, t.ultrahdrBin,
		"-m", "0", "-p", hdrPath, "-y", sdrPath,
		"-w", strconv.Itoa(width), "-h", strconv.Itoa(height),
		"-a", "4", "-b", "3", "-C", "0", "-c", "0", "-t", "0", "-R", "1",
		"-s", "4", "-M", "0", "-Q", "88", "-q", "94", "-D", "0",
		"-k", "1", "-K", "4", "-L", "812", "-z", outputPath,
	)
	if output, err := ultrahdr.CombinedOutput(); err != nil {
		return nil, commandError("encode gain map", err, output)
	}
	probe := exec.CommandContext(ctx, t.ultrahdrBin, "-m", "1", "-j", outputPath, "-P")
	probeOutput, err := probe.CombinedOutput()
	if err != nil || !bytesContainFold(probeOutput, "gainmap") || !bytesContainFold(probeOutput, "hdrcapacitymax 4") {
		return nil, commandError("probe gain map", err, probeOutput)
	}
	encoded, err := os.ReadFile(outputPath)
	if err != nil || len(encoded) < 4 || len(encoded) > maxOutputBytes || encoded[0] != 0xff || encoded[1] != 0xd8 || encoded[len(encoded)-2] != 0xff || encoded[len(encoded)-1] != 0xd9 {
		return nil, errors.New("encoded gain map is invalid")
	}
	if !strings.Contains(string(encoded), "urn:iso:std:iso:ts:21496:-1") {
		return nil, errors.New("encoded gain map is missing ISO 21496-1 signaling")
	}
	return encoded, nil
}

func expandSDRToLinearHDR(sdr []byte) []byte {
	hdr := make([]byte, len(sdr)*2)
	for source, target := 0, 0; source+3 < len(sdr); source, target = source+4, target+8 {
		r := srgbToLinear(float32(sdr[source]) / 255)
		g := srgbToLinear(float32(sdr[source+1]) / 255)
		b := srgbToLinear(float32(sdr[source+2]) / 255)
		// Keep the SDR and HDR intents in the same BT.709 primaries; EDR does
		// not require a wider gamut. The 4x scale is the intended linear-light
		// remap and the encoder is told that the target display has 4x capacity.
		// The gain-map encoder may still report larger ratios for near-black
		// samples after the lossy SDR base is quantized, so do not describe its
		// derived maxContentBoost metadata as an exact 4x content bound.
		boost := linearIntentBoost
		values := [4]float32{
			clamp(r*boost, 0, linearIntentBoost),
			clamp(g*boost, 0, linearIntentBoost),
			clamp(b*boost, 0, linearIntentBoost),
			1,
		}
		for channel, value := range values {
			binary.LittleEndian.PutUint16(hdr[target+channel*2:], float32ToHalf(value))
		}
	}
	return hdr
}

func srgbToLinear(value float32) float32 {
	if value <= 0.04045 {
		return value / 12.92
	}
	return float32(math.Pow(float64((value+0.055)/1.055), 2.4))
}

func float32ToHalf(value float32) uint16 {
	bits := math.Float32bits(value)
	sign := uint16((bits >> 16) & 0x8000)
	exponent := int((bits >> 23) & 0xff)
	mantissa := bits & 0x7fffff
	if exponent == 0xff {
		if mantissa != 0 {
			return sign | 0x7e00
		}
		return sign | 0x7c00
	}
	halfExponent := exponent - 127 + 15
	if halfExponent >= 31 {
		return sign | 0x7c00
	}
	if halfExponent <= 0 {
		if halfExponent < -10 {
			return sign
		}
		mantissa |= 0x800000
		shift := uint32(14 - halfExponent)
		rounded := (mantissa + (1 << (shift - 1))) >> shift
		return sign | uint16(rounded)
	}
	roundedMantissa := mantissa + 0x1000
	if roundedMantissa&0x800000 != 0 {
		roundedMantissa = 0
		halfExponent++
		if halfExponent >= 31 {
			return sign | 0x7c00
		}
	}
	return sign | uint16(halfExponent<<10) | uint16(roundedMantissa>>13)
}

func boundedDimension(value string, maximum int) (int, error) {
	dimension, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || dimension <= 0 || dimension > maximum {
		return 0, errors.New("dimension out of range")
	}
	return dimension, nil
}

func (t *transformer) recordBusy() {
	t.metricsMu.Lock()
	t.metrics.busyRejected++
	t.metricsMu.Unlock()
}

func (t *transformer) recordInvalid() {
	t.metricsMu.Lock()
	t.metrics.invalidRejected++
	t.metricsMu.Unlock()
}

func (t *transformer) recordFailure(err error, duration time.Duration) {
	t.metricsMu.Lock()
	t.metrics.failures++
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "deadline") || strings.Contains(strings.ToLower(err.Error()), "signal: killed") {
		t.metrics.timeouts++
	}
	t.metrics.lastConversionMillis = duration.Milliseconds()
	if duration.Milliseconds() > t.metrics.maxConversionMillis {
		t.metrics.maxConversionMillis = duration.Milliseconds()
	}
	t.metricsMu.Unlock()
}

func (t *transformer) recordCompleted(duration time.Duration, outputBytes int) {
	t.metricsMu.Lock()
	t.metrics.completed++
	t.metrics.lastConversionMillis = duration.Milliseconds()
	if duration.Milliseconds() > t.metrics.maxConversionMillis {
		t.metrics.maxConversionMillis = duration.Milliseconds()
	}
	t.metrics.lastOutputBytes = outputBytes
	t.metricsMu.Unlock()
}

func commandError(phase string, err error, output []byte) error {
	detail := strings.Join(strings.Fields(string(output)), " ")
	if len(detail) > 180 {
		detail = detail[:180]
	}
	if err == nil {
		return fmt.Errorf("%s: validation failed: %s", phase, detail)
	}
	return fmt.Errorf("%s: %w: %s", phase, err, detail)
}

func bytesContainFold(value []byte, marker string) bool {
	return strings.Contains(strings.ToLower(string(value)), strings.ToLower(marker))
}

func safeReason(err error) string {
	if err == nil {
		return "unknown"
	}
	reason := strings.Join(strings.Fields(err.Error()), " ")
	if len(reason) > 200 {
		reason = reason[:200]
	}
	return reason
}

func clamp01(value float32) float32 { return clamp(value, 0, 1) }

func clamp(value float32, minimum float32, maximum float32) float32 {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func envString(name string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func envDuration(name string, fallback time.Duration) time.Duration {
	value, err := time.ParseDuration(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
