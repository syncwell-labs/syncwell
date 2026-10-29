package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"syncwell/internal/crdt"
	"syncwell/internal/store"
)

type hookSink struct {
	mu     sync.Mutex
	events []WebhookEvent
	ch     chan WebhookEvent
}

func newHookSink() (*hookSink, *httptest.Server) {
	s := &hookSink{ch: make(chan WebhookEvent, 32)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ev WebhookEvent
		if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.events = append(s.events, ev)
		s.mu.Unlock()
		s.ch <- ev
		w.WriteHeader(http.StatusNoContent)
	}))
	return s, srv
}

func (s *hookSink) wait(t *testing.T, event string) WebhookEvent {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-s.ch:
			if ev.Event == event {
				return ev
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s webhook", event)
			return WebhookEvent{}
		}
	}
}

type memStore struct {
	mu   sync.Mutex
	snap map[string]map[string]crdt.Register
}

func (m *memStore) Load(room string) (map[string]crdt.Register, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.snap[room]; ok {
		return s, nil
	}
	return map[string]crdt.Register{}, nil
}
func (m *memStore) Append(string, store.Record) error { return nil }
func (m *memStore) Compact(room string, snap map[string]crdt.Register) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.snap == nil {
		m.snap = map[string]map[string]crdt.Register{}
	}
	m.snap[room] = snap
	return nil
}
func (m *memStore) Close() error        { return nil }
func (m *memStore) Delete(string) error { return nil }

func TestWebhooksEmitRoomLifecycle(t *testing.T) {
	sink, srv := newHookSink()
	defer srv.Close()

	hub := NewHub(Config{
		Store:               &memStore{},
		CompactEvery:        1,
		WebhookURLs:         []string{srv.URL},
		WebhookSecret:       "whsec",
		WebhookAllowPrivate: true,
	})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = hub.Shutdown(ctx)
	}()

	r, err := hub.room("hook-room")
	if err != nil {
		t.Fatal(err)
	}
	if got := sink.wait(t, "room.created"); got.Room != "hook-room" || got.Node != "local" {
		t.Fatalf("unexpected created event: %+v", got)
	}

	cl := &Client{
		id: "c1", room: r, send: make(chan []byte, 8),
		claims: TokenClaims{Caps: map[string]bool{CapRead: true, CapWrite: true, CapPresence: true}},
	}
	r.cmds <- command{kind: "join", client: cl}
	r.cmds <- command{kind: "op", client: cl, in: Inbound{Type: "op", Epoch: 1, Key: "k", Register: ptr(serverReg("v", 1, "c1"))}}
	if got := sink.wait(t, "room.persisted"); got.Room != "hook-room" {
		t.Fatalf("unexpected persisted event: %+v", got)
	}

	if !hub.EvictRoom("hook-room") {
		t.Fatal("evict failed")
	}
	if got := sink.wait(t, "room.evicted"); got.Room != "hook-room" {
		t.Fatalf("unexpected evicted event: %+v", got)
	}
}

func ptr(r crdt.Register) *crdt.Register { return &r }

func TestWebhookSignatureTimestamped(t *testing.T) {
	const secret = "whsec"
	sigCh := make(chan struct {
		sig, ts, body string
	}, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sigCh <- struct {
			sig, ts, body string
		}{r.Header.Get("X-Syncwell-Signature"), r.Header.Get("X-Syncwell-Timestamp"), string(body)}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	d := NewWebhookDispatcher(WebhookConfig{
		URLs:         []string{srv.URL},
		Secret:       secret,
		AllowPrivate: true,
	})
	defer d.Close(context.Background())
	d.Emit("room.created", "r1", "local", nil)

	var got struct{ sig, ts, body string }
	select {
	case got = <-sigCh:
	case <-time.After(2 * time.Second):
		t.Fatal("webhook not delivered")
	}

	if got.ts == "" {
		t.Fatal("missing X-Syncwell-Timestamp header")
	}
	if len(got.sig) <= len("v1=") || got.sig[:3] != "v1=" {
		t.Fatalf("signature not v1-prefixed: %q", got.sig)
	}
	ts, err := strconv.ParseInt(got.ts, 10, 64)
	if err != nil {
		t.Fatalf("bad timestamp: %v", err)
	}
	if delta := time.Since(time.Unix(ts, 0)); delta > time.Minute || delta < -time.Minute {
		t.Fatalf("timestamp not near now: %v", delta)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v1:" + got.ts + ":"))
	mac.Write([]byte(got.body))
	want := "v1=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(got.sig), []byte(want)) {
		t.Fatalf("signature mismatch\n got: %s\nwant: %s", got.sig, want)
	}

	// A body tamper must break the signature.
	mac.Reset()
	mac.Write([]byte("v1:" + got.ts + ":"))
	mac.Write([]byte(got.body + "x"))
	if hmac.Equal([]byte(got.sig), []byte("v1="+hex.EncodeToString(mac.Sum(nil)))) {
		t.Fatal("tampered body produced a matching signature")
	}
}
