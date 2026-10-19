package server

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestLogEventWritesStructuredJSON(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	LogEvent("room_gc", map[string]any{"room": "demo", "tombstones": 3})

	out := buf.String()
	if !strings.Contains(out, `"event":"room_gc"`) {
		t.Fatalf("missing event field: %s", out)
	}
	if !strings.Contains(out, `"room":"demo"`) {
		t.Fatalf("missing room field: %s", out)
	}
	if !strings.Contains(out, `"tombstones":3`) {
		t.Fatalf("missing tombstones field: %s", out)
	}
}
