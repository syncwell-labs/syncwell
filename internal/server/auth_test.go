package server

import "testing"

func TestTokenRoundTrip(t *testing.T) {
	const secret = "s3cret"
	tok := SignAccessTokenForSubject(secret, "roomA", "", 1000, DefaultRoomCaps(), "jti-1", 900, 900)

	if !VerifyToken(secret, "roomA", tok, 999) {
		t.Fatal("valid token rejected")
	}
	if VerifyToken(secret, "roomA", tok, 1001) {
		t.Fatal("expired token accepted")
	}
	if VerifyToken(secret, "roomB", tok, 999) {
		t.Fatal("token accepted for the wrong room")
	}
	if VerifyToken("wrong", "roomA", tok, 999) {
		t.Fatal("token accepted under the wrong secret")
	}
	if VerifyToken(secret, "roomA", tok+"x", 999) {
		t.Fatal("tampered token accepted")
	}
	if VerifyToken(secret, "roomA", "garbage", 999) {
		t.Fatal("malformed token accepted")
	}
}

func TestAccessTokenRoundTripWithCapabilities(t *testing.T) {
	const secret = "s3cret"
	const room = "roomA"
	const jti = "token-123"
	tok := SignAccessTokenForSubject(secret, room, "", 2000, []string{CapPresence, CapWrite, CapRead}, jti, 1900, 1900)

	claims, ok := VerifyAccessToken(secret, room, tok, 1999)
	if !ok {
		t.Fatal("valid capability token rejected")
	}
	if claims.Exp != 2000 {
		t.Fatalf("unexpected expiry: %d", claims.Exp)
	}
	if claims.Room != room {
		t.Fatalf("unexpected room: %q", claims.Room)
	}
	if claims.JTI != jti {
		t.Fatalf("unexpected jti: %q", claims.JTI)
	}
	for _, cap := range []string{CapRead, CapWrite, CapPresence} {
		if !claims.Can(cap) {
			t.Fatalf("missing capability %q", cap)
		}
	}
	if claims.Can("admin") {
		t.Fatal("unexpected admin capability present")
	}
}

func TestAccessTokenRoundTripWithPrincipalClaims(t *testing.T) {
	const secret = "s3cret"
	const room = "roomA"
	const subject = "user-42"
	const jti = "token-456"
	tok := SignAccessTokenForSubject(secret, room, subject, 2000, []string{CapRead, CapPresence}, jti, 1500, 1600)

	claims, ok := VerifyAccessToken(secret, room, tok, 1700)
	if !ok {
		t.Fatal("principal token rejected")
	}
	if claims.Room != room {
		t.Fatalf("unexpected room: %q", claims.Room)
	}
	if claims.Sub != subject {
		t.Fatalf("unexpected subject: %q", claims.Sub)
	}
	if claims.Exp != 2000 || claims.Iat != 1500 || claims.Nbf != 1600 {
		t.Fatalf("unexpected time claims: %+v", claims)
	}
	if claims.JTI != jti {
		t.Fatalf("unexpected jti: %q", claims.JTI)
	}
	if !claims.Can(CapRead) || !claims.Can(CapPresence) || claims.Can(CapWrite) || claims.Can(CapAdmin) {
		t.Fatalf("unexpected capabilities: %+v", claims.Caps)
	}
}

func TestAccessTokenCompatibilityWithLegacyRoomToken(t *testing.T) {
	const secret = "s3cret"
	tok := SignAccessTokenForSubject(secret, "roomA", "", 1000, DefaultRoomCaps(), "jti-2", 900, 900)

	claims, ok := VerifyAccessToken(secret, "roomA", tok, 999)
	if !ok {
		t.Fatal("legacy token rejected")
	}
	if claims.JTI == "" {
		t.Fatalf("token unexpectedly had no jti")
	}
	for _, cap := range []string{CapRead, CapWrite, CapPresence} {
		if !claims.Can(cap) {
			t.Fatalf("legacy token missing capability %q", cap)
		}
	}
}

func TestAccessTokenRejectsNotBeforeClaims(t *testing.T) {
	const secret = "s3cret"
	tok := SignAccessTokenForSubject(secret, "roomA", "user-1", 2000, []string{CapRead}, "jti-1", 1500, 1800)

	if _, ok := VerifyAccessToken(secret, "roomA", tok, 1700); ok {
		t.Fatal("token accepted before nbf")
	}
	if _, ok := VerifyAccessToken(secret, "roomA", tok, 1800); !ok {
		t.Fatal("token rejected at nbf")
	}
}

func TestAccessTokenRoundTripWithAdminCapability(t *testing.T) {
	const secret = "s3cret"
	tok := SignAccessTokenForSubject(secret, "roomA", "", 2000, []string{CapAdmin}, "admin-token", 1900, 1900)

	claims, ok := VerifyAccessToken(secret, "roomA", tok, 1999)
	if !ok {
		t.Fatal("admin capability token rejected")
	}
	if !claims.Can(CapAdmin) {
		t.Fatal("admin capability missing")
	}
	if claims.Can(CapRead) || claims.Can(CapWrite) || claims.Can(CapPresence) {
		t.Fatal("admin-only token unexpectedly gained other capabilities")
	}
}

func TestValidateCapsListRejectsUnknownAndEmpty(t *testing.T) {
	if _, err := ValidateCapsList([]string{"read", "writee"}); err == nil {
		t.Fatal("unknown capability accepted")
	}
	if _, err := ValidateCapsList([]string{" "}); err == nil {
		t.Fatal("empty capability list accepted")
	}
	got, err := ValidateCapsList([]string{"admin", "read", "read"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != CapRead || got[1] != CapAdmin {
		t.Fatalf("unexpected normalized caps: %v", got)
	}
}

func TestParseCapsFailsClosedForUnknownNonEmptyList(t *testing.T) {
	if caps := parseCapsCSV("bogus"); len(caps) != 0 {
		t.Fatalf("unknown caps granted: %v", caps)
	}
	if caps := parseCapsCSV(""); !caps[CapRead] || !caps[CapWrite] || !caps[CapPresence] {
		t.Fatalf("empty caps lost legacy default: %v", caps)
	}
}
