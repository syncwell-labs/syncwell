package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const redisLeaseTTL = 15 * time.Second

var (
	errRedisLeaseHeld = errors.New("redis room lease is held")
	errRedisFenceLost = errors.New("redis room lease fence lost")
)

// RedisRoomLog stores the durable per-room event stream and fenced owner lease.
// Raw room IDs are used only to derive the key hash and are never sent to Redis.
type RedisRoomLog struct {
	client redis.UniversalClient
}

// RedisRoomStore is the room-ownership and replay contract consumed by Hub.
// Keeping Redis commands behind this interface lets unit tests control lease
// time and replay offsets without changing the production Redis scripts.
type RedisRoomStore interface {
	Acquire(context.Context, string, string, string) (RedisLease, error)
	Renew(context.Context, RedisLease) error
	Release(context.Context, RedisLease) error
	Owner(context.Context, string) (string, error)
	Append(context.Context, RedisLease, ClusterEvent) (string, error)
	Replay(context.Context, string, string, int64) ([]ClusterEvent, string, error)
	SaveSnapshot(context.Context, RedisLease, string, string, map[string]json.RawMessage) error
	Purge(context.Context, RedisLease, string) error
	LoadSnapshot(context.Context, string) (string, map[string]json.RawMessage, error)
}

// NewRedisRoomLog creates a Redis-backed room log. The caller owns client.
func NewRedisRoomLog(client redis.UniversalClient) (*RedisRoomLog, error) {
	if client == nil {
		return nil, errors.New("redis client is required")
	}
	return &RedisRoomLog{client: client}, nil
}

func redisRoomPrefix(room string) string {
	return "sw:{" + redisRoomHash(room) + "}:"
}

func redisRoomHash(room string) string {
	sum := sha256.Sum256([]byte(room))
	return hex.EncodeToString(sum[:])
}

func validRedisNodeID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, ch := range id {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '.' || ch == '_' || ch == '-') {
			return false
		}
	}
	return true
}

type RedisLease struct {
	room  string
	value string
	epoch uint64
}

var acquireRoomLease = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 then return false end
local epoch = redis.call('INCR', KEYS[2])
local value = ARGV[1] .. ':' .. tostring(epoch)
redis.call('SET', KEYS[1], value, 'PX', ARGV[2])
return {value, tostring(epoch)}
`)

var renewRoomLease = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
return redis.call('PEXPIRE', KEYS[1], ARGV[2])
`)

var releaseRoomLease = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
return redis.call('DEL', KEYS[1])
`)

var appendRoomEvent = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return redis.error_reply('FENCE_LOST') end
local fence = redis.call('GET', KEYS[2])
if fence ~= ARGV[2] then return redis.error_reply('FENCE_LOST') end
return redis.call('XADD', KEYS[3], '*', 'payload', ARGV[3], 'fence', ARGV[2])
`)

var saveRoomSnapshot = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return redis.error_reply('FENCE_LOST') end
if redis.call('GET', KEYS[2]) ~= ARGV[2] then return redis.error_reply('FENCE_LOST') end
redis.call('SET', KEYS[3], ARGV[3])
return redis.call('XTRIM', KEYS[4], 'MINID', '~', ARGV[4])
`)

var purgeRoomState = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return redis.error_reply('FENCE_LOST') end
if redis.call('GET', KEYS[2]) ~= ARGV[2] then return redis.error_reply('FENCE_LOST') end
return redis.call('DEL', KEYS[1], KEYS[3], KEYS[4])
`)

// Acquire atomically increments a persistent fencing epoch and creates a TTL lease.
func (l *RedisRoomLog) Acquire(ctx context.Context, room, nodeID, nonce string) (RedisLease, error) {
	if !validRedisNodeID(nodeID) || nonce == "" || strings.Contains(nonce, ":") {
		return RedisLease{}, errors.New("invalid Redis lease identity")
	}
	p := redisRoomPrefix(room)
	res, err := acquireRoomLease.Run(ctx, l.client, []string{p + "owner", p + "fence"}, nodeID+":"+nonce, redisLeaseTTL.Milliseconds()).Slice()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return RedisLease{}, errRedisLeaseHeld
		}
		return RedisLease{}, err
	}
	if len(res) != 2 {
		return RedisLease{}, errRedisLeaseHeld
	}
	value, ok := res[0].(string)
	if !ok {
		return RedisLease{}, errors.New("invalid redis lease response")
	}
	epochText, ok := res[1].(string)
	if !ok {
		return RedisLease{}, errors.New("invalid redis fence response")
	}
	epoch, err := strconv.ParseUint(epochText, 10, 64)
	if err != nil {
		return RedisLease{}, fmt.Errorf("parse redis fencing epoch: %w", err)
	}
	return RedisLease{room: room, value: value, epoch: epoch}, nil
}

// Renew extends the lease only if the complete owner token still matches.
func (l *RedisRoomLog) Renew(ctx context.Context, lease RedisLease) error {
	key := redisRoomPrefix(lease.room) + "owner"
	n, err := renewRoomLease.Run(ctx, l.client, []string{key}, lease.value, redisLeaseTTL.Milliseconds()).Int()
	if err != nil {
		return err
	}
	if n != 1 {
		return errRedisFenceLost
	}
	return nil
}

