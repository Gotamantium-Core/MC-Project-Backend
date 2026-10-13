package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLoginLimiterWindow(t *testing.T) {
	now := time.Unix(0, 0)
	l := newLoginLimiter(2, time.Minute)
	l.now = func() time.Time { return now }

	if !l.allow("k") || !l.allow("k") {
		t.Fatal("first two attempts should be allowed")
	}
	if l.allow("k") {
		t.Fatal("third attempt should be blocked")
	}

	// A different key is unaffected.
	if !l.allow("other") {
		t.Fatal("a different key should have its own budget")
	}

	// Crossing the window resets the budget.
	now = now.Add(2 * time.Minute)
	if !l.allow("k") {
		t.Fatal("attempt after the window should be allowed")
	}
}

func TestLoginLimiterReset(t *testing.T) {
	l := newLoginLimiter(1, time.Minute)

	if !l.allow("k") {
		t.Fatal("first attempt should be allowed")
	}
	if l.allow("k") {
		t.Fatal("second attempt should be blocked")
	}
	l.reset("k")
	if !l.allow("k") {
		t.Fatal("attempt after reset should be allowed")
	}
}

func TestLoginLimiterDisabled(t *testing.T) {
	l := newLoginLimiter(0, time.Minute)
	for i := 0; i < 100; i++ {
		if !l.allow("k") {
			t.Fatal("a max of 0 should disable the limiter")
		}
	}
}

func TestLoginKeyUsesPrincipalThenAddress(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	req.RemoteAddr = "10.0.0.1:5555"

	if got := loginKey(req, " 22cs001 "); got != "p:22CS001" {
		t.Fatalf("loginKey = %q, want p:22CS001", got)
	}
	if got := loginKey(req, "  "); got != "ip:10.0.0.1" {
		t.Fatalf("loginKey = %q, want ip:10.0.0.1", got)
	}
}

func TestLoginRateLimitsAfterFailures(t *testing.T) {
	cfg := testConfig()
	cfg.LoginMaxAttempts = 3
	cfg.LoginWindow = time.Minute
	ts, _ := newTestServerWithConfig(t, cfg)
	registerUser(t, ts, "22CS070", "1111")

	// Two failed attempts are 401; the next is over the limit.
	for i := 0; i < 3; i++ {
		if _, status := login(t, ts, "22CS070", "wrong"); status != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status %d, want 401", i+1, status)
		}
	}
	if _, status := login(t, ts, "22CS070", "wrong"); status != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 after exceeding the limit", status)
	}
}

func TestSuccessfulLoginResetsLimiter(t *testing.T) {
	cfg := testConfig()
	cfg.LoginMaxAttempts = 3
	ts, _ := newTestServerWithConfig(t, cfg)
	registerUser(t, ts, "22CS071", "1111")

	if _, status := login(t, ts, "22CS071", "wrong"); status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", status)
	}
	if _, status := login(t, ts, "22CS071", "1111"); status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	// The reset means a fresh budget of failures.
	for i := 0; i < 3; i++ {
		if _, status := login(t, ts, "22CS071", "wrong"); status != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status %d, want 401", i+1, status)
		}
	}
}
