package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"syncwell/internal/server"
)

func TestPublicBind(t *testing.T) {
	cases := map[string]bool{
		":8080":            true,
		"0.0.0.0:8080":     true,
		"[::]:8080":        true,
		"127.0.0.1:8080":   false,
		"localhost:8080":   false,
		"LOCALHOST:8080":   false,
		"[::1]:8080":       false,
		"192.168.1.10:80":  true,
		"example.com:8080": true,
		"garbage":          true,
	}
	for addr, want := range cases {
		if got := publicBind(addr); got != want {
			t.Errorf("publicBind(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestTLSMinVersionNegotiation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		minimum string
		wantOK  bool
	}{
		{name: "default rejects TLS 1.2", minimum: "1.3", wantOK: false},
		{name: "opt-in accepts TLS 1.2", minimum: "1.2", wantOK: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			min, err := parseTLSMinVersion(tc.minimum)
			if err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
			srv.TLS = &tls.Config{MinVersion: min}
			srv.StartTLS()
			defer srv.Close()
			client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12, InsecureSkipVerify: true}}} // test certificate
			resp, err := client.Get(srv.URL)
			if (err == nil) != tc.wantOK {
				t.Fatalf("TLS 1.2 request error = %v, want success %v", err, tc.wantOK)
			}
			if resp != nil {
				_ = resp.Body.Close()
			}
		})
	}
}

func TestEnvFallbacks(t *testing.T) {
	t.Setenv("TEST_INT", "7")
	if got := envInt("TEST_INT", 1); got != 7 {
		t.Errorf("envInt = %d, want 7", got)
	}
	t.Setenv("TEST_INT", "nope")
	if got := envInt("TEST_INT", 1); got != 1 {
		t.Errorf("envInt fallback = %d, want 1", got)
	}
	t.Setenv("TEST_INT64", "9007199254740993")
	if got := envInt64("TEST_INT64", 1); got != 9007199254740993 {
		t.Errorf("envInt64 = %d", got)
	}
	t.Setenv("TEST_DUR", "250ms")
	if got := envDuration("TEST_DUR", time.Second); got != 250*time.Millisecond {
		t.Errorf("envDuration = %v", got)
	}
	t.Setenv("TEST_DUR", "bad")
	if got := envDuration("TEST_DUR", time.Second); got != time.Second {
		t.Errorf("envDuration fallback = %v", got)
	}
	t.Setenv("TEST_BOOL", "true")
	if !envBool("TEST_BOOL", false) {
		t.Error("envBool should be true")
	}
	t.Setenv("TEST_BOOL", "bad")
	if envBool("TEST_BOOL", false) {
		t.Error("envBool should fall back to false")
	}
}
func TestTLSMinParser(t *testing.T) {
	tests := []struct {
		input   string
		wantMin uint16
		wantErr bool
	}{
		{"1.2", tls.VersionTLS12, false},
		{"1.3", tls.VersionTLS13, false},
		{" 1.2 ", tls.VersionTLS12, false},
		{"1.2.1", 0, true},
		{"2.0", 0, true},
		{"", 0, true},
		{"1", 0, true},
	}
	for _, tt := range tests {
		got, err := parseTLSMinVersion(tt.input)
		if (err != nil) != tt.wantErr || got != tt.wantMin {
			t.Errorf("parseTLSMinVersion(%q) = (%d, %v), want (%d, err=%v)", tt.input, got, err, tt.wantMin, tt.wantErr)
		}
	}
}

func TestParseNodeAdvertise(t *testing.T) {
	got, err := parseNodeAdvertise([]string{"node-a=wss://a.example/ws", "node-b=wss://b.example/ws"})
	if err != nil || got["node-b"] != "wss://b.example/ws" {
		t.Fatalf("parseNodeAdvertise() = %#v, %v", got, err)
	}
	for _, bad := range [][]string{{"bad"}, {"node:a=wss://host/ws"}, {"node=wss://user@host/ws"}, {"node=https://host/ws"}, {"node=wss://host/ws?token=x"}, {"node=a=wss://host/ws"}, {"node=wss://host/ws", "node=wss://other/ws"}} {
		if _, err := parseNodeAdvertise(bad); err == nil {
			t.Errorf("parseNodeAdvertise(%q) unexpectedly succeeded", bad)
		}
	}
	if !isLoopbackHost("127.0.0.1:6379") || !isLoopbackHost("localhost:6379") || isLoopbackHost("redis.example:6379") {
		t.Fatal("Redis loopback host classification is incorrect")
	}
}

func TestVerifyRedisDurability(t *testing.T) {
	if addr := os.Getenv("SYNCWELL_TEST_REDIS_ADDR"); addr != "" {
		client := redis.NewClient(&redis.Options{Addr: addr})
		defer client.Close()
		if err := verifyRedisDurability(context.Background(), client); err != nil {
			t.Fatalf("CI Redis service durability settings were rejected: %v", err)
		}
		return
	}
	bin, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server is not installed")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "--bind", "127.0.0.1", "--port", strconv.Itoa(port), "--save", "", "--appendonly", "yes", "--appendfsync", "everysec", "--dir", t.TempDir(), "--protected-mode", "no")
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	client := redis.NewClient(&redis.Options{Addr: net.JoinHostPort("127.0.0.1", strconv.Itoa(port))})
	t.Cleanup(func() { _ = client.Close() })
	deadline := time.Now().Add(3 * time.Second)
	for client.Ping(context.Background()).Err() != nil {
		if time.Now().After(deadline) {
			t.Fatal("redis-server did not become ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := verifyRedisDurability(context.Background(), client); err != nil {
		t.Fatalf("valid AOF settings rejected: %v", err)
	}
	if err := client.ConfigSet(context.Background(), "appendfsync", "always").Err(); err != nil {
		t.Fatal(err)
	}
	if err := verifyRedisDurability(context.Background(), client); err == nil {
		t.Fatal("non-everysec appendfsync setting was accepted")
	}
}

func TestAuditUnsafeStartupFlags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	audit, err := server.OpenAuditLogger(path)
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Close()
	if err := auditUnsafeStartup(audit, true, true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := map[string]bool{"server.start.insecure": false, "server.start.allow_any_origin": false}
	for _, line := range lines {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("invalid audit JSON %q: %v", line, err)
		}
		if action, ok := event["action"].(string); ok {
			if _, exists := want[action]; exists {
				want[action] = true
			}
		}
	}
	for action, found := range want {
		if !found {
			t.Errorf("startup audit event %q missing", action)
		}
	}
}
