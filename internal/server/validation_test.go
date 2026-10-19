package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"syncwell/internal/crdt"
)

func validInboundRegister() *crdt.Register {
	b, _ := json.Marshal("value")
	return &crdt.Register{Value: b, TS: crdt.Timestamp{Counter: 1, Node: "node"}}
}

func TestValidateInboundAcceptsExpectedMessages(t *testing.T) {
	limits := Limits{}.withDefaults()
	if err := validateInbound(Inbound{Type: "op", Key: "title", Register: validInboundRegister()}, limits); err != nil {
		t.Fatalf("valid op rejected: %v", err)
	}
	if err := validateInbound(Inbound{Type: "merge", State: map[string]crdt.Register{"title": *validInboundRegister()}}, limits); err != nil {
		t.Fatalf("valid merge rejected: %v", err)
	}
	if err := validateInbound(Inbound{Type: "presence", Data: json.RawMessage(`{"cursor":1}`)}, limits); err != nil {
		t.Fatalf("valid presence rejected: %v", err)
	}
	if err := validateInbound(Inbound{Type: "ack", Clock: 5}, limits); err != nil {
		t.Fatalf("valid ack rejected: %v", err)
	}
}

func TestValidateInboundRejectsUnsafeMessages(t *testing.T) {
	limits := Limits{}.withDefaults()
	tests := []Inbound{
		{Type: "op", Key: "", Register: validInboundRegister()},
		{Type: "op", Key: strings.Repeat("k", maxKeyBytes+1), Register: validInboundRegister()},
		{Type: "op", Key: "title"},
		{Type: "op", Key: "title", Register: &crdt.Register{Value: json.RawMessage(`not-json`), TS: crdt.Timestamp{Counter: 1, Node: "node"}}},
		{Type: "op", Key: "title", Register: &crdt.Register{Value: json.RawMessage(`"ok"`), TS: crdt.Timestamp{Counter: 1}}},
		{Type: "presence", Data: json.RawMessage(`not-json`)},
		{Type: "presence", Data: json.RawMessage(strings.Repeat("x", maxPresenceSize+1))},
		{Protocol: protocolVersion + 1, Type: "ack"},
		{Type: "unknown"},
	}
	for i, in := range tests {
		if err := validateInbound(in, limits); err == nil {
			t.Fatalf("unsafe message %d was accepted: %+v", i, in)
		}
	}
}

func TestValidateInboundRejectsOversizedMerge(t *testing.T) {
	limits := Limits{}.withDefaults()
	state := make(map[string]crdt.Register, maxStateEntries+1)
	reg := *validInboundRegister()
	for i := 0; i <= maxStateEntries; i++ {
		state[string(rune('a'+(i%26)))+strings.Repeat("x", i/26)] = reg
	}
	if err := validateInbound(Inbound{Type: "merge", State: state}, limits); err == nil {
		t.Fatal("oversized merge state was accepted")
	}
}

func TestValidateInboundUsesConfiguredLimits(t *testing.T) {
	limits := Limits{MaxKeyBytes: 3}.withDefaults()
	if err := validateInbound(Inbound{Type: "op", Key: "abcd", Register: validInboundRegister()}, limits); err == nil {
		t.Fatal("key longer than configured limit was accepted")
	}

	limits = Limits{MaxValueBytes: 4}.withDefaults()
	if err := validateInbound(Inbound{Type: "op", Key: "k", Register: validInboundRegister()}, limits); err == nil {
		t.Fatal("value longer than configured limit was accepted")
	}
}

func TestMessageRateLimiter(t *testing.T) {
	start := time.Unix(100, 0)
	limiter := newMessageRateLimiter(2)

	if !limiter.allow(start) || !limiter.allow(start.Add(100*time.Millisecond)) {
		t.Fatal("initial messages should be allowed")
	}
	if limiter.allow(start.Add(200 * time.Millisecond)) {
		t.Fatal("message beyond configured per-second limit was allowed")
	}
	if !limiter.allow(start.Add(time.Second)) {
		t.Fatal("limiter did not reset after a second")
	}
}
