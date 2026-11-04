package server

import (
	"encoding/json"
	"log"
	"net/http"
)

// LogEvent writes a small structured JSON log line. The goal is to make the
// operational events in this server easy to scrape and grep without requiring a
// full logging stack.
func LogEvent(event string, fields map[string]any) {
	payload := map[string]any{"event": event}
	for k, v := range fields {
		payload[k] = v
	}
	b, err := json.Marshal(payload)
	if err != nil {
		log.Printf(`{"event":"log_error","error":%q}`, err.Error())
		return
	}
	log.Print(string(b))
}

func LogRequestEvent(req *http.Request, event string, fields map[string]any) {
	withID := make(map[string]any, len(fields)+1)
	for key, value := range fields {
		withID[key] = value
	}
	if id := RequestID(req.Context()); id != "" {
		withID["request_id"] = id
	}
	LogEvent(event, withID)
}
