package crdt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// These fixtures are shared with the JS SDK (sdk/conformance.test.mjs). Both
// implementations must resolve every tie identically, otherwise two replicas
// that see the same two writes in different orders would diverge.
type confRegister struct {
	Value json.RawMessage `json:"v"`
	TS    Timestamp       `json:"t"`
}

type confCase struct {
	Name   string          `json:"name"`
	A      confRegister    `json:"a"`
	B      *confRegister   `json:"b"`
	CanonA string          `json:"canonA"`
	CanonB string          `json:"canonB"`
	Want   json.RawMessage `json:"want"`
}

// register decodes the fixture's `v` field: it holds JSON *text* so that the
// raw lexeme (e.g. "1.0" vs "1") survives the fixture's own JSON parse.
func (c confRegister) register(t *testing.T) Register {
	t.Helper()
	var text string
	if err := json.Unmarshal(c.Value, &text); err != nil {
		t.Fatalf("fixture value is not a JSON string: %v", err)
	}
	return Register{Value: json.RawMessage(text), TS: c.TS}
}

func TestConformanceFixtures(t *testing.T) {
	path := filepath.Join("..", "..", "test", "conformance", "tiebreak_cases.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixtures: %v", err)
	}
	var cases []confCase
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatalf("parse fixtures: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("no conformance fixtures found")
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			a := tc.A.register(t)

			// Canonical encoding must be byte-identical to the SDK's output.
			if tc.CanonA != "" {
				if got := string(canonicalJSON(a.Value)); got != tc.CanonA {
					t.Fatalf("canonical mismatch:\n got %q\nwant %q", got, tc.CanonA)
				}
			}

			if tc.B == nil {
				return
			}
			b := tc.B.register(t)
			if tc.CanonB != "" {
				if got := string(canonicalJSON(b.Value)); got != tc.CanonB {
					t.Fatalf("canonical mismatch:\n got %q\nwant %q", got, tc.CanonB)
				}
			}

			// The winner must be independent of the order writes are observed
			// in, and must match the expected value.
			ab := NewLWWMap()
			ab.ApplyRegister("k", a)
			ab.ApplyRegister("k", b)

			ba := NewLWWMap()
			ba.ApplyRegister("k", b)
			ba.ApplyRegister("k", a)

			gotAB, _ := ab.Get("k")
			gotBA, _ := ba.Get("k")
			if !jsonSemanticEqual(gotAB, gotBA) {
				t.Fatalf("order-dependent result: a,b=%s b,a=%s", gotAB, gotBA)
			}
			if !jsonSemanticEqual(gotAB, tc.Want) {
				t.Fatalf("winner mismatch: got %s want %s", gotAB, tc.Want)
			}

			// ResolveRegister itself must be commutative for the tie.
			w1 := ResolveRegister(a, b)
			w2 := ResolveRegister(b, a)
			if !jsonSemanticEqual(w1.Value, w2.Value) || w1.Deleted != w2.Deleted {
				t.Fatalf("ResolveRegister not commutative: %+v vs %+v", w1, w2)
			}
		})
	}
}

func jsonSemanticEqual(a, b json.RawMessage) bool {
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}
