package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestValidateEgressURLRejectsSSRFTargets(t *testing.T) {
	bad := []string{
		"http://example.com/hook",
		"https://127.0.0.1/hook",
		"https://127.0.0.1:8443/hook",
		"https://localhost/hook",
		"https://10.0.0.1/hook",
		"https://172.16.0.1/hook",
		"https://192.168.1.1/hook",
		"https://169.254.169.254/latest/meta-data",
		"https://[::1]/hook",
		"https://[fc00::1]/hook",
		"https://user:pass@example.com/hook",
		"https://example.com/hook#fragment",
		"ftp://example.com/hook",
		"//example.com/hook",
	}
	for _, raw := range bad {
		if err := validateEgressURL(raw, false); err == nil {
			t.Errorf("validateEgressURL(%q) = nil, want error", raw)
		}
	}
	if err := validateEgressURL("https://example.com/hook", false); err != nil {
		t.Errorf("public https rejected: %v", err)
	}
	if err := validateEgressURL("http://127.0.0.1:8080/hook", true); err != nil {
		t.Errorf("explicit private loopback rejected: %v", err)
	}
	if err := validateEgressURL("http://example.com/hook", true); err == nil {
		t.Error("public plaintext http accepted even with AllowPrivate")
	}
}

func TestSafeTransportBlocksPrivateDial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("secret"))
	}))
	defer srv.Close()

	client := &http.Client{Timeout: time.Second, Transport: newSafeTransport(false)}
	if _, err := client.Get(srv.URL); err == nil {
		t.Fatal("safe transport connected to a loopback destination")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	allowed := &http.Client{Timeout: time.Second, Transport: newSafeTransport(true)}
	resp, err := allowed.Do(req)
	if err != nil {
		t.Fatalf("AllowPrivate transport failed: %v", err)
	}
	resp.Body.Close()
}

func TestValidateWebhookURLs(t *testing.T) {
	if err := ValidateWebhookURLs([]string{"https://example.com/hook"}, false); err != nil {
		t.Fatalf("public webhook rejected: %v", err)
	}
	if err := ValidateWebhookURLs([]string{"https://169.254.169.254/hook"}, false); err == nil {
		t.Fatal("metadata webhook URL accepted")
	}
	if err := ValidateWebhookURLs([]string{"http://127.0.0.1:9000/hook"}, false); err == nil {
		t.Fatal("loopback webhook URL accepted without AllowPrivate")
	}
	if err := ValidateWebhookURLs([]string{"http://127.0.0.1:9000/hook"}, true); err != nil {
		t.Fatalf("explicit local webhook rejected: %v", err)
	}
	if err := (Config{WebhookURLs: []string{"https://10.0.0.5/hook"}}).Validate(); err == nil {
		t.Fatal("Config.Validate accepted an SSRF webhook URL")
	}
}

func TestValidateRedirectPolicy(t *testing.T) {
	check := validateRedirect(false)
	via := []*http.Request{{URL: mustURL(t, "https://example.com/hook")}}
	if err := check(mustRequest(t, "https://169.254.169.254/latest"), via); err == nil {
		t.Fatal("redirect to metadata address accepted")
	}
	if err := check(mustRequest(t, "http://example.com/plain"), via); err == nil {
		t.Fatal("https-to-http redirect accepted")
	}
	if err := check(mustRequest(t, "https://example.com/next"), via); err != nil {
		t.Fatalf("public https redirect rejected: %v", err)
	}
	// The downgrade check must inspect the immediately preceding hop, not the
	// first request in the chain.
	chain := []*http.Request{
		{URL: mustURL(t, "http://localhost/hook")},
		{URL: mustURL(t, "https://example.com/next")},
	}
	if err := validateRedirect(true)(mustRequest(t, "http://localhost/plain"), chain); err == nil {
		t.Fatal("https-to-http downgrade accepted after an initial http hop")
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func mustRequest(t *testing.T, raw string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestWebsocketTokenCarriers(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ws?room=r&token=query-token", nil)
	req.Header.Set("Authorization", "Bearer bearer-token")
	req.Header.Set("Sec-WebSocket-Protocol", "syncwell, syncwell.token.sub-token")
	token, proto := websocketToken(req)
	if token != "bearer-token" {
		t.Fatalf("token = %q, want Authorization precedence", token)
	}
	if proto != "syncwell" {
		t.Fatalf("protocol = %q, want syncwell", proto)
	}

	req = httptest.NewRequest(http.MethodGet, "/ws?room=r", nil)
	req.Header.Set("Sec-WebSocket-Protocol", "syncwell.token.sub-token")
	token, proto = websocketToken(req)
	if token != "sub-token" || proto != "syncwell.token.sub-token" {
		t.Fatalf("subprotocol carrier = (%q,%q)", token, proto)
	}

	req = httptest.NewRequest(http.MethodGet, "/ws?room=r&token=query-token", nil)
	token, proto = websocketToken(req)
	if token != "query-token" || proto != "" {
		t.Fatalf("legacy query carrier = (%q,%q)", token, proto)
	}
}

func TestOriginPolicyHelpers(t *testing.T) {
	for _, raw := range []string{"*", "https://*.example.com", "https://example.com/path", "https://example.com?q=1", "file://x"} {
		if _, err := normalizeOrigin(raw); err == nil {
			t.Errorf("normalizeOrigin(%q) accepted", raw)
		}
	}
	got, err := normalizeOrigin("HTTPS://App.Example.com:443")
	if err != nil || got != "https://app.example.com" {
		t.Fatalf("normalizeOrigin = (%q,%v)", got, err)
	}
	if got, err := normalizeOrigin("http://[::1]:80"); err != nil || got != "http://[::1]" {
		t.Fatalf("normalizeOrigin ipv6 default port = (%q,%v)", got, err)
	}
	h := NewHub(Config{Origins: []string{"https://app.example.com"}})
	if !h.originAllowed("https://app.example.com") {
		t.Fatal("exact origin rejected")
	}
	if h.originAllowed("https://app.example.com.evil.test") || h.originAllowed("https://app.example.com/") {
		t.Fatal("non-exact origin accepted")
	}
	if h.originAllowed("") {
		t.Fatal("missing Origin accepted when an allow-list is configured")
	}
	strict := NewHub(Config{RequireOrigins: true})
	if strict.originAllowed("") || strict.originAllowed("https://x.test") {
		t.Fatal("RequireOrigins accepted an unconfigured origin")
	}
	any := NewHub(Config{AllowAnyOrigin: true})
	if !any.originAllowed("") || !any.originAllowed("https://x.test") {
		t.Fatal("AllowAnyOrigin rejected an origin")
	}
}

func TestOriginOKMatchesNormalizedOrigins(t *testing.T) {
	allowed := []string{"https://App.Example.com:443", "http://localhost:8080"}
	if !originOK("https://app.example.com:443", allowed) {
		t.Fatal("normalized exact origin rejected")
	}
	if !originOK("http://LOCALHOST:8080", allowed) {
		t.Fatal("case-insensitive host rejected")
	}
	if originOK("https://app.example.com:8443", allowed) {
		t.Fatal("origin with a different port accepted")
	}
	if originOK("https://app.example.com.evil.test", allowed) {
		t.Fatal("suffix attack accepted")
	}
	if !originOK("", allowed) {
		t.Fatal("empty Origin (non-browser client) rejected")
	}
	if originOK("not-a-url", allowed) {
		t.Fatal("malformed Origin accepted")
	}
}
