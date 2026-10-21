package server

import (
	"encoding/json"
	"testing"
	"time"
)

func FuzzValidateInbound(f *testing.F) {
	seeds := []string{
		`{}`,
		`{"type":"op","key":"k","ts":{"c":1,"n":"a"},"value":1}`,
		`{"type":"merge","state":{}}`,
		`{"type":"presence","data":{"cursor":{"x":1}}}`,
		`{"type":"unknown"}`,
		`{"type":"op","value":`,
		`null`,
		`[]`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	limits := Limits{}.withDefaults()
	f.Fuzz(func(t *testing.T, data []byte) {
		var in Inbound
		if err := json.Unmarshal(data, &in); err != nil {
			return
		}
		_ = validateInbound(in, limits)
	})
}

func FuzzUnmarshalClusterEvent(f *testing.F) {
	for _, s := range []string{
		`{}`,
		`{"room":"r","kind":"op","from":"n1","op":{"key":"k"}}`,
		`{"room":"r","kind":"merge","from":"n1","state":{}}`,
		`{"room":`,
		`null`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = UnmarshalClusterEvent(data)
	})
}

func FuzzVerifyAccessToken(f *testing.F) {
	for _, s := range []string{
		"",
		"123.abc",
		"v2..sig",
		"v2.e30.sig",
		"....",
		"not-a-token",
	} {
		f.Add(s)
	}
	now := time.Now().Unix()
	f.Fuzz(func(t *testing.T, token string) {
		_, _ = VerifyAccessToken("secret", "room", token, now)
	})
}
