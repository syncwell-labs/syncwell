package server

import (
	"net/http"
	"strings"
)

// BodyLimit wraps a handler and rejects request bodies larger than maxBytes.
// It must be placed in the chain before the handler reads req.Body.
func BodyLimit(next http.HandlerFunc, maxBytes int64) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		next(w, r)
	})
}

// originOK reports whether the request's Origin is acceptable for a
// state-changing endpoint. An empty Origin (non-browser clients, curl) is
// allowed; a non-empty Origin must exactly match one of the configured origins.
// Both sides go through normalizeOrigin so configuration and request casing or
// default ports cannot disagree with the WebSocket origin policy.
func originOK(origin string, allowed []string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return true
	}
	got, err := normalizeOrigin(origin)
	if err != nil {
		return false
	}
	for _, a := range allowed {
		want, err := normalizeOrigin(a)
		if err == nil && want == got {
			return true
		}
	}
	return false
}
