package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Egress policy for operator-configured HTTP clients (webhooks today). The
// URL is validated before use and, crucially, every connection is dialed by
// IP after re-resolving the hostname. That second check closes the DNS
// rebinding window: a hostname that resolves to a public address during
// validation but to loopback/private space at connect time is still refused.

var reservedEgressNets = mustParseCIDRs(
	"0.0.0.0/8",
	"10.0.0.0/8",
	"100.64.0.0/10",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.0.0.0/24",
	"192.0.2.0/24",
	"192.88.99.0/24",
	"192.168.0.0/16",
	"198.18.0.0/15",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"224.0.0.0/4",
	"240.0.0.0/4",
	"255.255.255.255/32",
	"::/128",
	"::1/128",
	"64:ff9b::/96",
	"100::/64",
	"2001::/32",
	"2001:2::/48",
	"2001:db8::/32",
	"fc00::/7",
	"fe80::/10",
	"ff00::/8",
)

func mustParseCIDRs(raw ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(raw))
	for _, s := range raw {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			panic("server: invalid reserved CIDR " + s + ": " + err.Error())
		}
		out = append(out, n)
	}
	return out
}

// addressBlocked reports whether ip is outside the public internet. It is
// intentionally conservative: loopback, private, link-local (including the
// cloud metadata address), carrier-grade NAT, documentation, benchmark, and
// multicast space are all refused.
func addressBlocked(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() {
		return true
	}
	for _, n := range reservedEgressNets {
		if n.Contains(ip) {
			return true
		}
	}
	return !ip.IsGlobalUnicast()
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// hostBlocked resolves host (unless it is a literal IP) and reports whether any
// returned address is in reserved space. Lookup failures are not treated as
// fatal here: the dialer repeats the check and refuses a private result.
func hostBlocked(host string) bool {
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return true
	}
	if strings.Contains(host, "%") {
		return true // scoped IPv6 address; never valid for public egress
	}
	if ip := net.ParseIP(host); ip != nil {
		return addressBlocked(ip)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		if addressBlocked(addr.IP) {
			return true
		}
	}
	return false
}

// validateEgressURL enforces the outbound URL policy. HTTPS is required for
// public destinations; plaintext HTTP is accepted only for loopback when the
// operator explicitly opted into private destinations (local development).
func validateEgressURL(raw string, allowPrivate bool) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("empty URL")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse URL: %w", err)
	}
	if u.User != nil {
		return errors.New("URL must not contain credentials")
	}
	if u.Hostname() == "" {
		return errors.New("URL is missing a host")
	}
	if u.Fragment != "" {
		return errors.New("URL must not contain a fragment")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
	case "http":
		if !allowPrivate || !loopbackHost(u.Hostname()) {
			return errors.New("URL must use https (plain http is allowed only for loopback when private destinations are enabled)")
		}
	default:
		return fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
	if !allowPrivate && hostBlocked(u.Hostname()) {
		return errors.New("URL resolves to a private, loopback, link-local, or reserved address")
	}
	return nil
}

// newSafeTransport returns an HTTP transport that re-checks every resolved
// address at connect time. Environment proxies are deliberately disabled:
// forwarding through an untrusted proxy would bypass the address policy.
func newSafeTransport(allowPrivate bool) *http.Transport {
	base, ok := http.DefaultTransport.(*http.Transport)
	var t *http.Transport
	if ok {
		t = base.Clone()
	} else {
		t = &http.Transport{}
	}
	t.Proxy = nil
	if t.TLSClientConfig == nil {
		t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		t.TLSClientConfig = t.TLSClientConfig.Clone()
		if t.TLSClientConfig.MinVersion < tls.VersionTLS12 {
			t.TLSClientConfig.MinVersion = tls.VersionTLS12
		}
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if allowPrivate {
			return dialer.DialContext(ctx, network, addr)
		}
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, candidate := range addrs {
			if addressBlocked(candidate.IP) {
				lastErr = fmt.Errorf("refusing connection to reserved address %s", candidate.IP)
				continue
			}
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		if lastErr == nil {
			lastErr = errors.New("host resolved to no usable addresses")
		}
		return nil, lastErr
	}
	return t
}

// validateRedirect applies the same egress policy to every redirect hop and
// refuses HTTPS-to-HTTP downgrades.
func validateRedirect(allowPrivate bool) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("stopped after 5 redirects")
		}
		if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return errors.New("refusing redirect from https to plaintext http")
		}
		return validateEgressURL(req.URL.String(), allowPrivate)
	}
}