// Release removes only the lease represented by lease; a stale process cannot
// release a successor's lease.
func (l *RedisRoomLog) Release(ctx context.Context, lease RedisLease) error {
	key := redisRoomPrefix(lease.room) + "owner"
	n, err := releaseRoomLease.Run(ctx, l.client, []string{key}, lease.value).Int()
	if err != nil {
		return err
	}
	if n != 1 {
		return errRedisFenceLost
	}
	return nil
}

// Owner returns the configured node ID from the current lease value.
func (l *RedisRoomLog) Owner(ctx context.Context, room string) (string, error) {
	value, err := l.client.Get(ctx, redisRoomPrefix(room)+"owner").Result()
	if err != nil {
		return "", err
	}
	sep := strings.IndexByte(value, ':')
	if sep <= 0 {
		return "", errors.New("invalid redis owner value")
	}
	return value[:sep], nil
}

// Append persists a fenced event and returns its Redis stream ID. The raw room
// ID is deliberately removed from the serialized payload.
func (l *RedisRoomLog) Append(ctx context.Context, lease RedisLease, ev ClusterEvent) (string, error) {
	if lease.room == "" || ev.Room != lease.room {
		return "", errors.New("event room does not match fenced lease")
	}
	ev.Room = ""
	payload, err := json.Marshal(ev)
	if err != nil {
		return "", err
	}
	p := redisRoomPrefix(lease.room)
	id, err := appendRoomEvent.Run(ctx, l.client, []string{p + "owner", p + "fence", p + "events"}, lease.value, strconv.FormatUint(lease.epoch, 10), payload).Text()
	if err != nil {
		if strings.Contains(err.Error(), "FENCE_LOST") {
			return "", errRedisFenceLost
		}
		return "", err
	}
	return id, nil
}

// Replay returns events strictly after offset in stream order. An empty offset
// starts at the beginning. Room identity is reconstructed locally and is never
// stored in the stream payload.
func (l *RedisRoomLog) Replay(ctx context.Context, room, offset string, count int64) ([]ClusterEvent, string, error) {
	p := redisRoomPrefix(room)
	start := "-"
	if offset != "" {
		start = "(" + offset
	}
	entries, err := l.client.XRangeN(ctx, p+"events", start, "+", count).Result()
	if err != nil {
		return nil, "", err
	}
	out := make([]ClusterEvent, 0, len(entries))
	last := ""
	for _, entry := range entries {
		payload, ok := entry.Values["payload"].(string)
		if !ok {
			return nil, "", fmt.Errorf("redis event %s has no payload", entry.ID)
		}
		var ev ClusterEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return nil, "", fmt.Errorf("decode redis event %s: %w", entry.ID, err)
		}
		ev.Room = room
		out = append(out, ev)
		last = entry.ID
	}
	return out, last, nil
}

type redisRoomSnapshot struct {
	Offset string                     `json:"offset"`
	State  map[string]json.RawMessage `json:"state"`
}

// SaveSnapshot atomically commits a compacted state and its exact replay offset,
// then trims only events through that offset. The room actor must ensure state
// corresponds exactly to offset while calling this method.
func (l *RedisRoomLog) SaveSnapshot(ctx context.Context, lease RedisLease, room, offset string, state map[string]json.RawMessage) error {
	if lease.room != room {
		return errors.New("snapshot room does not match fenced lease")
	}
	if offset == "" {
		return errors.New("snapshot stream offset is required")
	}
	snapshot, err := json.Marshal(redisRoomSnapshot{Offset: offset, State: state})
	if err != nil {
		return err
	}
	p := redisRoomPrefix(room)
	_, err = saveRoomSnapshot.Run(ctx, l.client, []string{p + "owner", p + "fence", p + "snapshot", p + "events"}, lease.value, strconv.FormatUint(lease.epoch, 10), snapshot, offset).Result()
	if err != nil && strings.Contains(err.Error(), "FENCE_LOST") {
		return errRedisFenceLost
	}
	return err
}

// Purge removes a room's lease, event stream, and snapshot only for the current
// fencing token. The fence counter is retained so purging cannot reset epochs.
func (l *RedisRoomLog) Purge(ctx context.Context, lease RedisLease, room string) error {
	if lease.room != room {
		return errors.New("purge room does not match fenced lease")
	}
	p := redisRoomPrefix(room)
	_, err := purgeRoomState.Run(ctx, l.client, []string{p + "owner", p + "fence", p + "events", p + "snapshot"}, lease.value, strconv.FormatUint(lease.epoch, 10)).Result()
	if err != nil && strings.Contains(err.Error(), "FENCE_LOST") {
		return errRedisFenceLost
	}
	return err
}

// LoadSnapshot returns the last committed snapshot and offset, if present.
func (l *RedisRoomLog) LoadSnapshot(ctx context.Context, room string) (string, map[string]json.RawMessage, error) {
	data, err := l.client.Get(ctx, redisRoomPrefix(room)+"snapshot").Bytes()
	if errors.Is(err, redis.Nil) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	var snapshot redisRoomSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return "", nil, err
	}
	return snapshot.Offset, snapshot.State, nil
}
