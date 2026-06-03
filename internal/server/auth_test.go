package server

import "testing"

func TestTokenRoundTrip(t *testing.T) {
	const secret = "s3cret"
	tok := SignToken(secret, "roomA", 1000)

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
