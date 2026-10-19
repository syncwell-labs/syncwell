package server

import (
	"encoding/json"
	"log"
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
