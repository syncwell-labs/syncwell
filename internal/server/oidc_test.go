package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func testRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func rsaJWK(pub *rsa.PublicKey, kid string) jwk {
	return jwk{
		Kty: "RSA",
		Kid: kid,
		Use: "sig",
		Alg: "RS256",
		N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

func signJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	signed, err := tok.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func baseClaims(issuer, audience string) jwt.MapClaims {
	now := time.Now()
	return jwt.MapClaims{
		"iss":  issuer,
		"aud":  audience,
		"sub":  "user-1",
		"room": "demo",
		"caps": "read,write",
		"iat":  now.Add(-time.Minute).Unix(),
		"exp":  now.Add(5 * time.Minute).Unix(),
		"jti":  "jwt-1",
	}
}

func newKeysServer(t *testing.T, issuer string, keys *[]jwk, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		doc := struct {
			Keys []jwk `json:"keys"`
		}{Keys: *keys}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	})
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"issuer":"` + issuer + `","jwks_uri":"http://` + r.Host + `/jwks"}`))
	})
	return httptest.NewServer(mux)
}

func TestOIDCVerify(t *testing.T) {
	key := testRSAKey(t)
	issuer := "https://idp.example"
	audience := "syncwell"
	keys := []jwk{rsaJWK(&key.PublicKey, "k1")}
	var mu sync.Mutex
	srv := newKeysServer(t, issuer, &keys, &mu)
	defer srv.Close()

	v, err := NewOIDCVerifier(context.Background(), OIDCConfig{
		Issuer:    issuer,
		Audience:  audience,
		JWKSURL:   srv.URL + "/jwks",
		RoomClaim: "room",
		CapsClaim: "caps",
		Client:    srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}

	valid := signJWT(t, key, "k1", baseClaims(issuer, audience))
	claims, ok := v.Verify(valid, "demo", time.Now())
	if !ok {
		t.Fatal("valid token rejected")
	}
	if claims.Sub != "user-1" || claims.JTI != "jwt-1" || !claims.Can(CapRead) || !claims.Can(CapWrite) || claims.Can(CapAdmin) {
		t.Fatalf("unexpected claims: %+v", claims)
	}

	adminClaims := baseClaims(issuer, audience)
	adminClaims["room"] = "*"
	adminClaims["caps"] = "read admin"
	admin, ok := v.Verify(signJWT(t, key, "k1", adminClaims), "*", time.Now())
	if !ok || !admin.Can(CapAdmin) {
		t.Fatal("admin token rejected")
	}

	cases := []struct {
		name string
		tok  string
		room string
	}{
		{"wrong room", valid, "other"},
		{"wrong issuer", signJWT(t, key, "k1", baseClaims("https://evil.example", audience)), "demo"},
		{"wrong audience", signJWT(t, key, "k1", baseClaims(issuer, "other")), "demo"},
		{"expired", signJWT(t, key, "k1", func() jwt.MapClaims {
			c := baseClaims(issuer, audience)
			c["exp"] = time.Now().Add(-2 * time.Minute).Unix()
			return c
		}()), "demo"},
		{"missing expiry", signJWT(t, key, "k1", func() jwt.MapClaims {
			c := baseClaims(issuer, audience)
			delete(c, "exp")
			return c
		}()), "demo"},
		{"malformed caps", signJWT(t, key, "k1", func() jwt.MapClaims {
			c := baseClaims(issuer, audience)
			c["caps"] = 42
			return c
		}()), "demo"},
		{"empty", "", "demo"},
		{"garbage", "not-a-jwt", "demo"},
		{"tampered", func() string {
			// Flip a middle signature character: replacing the final base64
			// chars can decode to the same bytes (unused padding bits), which
			// made this case flaky.
			b := []byte(valid)
			mid := len(b)/2 + len(b)/4
			if b[mid] == 'A' {
				b[mid] = 'B'
			} else {
				b[mid] = 'A'
			}
			return string(b)
		}(), "demo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := v.Verify(tc.tok, tc.room, time.Now()); ok {
				t.Fatal("token accepted, want rejection")
			}
		})
	}

	none, err := jwt.NewWithClaims(jwt.SigningMethodNone, baseClaims(issuer, audience)).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Verify(none, "demo", time.Now()); ok {
		t.Fatal("alg=none accepted")
	}
}

func TestOIDCDefaultCapsAndDiscovery(t *testing.T) {
	key := testRSAKey(t)
	issuer := "https://idp.example"
	keys := []jwk{rsaJWK(&key.PublicKey, "k1")}
	var mu sync.Mutex
	srv := newKeysServer(t, issuer, &keys, &mu)
	defer srv.Close()

	// The discovery document advertises the live test server as issuer.
	v, err := NewOIDCVerifier(context.Background(), OIDCConfig{
		Issuer:    srv.URL,
		JWKSURL:   "",
		CapsClaim: "caps",
		Client:    srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	claims := baseClaims(srv.URL, "anything")
	delete(claims, "caps")
	claims["syncwell_room"] = "demo"
	// A missing caps claim is a deny, not a silent grant of default caps:
	// an IdP that does not provision the claim must not give every user
	// write access.
	if _, ok := v.Verify(signJWT(t, key, "k1", claims), "demo", time.Now()); ok {
		t.Fatal("missing caps claim accepted, want fail-closed deny")
	}

	// An empty caps list is also a deny.
	claims["caps"] = ""
	if _, ok := v.Verify(signJWT(t, key, "k1", claims), "demo", time.Now()); ok {
		t.Fatal("empty caps claim accepted, want fail-closed deny")
	}

	// A well-formed caps claim still verifies.
	claims["caps"] = "read,write,presence"
	got, ok := v.Verify(signJWT(t, key, "k1", claims), "demo", time.Now())
	if !ok {
		t.Fatal("token with explicit caps rejected")
	}
	if !got.Can(CapRead) || !got.Can(CapWrite) || !got.Can(CapPresence) || got.Can(CapAdmin) {
		t.Fatalf("unexpected caps: %+v", got.Caps)
	}
}

func TestOIDCWildcardRoomRequiresAdmin(t *testing.T) {
	key := testRSAKey(t)
	issuer := "https://idp.example"
	keys := []jwk{rsaJWK(&key.PublicKey, "k1")}
	var mu sync.Mutex
	srv := newKeysServer(t, issuer, &keys, &mu)
	defer srv.Close()

	v, err := NewOIDCVerifier(context.Background(), OIDCConfig{
		Issuer:    srv.URL,
		JWKSURL:   "",
		CapsClaim: "caps",
		Client:    srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	claims := baseClaims(srv.URL, "anything")
	claims["syncwell_room"] = "*"
	claims["caps"] = "read"

	// A wildcard room claim must not grant room-scoped access with
	// non-admin capabilities — that would read every room.
	if _, ok := v.Verify(signJWT(t, key, "k1", claims), "demo", time.Now()); ok {
		t.Fatal("wildcard room with read cap accepted for a specific room")
	}

	// The admin path (room == "*") still accepts it if CapAdmin is present.
	claims["caps"] = "read,admin"
	got, ok := v.Verify(signJWT(t, key, "k1", claims), "*", time.Now())
	if !ok {
		t.Fatal("wildcard room with admin cap rejected on admin path")
	}
	if !got.Can(CapAdmin) {
		t.Fatal("admin cap missing")
	}
	// But even with admin cap it must not unlock a *room-scoped* check via
	// the wildcard alone; the admin endpoint uses room=="*" exclusively.
	if _, ok := v.Verify(signJWT(t, key, "k1", claims), "demo", time.Now()); ok {
		t.Fatal("wildroom admin token accepted for a specific room")
	}
}

func TestOIDCKeyRotation(t *testing.T) {
	k1, k2 := testRSAKey(t), testRSAKey(t)
	issuer := "https://idp.example"
	keys := []jwk{rsaJWK(&k1.PublicKey, "k1")}
	var mu sync.Mutex
	srv := newKeysServer(t, issuer, &keys, &mu)
	defer srv.Close()

	v, err := NewOIDCVerifier(context.Background(), OIDCConfig{
		Issuer:    issuer,
		Audience:  "syncwell",
		JWKSURL:   srv.URL + "/jwks",
		RoomClaim: "room",
		CapsClaim: "caps",
		Client:    srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Verify(signJWT(t, k1, "k1", baseClaims(issuer, "syncwell")), "demo", time.Now()); !ok {
		t.Fatal("k1 rejected")
	}

	mu.Lock()
	keys = []jwk{rsaJWK(&k2.PublicKey, "k2")}
	mu.Unlock()
	// Simulate a cache older than the anti-thrash window.
	v.mu.Lock()
	v.fetched = time.Now().Add(-time.Minute)
	v.mu.Unlock()

	if _, ok := v.Verify(signJWT(t, k2, "k2", baseClaims(issuer, "syncwell")), "demo", time.Now()); !ok {
		t.Fatal("rotated key rejected")
	}
}

func TestOIDCAdminAllowed(t *testing.T) {
	key := testRSAKey(t)
	issuer := "https://idp.example"
	keys := []jwk{rsaJWK(&key.PublicKey, "k1")}
	var mu sync.Mutex
	srv := newKeysServer(t, issuer, &keys, &mu)
	defer srv.Close()

	v, err := NewOIDCVerifier(context.Background(), OIDCConfig{
		Issuer:    issuer,
		JWKSURL:   srv.URL + "/jwks",
		RoomClaim: "room",
		CapsClaim: "caps",
		Client:    srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHub(Config{OIDC: v})

	req := func(token string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/stats", nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		return r
	}
	if h.adminAllowed(req("")) {
		t.Fatal("anonymous admin allowed with OIDC enabled")
	}
	user := signJWT(t, key, "k1", baseClaims(issuer, ""))
	if h.adminAllowed(req(user)) {
		t.Fatal("non-admin OIDC token allowed")
	}
	adminClaims := baseClaims(issuer, "")
	adminClaims["caps"] = "read admin"
	adminClaims["room"] = "*"
	if !h.adminAllowed(req(signJWT(t, key, "k1", adminClaims))) {
		t.Fatal("admin OIDC token rejected")
	}

	open := NewHub(Config{})
	if !open.adminAllowed(req("")) {
		t.Fatal("open hub should allow anonymous admin")
	}
}

func TestOIDCConfigRejectedWithoutIssuer(t *testing.T) {
	if _, err := NewOIDCVerifier(context.Background(), OIDCConfig{JWKSURL: "http://x"}); err == nil {
		t.Fatal("expected error without issuer")
	}
}

func TestOIDCCapsClaimShapes(t *testing.T) {
	claims := jwt.MapClaims{
		"a": "read, write  presence",
		"b": []any{"read", "admin"},
		"c": []string{"write"},
	}
	if got := strings.Join(capsClaim(claims, "a"), ","); got != "read,write,presence" {
		t.Fatalf("string caps = %q", got)
	}
	if got := strings.Join(capsClaim(claims, "b"), ","); got != "read,admin" {
		t.Fatalf("array caps = %q", got)
	}
	if got := strings.Join(capsClaim(claims, "c"), ","); got != "write" {
		t.Fatalf("string-slice caps = %q", got)
	}
	if got := capsClaim(claims, "missing"); len(got) != 0 {
		t.Fatalf("missing caps = %v", got)
	}
}

func TestOIDCRejectsPlaintextMetadataURLs(t *testing.T) {
	if _, err := NewOIDCVerifier(context.Background(), OIDCConfig{
		Issuer:  "http://idp.example",
		JWKSURL: "https://idp.example/jwks",
	}); err == nil {
		t.Fatal("plaintext issuer accepted")
	}
	if _, err := NewOIDCVerifier(context.Background(), OIDCConfig{
		Issuer:  "https://idp.example",
		JWKSURL: "http://idp.example/jwks",
	}); err == nil {
		t.Fatal("plaintext JWKS URL accepted")
	}
}
