package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"taskman/internal/service"
)

func TestBackoffDelay(t *testing.T) {
	cases := []struct {
		fails int
		want  time.Duration
	}{
		{0, 0},
		{-1, 0},
		{1, 500 * time.Millisecond},
		{2, 1000 * time.Millisecond},
		{10, 5000 * time.Millisecond}, // would be 5s exactly, at the cap
		{20, 5 * time.Second},         // way past the cap
	}
	for _, c := range cases {
		got := backoffDelay(c.fails)
		if got != c.want {
			t.Errorf("backoffDelay(%d) = %v, want %v", c.fails, got, c.want)
		}
	}
}

func TestLoginLimiterFailResetCycle(t *testing.T) {
	l := newLoginLimiter()
	if d := backoffDelay(l.fails["1.2.3.4"]); d != 0 {
		t.Fatalf("fresh limiter should have zero delay, got %v", d)
	}
	l.fail("1.2.3.4")
	l.fail("1.2.3.4")
	if got := l.fails["1.2.3.4"]; got != 2 {
		t.Fatalf("fails = %d, want 2", got)
	}
	l.reset("1.2.3.4")
	if got := l.fails["1.2.3.4"]; got != 0 {
		t.Fatalf("after reset fails = %d, want 0", got)
	}
	// A different IP is tracked independently.
	l.fail("5.6.7.8")
	if got := l.fails["1.2.3.4"]; got != 0 {
		t.Fatalf("unrelated IP's failure leaked into 1.2.3.4's counter: %d", got)
	}
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		remoteAddr string
		want       string
	}{
		{"192.168.1.5:54321", "192.168.1.5"},
		{"[::1]:8080", "::1"},
		{"not-a-valid-addr", "not-a-valid-addr"}, // fallback: return as-is
	}
	for _, c := range cases {
		r := &http.Request{RemoteAddr: c.remoteAddr}
		if got := clientIP(r); got != c.want {
			t.Errorf("clientIP(%q) = %q, want %q", c.remoteAddr, got, c.want)
		}
	}
}

func TestSessionCookieRoundTrip(t *testing.T) {
	rec := httptest.NewRecorder()
	expiry := time.Now().Add(service.SessionTTL).Truncate(time.Second)
	setSessionCookie(rec, "abc123", expiry)

	resp := rec.Result()
	var got *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			got = c
		}
	}
	if got == nil {
		t.Fatalf("no %s cookie set", sessionCookieName)
	}
	if got.Value != "abc123" {
		t.Errorf("cookie value = %q, want %q", got.Value, "abc123")
	}
	if !got.HttpOnly {
		t.Errorf("cookie not HttpOnly")
	}
	if got.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie SameSite = %v, want Lax", got.SameSite)
	}
	if got.Path != "/" {
		t.Errorf("cookie Path = %q, want /", got.Path)
	}
	if got.Secure {
		t.Errorf("cookie must not be Secure (plain-HTTP LAN tool per docs/AUTH_FEATURES.md)")
	}

	rec2 := httptest.NewRecorder()
	clearSessionCookie(rec2)
	resp2 := rec2.Result()
	var cleared *http.Cookie
	for _, c := range resp2.Cookies() {
		if c.Name == sessionCookieName {
			cleared = c
		}
	}
	if cleared == nil {
		t.Fatalf("clearSessionCookie set no cookie")
	}
	if cleared.MaxAge >= 0 {
		t.Errorf("clearSessionCookie MaxAge = %d, want negative (delete)", cleared.MaxAge)
	}
}
