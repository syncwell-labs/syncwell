// Command syncwell runs the collaboration engine: a websocket sync endpoint at
// /ws plus an optional static file server for the demo app. It compiles to a
// single self-contained binary with no runtime dependencies.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"

	"syncwell/internal/server"
)

func main() {
	addr := flag.String("addr", envOr("SYNCWELL_ADDR", ":8080"), "listen address")
	static := flag.String("static", "demo", "directory of static files to serve at / (skipped if missing)")
	sdk := flag.String("sdk", "sdk", "directory of the client SDK to serve at /sdk/ (skipped if missing)")
	flag.Parse()

	hub := server.NewHub()
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", hub.ServeWS)
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

	log.Printf("syncwell listening on %s  (websocket: /ws?room=ID)", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
