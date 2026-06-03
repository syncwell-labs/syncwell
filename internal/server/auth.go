package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// Room access tokens are stateless, room-scoped, and expiring. A token is
//
//	"<exp>.<hex hmac-sha256(secret, "<room>|<exp>")>"
//
// The server needs only the shared secret to verify — no token database. Auth
// is disabled entirely when the secret is empty (open dev mode).

// SignToken issues a token granting access to `room` until `exp` (unix seconds).
func SignToken(secret, room string, exp int64) string {
	expStr := strconv.FormatInt(exp, 10)
	return expStr + "." + sign(secret, room+"|"+expStr)
}

func sign(secret, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyToken reports whether token grants access to room at time now (unix s).
// Constant-time on the signature; rejects expired or malformed tokens.
func VerifyToken(secret, room, token string, now int64) bool {
	dot := strings.LastIndexByte(token, '.')
	if dot <= 0 {
		return false
	}
	expStr, sig := token[:dot], token[dot+1:]
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || now > exp {
		return false
	}
	want := sign(secret, room+"|"+expStr)
	return hmac.Equal([]byte(sig), []byte(want))
}
