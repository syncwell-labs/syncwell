package server

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
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

func TestLogRequestEventIncludesRequestID(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	req := httptest.NewRequest(http.MethodPost, "/admin/api/rooms/r/resnapshot", nil)
	ctx := context.WithValue(req.Context(), requestIDContextKey{}, "request-1234")
	LogRequestEvent(req.WithContext(ctx), "admin_action", map[string]any{"action": "room.resnapshot"})
	if !strings.Contains(buf.String(), `"request_id":"request-1234"`) {
		t.Fatalf("request id missing from structured event: %s", buf.String())
	}
}
