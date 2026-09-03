package scrape

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestViviCollectPDFLinks(t *testing.T) {
	serviceDate := time.Date(2026, 2, 26, 0, 0, 0, 0, time.FixedZone("EET", 2*3600))
	html := `
<table>
  <tr>
    <td><a href="/uploads/Saraksti/base-a.pdf">➡️ Base A</a></td>
    <td><a href="/uploads/Izmainas/change-old.pdf">No 20. februāra | Izmaiņas</a></td>
  </tr>
  <tr>
    <td><a href="/uploads/Saraksti/base-b.pdf">➡️ Base B</a></td>
    <td><a href="/uploads/Izmainas/change-future.pdf">No 28. februāra | Izmaiņas</a></td>
  </tr>
</table>`
	base, changes, err := viviCollectPDFLinks("https://www.vivi.lv/lv/informacija-pasazieriem/", html, serviceDate)
	if err != nil {
		t.Fatalf("collect links: %v", err)
	}
	if len(base) != 2 {
		t.Fatalf("expected 2 base links, got %d", len(base))
	}
	if len(changes) != 1 {
		t.Fatalf("expected 1 active change link, got %d", len(changes))
	}
}

func TestViviCollectPDFLinksRejectsUnsafeTargetsAndExcessLinks(t *testing.T) {
	serviceDate := time.Date(2026, 2, 26, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		page string
		html string
	}{
		{name: "insecure origin", page: "http://www.vivi.lv/lv/", html: `<a href="/uploads/Saraksti/base.pdf">Base</a>`},
		{name: "cross origin", page: "https://www.vivi.lv/lv/", html: `<a href="https://example.com/uploads/Saraksti/base.pdf">Base</a>`},
		{name: "downgrade", page: "https://www.vivi.lv/lv/", html: `<a href="http://www.vivi.lv/uploads/Saraksti/base.pdf">Base</a>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := viviCollectPDFLinks(tc.page, tc.html, serviceDate); err == nil {
				t.Fatalf("expected unsafe PDF target to be rejected")
			}
		})
	}

	var links strings.Builder
	for idx := 0; idx <= viviPDFMaxLinks; idx++ {
		fmt.Fprintf(&links, `<a href="/uploads/Saraksti/base-%02d.pdf">Base</a>`, idx)
	}
	if _, _, err := viviCollectPDFLinks("https://www.vivi.lv/lv/", links.String(), serviceDate); err == nil || !strings.Contains(err.Error(), "more than 32") {
		t.Fatalf("link cap error = %v", err)
	}
}

func TestViviIngressBoundsLandingAndRequiresSafeRedirects(t *testing.T) {
	if viviPDFMaxInputBytes >= 16<<20 {
		t.Fatalf("PDF input limit %d leaves no headroom in the 16 MiB runtime tmpfs", viviPDFMaxInputBytes)
	}
	if _, err := viviReadBounded(bytes.NewReader(nil), viviLandingMaxBytes+1, viviLandingMaxBytes, "landing page"); err == nil {
		t.Fatalf("expected declared oversized landing page to fail")
	}
	if _, err := viviReadBounded(bytes.NewReader(bytes.Repeat([]byte{'x'}, viviLandingMaxBytes+1)), -1, viviLandingMaxBytes, "landing page"); err == nil {
		t.Fatalf("expected streamed oversized landing page to fail")
	}

	origin, err := url.Parse("https://www.vivi.lv/lv/")
	if err != nil {
		t.Fatalf("parse origin: %v", err)
	}
	client := viviSafeHTTPClient(time.Second, origin, staticViviResolver{
		"www.vivi.lv": {{IP: net.ParseIP("93.184.216.34")}},
	})
	for _, target := range []string{
		"http://www.vivi.lv/uploads/Saraksti/base.pdf",
		"https://example.com/uploads/Saraksti/base.pdf",
		"https://www.vivi.lv:444/uploads/Saraksti/base.pdf",
	} {
		req, reqErr := http.NewRequest(http.MethodGet, target, nil)
		if reqErr != nil {
			t.Fatalf("build redirect request: %v", reqErr)
		}
		if err := client.CheckRedirect(req, nil); err == nil {
			t.Fatalf("unsafe redirect %q was accepted", target)
		}
	}
	safeReq, err := http.NewRequest(http.MethodGet, "https://www.vivi.lv/uploads/Saraksti/base.pdf", nil)
	if err != nil {
		t.Fatalf("build safe redirect request: %v", err)
	}
	if err := client.CheckRedirect(safeReq, nil); err != nil {
		t.Fatalf("safe same-origin redirect failed: %v", err)
	}
}

func TestViviResolutionRejectsPrivateAndMixedAddresses(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "::1", "fe80::1", "100.64.0.1", "192.0.2.1"} {
		if _, err := viviResolvePublicAddresses(context.Background(), staticViviResolver{}, host); err == nil {
			t.Fatalf("non-public address %s was accepted", host)
		}
	}
	resolver := staticViviResolver{
		"safe.example":  {{IP: net.ParseIP("93.184.216.34")}},
		"mixed.example": {{IP: net.ParseIP("93.184.216.34")}, {IP: net.ParseIP("127.0.0.1")}},
	}
	addresses, err := viviResolvePublicAddresses(context.Background(), resolver, "safe.example")
	if err != nil || len(addresses) != 1 {
		t.Fatalf("public resolution = %v, %v", addresses, err)
	}
	if _, err := viviResolvePublicAddresses(context.Background(), resolver, "mixed.example"); err == nil {
		t.Fatalf("mixed public/private resolution was accepted")
	}
}

func TestViviParseEffectiveDate(t *testing.T) {
	serviceDate := time.Date(2026, 2, 26, 0, 0, 0, 0, time.UTC)
	got, ok := viviParseEffectiveDate("No 28. februāra | Izmaiņas", time.UTC, serviceDate)
	if !ok {
		t.Fatalf("expected date parse success")
	}
	if got.Format("2006-01-02") != "2026-02-28" {
		t.Fatalf("expected 2026-02-28, got %s", got.Format("2006-01-02"))
	}
}

func TestViviParseScheduleLines(t *testing.T) {
	serviceDate := time.Date(2026, 2, 26, 0, 0, 0, 0, time.UTC)
	trains, err := viviParseScheduleLines([]string{
		"Pasažieru vilciena nr. 1001 1002",
		"Rīga 10.00 11.00",
		"Jelgava 10.45 11.45",
	}, serviceDate)
	if err != nil {
		t.Fatalf("parse lines: %v", err)
	}
	if len(trains) != 2 {
		t.Fatalf("train count = %d, want 2", len(trains))
	}
	if got := trains[0].TrainNumber; got != "1001" {
		t.Fatalf("first train number = %q", got)
	}
	if got := trains[0].FromStation; got != "Rīga" {
		t.Fatalf("first train origin = %q", got)
	}
	if got := trains[1].ArrivalAt.Format(time.RFC3339); got != "2026-02-26T11:45:00Z" {
		t.Fatalf("second train arrival = %q", got)
	}
}

func TestViviExtractPDFLinesUsesBoundedChildProcess(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		command := writePDFTextTestCommand(t, `printf 'Vilciena nr 1001 1002\nRīga 10.00 11.00\nJelgava 10.45 11.45\n'`)
		lines, err := viviExtractPDFLinesWithCommand(context.Background(), []byte("%PDF-test"), command, time.Second, 1024, 1024, 1024)
		if err != nil {
			t.Fatalf("extract: %v", err)
		}
		if got := strings.Join(lines, "|"); !strings.Contains(got, "Rīga 10.00 11.00") {
			t.Fatalf("unexpected extracted lines: %q", got)
		}
	})

	t.Run("input limit", func(t *testing.T) {
		command := writePDFTextTestCommand(t, `exit 0`)
		_, err := viviExtractPDFLinesWithCommand(context.Background(), []byte("oversized"), command, time.Second, 4, 1024, 1024)
		if err == nil || !strings.Contains(err.Error(), "pdf exceeds 4 byte limit") {
			t.Fatalf("error = %v, want input limit", err)
		}
	})

	t.Run("output limit", func(t *testing.T) {
		command := writePDFTextTestCommand(t, `while :; do printf '0123456789abcdef'; done`)
		_, err := viviExtractPDFLinesWithCommand(context.Background(), []byte("%PDF-test"), command, time.Second, 1024, 128, 1024)
		if err == nil || !strings.Contains(err.Error(), "output exceeds 128 byte limit") {
			t.Fatalf("error = %v, want output limit", err)
		}
	})

	t.Run("diagnostic limit", func(t *testing.T) {
		command := writePDFTextTestCommand(t, `while :; do printf 'diagnostic-output' >&2; done`)
		_, err := viviExtractPDFLinesWithCommand(context.Background(), []byte("%PDF-test"), command, time.Second, 1024, 1024, 128)
		if err == nil || !strings.Contains(err.Error(), "diagnostics exceed 128 byte limit") {
			t.Fatalf("error = %v, want diagnostics limit", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		command := writePDFTextTestCommand(t, `sleep 10`)
		started := time.Now()
		_, err := viviExtractPDFLinesWithCommand(context.Background(), []byte("%PDF-test"), command, 50*time.Millisecond, 1024, 1024, 1024)
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("error = %v, want timeout", err)
		}
		if elapsed := time.Since(started); elapsed > 2*time.Second {
			t.Fatalf("timed-out child took %s to reap", elapsed)
		}
	})

	t.Run("crash", func(t *testing.T) {
		command := writePDFTextTestCommand(t, `kill -SEGV $$`)
		_, err := viviExtractPDFLinesWithCommand(context.Background(), []byte("%PDF-test"), command, time.Second, 1024, 1024, 1024)
		if err == nil || !strings.Contains(err.Error(), "pdftotext failed") {
			t.Fatalf("error = %v, want child failure", err)
		}
	})

	t.Run("malformed pdf diagnostic", func(t *testing.T) {
		command := writePDFTextTestCommand(t, `printf 'Syntax Error: malformed PDF' >&2; exit 1`)
		_, err := viviExtractPDFLinesWithCommand(context.Background(), []byte("not-a-pdf"), command, time.Second, 1024, 1024, 1024)
		if err == nil || !strings.Contains(err.Error(), "malformed PDF") {
			t.Fatalf("error = %v, want bounded parser diagnostic", err)
		}
	})

	t.Run("caller cancellation", func(t *testing.T) {
		command := writePDFTextTestCommand(t, `sleep 10`)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := viviExtractPDFLinesWithCommand(ctx, []byte("%PDF-test"), command, time.Second, 1024, 1024, 1024)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context cancellation", err)
		}
	})

	t.Run("child environment excludes application secrets", func(t *testing.T) {
		t.Setenv("TRAINBOT_TEST_SECRET", "must-not-leak")
		command := writePDFTextTestCommand(t, `
if [ "${TRAINBOT_TEST_SECRET+x}" = x ]; then
  printf 'secret leaked' >&2
  exit 1
fi
printf 'Vilciena nr 1001\nRīga 10.00 10.01\nJelgava 10.45 10.46\n'
`)
		if _, err := viviExtractPDFLinesWithCommand(context.Background(), []byte("%PDF-test"), command, time.Second, 1024, 1024, 1024); err != nil {
			t.Fatalf("minimal child environment: %v", err)
		}
	})
}

type staticViviResolver map[string][]net.IPAddr

func (r staticViviResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	items, ok := r[host]
	if !ok {
		return nil, fmt.Errorf("host %s is not configured", host)
	}
	return items, nil
}

func writePDFTextTestCommand(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-pdftotext")
	contents := "#!/bin/sh\nset -eu\n" + body + "\n"
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatalf("write fake pdftotext: %v", err)
	}
	return path
}
