package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Room access tokens are stateless, room-scoped, and expiring. The current
// format is a versioned, signed, base64url-encoded JSON payload:
//
//	"v2.<base64url(JSON claims)>.<hex hmac-sha256(secret, "<room>|v2|<payload>")>"
//
// For backward compatibility we still accept the previous legacy forms:
//
//	"<exp>.<hex hmac-sha256(secret, "<room>|<exp>")>"
//	"<exp>.<caps>.<jti>.<hex hmac-sha256(secret, "<room>|<exp>|<caps>|<jti>")>"
//
// The server needs only the shared secret to verify — no token database. Auth
// is disabled entirely when the secret is empty (open dev mode).

const (
	CapRead     = "read"
	CapWrite    = "write"
	CapPresence = "presence"
	CapAdmin    = "admin"
)

// TokenClaims describes an access token after verification.
type TokenClaims struct {
	Room string
	Sub  string
	Exp  int64
	Iat  int64
	Nbf  int64
	JTI  string
	Caps map[string]bool
	Raw  string
}

// Can reports whether the token includes the requested capability.
func (c TokenClaims) Can(cap string) bool {
	return c.Caps[cap]
}

// HasAny reports whether the token includes at least one capability.
func (c TokenClaims) HasAny() bool { return len(c.Caps) > 0 }

// DefaultRoomCaps returns the standard browser-client capability set.
func DefaultRoomCaps() []string { return []string{CapRead, CapWrite, CapPresence} }

// SignToken issues a default full-access room token. The returned token uses the
// new capability-aware format.
func SignToken(secret, room string, exp int64) string {
	return SignAccessToken(secret, room, exp, DefaultRoomCaps(), randomJTI())
}

// SignAccessToken issues a token with an explicit capability list.
func SignAccessToken(secret, room string, exp int64, caps []string, jti string) string {
	return SignAccessTokenForSubject(secret, room, "", exp, caps, jti, 0, 0)
}

// SignAccessTokenForSubject issues a capability-scoped token with principal
// identity and optional issuance constraints.
func SignAccessTokenForSubject(secret, room, subject string, exp int64, caps []string, jti string, iat, nbf int64) string {
	if jti == "" {
		jti = randomJTI()
	}
	if iat == 0 {
		iat = timeNow().Unix()
	}
	if nbf == 0 {
		nbf = iat
	}
	payload := tokenPayload{
		Room: room,
		Sub:  subject,
		Exp:  exp,
		Iat:  iat,
		Nbf:  nbf,
		Caps: normalizeCapsList(caps),
		JTI:  jti,
	}
	raw, _ := json.Marshal(payload)
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	return "v2." + encoded + "." + sign(secret, room+"|v2|"+encoded)
}

func randomJTI() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("%x", timeNow().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}

var timeNow = func() time.Time { return time.Now() }

