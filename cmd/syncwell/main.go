// Command syncwell runs the collaboration engine: a websocket sync endpoint at
// /ws plus an optional static file server for the demo app. It compiles to a
// single self-contained binary with no runtime dependencies.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"syncwell/internal/server"
	"syncwell/internal/store"
	"syncwell/internal/version"
)

// envKeysSeen records the environment variables consulted for flag defaults,
// so -help can annotate each flag with its real env equivalent.
var envKeysSeen = map[string]bool{}

func noteEnv(key string) { envKeysSeen[key] = true }

// flagGroups orders -help output by task instead of alphabetically.
var flagGroups = []struct {
	title string
	names []string
}{
	{"Core / server", []string{"addr", "data", "origins", "insecure", "static", "sdk", "version"}},
	{"Auth & room tokens", []string{"secret", "secret-previous", "admin-token", "revoked-jtis", "oidc-issuer", "oidc-audience", "oidc-jwks", "oidc-room-claim", "oidc-caps-claim", "oidc-refresh"}},
	{"Rate limits & network trust", []string{"admin-rate", "admin-burst", "admin-max-failures", "admin-lockout", "admin-allowlist", "ws-rate", "cluster-rate", "cluster-allowlist", "trust-proxy", "proxy-hops"}},
	{"Capacity limits", []string{"max-clients", "max-rooms", "idle-room-ttl", "max-message-bytes", "max-room-id-bytes", "max-key-bytes", "max-node-bytes", "max-value-bytes", "max-presence-bytes", "max-state-entries", "max-messages-per-second", "compact"}},
	{"Durability & backup", []string{"fsync", "fsync-interval", "max-log-bytes", "backup", "restore", "verify-backup"}},
	{"Clustering", []string{"node-id", "node-peers", "cluster-peers", "cluster-path", "anti-entropy"}},
	{"Webhooks & tracing", []string{"webhook-urls", "webhook-secret", "webhook-timeout", "webhook-queue", "otel-endpoint", "otel-insecure", "otel-headers", "otel-sample-rate", "otel-service-name"}},
	{"Token minting (prints a token and exits)", []string{"mint", "mint-sub", "mint-caps", "mint-jti", "mint-iat", "mint-nbf", "ttl"}},
}

// usage replaces the stock flag dump with a grouped help screen that leads
// with the quickstart and shows env equivalents.
func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprint(out, `syncwell — real-time collaboration server

Usage:
  syncwell [flags]               start the server
  syncwell -secret S -mint ROOM  print a room token and exit

Quickstart:
  syncwell -addr 127.0.0.1:8080  run the demo at http://localhost:8080
                                 (loopback bind = no credentials needed)

Flags marked (env NAME) can also be set through that environment variable.

`)
	seen := map[string]bool{}
	for _, g := range flagGroups {
		var flags []*flag.Flag
		for _, name := range g.names {
			if f := flag.CommandLine.Lookup(name); f != nil {
				flags = append(flags, f)
				seen[name] = true
			}
		}
		if len(flags) == 0 {
			continue
		}
		fmt.Fprintf(out, "%s:\n", g.title)
		for _, f := range flags {
			printFlag(out, f)
		}
		fmt.Fprintln(out)
	}
	var rest []*flag.Flag
	flag.CommandLine.VisitAll(func(f *flag.Flag) {
		if !seen[f.Name] {
			rest = append(rest, f)
		}
	})
	if len(rest) > 0 {
		fmt.Fprintf(out, "Other flags:\n")
		for _, f := range rest {
			printFlag(out, f)
		}
	}
}

func printFlag(out io.Writer, f *flag.Flag) {
	typ, usageStr := flag.UnquoteUsage(f)
	if env := envVarForFlag(f.Name); env != "" {
		usageStr += " (env " + env + ")"
	}
	if def := defaultSuffix(f.DefValue); def != "" {
		usageStr += def
	}
	if typ != "" && typ != "bool" {
		fmt.Fprintf(out, "  -%s %s\n      %s\n", f.Name, typ, usageStr)
	} else {
		fmt.Fprintf(out, "  -%s\n      %s\n", f.Name, usageStr)
	}
}

