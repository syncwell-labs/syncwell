package main

import (
	"testing"
	"time"
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