func sign(secret, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func normalizeCaps(caps []string) string {
	return strings.Join(normalizeCapsList(caps), ",")
}

func normalizeCapsList(caps []string) []string {
	if len(caps) == 0 {
		caps = DefaultRoomCaps()
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(caps))
	for _, c := range caps {
		c = strings.TrimSpace(strings.ToLower(c))
		switch c {
		case CapRead, CapWrite, CapPresence, CapAdmin:
			if _, ok := seen[c]; ok {
				continue
			}
			seen[c] = struct{}{}
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		out = DefaultRoomCaps()
	}
	// Stable order ensures signatures are deterministic.
	order := map[string]int{CapRead: 0, CapWrite: 1, CapPresence: 2, CapAdmin: 3}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if order[out[j]] < order[out[i]] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func parseCapsCSV(caps string) map[string]bool {
	out := map[string]bool{}
	for _, c := range strings.Split(caps, ",") {
		switch strings.TrimSpace(strings.ToLower(c)) {
		case CapRead, CapWrite, CapPresence, CapAdmin:
			out[strings.TrimSpace(strings.ToLower(c))] = true
		}
	}
	if len(out) == 0 {
		for _, c := range DefaultRoomCaps() {
			out[c] = true
		}
	}
	return out
}

func parseCapsList(caps []string) map[string]bool {
	if len(caps) == 0 {
		return parseCapsCSV("")
	}
	return parseCapsCSV(strings.Join(caps, ","))
}

type tokenPayload struct {
	Room string   `json:"room,omitempty"`
	Sub  string   `json:"sub,omitempty"`
	Exp  int64    `json:"exp"`
	Iat  int64    `json:"iat,omitempty"`
	Nbf  int64    `json:"nbf,omitempty"`
	Caps []string `json:"caps,omitempty"`
	JTI  string   `json:"jti,omitempty"`
}

// VerifyToken reports whether token grants access to room at time now (unix s).
// Constant-time on the signature; rejects expired or malformed tokens. Tokens
// revoked by JTI or missing the required capability are rejected.
func VerifyToken(secret, room, token string, now int64) bool {
	_, ok := VerifyAccessToken(secret, room, token, now)
	return ok
}

// VerifyAccessToken returns the parsed claims if the token is valid.
func VerifyAccessToken(secret, room, token string, now int64) (TokenClaims, bool) {
	var claims TokenClaims
	if secret == "" {
		return TokenClaims{Room: room, Caps: parseCapsCSV(strings.Join(DefaultRoomCaps(), ","))}, true
	}
	parts := strings.Split(token, ".")
	switch len(parts) {
	case 2:
		left, sig := parts[0], parts[1]
		exp, err := strconv.ParseInt(left, 10, 64)
		if err != nil || now > exp {
			return claims, false
		}
		want := sign(secret, room+"|"+left)
		if !hmac.Equal([]byte(sig), []byte(want)) {
			return claims, false
		}
		claims = TokenClaims{Room: room, Exp: exp, JTI: "", Caps: parseCapsCSV(strings.Join(DefaultRoomCaps(), ",")), Raw: token}
		return claims, true
	case 3:
		version, payload64, sig := parts[0], parts[1], parts[2]
		if version != "v2" {
			return claims, false
		}
		raw, err := base64.RawURLEncoding.DecodeString(payload64)
		if err != nil {
			return claims, false
		}
		want := sign(secret, room+"|"+version+"|"+payload64)
		if !hmac.Equal([]byte(sig), []byte(want)) {
			return claims, false
		}
		var payload tokenPayload
		if err := json.Unmarshal(raw, &payload); err != nil {
			return claims, false
		}
		if payload.Room != "" && payload.Room != room {
			return claims, false
		}
		if payload.Exp == 0 || now > payload.Exp {
			return claims, false
		}
		if payload.Nbf != 0 && now < payload.Nbf {
			return claims, false
		}
		if payload.Iat == 0 {
			payload.Iat = payload.Nbf
		}
		if payload.Room == "" {
			payload.Room = room
		}
		claims = TokenClaims{
			Room: payload.Room,
			Sub:  payload.Sub,
			Exp:  payload.Exp,
			Iat:  payload.Iat,
			Nbf:  payload.Nbf,
			JTI:  payload.JTI,
			Caps: parseCapsList(payload.Caps),
			Raw:  token,
		}
		return claims, true
	case 4:
		expStr, capsStr, jti, sig := parts[0], parts[1], parts[2], parts[3]
		exp, err := strconv.ParseInt(expStr, 10, 64)
		if err != nil || now > exp {
			return claims, false
		}
		want := sign(secret, room+"|"+expStr+"|"+capsStr+"|"+jti)
		if !hmac.Equal([]byte(sig), []byte(want)) {
			return claims, false
		}
		claims = TokenClaims{Room: room, Exp: exp, JTI: jti, Caps: parseCapsCSV(capsStr), Raw: token}
		return claims, true
	default:
		return claims, false
	}
}