// envVarForFlag derives the SYNCWELL_* name by convention and confirms the
// flag actually consults it (the env helpers register every key they read).
func envVarForFlag(name string) string {
	key := "SYNCWELL_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	if envKeysSeen[key] {
		return key
	}
	return ""
}

func defaultSuffix(def string) string {
	if def == "" || def == "false" || def == "0" || def == "0s" {
		return ""
	}
	return " (default " + def + ")"
}

func main() {
	flag.Usage = usage
	addr := flag.String("addr", envOr("SYNCWELL_ADDR", ":"+envOr("PORT", "8080")), "listen address")
	static := flag.String("static", envOr("SYNCWELL_STATIC", "examples/demos"), "directory of static files to serve at / (skipped if missing)")
	sdk := flag.String("sdk", envOr("SYNCWELL_SDK", "clients/javascript"), "directory of the client SDK to serve at /sdk/ (skipped if missing)")
	data := flag.String("data", envOr("SYNCWELL_DATA", ""), "data directory for persistence (empty = in-memory)")
	fsyncMode := flag.String("fsync", envOr("SYNCWELL_FSYNC", "batch"), "durability: os (fastest), batch (default), every (power-loss safe)")
	fsyncInterval := flag.Duration("fsync-interval", envDuration("SYNCWELL_FSYNC_INTERVAL", 5*time.Millisecond), "batch fsync interval")
	maxLogBytes := flag.Int64("max-log-bytes", envInt64("SYNCWELL_MAX_LOG_BYTES", 64<<20), "compact a room log once it reaches this many bytes (0 = unlimited)")
	backupDir := flag.String("backup", "", "copy -data into this directory with a checksummed manifest and exit")
	restoreDir := flag.String("restore", "", "verify this backup directory and restore it into -data, then exit")
	verifyBackupDir := flag.String("verify-backup", "", "verify a backup directory and exit")
	compact := flag.Int("compact", envInt("SYNCWELL_COMPACT", 256), "winning writes between maintenance passes (tombstone GC + compaction)")
	secret := flag.String("secret", envOr("SYNCWELL_SECRET", ""), "HMAC secret for room tokens (empty on a public bind = generated once)")
	secretPrevious := flag.String("secret-previous", envOr("SYNCWELL_SECRET_PREVIOUS", ""), "comma-separated old secrets still accepted for token verification (rotation)")
	adminToken := flag.String("admin-token", envOr("SYNCWELL_ADMIN_TOKEN", ""), "bearer token for operational endpoints like /stats (empty on a public bind = generated once)")
	oidcIssuer := flag.String("oidc-issuer", envOr("SYNCWELL_OIDC_ISSUER", ""), "OIDC issuer URL; enables external JWT room/admin auth")
	oidcAudience := flag.String("oidc-audience", envOr("SYNCWELL_OIDC_AUDIENCE", ""), "required aud claim when set")
	oidcJWKS := flag.String("oidc-jwks", envOr("SYNCWELL_OIDC_JWKS", ""), "JWKS URL (empty = discover from the issuer)")
	oidcRoomClaim := flag.String("oidc-room-claim", envOr("SYNCWELL_OIDC_ROOM_CLAIM", "syncwell_room"), "JWT claim holding the allowed room (or *)")
	oidcCapsClaim := flag.String("oidc-caps-claim", envOr("SYNCWELL_OIDC_CAPS_CLAIM", "syncwell_caps"), "JWT claim holding space/comma-separated capabilities")
	oidcRefresh := flag.Duration("oidc-refresh", envDuration("SYNCWELL_OIDC_REFRESH", 15*time.Minute), "how often to refresh JWKS signing keys")
	otelEndpoint := flag.String("otel-endpoint", envOr("SYNCWELL_OTEL_ENDPOINT", ""), "OTLP/HTTP trace endpoint (e.g. http://localhost:4318); empty = tracing disabled")
	otelInsecure := flag.Bool("otel-insecure", envBool("SYNCWELL_OTEL_INSECURE", false), "send OTLP over plaintext (implied by an http:// endpoint)")
	otelHeaders := flag.String("otel-headers", envOr("SYNCWELL_OTEL_HEADERS", ""), "comma-separated key=value headers for the OTLP exporter")
	otelSample := flag.Float64("otel-sample-rate", envFloat("SYNCWELL_OTEL_SAMPLE_RATE", 0.1), "fraction of traces to sample (0..1)")
	otelService := flag.String("otel-service-name", envOr("SYNCWELL_OTEL_SERVICE_NAME", "syncwell"), "service.name resource attribute")
	insecure := flag.Bool("insecure", envBool("SYNCWELL_INSECURE", false), "allow a public bind without generating credentials (loudly unsafe)")
	origins := flag.String("origins", envOr("SYNCWELL_ORIGINS", ""), "comma-separated allowed Origins (empty = allow any)")
	adminRate := flag.Int("admin-rate", envInt("SYNCWELL_ADMIN_RATE", 0), "max admin requests/min per IP (0 = default 120, negative = unlimited)")
	wsRate := flag.Int("ws-rate", envInt("SYNCWELL_WS_RATE", 0), "max websocket upgrade attempts/min per IP (0 = default 300, negative = unlimited)")
	adminBurst := flag.Int("admin-burst", envInt("SYNCWELL_ADMIN_BURST", 0), "admin burst per IP (0 = default 30)")
	adminMaxFailures := flag.Int("admin-max-failures", envInt("SYNCWELL_ADMIN_MAX_FAILURES", 0), "admin auth failures before lockout (0 = default 10)")
	adminLockout := flag.Duration("admin-lockout", envDuration("SYNCWELL_ADMIN_LOCKOUT", 0), "lockout after repeated admin auth failures (0 = default 5m)")
	adminAllowlist := flag.String("admin-allowlist", envOr("SYNCWELL_ADMIN_ALLOWLIST", ""), "comma-separated CIDRs allowed to reach /stats and /metrics (empty = any)")
	clusterRate := flag.Int("cluster-rate", envInt("SYNCWELL_CLUSTER_RATE", 0), "max cluster relay requests/min per IP (0 = default 6000, negative = unlimited)")
	clusterAllowlist := flag.String("cluster-allowlist", envOr("SYNCWELL_CLUSTER_ALLOWLIST", ""), "comma-separated CIDRs allowed to reach /cluster/event (empty = any)")
	trustProxy := flag.Bool("trust-proxy", envBool("SYNCWELL_TRUST_PROXY", false), "use X-Forwarded-For for rate limits (only behind a trusted proxy)")
	proxyHops := flag.Int("proxy-hops", envInt("SYNCWELL_PROXY_HOPS", 1), "number of trusted proxies in front of us; X-Forwarded-For is parsed right-to-left by this count")
	maxClients := flag.Int("max-clients", envInt("SYNCWELL_MAX_CLIENTS", 256), "max clients per room (0 = unlimited, negative = default 256)")
	maxRooms := flag.Int("max-rooms", envInt("SYNCWELL_MAX_ROOMS", 10000), "max live rooms before new rooms are refused (0 = unlimited, negative = default 10000)")
	idleRoomTTL := flag.Duration("idle-room-ttl", envDuration("SYNCWELL_IDLE_ROOM_TTL", 0), "evict empty rooms after this idle period (0 = default 60s, negative = disabled)")
	maxMessageBytes := flag.Int("max-message-bytes", envInt("SYNCWELL_MAX_MESSAGE_BYTES", 0), "max websocket message bytes (0 = default)")
	maxRoomIDBytes := flag.Int("max-room-id-bytes", envInt("SYNCWELL_MAX_ROOM_ID_BYTES", 0), "max room id bytes (0 = default)")
	maxKeyBytes := flag.Int("max-key-bytes", envInt("SYNCWELL_MAX_KEY_BYTES", 0), "max document key bytes (0 = default)")
	maxNodeBytes := flag.Int("max-node-bytes", envInt("SYNCWELL_MAX_NODE_BYTES", 0), "max client node id bytes (0 = default)")
	maxValueBytes := flag.Int("max-value-bytes", envInt("SYNCWELL_MAX_VALUE_BYTES", 0), "max register JSON value bytes (0 = default)")
	maxPresenceBytes := flag.Int("max-presence-bytes", envInt("SYNCWELL_MAX_PRESENCE_BYTES", 0), "max presence JSON bytes (0 = default)")
	maxStateEntries := flag.Int("max-state-entries", envInt("SYNCWELL_MAX_STATE_ENTRIES", 0), "max registers accepted in one merge frame (0 = default)")
	maxMessagesPerSecond := flag.Int("max-messages-per-second", envInt("SYNCWELL_MAX_MESSAGES_PER_SECOND", 0), "max websocket messages per client per second (0 = default)")
	nodeID := flag.String("node-id", envOr("SYNCWELL_NODE_ID", ""), "unique node id (required when clustering)")
	nodePeers := flag.String("node-peers", envOr("SYNCWELL_NODE_PEERS", ""), "comma-separated peer node ids for room placement")
	clusterPeers := flag.String("cluster-peers", envOr("SYNCWELL_CLUSTER_PEERS", ""), "comma-separated peer cluster event URLs (empty = single node)")
	clusterPath := flag.String("cluster-path", envOr("SYNCWELL_CLUSTER_PATH", "/cluster/event"), "HTTP path for cluster event posts")
	antiEntropy := flag.Duration("anti-entropy", envDuration("SYNCWELL_ANTI_ENTROPY", 30*time.Second), "how often to push full room state to peers (0 = disabled)")
	webhookURLs := flag.String("webhook-urls", envOr("SYNCWELL_WEBHOOK_URLS", ""), "comma-separated HTTP endpoints for room milestone webhooks")
	webhookSecret := flag.String("webhook-secret", envOr("SYNCWELL_WEBHOOK_SECRET", ""), "HMAC-SHA256 secret used to sign webhook bodies")
	webhookTimeout := flag.Duration("webhook-timeout", envDuration("SYNCWELL_WEBHOOK_TIMEOUT", 5*time.Second), "webhook delivery timeout")
	webhookQueue := flag.Int("webhook-queue", envInt("SYNCWELL_WEBHOOK_QUEUE", 0), "webhook queue size (0 = default 256)")
	tlsCert := flag.String("tls-cert", envOr("SYNCWELL_TLS_CERT", ""), "TLS certificate file (requires -tls-key)")
	tlsKey := flag.String("tls-key", envOr("SYNCWELL_TLS_KEY", ""), "TLS private key file (requires -tls-cert)")
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
	if (*tlsCert == "") != (*tlsKey == "") {
		log.Fatal("-tls-cert and -tls-key must be provided together (or SYNCWELL_TLS_CERT and SYNCWELL_TLS_KEY)")
	}

	// Token-minting helper: `syncwell -secret S -mint room` prints a token.
	if *mint != "" {
		if *secret == "" {
			log.Fatal("-mint requires -secret or SYNCWELL_SECRET")
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

	// Offline maintenance modes: verify, restore, and back up without serving.
	if *verifyBackupDir != "" {
		m, err := store.VerifyBackup(*verifyBackupDir)
		if err != nil {
			log.Fatalf("verify backup %q: %v", *verifyBackupDir, err)
		}
		fmt.Printf("backup ok: %d files, created %s\n", len(m.Files), m.Created.Format(time.RFC3339))
		return
	}
	if *restoreDir != "" {
		if *data == "" {
			log.Fatal("-restore requires -data or SYNCWELL_DATA")
		}
		m, err := store.Restore(*restoreDir, *data)
		if err != nil {
			log.Fatalf("restore %q into %q: %v", *restoreDir, *data, err)
		}
		fmt.Printf("restored %d files into %s\n", len(m.Files), *data)
		return
	}
	if *backupDir != "" {
		if *data == "" {
			log.Fatal("-backup requires -data or SYNCWELL_DATA")
		}
		fs, err := store.Open(*data)
		if err != nil {
			log.Fatalf("open data dir %q: %v", *data, err)
		}
		m, backupErr := fs.Backup(*backupDir)
		if closeErr := fs.Close(); backupErr == nil {
			backupErr = closeErr
		}
		if backupErr != nil {
			log.Fatalf("backup %q: %v", *data, backupErr)
		}
		fmt.Printf("backed up %d files to %s\n", len(m.Files), *backupDir)
		return
	}

	secretGenerated := false
	if publicBind(*addr) && !*insecure {
		if *secret == "" {
			generated, err := randomSecret()
			if err != nil {
				log.Fatalf("generate auth secret: %v", err)
			}
			*secret = generated
			secretGenerated = true
			// Print the credential to stderr for the operator starting the
			// process, but keep only a fingerprint in the structured log so
			// log aggregation never becomes a credential store.
			fmt.Fprintf(os.Stderr, "\n** generated room-token secret (copy it now, shown once): %s **\n"+
				"** set -secret/SYNCWELL_SECRET to keep it stable across restarts **\n\n", generated)
			server.LogEvent("auth_secret_generated", map[string]any{
				"fingerprint": secretFingerprint(generated),
				"note":        "printed to stderr once; set -secret/SYNCWELL_SECRET to keep it stable across restarts",
			})
		}
		if *adminToken == "" {
			if strings.TrimSpace(*clusterPeers) != "" {
				log.Fatal("clustering requires an explicit -admin-token/SYNCWELL_ADMIN_TOKEN; auto-generated credentials differ per node")
			}
			generated, err := randomSecret()
			if err != nil {
				log.Fatalf("generate admin token: %v", err)
			}
			*adminToken = generated
			fmt.Fprintf(os.Stderr, "\n** generated admin token (copy it now, shown once): %s **\n"+
				"** set -admin-token/SYNCWELL_ADMIN_TOKEN to keep it stable **\n\n", generated)
			server.LogEvent("admin_token_generated", map[string]any{
				"fingerprint": secretFingerprint(generated),
				"note":        "printed to stderr once; set -admin-token/SYNCWELL_ADMIN_TOKEN to keep it stable",
			})
		}
	} else if publicBind(*addr) && *insecure && (*secret == "" || *adminToken == "") {
		fmt.Fprintf(os.Stderr, "\n** INSECURE MODE (-insecure): public bind with no room/admin auth **\n"+
			"** anyone who can reach this address can read and write every room — never expose it to the internet **\n\n")
		server.LogEvent("insecure_public_bind", map[string]any{
			"room_auth":  *secret != "",
			"admin_auth": *adminToken != "",
			"warning":    "public bind with missing credentials; do not expose this to the internet",
		})
	}

	var otelHeadersMap map[string]string
	for _, part := range splitList(*otelHeaders) {
		k, v, ok := strings.Cut(part, "=")
		if !ok || strings.TrimSpace(k) == "" {
			log.Fatalf("-otel-headers entry %q must be key=value", part)
		}
		if otelHeadersMap == nil {
			otelHeadersMap = map[string]string{}
		}
		otelHeadersMap[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	tracerProvider, traceErr := server.InitTracing(context.Background(), server.TracingConfig{
		Endpoint:    *otelEndpoint,
		Insecure:    *otelInsecure,
		Headers:     otelHeadersMap,
		SampleRate:  *otelSample,
		ServiceName: *otelService,
	})
	if traceErr != nil {
		server.LogEvent("tracing_init_error", map[string]any{"error": traceErr.Error(), "note": "continuing without tracing"})
		log.Printf("warning: tracing disabled: %v", traceErr)
	}

	mode, err := store.ParseFsyncMode(*fsyncMode)
	if err != nil {
		log.Fatalf("%v", err)
	}

	var st store.Store
	if *data != "" {
		fs, err := store.OpenWithOptions(*data, store.Options{Fsync: mode, SyncInterval: *fsyncInterval})
		if err != nil {
			log.Fatalf("open data dir %q: %v", *data, err)
		}
		st = fs
		server.LogEvent("persistence_enabled", map[string]any{"dir": *data, "fsync": mode.String(), "max_log_bytes": *maxLogBytes})
	}

	var allowedOrigins []string
	if *origins != "" {
		allowedOrigins = strings.Split(*origins, ",")
	}
	var clusterPeerList []string
	if *clusterPeers != "" {
		clusterPeerList = strings.Split(*clusterPeers, ",")
	}
	var nodePeerList []string
	if *nodePeers != "" {
		nodePeerList = strings.Split(*nodePeers, ",")
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
	cfg := server.Config{
		Store:                st,
		CompactEvery:         *compact,
		Secret:               *secret,
		PreviousSecrets:      splitList(*secretPrevious),
		AdminToken:           *adminToken,
		RevokedJTIs:          revoked,
		Origins:              allowedOrigins,
		AdminRatePerMinute:   *adminRate,
		WSRatePerMinute:      *wsRate,
		AdminBurst:           *adminBurst,
		AdminMaxFailures:     *adminMaxFailures,
		AdminLockout:         *adminLockout,
		AdminAllowlist:       splitList(*adminAllowlist),
		ClusterRatePerMinute: *clusterRate,
		ClusterAllowlist:     splitList(*clusterAllowlist),
		TrustProxyHeaders:    *trustProxy,
		ProxyHops:            *proxyHops,
		MaxClients:           *maxClients,
		MaxRooms:             *maxRooms,
		MaxLogBytes:          *maxLogBytes,
		IdleRoomTTL:          *idleRoomTTL,
		NodeID:               *nodeID,
		Peers:                nodePeerList,
		ClusterPeers:         clusterPeerList,
		ClusterPath:          *clusterPath,
		AntiEntropyInterval:  *antiEntropy,
		WebhookURLs:          splitList(*webhookURLs),
		WebhookSecret:        *webhookSecret,
		WebhookTimeout:       *webhookTimeout,
		WebhookQueue:         *webhookQueue,
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
	}
	var oidcVerifier *server.OIDCVerifier
	if strings.TrimSpace(*oidcIssuer) != "" {
		verifier, oidcErr := server.NewOIDCVerifier(context.Background(), server.OIDCConfig{
			Issuer:    *oidcIssuer,
			Audience:  *oidcAudience,
			JWKSURL:   *oidcJWKS,
			RoomClaim: *oidcRoomClaim,
			CapsClaim: *oidcCapsClaim,
			Refresh:   *oidcRefresh,
		})
		oidcVerifier = verifier
		if oidcErr != nil {
			server.LogEvent("oidc_jwks_unavailable", map[string]any{"error": oidcErr.Error(), "note": "tokens fail closed until a JWKS fetch succeeds"})
			log.Printf("warning: OIDC enabled but JWKS is not available yet: %v", oidcErr)
		} else {
			server.LogEvent("oidc_enabled", map[string]any{"issuer": *oidcIssuer, "audience": *oidcAudience, "room_claim": *oidcRoomClaim, "caps_claim": *oidcCapsClaim})
		}
	} else if *oidcAudience != "" || *oidcJWKS != "" {
		log.Fatal("-oidc-audience/-oidc-jwks require -oidc-issuer (or SYNCWELL_OIDC_ISSUER)")
	}
	cfg.OIDC = oidcVerifier
	if err := cfg.Validate(); err != nil {
		log.Fatal(err)
	}
	hub := server.NewHub(cfg)
	if *secret != "" {
		server.LogEvent("room_token_auth_enabled", map[string]any{"caps": "read,write,presence"})
	}
	if *adminToken != "" {
		server.LogEvent("admin_token_enabled", map[string]any{"endpoints": "/stats,/metrics"})
	}
	if len(clusterPeerList) > 0 {
		server.LogEvent("cluster_relay_enabled", map[string]any{"peers": len(clusterPeerList), "path": *clusterPath})
	}
	if len(splitList(*webhookURLs)) > 0 {
		server.LogEvent("webhooks_enabled", map[string]any{"urls": len(splitList(*webhookURLs)), "signed": *webhookSecret != ""})
	}
	mux := http.NewServeMux()
	mux.Handle("/ws", hub.WSHandler(hub.ServeWS))
	mux.Handle("/stats", hub.AdminHandler(hub.ServeStats))
	mux.Handle("/metrics", hub.AdminHandler(hub.ServeMetrics))
	mux.Handle("/cluster/event", hub.ClusterHandler(hub.ServeClusterEvent))
	mux.HandleFunc("/admin", hub.ServeAdminUI)
	mux.Handle("/admin/api/rooms", hub.AdminHandler(hub.ServeAdminRooms))
	mux.Handle("/admin/api/rooms/", hub.AdminHandler(hub.ServeAdminRoomAction))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", hub.ServeReady)
	// Demo helper: list non-loopback IPv4 addresses so the share UI can offer
	// phone-friendly URLs when the page was opened via localhost.
	mux.HandleFunc("/demo/lan", func(w http.ResponseWriter, _ *http.Request) {
		type lanResponse struct {
			Addresses []string `json:"addresses"`
		}
		resp := lanResponse{Addresses: []string{}}
		// Only enumerate interface addresses for loopback binds. A public
		// server must not disclose its private network topology to any
		// unauthenticated visitor; the share UI hides the empty list.
		if !publicBind(*addr) {
			ifaces, err := net.Interfaces()
			if err == nil {
				for _, iface := range ifaces {
					if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
						continue
					}
					addrs, addErr := iface.Addrs()
					if addErr != nil {
						continue
					}
					for _, a := range addrs {
						var ip net.IP
						switch v := a.(type) {
						case *net.IPNet:
							ip = v.IP
						case *net.IPAddr:
							ip = v.IP
						}
						if ip4 := ip.To4(); ip4 != nil && !ip4.IsLoopback() {
							resp.Addresses = append(resp.Addresses, ip4.String())
						}
					}
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(resp)
	})
	// Serve the client SDK at /sdk/ so the demo can import it with an absolute
	// path regardless of where the static root is.
	if _, err := os.Stat(*sdk); err == nil {
		mux.Handle("/sdk/", http.StripPrefix("/sdk/", http.FileServer(http.Dir(*sdk))))
	}
	if _, err := os.Stat(*static); err == nil {
		mux.Handle("/", http.FileServer(http.Dir(*static)))
		server.LogEvent("serving_demo", map[string]any{"dir": *static})
	}

	srv := &http.Server{Addr: *addr, Handler: server.TracingMiddleware(mux)}
	if *tlsCert != "" {
		srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}

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
		if tracerProvider != nil {
			_ = tracerProvider.Shutdown(ctx)
		}
	}()

	scheme := "http"
	if *tlsCert != "" {
		scheme = "https"
	}
	server.LogEvent("startup", map[string]any{"version": version.String(), "addr": *addr, "scheme": scheme, "websocket": "/ws?room=ID"})
	printStartupBanner(scheme, *addr, *static, *data, *secret != "", secretGenerated)
	var serveErr error
	if *tlsCert != "" {
		server.LogEvent("tls_enabled", map[string]any{"cert": *tlsCert, "min_version": "1.2"})
		serveErr = srv.ListenAndServeTLS(*tlsCert, *tlsKey)
	} else {
		serveErr = srv.ListenAndServe()
	}
	if serveErr != nil && serveErr != http.ErrServerClosed {
		log.Fatal(serveErr)
	}
}

// printStartupBanner writes the human-readable "what do I do now" summary to
// stderr; the structured JSON log stays machine-only.
func printStartupBanner(scheme, addr, staticDir, dataDir string, authOn, secretGenerated bool) {
	base := displayURL(scheme, addr)
	fmt.Fprintf(os.Stderr, "%s listening on %s\n", version.String(), base)
	if _, err := os.Stat(staticDir); err == nil {
		if authOn {
			hint := "syncwell -mint demo"
			if secretGenerated {
				hint = "syncwell -secret <secret printed above> -mint demo"
			}
			fmt.Fprintf(os.Stderr, "  demos:    %s/ — room token required; mint one: %s\n", base, hint)
		} else {
			fmt.Fprintf(os.Stderr, "  demos:    %s/ — open in two windows to collaborate (no token needed)\n", base)
		}
	} else {
		fmt.Fprintf(os.Stderr, "  demos:    not served (no %q directory)\n", staticDir)
	}
	if dataDir == "" {
		fmt.Fprintf(os.Stderr, "  storage:  in-memory — pass -data DIR (or SYNCWELL_DATA) to survive restarts\n")
	} else {
		fmt.Fprintf(os.Stderr, "  storage:  %s\n", dataDir)
	}
}

// displayURL renders a browser-friendly URL for addr: empty and wildcard hosts
// become localhost so the printed link is clickable.
func displayURL(scheme, addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return scheme + "://" + addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" || strings.EqualFold(host, "localhost") {
		host = "localhost"
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}

func envOr(key, def string) string {
	noteEnv(key)
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envFloat(key string, def float64) float64 {
	noteEnv(key)
	raw := os.Getenv(key)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		log.Printf("warning: %s=%q is not a number — using default %v", key, raw, def)
		return def
	}
	return v
}

func envInt(key string, def int) int {
	noteEnv(key)
	raw := os.Getenv(key)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		log.Printf("warning: %s=%q is not an integer — using default %d", key, raw, def)
		return def
	}
	return v
}

func envInt64(key string, def int64) int64 {
	noteEnv(key)
	raw := os.Getenv(key)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		log.Printf("warning: %s=%q is not an integer — using default %d", key, raw, def)
		return def
	}
	return v
}

func envDuration(key string, def time.Duration) time.Duration {
	noteEnv(key)
	raw := os.Getenv(key)
	if raw == "" {
		return def
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		log.Printf("warning: %s=%q is not a duration (use e.g. 5ms, 30s, 2m) — using default %s", key, raw, def)
		return def
	}
	return v
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func envBool(key string, def bool) bool {
	noteEnv(key)
	raw := os.Getenv(key)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		log.Printf("warning: %s=%q is not a boolean (use true/false or 1/0) — using default %v", key, raw, def)
		return def
	}
	return v
}

// publicBind reports whether addr accepts connections from outside this host.
// Wildcard and non-loopback hosts are public; loopback is safe for local dev.
func publicBind(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return true
	}
	if host == "0.0.0.0" || host == "::" {
		return true
	}
	if strings.EqualFold(host, "localhost") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsLoopback()
	}
	return true
}

func randomSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// secretFingerprint returns a short SHA-256 prefix for correlating log lines
// without exposing the credential itself.
func secretFingerprint(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:8])
}
