// Command syncwell runs the collaboration engine: a websocket sync endpoint at
// /ws plus an optional static file server for the demo app. It compiles to a
// single self-contained binary with no runtime dependencies.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"syncwell/internal/server"
	"syncwell/internal/store"
	"syncwell/internal/version"
)

func main() {
	addr := flag.String("addr", envOr("SYNCWELL_ADDR", ":8080"), "listen address")
	static := flag.String("static", "demo", "directory of static files to serve at / (skipped if missing)")
	sdk := flag.String("sdk", "sdk", "directory of the client SDK to serve at /sdk/ (skipped if missing)")
	data := flag.String("data", envOr("SYNCWELL_DATA", ""), "data directory for persistence (empty = in-memory)")
	compact := flag.Int("compact", 256, "winning writes between maintenance passes (tombstone GC + compaction)")
	secret := flag.String("secret", envOr("SYNCWELL_SECRET", ""), "HMAC secret for room tokens (empty = auth disabled)")
	adminToken := flag.String("admin-token", envOr("SYNCWELL_ADMIN_TOKEN", ""), "bearer token for operational endpoints like /stats (empty = open)")
	origins := flag.String("origins", envOr("SYNCWELL_ORIGINS", ""), "comma-separated allowed Origins (empty = allow any)")
	maxClients := flag.Int("max-clients", 0, "max clients per room (0 = unlimited)")
	maxRooms := flag.Int("max-rooms", 0, "max live rooms before new rooms are refused (0 = unlimited)")
	idleRoomTTL := flag.Duration("idle-room-ttl", 0, "evict empty rooms after this idle period (0 = default 60s, negative = disabled)")
	maxMessageBytes := flag.Int("max-message-bytes", 0, "max websocket message bytes (0 = default)")
	maxRoomIDBytes := flag.Int("max-room-id-bytes", 0, "max room id bytes (0 = default)")
	maxKeyBytes := flag.Int("max-key-bytes", 0, "max document key bytes (0 = default)")
	maxNodeBytes := flag.Int("max-node-bytes", 0, "max client node id bytes (0 = default)")
	maxValueBytes := flag.Int("max-value-bytes", 0, "max register JSON value bytes (0 = default)")
	maxPresenceBytes := flag.Int("max-presence-bytes", 0, "max presence JSON bytes (0 = default)")
	maxStateEntries := flag.Int("max-state-entries", 0, "max registers accepted in one merge frame (0 = default)")
	maxMessagesPerSecond := flag.Int("max-messages-per-second", 0, "max websocket messages per client per second (0 = default)")
	clusterPeers := flag.String("cluster-peers", envOr("SYNCWELL_CLUSTER_PEERS", ""), "comma-separated peer cluster event URLs (empty = local bus only)")
	clusterPath := flag.String("cluster-path", envOr("SYNCWELL_CLUSTER_PATH", "/cluster/event"), "HTTP path for cluster event posts")
	showVersion := flag.Bool("version", false, "print version and exit")
	mint := flag.String("mint", "", "issue a token for this room and exit (requires -secret)")
	mintSub := flag.String("mint-sub", "", "optional subject/principal id for -mint")
	mintCaps := flag.String("mint-caps", "read,write,presence", "comma-separated capabilities for -mint")
	mintJTI := flag.String("mint-jti", "", "optional token id for -mint")
	mintIAT := flag.Int64("mint-iat", 0, "optional issued-at unix timestamp for -mint (0 = now)")
	mintNBF := flag.Int64("mint-nbf", 0, "optional not-before unix timestamp for -mint (0 = mint time)")
	revokedJTIs := flag.String("revoked-jtis", envOr("SYNCWELL_REVOKED_JTIS", ""), "comma-separated revoked token JTIs")
	ttl := flag.Duration("ttl", 24*time.Hour, "token lifetime for -mint")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String())
		return
	}

	// Token-minting helper: `syncwell -secret S -mint room` prints a token.
	if *mint != "" {
		if *secret == "" {
			log.Fatal("-mint requires -secret")
		}
		iat := *mintIAT
		if iat == 0 {
			iat = time.Now().Unix()
		}
		nbf := *mintNBF
		if nbf == 0 {
			nbf = iat
		}
		fmt.Println(server.SignAccessTokenForSubject(*secret, *mint, *mintSub, time.Now().Add(*ttl).Unix(), strings.Split(*mintCaps, ","), *mintJTI, iat, nbf))
		return
	}

	var st store.Store
	if *data != "" {
		fs, err := store.Open(*data)
		if err != nil {
			log.Fatalf("open data dir %q: %v", *data, err)
		}
		st = fs
		server.LogEvent("persistence_enabled", map[string]any{"dir": *data})
	}

	var allowedOrigins []string
	if *origins != "" {
		allowedOrigins = strings.Split(*origins, ",")
	}
	var clusterPeerList []string
	if *clusterPeers != "" {
		clusterPeerList = strings.Split(*clusterPeers, ",")
	}
	revoked := map[string]struct{}{}
	if *revokedJTIs != "" {
		for _, jti := range strings.Split(*revokedJTIs, ",") {
			jti = strings.TrimSpace(jti)
			if jti != "" {
				revoked[jti] = struct{}{}
			}
		}
	}
	hub := server.NewHub(server.Config{
		Store:        st,
		CompactEvery: *compact,
		Secret:       *secret,
		AdminToken:   *adminToken,
		RevokedJTIs:  revoked,
		Origins:      allowedOrigins,
		MaxClients:   *maxClients,
		MaxRooms:     *maxRooms,
		IdleRoomTTL:  *idleRoomTTL,
		ClusterPeers: clusterPeerList,
		ClusterPath:  *clusterPath,
		Limits: server.Limits{
			MaxMessageBytes:      *maxMessageBytes,
			MaxRoomIDBytes:       *maxRoomIDBytes,
			MaxKeyBytes:          *maxKeyBytes,
			MaxNodeIDBytes:       *maxNodeBytes,
			MaxValueBytes:        *maxValueBytes,
			MaxPresenceBytes:     *maxPresenceBytes,
			MaxStateEntries:      *maxStateEntries,
			MaxMessagesPerSecond: *maxMessagesPerSecond,
		},
	})
	if *secret != "" {
		server.LogEvent("room_token_auth_enabled", map[string]any{"caps": "read,write,presence"})
	}
	if *adminToken != "" {
		server.LogEvent("admin_token_enabled", map[string]any{"endpoints": "/stats,/metrics"})
	}
	if len(clusterPeerList) > 0 {
		server.LogEvent("cluster_relay_enabled", map[string]any{"peers": len(clusterPeerList), "path": *clusterPath})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", hub.ServeWS)
	mux.HandleFunc("/stats", hub.ServeStats)
	mux.HandleFunc("/metrics", hub.ServeMetrics)
	mux.HandleFunc("/cluster/event", hub.ServeClusterEvent)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", hub.ServeReady)
	// Serve the client SDK at /sdk/ so the demo can import it with an absolute
	// path regardless of where the static root is.
	if _, err := os.Stat(*sdk); err == nil {
		mux.Handle("/sdk/", http.StripPrefix("/sdk/", http.FileServer(http.Dir(*sdk))))
	}
	if _, err := os.Stat(*static); err == nil {
		mux.Handle("/", http.FileServer(http.Dir(*static)))
		server.LogEvent("serving_demo", map[string]any{"dir": *static})
	}

	srv := &http.Server{Addr: *addr, Handler: mux}

	// Graceful shutdown: drain connections and flush persistence on SIGINT/TERM.
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		server.LogEvent("shutdown_start", map[string]any{"addr": *addr})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hub.Shutdown(ctx)
		_ = srv.Shutdown(ctx)
		if st != nil {
			_ = st.Close()
		}
	}()

	server.LogEvent("startup", map[string]any{"version": version.String(), "addr": *addr, "websocket": "/ws?room=ID"})
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
