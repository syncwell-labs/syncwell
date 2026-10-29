package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"syncwell/internal/crdt"
)

type fakeRedisClock struct {
	mu     sync.Mutex
	now    time.Time
	ticker *fakeRedisTicker
}

func newFakeRedisClock() *fakeRedisClock {
	return &fakeRedisClock{now: time.Unix(1_800_000_000, 0)}
}

func (c *fakeRedisClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeRedisClock) NewTicker(time.Duration) redisTicker {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeRedisTicker{clock: c, ch: make(chan time.Time, 16)}
	c.ticker = t
	return t
}

func (c *fakeRedisClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now, ticker := c.now, c.ticker
	c.mu.Unlock()
	if ticker != nil {
		ticker.ch <- now
	}
}

type fakeRedisTicker struct {
	clock *fakeRedisClock
	ch    chan time.Time
	mu    sync.Mutex
	stop  bool
}

func (t *fakeRedisTicker) Chan() <-chan time.Time { return t.ch }
func (t *fakeRedisTicker) Stop() {
	t.mu.Lock()
	t.stop = true
	t.mu.Unlock()
}

type fakeStreamEntry struct {
	id    uint64
	event ClusterEvent
}

type fakeRedisRoomStore struct {
	mu       sync.Mutex
	clock    *fakeRedisClock
	lease    RedisLease
	expires  time.Time
	epoch    uint64
	sequence uint64
	events   []fakeStreamEntry
	offset   uint64
	state    map[string]json.RawMessage
	renews   int
}

func (f *fakeRedisRoomStore) Acquire(_ context.Context, room, node, nonce string) (RedisLease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lease.value != "" && f.clock.Now().Before(f.expires) {
		return RedisLease{}, errRedisLeaseHeld
	}
	f.epoch++
	f.lease = RedisLease{room: room, value: fmt.Sprintf("%s:%s:%d", node, nonce, f.epoch), epoch: f.epoch}
	f.expires = f.clock.Now().Add(redisLeaseTTL)
	return f.lease, nil
}

func (f *fakeRedisRoomStore) Renew(_ context.Context, lease RedisLease) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renews++
	if f.lease.value != lease.value || !f.clock.Now().Before(f.expires) {
		return errRedisFenceLost
	}
	f.expires = f.clock.Now().Add(redisLeaseTTL)
	return nil
}

func (f *fakeRedisRoomStore) Release(_ context.Context, lease RedisLease) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lease.value != lease.value {
		return errRedisFenceLost
	}
	f.lease = RedisLease{}
	return nil
}

func (f *fakeRedisRoomStore) Owner(_ context.Context, room string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lease.room != room || f.lease.value == "" || !f.clock.Now().Before(f.expires) {
		return "", errors.New("lease absent")
	}
	for i, ch := range f.lease.value {
		if ch == ':' {
			return f.lease.value[:i], nil
		}
	}
	return "", errors.New("invalid lease")
}

func (f *fakeRedisRoomStore) Append(_ context.Context, lease RedisLease, event ClusterEvent) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lease.value != lease.value || !f.clock.Now().Before(f.expires) {
		return "", errRedisFenceLost
	}
	f.sequence++
	f.events = append(f.events, fakeStreamEntry{id: f.sequence, event: event})
	return fmt.Sprintf("%d-0", f.sequence), nil
}

func (f *fakeRedisRoomStore) Replay(_ context.Context, room, after string, count int64) ([]ClusterEvent, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var offset uint64
	if after != "" {
		id := after
		if len(id) > 2 && id[len(id)-2:] == "-0" {
			id = id[:len(id)-2]
		}
		parsed, err := strconv.ParseUint(id, 10, 64)
		if err != nil {
			return nil, "", err
		}
		offset = parsed
	}
	var events []ClusterEvent
	var last string
	for _, entry := range f.events {
		if entry.id <= offset {
			continue
		}
		if int64(len(events)) == count {
			break
		}
		event := entry.event
		event.Room = room
		events = append(events, event)
		last = fmt.Sprintf("%d-0", entry.id)
	}
	return events, last, nil
}

func (f *fakeRedisRoomStore) SaveSnapshot(_ context.Context, lease RedisLease, room, offset string, state map[string]json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lease.value != lease.value || room != lease.room || !f.clock.Now().Before(f.expires) {
		return errRedisFenceLost
	}
	parsed, err := strconv.ParseUint(offset[:len(offset)-2], 10, 64)
	if err != nil {
		return err
	}
	f.offset = parsed
	f.state = state
	remaining := f.events[:0]
	for _, entry := range f.events {
		if entry.id > parsed {
			remaining = append(remaining, entry)
		}
	}
	f.events = remaining
	return nil
}

