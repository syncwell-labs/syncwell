package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/golang-jwt/jwt/v5"
)

// OIDCConfig configures verification of external identity-provider JWTs.
type OIDCConfig struct {
	Issuer    string        // required; checked against the iss claim
	Audience  string        // optional; checked against aud when set
	JWKSURL   string        // optional; discovered from the issuer when empty
	RoomClaim string        // default "syncwell_room"
	CapsClaim string        // default "syncwell_caps"
	Refresh   time.Duration // default 15m
	Client    *http.Client  // default 5s timeout
}

// OIDCVerifier validates RS*/ES*/PS* JWTs against a cached JWKS. It fails
// closed: a token is accepted only after signature, issuer, audience, expiry,
// and room-claim checks pass.
type OIDCVerifier struct {
	issuer    string
	audience  string
	roomClaim string
	capsClaim string
	jwksURL   string
	refresh   time.Duration
	client    *http.Client

	refreshMu sync.Mutex
	mu        sync.RWMutex
	keys      map[string]any
	fetched   time.Time
	attempted time.Time
}

var oidcMethods = []string{
	"RS256", "RS384", "RS512",
	"PS256", "PS384", "PS512",
	"ES256", "ES384", "ES512",
}

const (
	defaultOIDCRoomClaim = "syncwell_room"
	defaultOIDCCapsClaim = "syncwell_caps"
	defaultOIDCRefresh   = 15 * time.Minute
)

// NewOIDCVerifier builds a verifier. When JWKSURL is empty it discovers the
// jwks_uri from <issuer>/.well-known/openid-configuration. A non-nil error
// means the initial JWKS fetch failed; the verifier is still returned and will
// retry on demand (tokens fail closed until a fetch succeeds).
func NewOIDCVerifier(ctx context.Context, cfg OIDCConfig) (*OIDCVerifier, error) {
	if strings.TrimSpace(cfg.Issuer) == "" {
		return nil, errors.New("oidc: issuer is required")
	}
	if cfg.RoomClaim == "" {
		cfg.RoomClaim = defaultOIDCRoomClaim
	}
	if cfg.CapsClaim == "" {
		cfg.CapsClaim = defaultOIDCCapsClaim
	}
	if cfg.Refresh <= 0 {
		cfg.Refresh = defaultOIDCRefresh
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 5 * time.Second}
	}
	v := &OIDCVerifier{
		issuer:    cfg.Issuer,
		audience:  cfg.Audience,
		roomClaim: cfg.RoomClaim,
		capsClaim: cfg.CapsClaim,
		jwksURL:   cfg.JWKSURL,
		refresh:   cfg.Refresh,
		client:    cfg.Client,
		keys:      map[string]any{},
	}
	if v.jwksURL == "" {
		if err := v.discover(ctx); err != nil {
			return v, err
		}
	}
	if err := v.refreshKeys(ctx); err != nil {
		return v, err
	}
	return v, nil
}

