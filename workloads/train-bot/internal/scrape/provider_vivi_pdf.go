package scrape

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	viviLandingMaxBytes       = 2 << 20
	viviPDFMaxInputBytes      = 8 << 20
	viviPDFMaxTextBytes       = 8 << 20
	viviPDFMaxDiagnosticBytes = 64 << 10
	viviPDFExtractTimeout     = 15 * time.Second
	viviPDFTextCommand        = "pdftotext"
	viviPDFMaxLinks           = 32
	viviMaxRedirects          = 5
	viviMaxResolvedAddresses  = 16
)

type viviIPResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type ViviPDFProvider struct {
	name      string
	pageURL   string
	userAgent string
	timeout   time.Duration
	client    *http.Client
	resolver  viviIPResolver
}

func NewViviPDFProvider(name string, pageURL string, userAgent string, timeout time.Duration) *ViviPDFProvider {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &ViviPDFProvider{
		name:      name,
		pageURL:   pageURL,
		userAgent: userAgent,
		timeout:   timeout,
		resolver:  net.DefaultResolver,
	}
}

func (p *ViviPDFProvider) Name() string {
	return p.name
}

func (p *ViviPDFProvider) Fetch(ctx context.Context, serviceDate time.Time) (RawSchedule, error) {
	if strings.TrimSpace(p.pageURL) == "" {
		return RawSchedule{}, fmt.Errorf("provider %s page URL is empty", p.name)
	}
	pageURL, err := url.Parse(p.pageURL)
	if err != nil {
		return RawSchedule{}, err
	}
	if err := viviValidateOrigin(pageURL); err != nil {
		return RawSchedule{}, err
	}
	client := p.client
	if client == nil {
		client = viviSafeHTTPClient(p.timeout, pageURL, p.resolver)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL.String(), nil)
	if err != nil {
		return RawSchedule{}, err
	}
	if p.userAgent != "" {
		req.Header.Set("User-Agent", p.userAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		return RawSchedule{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return RawSchedule{}, fmt.Errorf("provider %s status %d", p.name, resp.StatusCode)
	}
	if err := viviValidateResponseURL(pageURL, resp.Request); err != nil {
		return RawSchedule{}, err
	}
	body, err := viviReadBounded(resp.Body, resp.ContentLength, viviLandingMaxBytes, "landing page")
	if err != nil {
		return RawSchedule{}, err
	}

	basePDFs, changePDFs, err := viviCollectPDFLinks(pageURL.String(), string(body), serviceDate)
	if err != nil {
		return RawSchedule{}, err
	}
	if len(basePDFs) == 0 && len(changePDFs) == 0 {
		return RawSchedule{}, fmt.Errorf("provider %s: no schedule pdf links found", p.name)
	}

	merged := map[string]RawTrain{}
	parseAndMerge := func(ctx context.Context, urls []string, override bool) error {
		for _, pdfURL := range urls {
			pdfBytes, err := p.fetchBytes(ctx, client, pageURL, pdfURL)
			if err != nil {
				return fmt.Errorf("fetch %s: %w", pdfURL, err)
			}
			trains, err := viviParseSchedulePDF(ctx, pdfBytes, serviceDate)
			if err != nil {
				return fmt.Errorf("parse %s: %w", pdfURL, err)
			}
			for _, train := range trains {
				key := viviTrainKey(train)
				if key == "" {
					continue
				}
				if _, exists := merged[key]; exists && !override {
					continue
				}
				merged[key] = train
			}
		}
		return nil
	}

	if err := parseAndMerge(ctx, basePDFs, false); err != nil {
		return RawSchedule{}, err
	}
	if err := parseAndMerge(ctx, changePDFs, true); err != nil {
		return RawSchedule{}, err
	}
	if len(merged) == 0 {
		return RawSchedule{}, fmt.Errorf("provider %s: parsed no trains from pdfs", p.name)
	}

	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := RawSchedule{
		SourceName: p.name,
		FetchedAt:  time.Now().UTC(),
		Trains:     make([]RawTrain, 0, len(keys)),
	}
	for _, k := range keys {
		out.Trains = append(out.Trains, merged[k])
	}
	return out, nil
}

func (p *ViviPDFProvider) fetchBytes(ctx context.Context, client *http.Client, origin *url.URL, target string) ([]byte, error) {
	targetURL, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	if err := viviValidateTargetURL(origin, targetURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	if p.userAgent != "" {
		req.Header.Set("User-Agent", p.userAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	if err := viviValidateResponseURL(origin, resp.Request); err != nil {
		return nil, err
	}
	return viviReadBounded(resp.Body, resp.ContentLength, viviPDFMaxInputBytes, "pdf")
}

var viviAnchorPDFRe = regexp.MustCompile(`(?is)<a[^>]*href=["']([^"']+\.pdf)["'][^>]*>(.*?)</a>`)
var stripTagRe = regexp.MustCompile(`(?is)<[^>]+>`)

func viviCollectPDFLinks(pageURL string, htmlBody string, serviceDate time.Time) ([]string, []string, error) {
	baseURL, err := url.Parse(pageURL)
	if err != nil {
		return nil, nil, err
	}
	if err := viviValidateOrigin(baseURL); err != nil {
		return nil, nil, err
	}

	baseSet := map[string]struct{}{}
	changeSet := map[string]struct{}{}
	matches := viviAnchorPDFRe.FindAllStringSubmatch(htmlBody, viviPDFMaxLinks+1)
	if len(matches) > viviPDFMaxLinks {
		return nil, nil, fmt.Errorf("schedule page exposes more than %d pdf links", viviPDFMaxLinks)
	}
	for _, m := range matches {
		if len(m) != 3 {
			continue
		}
		href := strings.TrimSpace(html.UnescapeString(m[1]))
		if href == "" {
			continue
		}
		u, err := url.Parse(href)
		if err != nil {
			continue
		}
		resolved := baseURL.ResolveReference(u)
		if err := viviValidateTargetURL(baseURL, resolved); err != nil {
			return nil, nil, err
		}
		resolved.Fragment = ""
		abs := resolved.String()
		lower := strings.ToLower(abs)
		linkText := strings.TrimSpace(stripTagRe.ReplaceAllString(html.UnescapeString(m[2]), " "))
		linkText = strings.Join(strings.Fields(linkText), " ")
		switch {
		case strings.Contains(lower, "/uploads/saraksti/"):
			baseSet[abs] = struct{}{}
		case strings.Contains(lower, "/uploads/izmainas/"):
			effDate, ok := viviParseEffectiveDate(linkText, serviceDate.Location(), serviceDate)
			if !ok || !serviceDate.Before(effDate) {
				changeSet[abs] = struct{}{}
			}
		}
	}
	if len(baseSet)+len(changeSet) > viviPDFMaxLinks {
		return nil, nil, fmt.Errorf("schedule page exposes more than %d pdf links", viviPDFMaxLinks)
	}

	base := make([]string, 0, len(baseSet))
	for u := range baseSet {
		base = append(base, u)
	}
	sort.Strings(base)
	changes := make([]string, 0, len(changeSet))
	for u := range changeSet {
		changes = append(changes, u)
	}
	sort.Strings(changes)
	return base, changes, nil
}

func viviReadBounded(body io.Reader, contentLength int64, maxBytes int64, label string) ([]byte, error) {
	if contentLength > maxBytes {
		return nil, fmt.Errorf("%s exceeds %d byte limit", label, maxBytes)
	}
	payload, err := io.ReadAll(io.LimitReader(body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > maxBytes {
		return nil, fmt.Errorf("%s exceeds %d byte limit", label, maxBytes)
	}
	return payload, nil
}

func viviValidateOrigin(origin *url.URL) error {
	if origin == nil || !strings.EqualFold(origin.Scheme, "https") || strings.TrimSpace(origin.Hostname()) == "" {
		return fmt.Errorf("ViVi origin must be an HTTPS URL")
	}
	if origin.User != nil {
		return fmt.Errorf("ViVi origin must not contain user information")
	}
	return nil
}

func viviValidateTargetURL(origin *url.URL, target *url.URL) error {
	if err := viviValidateOrigin(origin); err != nil {
		return err
	}
	if target == nil || !strings.EqualFold(target.Scheme, "https") || strings.TrimSpace(target.Hostname()) == "" {
		return fmt.Errorf("ViVi target must be an HTTPS URL")
	}
	if target.User != nil {
		return fmt.Errorf("ViVi target must not contain user information")
	}
	if !strings.EqualFold(origin.Hostname(), target.Hostname()) || viviEffectivePort(origin) != viviEffectivePort(target) {
		return fmt.Errorf("ViVi target must remain on origin %s", origin.Host)
	}
	return nil
}

func viviValidateResponseURL(origin *url.URL, request *http.Request) error {
	if request == nil || request.URL == nil {
		return fmt.Errorf("ViVi response URL is unavailable")
	}
	return viviValidateTargetURL(origin, request.URL)
}

func viviEffectivePort(target *url.URL) string {
	if port := target.Port(); port != "" {
		return port
	}
	if strings.EqualFold(target.Scheme, "https") {
		return "443"
	}
	return ""
}

func viviSafeHTTPClient(timeout time.Duration, origin *url.URL, resolver viviIPResolver) *http.Client {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = viviSafeDialContext(resolver, dialer)
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= viviMaxRedirects {
				return fmt.Errorf("ViVi redirect limit exceeded")
			}
			return viviValidateTargetURL(origin, req.URL)
		},
	}
}

func viviSafeDialContext(resolver viviIPResolver, dialer *net.Dialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network string, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("parse ViVi network address: %w", err)
		}
		addresses, err := viviResolvePublicAddresses(ctx, resolver, host)
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, item := range addresses {
			conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(item.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		return nil, fmt.Errorf("dial ViVi public address: %w", lastErr)
	}
}

func viviResolvePublicAddresses(ctx context.Context, resolver viviIPResolver, host string) ([]netip.Addr, error) {
	if resolver == nil {
		return nil, fmt.Errorf("ViVi resolver is unavailable")
	}
	if literal, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		if !viviIsPublicAddress(literal) {
			return nil, fmt.Errorf("ViVi address is not publicly routable")
		}
		return []netip.Addr{literal.Unmap()}, nil
	}
	resolved, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve ViVi host: %w", err)
	}
	if len(resolved) == 0 || len(resolved) > viviMaxResolvedAddresses {
		return nil, fmt.Errorf("ViVi host resolved to an invalid number of addresses")
	}
	out := make([]netip.Addr, 0, len(resolved))
	for _, item := range resolved {
		addr, ok := netip.AddrFromSlice(item.IP)
		if !ok || !viviIsPublicAddress(addr) {
			return nil, fmt.Errorf("ViVi host resolved to a non-public address")
		}
		out = append(out, addr.Unmap())
	}
	return out, nil
}

func viviIsPublicAddress(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsMulticast() || addr.IsUnspecified() {
		return false
	}
	blocked := []netip.Prefix{
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("240.0.0.0/4"),
		netip.MustParsePrefix("2001:db8::/32"),
	}
	for _, prefix := range blocked {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

var lvMonthByPrefix = map[string]time.Month{
	"janv":  time.January,
	"febru": time.February,
	"mart":  time.March,
	"apri":  time.April,
	"mai":   time.May,
	"jun":   time.June,
	"jul":   time.July,
	"aug":   time.August,
	"sept":  time.September,
	"okt":   time.October,
	"novem": time.November,
	"decem": time.December,
}

var effectiveDateRe = regexp.MustCompile(`(?i)no\s+(\d{1,2})\.\s*([[:alpha:]\x80-\xff]+)`)

func viviParseEffectiveDate(text string, loc *time.Location, serviceDate time.Time) (time.Time, bool) {
	m := effectiveDateRe.FindStringSubmatch(strings.ToLower(text))
	if len(m) != 3 {
		return time.Time{}, false
	}
	day, err := strconv.Atoi(m[1])
	if err != nil {
		return time.Time{}, false
	}
	monthWord := strings.TrimSpace(m[2])
	var month time.Month
	for prefix, v := range lvMonthByPrefix {
		if strings.HasPrefix(monthWord, prefix) {
			month = v
			break
		}
	}
	if month == 0 {
		return time.Time{}, false
	}
	year := serviceDate.Year()
	return time.Date(year, month, day, 0, 0, 0, 0, loc), true
}

type viviRow struct {
	Station string
	Times   []string
}

var trainNumberRe = regexp.MustCompile(`\b\d{3,5}\b`)
var dotTimeRe = regexp.MustCompile(`\b([0-2]?\d)\.([0-5]\d)\b`)

func viviParseSchedulePDF(ctx context.Context, pdfBytes []byte, serviceDate time.Time) ([]RawTrain, error) {
	lines, err := viviExtractPDFLines(ctx, pdfBytes)
	if err != nil {
		return nil, err
	}
	return viviParseScheduleLines(lines, serviceDate)
}

func viviParseScheduleLines(lines []string, serviceDate time.Time) ([]RawTrain, error) {
	trainNumbers := make([]string, 0)
	rows := make([]viviRow, 0)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(strings.ToLower(trimmed), "vilciena nr") {
			nums := trainNumberRe.FindAllString(trimmed, -1)
			if len(nums) > len(trainNumbers) {
				trainNumbers = nums
			}
			continue
		}
		if len(trainNumbers) == 0 {
			continue
		}
		timeLocs := dotTimeRe.FindAllStringIndex(trimmed, -1)
		if len(timeLocs) < 2 {
			continue
		}
		firstTimeIdx := timeLocs[0][0]
		station := strings.TrimSpace(trimmed[:firstTimeIdx])
		station = strings.Trim(station, "-–—:|")
		station = strings.Join(strings.Fields(station), " ")
		stationLower := strings.ToLower(station)
		if station == "" ||
			strings.Contains(stationLower, "vilciena nr") ||
			strings.Contains(stationLower, "autobuss") ||
			strings.Contains(stationLower, "ceļa nr") ||
			strings.Contains(stationLower, "piezīmes") {
			continue
		}
		tokens := dotTimeRe.FindAllString(trimmed, -1)
		if len(tokens) == 0 {
			continue
		}
		rows = append(rows, viviRow{Station: station, Times: tokens})
	}
	if len(trainNumbers) == 0 || len(rows) == 0 {
		return nil, fmt.Errorf("no train table found")
	}

	out := make([]RawTrain, 0, len(trainNumbers))
	for idx, number := range trainNumbers {
		stops := make([]RawStop, 0, len(rows))
		var prevTime *time.Time
		for seq, row := range rows {
			if idx >= len(row.Times) {
				continue
			}
			t, err := viviParseDotTime(row.Times[idx], serviceDate, prevTime)
			if err != nil {
				continue
			}
			tCopy := t
			stop := RawStop{
				StationName: row.Station,
				Seq:         seq + 1,
				ArrivalAt:   &tCopy,
				DepartureAt: &tCopy,
			}
			stops = append(stops, stop)
			prevTime = &t
		}
		if len(stops) < 2 {
			continue
		}
		dep := *stops[0].DepartureAt
		arr := *stops[len(stops)-1].ArrivalAt
		out = append(out, RawTrain{
			TrainNumber: strings.TrimSpace(number),
			ServiceDate: serviceDate.Format("2006-01-02"),
			FromStation: stops[0].StationName,
			ToStation:   stops[len(stops)-1].StationName,
			DepartureAt: dep,
			ArrivalAt:   arr,
			Stops:       stops,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no trains parsed from pdf")
	}
	return out, nil
}

func viviParseDotTime(raw string, serviceDate time.Time, prev *time.Time) (time.Time, error) {
	m := dotTimeRe.FindStringSubmatch(strings.TrimSpace(raw))
	if len(m) != 3 {
		return time.Time{}, fmt.Errorf("invalid time token %q", raw)
	}
	hour, err := strconv.Atoi(m[1])
	if err != nil {
		return time.Time{}, err
	}
	minute, err := strconv.Atoi(m[2])
	if err != nil {
		return time.Time{}, err
	}
	t := time.Date(serviceDate.Year(), serviceDate.Month(), serviceDate.Day(), hour, minute, 0, 0, serviceDate.Location())
	if prev != nil && t.Before(*prev) {
		t = t.Add(24 * time.Hour)
	}
	return t, nil
}

func viviTrainKey(t RawTrain) string {
	if strings.TrimSpace(t.ServiceDate) == "" {
		return ""
	}
	if strings.TrimSpace(t.TrainNumber) != "" {
		return fmt.Sprintf("%s|%s", t.ServiceDate, strings.ToLower(strings.TrimSpace(t.TrainNumber)))
	}
	return fmt.Sprintf("%s|%s|%s|%s", t.ServiceDate, strings.ToLower(strings.TrimSpace(t.FromStation)), strings.ToLower(strings.TrimSpace(t.ToStation)), t.DepartureAt.UTC().Format("1504"))
}

func viviExtractPDFLines(ctx context.Context, pdfBytes []byte) ([]string, error) {
	return viviExtractPDFLinesWithCommand(ctx, pdfBytes, viviPDFTextCommand, viviPDFExtractTimeout, viviPDFMaxInputBytes, viviPDFMaxTextBytes, viviPDFMaxDiagnosticBytes)
}

func viviExtractPDFLinesWithCommand(
	ctx context.Context,
	pdfBytes []byte,
	command string,
	timeout time.Duration,
	maxInputBytes int,
	maxTextBytes int,
	maxDiagnosticBytes int,
) ([]string, error) {
	if maxInputBytes <= 0 || len(pdfBytes) > maxInputBytes {
		return nil, fmt.Errorf("pdf exceeds %d byte limit", maxInputBytes)
	}
	if timeout <= 0 {
		timeout = viviPDFExtractTimeout
	}
	if maxTextBytes <= 0 {
		maxTextBytes = viviPDFMaxTextBytes
	}
	if maxDiagnosticBytes <= 0 {
		maxDiagnosticBytes = viviPDFMaxDiagnosticBytes
	}

	tmp, err := os.CreateTemp("", "vivi-schedule-*.pdf")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if _, err := tmp.Write(pdfBytes); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}

	extractCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(extractCtx, command, "-layout", "-enc", "UTF-8", tmpPath, "-")
	cmd.Env = []string{
		"PATH=/usr/bin:/bin",
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second
	cmd.Cancel = func() error {
		return killPDFProcessGroup(cmd)
	}
	stdout := newKillingBoundedBuffer(maxTextBytes, cmd)
	stderr := newKillingBoundedBuffer(maxDiagnosticBytes, cmd)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	runErr := cmd.Run()
	if stdout.Exceeded() {
		return nil, fmt.Errorf("pdftotext output exceeds %d byte limit", maxTextBytes)
	}
	if stderr.Exceeded() {
		return nil, fmt.Errorf("pdftotext diagnostics exceed %d byte limit", maxDiagnosticBytes)
	}
	if errors.Is(extractCtx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("pdftotext timed out after %s", timeout)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if runErr != nil {
		diagnostic := strings.Join(strings.Fields(stderr.String()), " ")
		if diagnostic == "" {
			return nil, fmt.Errorf("pdftotext failed: %w", runErr)
		}
		return nil, fmt.Errorf("pdftotext failed: %w: %s", runErr, diagnostic)
	}

	text := strings.ReplaceAll(stdout.String(), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) == 1 && strings.TrimSpace(lines[0]) == "" {
		return nil, fmt.Errorf("pdftotext produced no text")
	}
	return lines, nil
}

type killingBoundedBuffer struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	limit    int
	exceeded bool
	cmd      *exec.Cmd
}

func newKillingBoundedBuffer(limit int, cmd *exec.Cmd) *killingBoundedBuffer {
	return &killingBoundedBuffer{limit: limit, cmd: cmd}
}

func (b *killingBoundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.exceeded {
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if remaining >= len(p) {
		_, _ = b.buf.Write(p)
		return len(p), nil
	}
	if remaining > 0 {
		_, _ = b.buf.Write(p[:remaining])
	}
	b.exceeded = true
	_ = killPDFProcessGroup(b.cmd)
	return len(p), nil
}

func (b *killingBoundedBuffer) Exceeded() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.exceeded
}

func (b *killingBoundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func killPDFProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
