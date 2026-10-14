package server

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// loginLimiter is a small fixed-window counter that throttles login attempts per
// principal. It is deliberately in-memory: the kiosk is a single process, and a
// limiter that survives a restart is not worth a table here.
//
// The window resets on a successful login, so a customer who mistypes their PIN
// a few times is not locked out after they get it right.
type loginLimiter struct {
	mu      sync.Mutex
	max     int
	window  time.Duration
	now     func() time.Time
	entries map[string]*limitEntry
}

type limitEntry struct {
	count   int
	resetAt time.Time
}

func newLoginLimiter(max int, window time.Duration) *loginLimiter {
	if window <= 0 {
		window = time.Minute
	}
	return &loginLimiter{
		max:     max,
		window:  window,
		now:     time.Now,
		entries: make(map[string]*limitEntry),
	}
}

// allow consumes one attempt for key and reports whether it is permitted. A max
// of 0 disables the limiter entirely.
func (l *loginLimiter) allow(key string) bool {
	if l.max <= 0 {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	entry := l.entries[key]
	if entry == nil || now.After(entry.resetAt) {
		l.entries[key] = &limitEntry{count: 1, resetAt: now.Add(l.window)}
		return true
	}
	if entry.count >= l.max {
		return false
	}
	entry.count++
	return true
}

// reset forgets a key's attempts, called after a successful login.
func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	delete(l.entries, key)
	l.mu.Unlock()
}

// loginKey identifies the thing being throttled: the canonical principal when
// one was supplied, otherwise the caller's address. Falling back to the address
// means a flood of empty-principal requests is still bounded.
func loginKey(r *http.Request, principal string) string {
	if p := strings.ToUpper(strings.TrimSpace(principal)); p != "" {
		return "p:" + p
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return "ip:" + host
}