func (v *OIDCVerifier) discover(ctx context.Context) error {
	url := strings.TrimRight(v.issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("oidc discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("oidc discovery: status %d", resp.StatusCode)
	}
	var doc struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return fmt.Errorf("oidc discovery: %w", err)
	}
	if doc.JWKSURI == "" {
		return errors.New("oidc discovery: no jwks_uri")
	}
	v.jwksURL = doc.JWKSURI
	return nil
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// refresh fetches the JWKS. Callers must hold refreshMu or be in NewOIDCVerifier.
func (v *OIDCVerifier) refreshKeys(ctx context.Context) error {
	v.refreshMu.Lock()
	defer v.refreshMu.Unlock()
	v.mu.Lock()
	v.attempted = time.Now()
	v.mu.Unlock()
	if v.jwksURL == "" {
		return errors.New("oidc: no jwks_url")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("oidc jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("oidc jwks: status %d", resp.StatusCode)
	}
	var doc struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return fmt.Errorf("oidc jwks: %w", err)
	}
	keys := make(map[string]any, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		pub, err := k.publicKey()
		if err != nil {
			continue
		}
		keys[k.Kid] = pub
	}
	if len(keys) == 0 {
		return errors.New("oidc jwks: no usable signing keys")
	}
	v.mu.Lock()
	v.keys = keys
	v.fetched = time.Now()
	v.mu.Unlock()
	return nil
}

func (k jwk) publicKey() (any, error) {
	switch k.Kty {
	case "RSA":
		nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil || len(nBytes) == 0 {
			return nil, errors.New("bad RSA modulus")
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil || len(eBytes) == 0 || len(eBytes) > 4 {
			return nil, errors.New("bad RSA exponent")
		}
		e := 0
		for _, b := range eBytes {
			e = e<<8 | int(b)
		}
		if e < 3 {
			return nil, errors.New("bad RSA exponent")
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}, nil
	case "EC":
		var curve elliptic.Curve
		switch k.Crv {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return nil, fmt.Errorf("unsupported curve %q", k.Crv)
		}
		xb, errX := base64.RawURLEncoding.DecodeString(k.X)
		yb, errY := base64.RawURLEncoding.DecodeString(k.Y)
		if errX != nil || errY != nil {
			return nil, errors.New("bad EC point")
		}
		x, y := new(big.Int).SetBytes(xb), new(big.Int).SetBytes(yb)
		if !curve.IsOnCurve(x, y) {
			return nil, errors.New("EC point not on curve")
		}
		return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
	default:
		return nil, fmt.Errorf("unsupported key type %q", k.Kty)
	}
}

func (v *OIDCVerifier) lookup(kid string) (any, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if len(v.keys) == 0 {
		return nil, false
	}
	if kid != "" {
		k, ok := v.keys[kid]
		return k, ok
	}
	if len(v.keys) == 1 {
		for _, k := range v.keys {
			return k, true
		}
	}
	return nil, false
}

func (v *OIDCVerifier) stale() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if len(v.keys) == 0 {
		return time.Since(v.attempted) > 30*time.Second
	}
	return time.Since(v.fetched) > v.refresh
}

func (v *OIDCVerifier) maybeRefresh(ctx context.Context) {
	if !v.stale() {
		return
	}
	_ = v.refreshKeys(ctx)
}

func (v *OIDCVerifier) keyfunc(t *jwt.Token) (any, error) {
	kid, _ := t.Header["kid"].(string)
	if k, ok := v.lookup(kid); ok {
		return k, nil
	}
	// Unknown kid: the IdP may have rotated keys. Refetch, but not right after a
	// successful fetch (prevents unbounded fetches from random kid values). If
	// the cache is empty, back off for 30s after a failed attempt.
	v.mu.RLock()
	recentSuccess := len(v.keys) > 0 && time.Since(v.fetched) < 5*time.Second
	recentFailure := len(v.keys) == 0 && time.Since(v.attempted) < 30*time.Second
	v.mu.RUnlock()
	if !recentSuccess && !recentFailure {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := v.refreshKeys(ctx); err != nil {
			return nil, err
		}
	}
	if k, ok := v.lookup(kid); ok {
		return k, nil
	}
	return nil, fmt.Errorf("oidc: unknown signing key %q", kid)
}

// Verify validates a JWT for a room. Passing room "*" skips the room-claim
// match (used for admin endpoints) but does not grant admin by itself.
func (v *OIDCVerifier) Verify(raw, room string, now time.Time) (TokenClaims, bool) {
	if raw == "" {
		return TokenClaims{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	v.maybeRefresh(ctx)
	cancel()

	claims := jwt.MapClaims{}
	opts := []jwt.ParserOption{
		jwt.WithValidMethods(oidcMethods),
		jwt.WithIssuer(v.issuer),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(30 * time.Second),
		jwt.WithTimeFunc(func() time.Time { return now }),
	}
	if v.audience != "" {
		opts = append(opts, jwt.WithAudience(v.audience))
	}
	parser := jwt.NewParser(opts...)
	token, err := parser.ParseWithClaims(raw, claims, v.keyfunc)
	if err != nil || !token.Valid {
		return TokenClaims{}, false
	}

	claimedRoom := stringClaim(claims, v.roomClaim)
	// A wildcard room token is only ever an admin credential (same rule as
	// HMAC v2): on a room-scoped check it must never grant access to every
	// room with whatever capabilities it carries.
	if claimedRoom == "*" && room != "*" && room != "" {
		return TokenClaims{}, false
	}
	if room != "*" && claimedRoom != room && claimedRoom != "*" {
		return TokenClaims{}, false
	}

	caps := capsClaim(claims, v.capsClaim)
	if len(caps) == 0 {
		// Fail closed: a missing or malformed caps claim is a deny, never a
		// silent grant of the default read/write/presence set. IdPs that do
		// not provision the claim leave every authenticated user with write
		// access otherwise.
		return TokenClaims{}, false
	}
	out := TokenClaims{
		Room: claimedRoom,
		Caps: map[string]bool{},
		Raw:  raw,
		JTI:  stringClaim(claims, "jti"),
		Sub:  stringClaim(claims, "sub"),
	}
	if out.Room == "" {
		out.Room = room
	}
	for _, c := range caps {
		out.Caps[c] = true
	}
	if exp, err := claims.GetExpirationTime(); err == nil && exp != nil {
		out.Exp = exp.Unix()
	}
	return out, true
}

func stringClaim(claims jwt.MapClaims, key string) string {
	if v, ok := claims[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func capsClaim(claims jwt.MapClaims, key string) []string {
	v, ok := claims[key]
	if !ok {
		return nil
	}
	var raw []string
	switch t := v.(type) {
	case string:
		raw = strings.FieldsFunc(t, func(r rune) bool {
			return r == ',' || unicode.IsSpace(r)
		})
	case []any:
		for _, item := range t {
			if s, ok := item.(string); ok {
				raw = append(raw, s)
			}
		}
	case []string:
		raw = t
	}
	out := make([]string, 0, len(raw))
	for _, c := range raw {
		if c = strings.TrimSpace(c); c != "" {
			out = append(out, c)
		}
	}
	return out
}
