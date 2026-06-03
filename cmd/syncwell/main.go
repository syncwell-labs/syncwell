// Command syncwell runs the collaboration engine: a websocket sync endpoint at
// /ws plus an optional static file server for the demo app. It compiles to a
// single self-contained binary with no runtime dependencies.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"syncwell/internal/server"
	"syncwell/internal/store"
)

func main() {
	addr := flag.String("addr", envOr("SYNCWELL_ADDR", ":8080"), "listen address")
	static := flag.String("static", "demo", "directory of static files to serve at / (skipped if missing)")
	sdk := flag.String("sdk", "sdk", "directory of the client SDK to serve at /sdk/ (skipped if missing)")
	data := flag.String("data", envOr("SYNCWELL_DATA", ""), "data directory for persistence (empty = in-memory)")
	compact := flag.Int("compact", 256, "winning writes between maintenance passes (tombstone GC + compaction)")
	flag.Parse()

	var st store.Store
	if *data != "" {
		fs, err := store.Open(*data)
		if err != nil {
			log.Fatalf("open data dir %q: %v", *data, err)
		}
		st = fs
		log.Printf("persistence enabled at %s/", *data)
	}

	hub := server.NewHub(st, *compact)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", hub.ServeWS)
	mux.HandleFunc("/stats", hub.ServeStats)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	// Serve the client SDK at /sdk/ so the demo can import it with an absolute
	// path regardless of where the static root is.
	if _, err := os.Stat(*sdk); err == nil {
		mux.Handle("/sdk/", http.StripPrefix("/sdk/", http.FileServer(http.Dir(*sdk))))
	}
	if _, err := os.Stat(*static); err == nil {
		mux.Handle("/", http.FileServer(http.Dir(*static)))
		log.Printf("serving demo from %s/", *static)
	}

	srv := &http.Server{Addr: *addr, Handler: mux}

	// Graceful shutdown: drain connections and flush persistence on SIGINT/TERM.
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		log.Print("shutting down…")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		if st != nil {
			_ = st.Close()
		}
	}()

	log.Printf("syncwell listening on %s  (websocket: /ws?room=ID)", *addr)
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
