package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTokenLimiterRefillsAndLocksOut(t *testing.T) {
	now := time.Unix(1000, 0)
	l := newTokenLimiter(60, 2, 2, time.Minute)
	l.now = func() time.Time { return now }

	if ok, _ := l.allow("a"); !ok {
		t.Fatal("first request should pass")
	}
	if ok, _ := l.allow("a"); !ok {
		t.Fatal("second request should pass")
	}
	if ok, wait := l.allow("a"); ok || wait <= 0 {
		t.Fatalf("third request should be limited with a retry hint, ok=%v wait=%v", ok, wait)
	}
	now = now.Add(time.Second)
	if ok, _ := l.allow("a"); !ok {
		t.Fatal("bucket should refill after a second")
	}

	l.fail("b")
	if _, blocked := l.blocked("b"); blocked {
		t.Fatal("one failure must not lock out")
	}
	l.fail("b")
	if wait, blocked := l.blocked("b"); !blocked || wait <= 0 {
		t.Fatalf("two failures should lock out, blocked=%v wait=%v", blocked, wait)
	}
	now = now.Add(2 * time.Minute)
	if _, blocked := l.blocked("b"); blocked {
		t.Fatal("lockout should expire")
	}
}

func TestTokenLimiterDisabled(t *testing.T) {
	var l *tokenLimiter
	if ok, _ := l.allow("a"); !ok {
		t.Fatal("nil limiter should allow")
	}
	l.fail("a")
	l.succeed("a")
}

func TestClientIPHonorsTrustProxy(t *testing.T) {
	h := NewHub(Config{})
	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.5")
	if got := h.clientIP(req); got != "10.0.0.5" {
		t.Fatalf("untrusted XFF used: %q", got)
	}

	// One trusted hop: the rightmost entry was appended by our proxy and is
	// the client it actually saw. Client-controlled leftmost entries must be
	// ignored so spoofed XFF cannot bypass rate limits or allow-lists.
	h = NewHub(Config{TrustProxyHeaders: true})
	if got := h.clientIP(req); got != "10.0.0.5" {
		t.Fatalf("trusted XFF (hops=1): %q", got)
	}

	spoofed := httptest.NewRequest(http.MethodGet, "/stats", nil)
	spoofed.RemoteAddr = "10.0.0.5:1234"
	spoofed.Header.Set("X-Forwarded-For", "10.99.99.99, 203.0.113.9")
	if got := h.clientIP(spoofed); got != "203.0.113.9" {
		t.Fatalf("spoofed leftmost XFF must be ignored: %q", got)
	}

	// Two trusted hops: strip both rightmost entries to reach the client.
	h = NewHub(Config{TrustProxyHeaders: true, ProxyHops: 2})
	if got := h.clientIP(spoofed); got != "10.99.99.99" {
		t.Fatalf("trusted XFF (hops=2): %q", got)
	}

	// Fewer hops than entries is fine; fewer entries than hops falls back to
	// the leftmost entry rather than RemoteAddr.
	single := httptest.NewRequest(http.MethodGet, "/stats", nil)
	single.RemoteAddr = "10.0.0.5:1234"
	single.Header.Set("X-Forwarded-For", "203.0.113.9")
	h = NewHub(Config{TrustProxyHeaders: true, ProxyHops: 2})
	if got := h.clientIP(single); got != "203.0.113.9" {
		t.Fatalf("short XFF with hops=2: %q", got)
	}
}

func TestWSHandlerRateLimit(t *testing.T) {
	h := NewHub(Config{WSRatePerMinute: 60})
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusSwitchingProtocols)
	})
	wrapped := h.WSHandler(ok)

	req := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/ws", nil)
		r.RemoteAddr = "203.0.113.7:4444"
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, r)
		return w
	}

	// Burst is capped at min(60, 60)=60; exhaust it.
	for i := 0; i < 60; i++ {
		if w := req(); w.Code != http.StatusSwitchingProtocols {
			t.Fatalf("attempt %d: status %d", i, w.Code)
		}
	}
	if w := req(); w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after burst exhausted, got %d", w.Code)
	}
	if w := req(); w.Header().Get("Retry-After") == "" {
		t.Fatal("429 without Retry-After header")
	}

	// A different IP still has its own bucket.
	other := httptest.NewRequest(http.MethodGet, "/ws", nil)
	other.RemoteAddr = "203.0.113.8:4444"
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, other)
	if w.Code != http.StatusSwitchingProtocols {
		t.Fatalf("fresh IP blocked: %d", w.Code)
	}
}

func TestAdminHandlerAllowlistAndRateLimit(t *testing.T) {
	h := NewHub(Config{
		AdminAllowlist:     []string{"10.0.0.0/8"},
		AdminRatePerMinute: 2,
		AdminBurst:         2,
	})
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	wrapped := h.AdminHandler(ok)

	req := func(ip string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/stats", nil)
		r.RemoteAddr = ip + ":1234"
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, r)
		return w
	}

	if w := req("10.1.2.3"); w.Code != http.StatusOK {
		t.Fatalf("allowlisted IP: status %d", w.Code)
	}
	if w := req("192.0.2.1"); w.Code != http.StatusForbidden {
		t.Fatalf("non-allowlisted IP: status %d", w.Code)
	}
	if w := req("10.1.2.3"); w.Code != http.StatusOK {
		t.Fatalf("second request: status %d", w.Code)
	}
	if w := req("10.1.2.3"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limit not applied: status %d", w.Code)
	}
}

func TestAdminHandlerLocksOutFailedAuth(t *testing.T) {
	h := NewHub(Config{
		AdminToken:         "right",
		AdminMaxFailures:   2,
		AdminLockout:       time.Minute,
		AdminRatePerMinute: 600,
		AdminBurst:         100,
	})
	handler := h.AdminHandler(func(w http.ResponseWriter, r *http.Request) {
		if !h.adminAllowed(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	call := func(token string) int {
		r := httptest.NewRequest(http.MethodGet, "/stats", nil)
		r.RemoteAddr = "198.51.100.7:9999"
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}

	if code := call("wrong"); code != http.StatusUnauthorized {
		t.Fatalf("first bad token: %d", code)
	}
	if code := call("wrong"); code != http.StatusUnauthorized {
		t.Fatalf("second bad token: %d", code)
	}
	if code := call("right"); code != http.StatusTooManyRequests {
		t.Fatalf("locked-out IP should get 429, got %d", code)
	}
}

func TestClusterHandlerAllowlist(t *testing.T) {
	h := NewHub(Config{ClusterAllowlist: []string{"192.0.2.0/24"}})
	handler := h.ClusterHandler(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	call := func(ip string) int {
		r := httptest.NewRequest(http.MethodPost, "/cluster/event", nil)
		r.RemoteAddr = ip + ":1111"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	if code := call("192.0.2.10"); code != http.StatusAccepted {
		t.Fatalf("peer in allowlist: %d", code)
	}
	if code := call("203.0.113.5"); code != http.StatusForbidden {
		t.Fatalf("non-peer: %d", code)
	}
}
