package server

import (
	"bufio"
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// tokenLimiter is a per-IP token bucket with an optional failure lockout. It
// is intentionally small and in-process: it protects a single node's admin and
// cluster endpoints, not a distributed edge.
type tokenLimiter struct {
	mu       sync.Mutex
	rate     float64 // tokens per second
	burst    float64
	maxFails int
	lockout  time.Duration
	states   map[string]*ipState
	now      func() time.Time
}

type ipState struct {
	tokens       float64
	last         time.Time
	failures     int
	blockedUntil time.Time
}

func newTokenLimiter(perMinute, burst, maxFails int, lockout time.Duration) *tokenLimiter {
	if perMinute < 0 {
		return nil
	}
	if perMinute == 0 {
		perMinute = 120
	}
	if burst <= 0 {
		burst = 30
	}
	if burst > perMinute {
		burst = perMinute
	}
	if maxFails <= 0 {
		maxFails = 10
	}
	if lockout <= 0 {
		lockout = 5 * time.Minute
	}
	return &tokenLimiter{
		rate:     float64(perMinute) / 60,
		burst:    float64(burst),
		maxFails: maxFails,
		lockout:  lockout,
		states:   make(map[string]*ipState),
		now:      time.Now,
	}
}

// allow consumes one token for ip. It returns false and a suggested retry
// delay when the bucket is empty.
func (l *tokenLimiter) allow(ip string) (bool, time.Duration) {
	if l == nil {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if s := l.blockedLocked(ip, now); s != nil {
		return false, s.blockedUntil.Sub(now)
	}
	st := l.stateLocked(ip, now)
	if st.tokens >= 1 {
		st.tokens--
		return true, 0
	}
	missing := 1 - st.tokens
	return false, time.Duration(missing / l.rate * float64(time.Second))
}

func (l *tokenLimiter) blocked(ip string) (time.Duration, bool) {
	if l == nil {
		return 0, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if s := l.blockedLocked(ip, l.now()); s != nil {
		return s.blockedUntil.Sub(l.now()), true
	}
	return 0, false
}

func (l *tokenLimiter) blockedLocked(ip string, now time.Time) *ipState {
	st := l.states[ip]
	if st == nil || st.blockedUntil.IsZero() || !now.Before(st.blockedUntil) {
		return nil
	}
	return st
}

// fail records an authentication failure and starts a lockout once the
// threshold is reached.
func (l *tokenLimiter) fail(ip string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	st := l.stateLocked(ip, now)
	st.failures++
	if st.failures >= l.maxFails {
		st.blockedUntil = now.Add(l.lockout)
		st.failures = 0
	}
}

func (l *tokenLimiter) succeed(ip string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if st := l.states[ip]; st != nil {
		st.failures = 0
	}
}

func (l *tokenLimiter) stateLocked(ip string, now time.Time) *ipState {
	st := l.states[ip]
	if st == nil {
		st = &ipState{tokens: l.burst, last: now}
		l.states[ip] = st
		l.maybePruneLocked(now)
		return st
	}
	elapsed := now.Sub(st.last).Seconds()
	if elapsed > 0 {
		st.tokens = math.Min(l.burst, st.tokens+elapsed*l.rate)
		st.last = now
	}
	return st
}

func (l *tokenLimiter) maybePruneLocked(now time.Time) {
	if len(l.states) < 4096 {
		return
	}
	cutoff := now.Add(-10 * time.Minute)
	for ip, st := range l.states {
		if st.last.Before(cutoff) && st.blockedUntil.Before(now) {
			delete(l.states, ip)
		}
	}
}

// clientIP extracts the peer address. Forwarded headers are used only when the
// operator opts in, since an untrusted client can set them.
//
// X-Forwarded-For is parsed right-to-left: the rightmost entries were appended
// by the trusted proxies closest to us, so the client-controlled prefix on the
// left can never be trusted. proxyHops says how many trusted hops sit between
// us and the first untrusted address (1 for a single reverse proxy). A
// leftmost parse would let any client spoof its way past rate limits, lockouts,
// and CIDR allow-lists behind an appending proxy such as nginx's default
// $proxy_add_x_forwarded_for.
func (h *Hub) clientIP(r *http.Request) string {
	if h.cfg.TrustProxyHeaders {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			hops := h.cfg.ProxyHops
			if hops <= 0 {
				hops = 1
			}
			parts := strings.Split(xff, ",")
			// The rightmost `hops` entries were appended by the trusted proxies
			// closest to us; the first untrusted address (and thus the client)
			// is at len(parts)-hops. A leftmost parse would trust the
			// client-controlled prefix and let anyone spoof past rate limits,
			// lockouts, and CIDR allow-lists behind an appending proxy such as
			// nginx's default $proxy_add_x_forwarded_for.
			idx := len(parts) - hops
			if idx < 0 {
				idx = 0
			}
			if ip := strings.TrimSpace(parts[idx]); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func parseCIDRs(values []string) ([]*net.IPNet, error) {
	var nets []*net.IPNet
	for _, raw := range values {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		_, network, err := net.ParseCIDR(raw)
		if err != nil {
			return nil, err
		}
		nets = append(nets, network)
	}
	return nets, nil
}

func ipAllowed(ip string, allow []*net.IPNet) bool {
	if len(allow) == 0 {
		return true
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, network := range allow {
		if network.Contains(parsed) {
			return true
		}
	}
	return false
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap exposes the underlying writer to http.ResponseController.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Hijack is required for websocket upgrades through middleware wrappers.
func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := s.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	return h.Hijack()
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// AdminHandler wraps an admin endpoint with allow-listing, per-IP rate
// limiting, and failure lockout.
func (h *Hub) AdminHandler(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := h.clientIP(r)
		if !ipAllowed(ip, h.adminNets) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if wait, blocked := h.adminLimiter.blocked(ip); blocked {
			w.Header().Set("Retry-After", retrySeconds(wait))
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		if ok, wait := h.adminLimiter.allow(ip); !ok {
			w.Header().Set("Retry-After", retrySeconds(wait))
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next(rec, r)
		switch {
		case rec.status == http.StatusUnauthorized:
			h.adminLimiter.fail(ip)
		case rec.status < http.StatusBadRequest:
			h.adminLimiter.succeed(ip)
		}
	})
}

// ClusterHandler wraps the node-to-node relay endpoint with allow-listing and
// rate limiting. The handler itself enforces the admin token (see
// ServeClusterEvent); this wrapper only adds transport-level guards.
func (h *Hub) ClusterHandler(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := h.clientIP(r)
		if !ipAllowed(ip, h.clusterNets) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if wait, blocked := h.clusterLimiter.blocked(ip); blocked {
			w.Header().Set("Retry-After", retrySeconds(wait))
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		if ok, wait := h.clusterLimiter.allow(ip); !ok {
			w.Header().Set("Retry-After", retrySeconds(wait))
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next(rec, r)
		switch {
		case rec.status == http.StatusUnauthorized:
			h.clusterLimiter.fail(ip)
		case rec.status < http.StatusBadRequest:
			h.clusterLimiter.succeed(ip)
		}
	})
}

// WSHandler wraps the websocket upgrade path with a per-IP connection-attempt
// limiter. Without it, unlimited upgrade attempts translate into unlimited
// goroutine pairs and HMAC verifications before any per-client message limit
// can apply. Lockout is deliberately not used here: the failure signal is an
// HTTP status the handler may legitimately return, not an auth failure.
func (h *Hub) WSHandler(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := h.clientIP(r)
		if !ipAllowed(ip, h.wsNets) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if wait, blocked := h.wsLimiter.blocked(ip); blocked {
			w.Header().Set("Retry-After", retrySeconds(wait))
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		if ok, wait := h.wsLimiter.allow(ip); !ok {
			w.Header().Set("Retry-After", retrySeconds(wait))
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	})
}

func retrySeconds(wait time.Duration) string {
	secs := int(math.Ceil(wait.Seconds()))
	if secs < 1 {
		secs = 1
	}
	return strconv.Itoa(secs)
}
