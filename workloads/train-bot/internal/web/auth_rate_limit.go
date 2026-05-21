package web

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	authConfigRequestsPerMinute     = 30
	authCompleteRequestsPerMinute   = 15
	authRateLimitWindow             = time.Minute
	authRateLimiterCleanupThreshold = 4096
)

type clientRateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	entries map[string]rateLimitEntry
}

type rateLimitEntry struct {
	windowStart time.Time
	count       int
}

func newClientRateLimiter(limit int, window time.Duration) *clientRateLimiter {
	return &clientRateLimiter{
		limit:   limit,
		window:  window,
		entries: make(map[string]rateLimitEntry),
	}
}

func (l *clientRateLimiter) allow(key string, now time.Time) (bool, time.Duration) {
	if l == nil || l.limit <= 0 || l.window <= 0 {
		return true, 0
	}
	key = strings.TrimSpace(key)
	if key == "" {
		key = "unknown"
	}
	now = now.UTC()

	l.mu.Lock()
	defer l.mu.Unlock()

	entry := l.entries[key]
	if entry.windowStart.IsZero() || !now.Before(entry.windowStart.Add(l.window)) {
		l.entries[key] = rateLimitEntry{windowStart: now, count: 1}
		l.cleanup(now)
		return true, 0
	}
	if entry.count >= l.limit {
		return false, entry.windowStart.Add(l.window).Sub(now)
	}
	entry.count++
	l.entries[key] = entry
	return true, 0
}

func (l *clientRateLimiter) cleanup(now time.Time) {
	if len(l.entries) <= authRateLimiterCleanupThreshold {
		return
	}
	for key, entry := range l.entries {
		if !now.Before(entry.windowStart.Add(l.window)) {
			delete(l.entries, key)
		}
	}
}

func authRateLimitKey(r *http.Request) string {
	remoteHost := remoteAddrHost(r)
	if trustedForwardedHeaderSource(remoteHost) {
		if ip := cleanClientIP(r.Header.Get("CF-Connecting-IP")); ip != "" {
			return ip
		}
	}
	if ip := cleanClientIP(remoteHost); ip != "" {
		return ip
	}
	if remoteHost != "" {
		return remoteHost
	}
	return "unknown"
}

func remoteAddrHost(r *http.Request) string {
	host := strings.TrimSpace(r.RemoteAddr)
	if splitHost, _, err := net.SplitHostPort(host); err == nil {
		host = splitHost
	}
	return host
}

func trustedForwardedHeaderSource(host string) bool {
	ip := net.ParseIP(strings.TrimSpace(host))
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate())
}

func cleanClientIP(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	ip := net.ParseIP(value)
	if ip == nil {
		return ""
	}
	return ip.String()
}

func setAuthRetryAfter(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := int64((retryAfter + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
}
