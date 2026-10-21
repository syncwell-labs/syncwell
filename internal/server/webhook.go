package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// WebhookConfig configures room-milestone webhooks. The zero value disables
// them. Delivery is asynchronous and bounded: a slow consumer can never block
// a room's single-owner goroutine.
type WebhookConfig struct {
	URLs    []string
	Secret  string
	Timeout time.Duration
	Queue   int
}

// WebhookEvent is the JSON payload POSTed to every configured URL.
type WebhookEvent struct {
	Event     string         `json:"event"` // room.created | room.persisted | room.evicted
	Room      string         `json:"room"`
	Node      string         `json:"node"`
	Timestamp time.Time      `json:"timestamp"`
	Data      map[string]any `json:"data,omitempty"`
}

// WebhookDispatcher fan-outs milestone events to configured HTTP endpoints.
type WebhookDispatcher struct {
	urls   []string
	secret string
	client *http.Client
	queue  chan WebhookEvent
	done   chan struct{}
	wg     sync.WaitGroup

	delivered atomic.Uint64
	failed    atomic.Uint64
	dropped   atomic.Uint64
}

// NewWebhookDispatcher starts delivery workers. It returns nil when no URLs
// are configured.
func NewWebhookDispatcher(cfg WebhookConfig) *WebhookDispatcher {
	urls := make([]string, 0, len(cfg.URLs))
	for _, u := range cfg.URLs {
		if u = strings.TrimSpace(u); u != "" {
			urls = append(urls, u)
		}
	}
	if len(urls) == 0 {
		return nil
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	queue := cfg.Queue
	if queue <= 0 {
		queue = 256
	}
	d := &WebhookDispatcher{
		urls:   urls,
		secret: cfg.Secret,
		client: &http.Client{Timeout: timeout},
		queue:  make(chan WebhookEvent, queue),
		done:   make(chan struct{}),
	}
	workers := 2
	if len(urls) > workers {
		workers = len(urls)
	}
	for i := 0; i < workers; i++ {
		d.wg.Add(1)
		go d.worker()
	}
	return d
}

// Emit queues an event for delivery. If the queue is saturated the event is
// dropped and counted rather than delaying the caller.
func (d *WebhookDispatcher) Emit(event, room, node string, data map[string]any) {
	if d == nil {
		return
	}
	ev := WebhookEvent{Event: event, Room: room, Node: node, Timestamp: time.Now().UTC(), Data: data}
	select {
	case d.queue <- ev:
	case <-d.done:
	default:
		d.dropped.Add(1)
		LogEvent("webhook_dropped", map[string]any{"event": event, "room": room})
	}
}

func (d *WebhookDispatcher) worker() {
	defer d.wg.Done()
	for {
		select {
		case <-d.done:
			return
		case ev := <-d.queue:
			d.deliver(ev)
		}
	}
}

func (d *WebhookDispatcher) deliver(ev WebhookEvent) {
	body, err := json.Marshal(ev)
	if err != nil {
		d.failed.Add(1)
		return
	}
	for _, url := range d.urls {
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			d.failed.Add(1)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "syncwell-webhook/1")
		req.Header.Set("X-Syncwell-Event", ev.Event)
		if d.secret != "" {
			// Sign "v1:<unix-seconds>:<body>" and expose the timestamp in its
			// own header so receivers can bound freshness: without it a
			// captured request could be replayed forever.
			ts := strconv.FormatInt(ev.Timestamp.Unix(), 10)
			mac := hmac.New(sha256.New, []byte(d.secret))
			mac.Write([]byte("v1:" + ts + ":"))
			mac.Write(body)
			req.Header.Set("X-Syncwell-Timestamp", ts)
			req.Header.Set("X-Syncwell-Signature", "v1="+hex.EncodeToString(mac.Sum(nil)))
		}
		resp, err := d.client.Do(req)
		if err != nil {
			d.failed.Add(1)
			LogEvent("webhook_error", map[string]any{"event": ev.Event, "room": ev.Room, "error": err.Error()})
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			d.failed.Add(1)
			LogEvent("webhook_error", map[string]any{"event": ev.Event, "room": ev.Room, "status": resp.StatusCode})
			continue
		}
		d.delivered.Add(1)
	}
}

// Metrics returns delivery counters for /metrics.
func (d *WebhookDispatcher) Metrics() (delivered, failed, dropped uint64) {
	if d == nil {
		return 0, 0, 0
	}
	return d.delivered.Load(), d.failed.Load(), d.dropped.Load()
}

// Close stops accepting events and waits for in-flight deliveries until ctx is
// done. Pending queued events are dropped deliberately: shutdown must be fast.
func (d *WebhookDispatcher) Close(ctx context.Context) {
	if d == nil {
		return
	}
	select {
	case <-d.done:
		return
	default:
		close(d.done)
	}
	wait := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(wait)
	}()
	select {
	case <-wait:
	case <-ctx.Done():
	}
}
