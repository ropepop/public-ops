package main

import (
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExpandSDRToLinearHDRPreservesDarkAndExpandsWhite(t *testing.T) {
	sdr := []byte{
		0, 0, 0, 255,
		128, 128, 128, 255,
		255, 255, 255, 255,
	}
	hdr := expandSDRToLinearHDR(sdr)
	if len(hdr) != len(sdr)*2 {
		t.Fatalf("HDR bytes = %d, want %d", len(hdr), len(sdr)*2)
	}
	if got := binary.LittleEndian.Uint16(hdr[0:2]); got != 0 {
		t.Fatalf("black red half = %#x", got)
	}
	if got := binary.LittleEndian.Uint16(hdr[6:8]); got != 0x3c00 {
		t.Fatalf("alpha half = %#x, want 1.0", got)
	}
	whiteRed := binary.LittleEndian.Uint16(hdr[16:18])
	if whiteRed < 0x43f0 || whiteRed > 0x4401 {
		t.Fatalf("white HDR boost half = %#x, want approximately 4.0", whiteRed)
	}
	midRed := binary.LittleEndian.Uint16(hdr[8:10])
	if midRed >= whiteRed {
		t.Fatalf("mid-tone half %#x must remain below white %#x", midRed, whiteRed)
	}
}

func TestRealTransformerProducesProbedGainMapWithoutResidualPixels(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is unavailable")
	}
	if _, err := exec.LookPath("ultrahdr_app"); err != nil {
		t.Skip("ultrahdr_app is unavailable")
	}
	root := t.TempDir()
	inputPath := filepath.Join(root, "synthetic.h264")
	generate := exec.Command("ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc2=size=160x240:rate=1", "-frames:v", "1",
		"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
		"-profile:v", "baseline", "-pix_fmt", "yuv420p", "-f", "h264", inputPath)
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate sanitized H.264 keyframe: %v: %s", err, output)
	}
	input, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "tmpfs-shaped-workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	service := &transformer{
		ffmpegBin:   "ffmpeg",
		ultrahdrBin: "ultrahdr_app",
		tempRoot:    workspace,
		timeout:     3 * time.Second,
		semaphore:   make(chan struct{}, 1),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := time.Now()
	encoded, err := service.transform(ctx, input, 160, 240)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) == 0 || time.Since(started) > 3*time.Second {
		t.Fatalf("transform output=%d duration=%s", len(encoded), time.Since(started))
	}
	outputPath := filepath.Join(root, "synthetic-gainmap.jpg")
	if err := os.WriteFile(outputPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	probe := exec.Command("ultrahdr_app", "-m", "1", "-P", "-j", outputPath)
	probeOutput, err := probe.CombinedOutput()
	if err != nil || !bytesContainFold(probeOutput, "gainmap") || !bytesContainFold(probeOutput, "hdrcapacitymax 4") {
		t.Fatalf("gain-map probe failed: %v: %s", err, probeOutput)
	}
	entries, err := os.ReadDir(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("request workspace retained %d pixel artifacts", len(entries))
	}
}

func TestFloat32ToHalfKnownValues(t *testing.T) {
	for value, want := range map[float32]uint16{
		0: 0x0000,
		1: 0x3c00,
		2: 0x4000,
		4: 0x4400,
	} {
		if got := float32ToHalf(value); got != want {
			t.Fatalf("half(%v) = %#x, want %#x", value, got, want)
		}
	}
}

func TestTransformEndpointRejectsUnboundedOrNonH264Input(t *testing.T) {
	service := &transformer{timeout: time.Second, semaphore: make(chan struct{}, 1)}
	for _, test := range []struct {
		name        string
		contentType string
		width       string
		height      string
		want        int
	}{
		{name: "wrong content", contentType: "image/png", width: "540", height: "1212", want: http.StatusBadRequest},
		{name: "too wide", contentType: "video/h264", width: "721", height: "1212", want: http.StatusBadRequest},
		{name: "too many pixels", contentType: "video/h264", width: "720", height: "1800", want: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/transform", strings.NewReader("not pixels"))
			req.Header.Set("Content-Type", test.contentType)
			req.Header.Set("X-Ticket-Width", test.width)
			req.Header.Set("X-Ticket-Height", test.height)
			rec := httptest.NewRecorder()
			service.handleTransform(rec, req)
			if rec.Code != test.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, test.want, rec.Body.String())
			}
		})
	}
}

func TestTransformEndpointHasExactlyOneRunningSlotAndNoWaitingBacklog(t *testing.T) {
	service := &transformer{timeout: time.Second, semaphore: make(chan struct{}, 1)}
	service.semaphore <- struct{}{}
	req := httptest.NewRequest(http.MethodPost, "/v1/transform", strings.NewReader("keyframe"))
	req.Header.Set("Content-Type", "video/h264")
	req.Header.Set("X-Ticket-Width", "540")
	req.Header.Set("X-Ticket-Height", "1212")
	rec := httptest.NewRecorder()
	service.handleTransform(rec, req)
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), "transformer_busy") {
		t.Fatalf("busy response = %d %s", rec.Code, rec.Body.String())
	}
}
