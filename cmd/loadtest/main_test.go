package main

import (
	"strings"
	"testing"
)

func TestValidateConnections(t *testing.T) {
	if err := validateConnections(20, 20, 0); err != nil {
		t.Fatalf("all requested clients connected: %v", err)
	}
	if err := validateConnections(20, 19, 1); err == nil || !strings.Contains(err.Error(), "connected=19 requested=20 failed=1") {
		t.Fatalf("partial connect should be actionable error, got %v", err)
	}
}
