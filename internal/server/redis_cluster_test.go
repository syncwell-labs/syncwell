package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"syncwell/internal/crdt"
)

func startTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	if addr := os.Getenv("SYNCWELL_TEST_REDIS_ADDR"); addr != "" {
		client := redis.NewClient(&redis.Options{Addr: addr})
		if err := client.FlushDB(context.Background()).Err(); err != nil {
			_ = client.Close()
			t.Fatalf("prepare test Redis database: %v", err)
		}
		t.Cleanup(func() { _ = client.Close() })
		return client
	}
	bin, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server is not installed")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "--bind", "127.0.0.1", "--port", strconv.Itoa(port), "--save", "", "--appendonly", "no", "--protected-mode", "no")
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
	})
	client := redis.NewClient(&redis.Options{Addr: net.JoinHostPort("127.0.0.1", strconv.Itoa(port))})
	t.Cleanup(func() { _ = client.Close() })
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if client.Ping(context.Background()).Err() == nil {
			return client
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("redis-server did not become ready")
	return nil
}

func TestRedisRoomLogFencingReplayAndSnapshotTrim(t *testing.T) {
	client := startTestRedis(t)
	ctx := context.Background()
	log, err := NewRedisRoomLog(client)
	if err != nil {
		t.Fatal(err)
	}
	room := "private-room-name-should-not-reach-redis"
	lease, err := log.Acquire(ctx, room, "node-a", "nonce-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log.Acquire(ctx, room, "node-b", "nonce-b"); err == nil {
		t.Fatal("second owner acquired a live lease")
	}
	key := "r"
	reg := crdt.Register{Value: json.RawMessage(`"value"`), TS: crdt.Timestamp{Counter: 1, Node: "node-a"}}
	firstID, err := log.Append(ctx, lease, ClusterEvent{Room: room, Kind: "op", From: "node-a", Key: key, Register: &reg})
	if err != nil {
		t.Fatal(err)
	}
	values, err := client.XRange(ctx, redisRoomPrefix(room)+"events", "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(redisRoomPrefix(room), room) || strings.Contains(values[0].Values["payload"].(string), room) {
		t.Fatal("raw room ID leaked to Redis key or event payload")
	}
	events, last, err := log.Replay(ctx, room, "", 10)
	if err != nil || len(events) != 1 || events[0].Room != room || last != firstID {
		t.Fatalf("replay = %#v, %q, %v", events, last, err)
	}
	secondID, err := log.Append(ctx, lease, ClusterEvent{Room: room, Kind: "presence", From: "node-a", ClientID: "client"})
	if err != nil {
		t.Fatal(err)
	}
	state := map[string]json.RawMessage{"r": json.RawMessage(`{"value":"value"}`)}
	if err := log.SaveSnapshot(ctx, lease, room, firstID, state); err != nil {
		t.Fatal(err)
	}
	events, last, err = log.Replay(ctx, room, firstID, 10)
	if err != nil || len(events) != 1 || events[0].Kind != "presence" || last != secondID {
		t.Fatalf("post-snapshot replay = %#v, %q, %v", events, last, err)
	}
	offset, loaded, err := log.LoadSnapshot(ctx, room)
	if err != nil || offset != firstID || len(loaded) != 1 {
		t.Fatalf("snapshot = %q %#v, %v", offset, loaded, err)
	}
	if err := log.Release(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if err := log.Renew(ctx, lease); err == nil {
		t.Fatal("renew succeeded after lease release")
	}
	if _, err := log.Append(ctx, lease, ClusterEvent{Room: room, Kind: "op", From: "node-a"}); err == nil {
		t.Fatal("stale lease appended an event")
	}
	if err := log.SaveSnapshot(ctx, lease, room, secondID, state); err == nil {
		t.Fatal("stale lease committed or trimmed a snapshot")
	}
}

func TestServeRouteUsesRedisLeaseOwner(t *testing.T) {
	client := startTestRedis(t)
	log, err := NewRedisRoomLog(client)
	if err != nil {
		t.Fatal(err)
	}
	room := "route-room"
	if _, err := log.Acquire(context.Background(), room, "node-b", "nonce"); err != nil {
		t.Fatal(err)
	}
	h := NewHub(Config{
		NodeID:       "node-a",
		RedisRoomLog: log,
		NodeAdvertise: map[string]string{
			"node-a": "wss://a.example/ws",
			"node-b": "wss://b.example/ws",
		},
	})
	rr := httptest.NewRecorder()
	h.ServeRoute(rr, httptest.NewRequest("GET", "/route?room="+room, nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"node":"node-b"`) || !strings.Contains(rr.Body.String(), `"url":"wss://b.example/ws"`) {
		t.Fatalf("route response = %d %s", rr.Code, rr.Body.String())
	}
}

func TestRedisRoomLogPurgeRetainsFenceCounter(t *testing.T) {
	client := startTestRedis(t)
	log, err := NewRedisRoomLog(client)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	room := "purged-room"
	first, err := log.Acquire(ctx, room, "node-a", "nonce-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append(ctx, first, ClusterEvent{Room: room, Kind: "presence", From: "node-a"}); err != nil {
		t.Fatal(err)
	}
	if err := log.Purge(ctx, first, room); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(ctx, redisRoomPrefix(room)+"owner").Result(); err == nil {
		t.Fatal("purge left an active lease")
	}
	if n, err := client.XLen(ctx, redisRoomPrefix(room)+"events").Result(); err != nil || n != 0 {
		t.Fatalf("purge did not remove the stream: %v", err)
	}
	second, err := log.Acquire(ctx, room, "node-a", "nonce-a")
	if err != nil {
		t.Fatal(err)
	}
	if second.epoch <= first.epoch {
		t.Fatalf("fencing epoch reset after purge: before=%d after=%d", first.epoch, second.epoch)
	}
	_ = log.Release(ctx, second)
}

func TestRedisModeRoomIdentifiersAreHashedForLogsAndTraces(t *testing.T) {
	room := "sensitive-customer-room"
	log := &RedisRoomLog{}
	h := NewHub(Config{RedisRoomLog: log})
	r := &Room{id: room, redisLog: log}
	for _, label := range []string{h.logRoomID(room), r.logRoomID()} {
		if label == room || label != redisRoomHash(room) {
			t.Fatalf("Redis mode room log label = %q", label)
		}
	}
}

func TestRedisOwnedRoomFencesPeerAndRestoresAfterRelease(t *testing.T) {
	client := startTestRedis(t)
	log, err := NewRedisRoomLog(client)
	if err != nil {
		t.Fatal(err)
	}
	advertise := map[string]string{"node-a": "wss://a.example/ws", "node-b": "wss://b.example/ws"}
	hA := NewHub(Config{NodeID: "node-a", RedisRoomLog: log, NodeAdvertise: advertise})
	hB := NewHub(Config{NodeID: "node-b", RedisRoomLog: log, NodeAdvertise: advertise})
	room := "shared-room"
	r, err := hA.room(room)
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan map[string]crdt.Register, 1)
	r.cmds <- command{kind: "export", export: ready}
	<-ready // startup replay has completed before this serialized command
	reg := crdt.Register{Value: json.RawMessage(`"restored"`), TS: crdt.Timestamp{Counter: 1, Node: "writer"}}
	if err := hA.ingestClusterEvent(ClusterEvent{Room: room, Kind: "op", From: "writer", Key: "title", Register: &reg}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		events, _, replayErr := log.Replay(context.Background(), room, "", 10)
		if replayErr == nil && len(events) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	req := httptest.NewRequest(http.MethodGet, "/ws?room="+room, nil)
	rr := httptest.NewRecorder()
	hB.ServeWS(rr, req)
	if rr.Code != http.StatusMisdirectedRequest || rr.Header().Get("X-Syncwell-Owner") != "node-a" || rr.Header().Get("Location") != advertise["node-a"] {
		t.Fatalf("non-owner websocket response = %d %v", rr.Code, rr.Header())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	if err := hA.Shutdown(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	rB, err := hB.room(room)
	if err != nil {
		t.Fatal(err)
	}
	state := make(chan map[string]crdt.Register, 1)
	rB.cmds <- command{kind: "export", export: state}
	restored := <-state
	if !restored["title"].Equal(reg) {
		t.Fatalf("re-homed state = %#v", restored)
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := hB.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}
