package server

import "testing"

// Protocol v1 is a public compatibility contract. Changing this constant (or
// the wire semantics it guards) requires a new protocol version, a major
// release, and an update to docs/protocol/PROTOCOL_COMPATIBILITY.md.
func TestProtocolVersionIsFrozen(t *testing.T) {
	const frozen = 1
	if protocolVersion != frozen {
		t.Fatalf("protocol version changed from v%d to v%d; update the compatibility policy and add a migration plan before changing it", frozen, protocolVersion)
	}
}

func TestProtocolVersionNegotiation(t *testing.T) {
	limits := Limits{}.withDefaults()
	for _, version := range []int{0, protocolVersion} {
		if err := validateInbound(Inbound{Protocol: version, Type: "ack"}, limits); err != nil {
			t.Fatalf("protocol %d should be accepted: %v", version, err)
		}
	}
	if err := validateInbound(Inbound{Protocol: protocolVersion + 1, Type: "ack"}, limits); err == nil {
		t.Fatalf("protocol %d should be rejected", protocolVersion+1)
	}
}
