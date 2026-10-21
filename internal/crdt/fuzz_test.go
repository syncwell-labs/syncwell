package crdt

import (
	"encoding/json"
	"testing"
)

func FuzzCanonicalJSON(f *testing.F) {
	for _, s := range []string{
		`null`,
		`true`,
		`1`,
		`"x"`,
		`[]`,
		`{}`,
		`{"b":1,"a":2}`,
		`{"n":9007199254740993}`,
		`{"s":"\u2028\ud83d\ude00"}`,
		`{"nested":{"arr":[1,2,{"x":null}]}}`,
		`{"bad":`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if !json.Valid(raw) {
			return // invalid input is returned verbatim by design
		}
		out := canonicalJSON(json.RawMessage(raw))
		if !json.Valid(out) {
			t.Fatalf("canonicalJSON produced invalid JSON: %q -> %q", raw, out)
		}
		if again := canonicalJSON(json.RawMessage(out)); string(again) != string(out) {
			t.Fatalf("canonicalJSON is not idempotent: %q -> %q -> %q", raw, out, again)
		}
	})
}
