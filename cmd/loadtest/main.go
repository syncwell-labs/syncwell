// Command loadtest opens many websocket clients across rooms, publishes a
// timestamped register per room, and reports connection success, sync latency,
// and throughput. It is a load/soak harness, not part of the server release.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"syncwell/internal/crdt"
	"syncwell/internal/server"
)

type sampleSet struct {
	mu   sync.Mutex
	data []float64
}

func (s *sampleSet) add(v float64) {
	s.mu.Lock()
	s.data = append(s.data, v)
	s.mu.Unlock()
}

func (s *sampleSet) snapshot() []float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]float64, len(s.data))
	copy(out, s.data)
	sort.Float64s(out)
	return out
}

func readRSSkb(pid int) int64 {
	if pid <= 0 {
		return -1
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if n, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
					return n
				}
			}
		}
	}
	return -1
}

func pct(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(float64(len(sorted)-1) * p / 100)
	return sorted[i]
}

func main() {
	base := flag.String("url", "ws://127.0.0.1:8080/ws", "websocket endpoint")
	clients := flag.Int("clients", 1000, "total clients")
	rooms := flag.Int("rooms", 10, "number of rooms")
	duration := flag.Duration("duration", 30*time.Second, "measured duration")
	warmup := flag.Duration("warmup", 3*time.Second, "warmup before measuring")
	interval := flag.Duration("interval", 100*time.Millisecond, "publish interval per room")
	secret := flag.String("secret", "", "room-token secret; mints a token per room")
	serverPID := flag.Int("server-pid", 0, "server PID for RSS sampling")
	flag.Parse()

	if *clients < *rooms {
		log.Fatal("clients must be >= rooms")
	}
	perRoom := *clients / *rooms
	if perRoom < 1 {
		log.Fatal("need at least one client per room")
	}

	samples := &sampleSet{}
	var connected, failed, published, received atomic.Uint64
	var connectedFirst sync.WaitGroup
	connectedFirst.Add(*clients)

	type pubState struct {
		conn    *websocket.Conn
		epoch   uint64
		counter uint64
		node    string
	}
	publishers := make([]*pubState, *rooms)
	for room := 0; room < *rooms; room++ {
		roomID := fmt.Sprintf("load-%d", room)
		node := fmt.Sprintf("lt-%d", room)
		for i := 0; i < perRoom; i++ {
			go func(roomID, node string, publisher bool) {
				u, err := url.Parse(*base)
				if err != nil {
					failed.Add(1)
					connectedFirst.Done()
					return
				}
				q := u.Query()
				q.Set("room", roomID)
				q.Set("client", node)
				if *secret != "" {
					q.Set("token", server.SignToken(*secret, roomID, time.Now().Add(time.Hour).Unix()))
				}
				u.RawQuery = q.Encode()
				conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
				if err != nil {
					failed.Add(1)
					connectedFirst.Done()
					return
				}
				connected.Add(1)

				// First frame is a snapshot; remember its epoch.
				_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
				_, first, err := conn.ReadMessage()
				if err != nil {
					failed.Add(1)
					_ = conn.Close()
					connectedFirst.Done()
					return
				}
				connectedFirst.Done()
				var snap server.Outbound
				_ = json.Unmarshal(first, &snap)
				_ = conn.SetReadDeadline(time.Time{})

				if publisher {
					publishers[room] = &pubState{conn: conn, epoch: snap.Epoch, node: node}
				}
				for {
					_, data, err := conn.ReadMessage()
					if err != nil {
						return
					}
					var msg server.Outbound
					if err := json.Unmarshal(data, &msg); err != nil {
						continue
					}
					if msg.Type != "op" || msg.Key != "lat" || msg.Register == nil {
						continue
					}
					var sent int64
					if err := json.Unmarshal(msg.Register.Value, &sent); err != nil || sent <= 0 {
						continue
					}
					if publisher {
						received.Add(1)
						continue
					}
					received.Add(1)
					samples.add(float64(time.Now().UnixNano()-sent) / 1e6)
				}
			}(roomID, node, i == 0)
		}
	}
	connectedFirst.Wait()
	if connected.Load() == 0 {
		log.Fatal("no clients connected")
	}
	fmt.Printf("connected=%d failed=%d rooms=%d clients/room=%d\n", connected.Load(), failed.Load(), *rooms, perRoom)
	if err := validateConnections(*clients, connected.Load(), failed.Load()); err != nil {
		log.Fatal(err)
	}

	// Warmup then discard samples to avoid startup noise.
	stop := make(chan struct{})
	var pubWG sync.WaitGroup
	pubWG.Add(*rooms)
	for room := 0; room < *rooms; room++ {
		p := publishers[room]
		if p == nil {
			pubWG.Done()
			continue
		}
		go func(p *pubState) {
			defer pubWG.Done()
			tick := time.NewTicker(*interval)
			defer tick.Stop()
			for {
				select {
				case <-tick.C:
					p.counter++
					reg := crdt.Register{
						Value: json.RawMessage(strconv.FormatInt(time.Now().UnixNano(), 10)),
						TS:    crdt.Timestamp{Counter: p.counter, Node: p.node},
					}
					msg := server.Inbound{Protocol: 1, Epoch: p.epoch, Type: "op", Key: "lat", Register: &reg}
					_ = p.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
					if err := p.conn.WriteJSON(msg); err != nil {
						return
					}
					published.Add(1)
				case <-stop:
					return
				}
			}
		}(p)
	}
	time.Sleep(*warmup)
	samples.mu.Lock()
	samples.data = nil
	samples.mu.Unlock()

	rssStart := readRSSkb(*serverPID)
	var rssMax atomic.Int64
	rssMax.Store(rssStart)
	rssDone := make(chan struct{})
	rssStopped := make(chan struct{})
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		defer close(rssStopped)
		for {
			select {
			case <-t.C:
				if rss := readRSSkb(*serverPID); rss > rssMax.Load() {
					rssMax.Store(rss)
				}
			case <-rssDone:
				return
			}
		}
	}()
	start := time.Now()
	time.Sleep(*duration)
	close(stop)
	pubWG.Wait()
	elapsed := time.Since(start)
	time.Sleep(500 * time.Millisecond)
	close(rssDone)
	<-rssStopped
	rssEnd := readRSSkb(*serverPID)

	all := samples.snapshot()
	mb := func(kb int64) string {
		if kb < 0 {
			return "n/a"
		}
		return fmt.Sprintf("%.1fMB", float64(kb)/1024)
	}
	fmt.Printf("published=%d received=%d elapsed=%s\n", published.Load(), received.Load(), elapsed.Round(time.Millisecond))
	fmt.Printf("latency_ms p50=%.3f p95=%.3f p99=%.3f max=%.3f\n", pct(all, 50), pct(all, 95), pct(all, 99), pct(all, 100))
	fmt.Printf("throughput_msg_s=%.0f rss_start=%s rss_max=%s rss_end=%s\n", float64(received.Load())/elapsed.Seconds(), mb(rssStart), mb(rssMax.Load()), mb(rssEnd))
	for _, r := range publishers {
		if r != nil {
			_ = r.conn.Close()
		}
	}
}

func validateConnections(requested int, connected, failed uint64) error {
	if connected != uint64(requested) {
		return fmt.Errorf("loadtest requires all requested clients to connect: connected=%d requested=%d failed=%d", connected, requested, failed)
	}
	return nil
}