func (f *fakeRedisRoomStore) Purge(_ context.Context, lease RedisLease, room string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lease.value != lease.value || room != lease.room {
		return errRedisFenceLost
	}
	f.events = nil
	f.state = nil
	f.lease = RedisLease{}
	return nil
}

func (f *fakeRedisRoomStore) LoadSnapshot(_ context.Context, room string) (string, map[string]json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.offset == 0 {
		return "", nil, nil
	}
	return fmt.Sprintf("%d-0", f.offset), f.state, nil
}

func TestRedisLeaseRenewalAndExpiryWithFakeClock(t *testing.T) {
	clock := newFakeRedisClock()
	backend := &fakeRedisRoomStore{clock: clock}
	lease, err := backend.Acquire(context.Background(), "room", "node-a", "nonce")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Acquire(context.Background(), "room", "node-b", "nonce-b"); !errors.Is(err, errRedisLeaseHeld) {
		t.Fatalf("second owner acquisition error = %v, want lease held", err)
	}
	h := NewHub(Config{redisTickerFactory: clock.NewTicker})
	r := &Room{hub: h, id: "room", redisLog: backend, redisLease: lease, cmds: make(chan command, 1), done: make(chan struct{})}
	done := make(chan struct{})
	go func() { r.renewRedisLease(); close(done) }()
	waitRenewals := func(want int) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			backend.mu.Lock()
			got := backend.renews
			backend.mu.Unlock()
			if got >= want {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("lease renewals did not reach %d", want)
	}
	deadline := time.Now().Add(time.Second)
	for {
		clock.mu.Lock()
		ready := clock.ticker != nil
		clock.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("renewal loop did not create its fake ticker")
		}
		time.Sleep(time.Millisecond)
	}
	clock.Advance(5 * time.Second)
	waitRenewals(1)
	clock.Advance(5 * time.Second)
	waitRenewals(2)
	clock.Advance(15 * time.Second) // now exactly at the renewed lease deadline
	waitRenewals(3)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("renewal loop did not stop after lease expiry")
	}
	if !r.redisLost.Load() || h.redisLeaseLosses.Load() != 1 {
		t.Fatalf("lease loss state = %v, metric=%d", r.redisLost.Load(), h.redisLeaseLosses.Load())
	}
	successor, err := backend.Acquire(context.Background(), "room", "node-b", "nonce-b")
	if err != nil {
		t.Fatalf("successor did not acquire after lease expiry: %v", err)
	}
	if successor.epoch <= lease.epoch {
		t.Fatalf("successor fence epoch = %d, want greater than stale epoch %d", successor.epoch, lease.epoch)
	}
	if _, err := backend.Append(context.Background(), lease, ClusterEvent{Room: "room", Kind: "op"}); !errors.Is(err, errRedisFenceLost) {
		t.Fatalf("stale owner append after successor acquisition = %v, want fence lost", err)
	}
}

func TestRedisReplayPaginationUsesExactOffsetsWithFakeStore(t *testing.T) {
	clock := newFakeRedisClock()
	backend := &fakeRedisRoomStore{clock: clock}
	lease, err := backend.Acquire(context.Background(), "room", "node-a", "nonce")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1001; i++ {
		value, _ := json.Marshal(fmt.Sprintf("value-%d", i))
		reg := crdt.Register{Value: value, TS: crdt.Timestamp{Counter: uint64(i + 1), Node: "writer"}}
		if _, err := backend.Append(context.Background(), lease, ClusterEvent{Room: "room", Kind: "op", From: "writer", Key: fmt.Sprintf("key-%04d", i), Register: &reg}); err != nil {
			t.Fatal(err)
		}
	}
	h := NewHub(Config{RedisRoomLog: backend})
	r := &Room{id: "room", hub: h, redisLog: backend, redisLease: lease, doc: crdt.NewLWWMap(), clock: crdt.NewClock("server:room"), limits: Limits{}.withDefaults()}
	if err := r.loadRedisState(); err != nil {
		t.Fatal(err)
	}
	if got := len(r.doc.Snapshot()); got != 1001 {
		t.Fatalf("replayed register count = %d, want 1001", got)
	}
	if r.redisOffset != "1001-0" {
		t.Fatalf("replay offset = %q, want 1001-0", r.redisOffset)
	}
}
