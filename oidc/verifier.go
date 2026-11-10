package oidc

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/plygo/ply/core"
)

const ClaimsKey = "oidc.claims"

var (
	ErrInvalidToken = errors.New("oidc: invalid token")
	ErrKeyNotFound  = errors.New("oidc: signing key not found")
)

var supportedAlgorithms = []string{
	"RS256", "RS384", "RS512",
	"PS256", "PS384", "PS512",
	"ES256", "ES384", "ES512",
	"EdDSA",
}

type VerifierConfig struct {
	Issuer     string
	Audience   string
	JWKSURL    string
	HTTPClient *http.Client
	CacheTTL   time.Duration
	MinRefresh time.Duration
	Leeway     time.Duration
	OnError    func(error)
}

type Verifier struct {
	issuer     string
	audience   string
	jwksURL    string
	client     *http.Client
	cacheTTL   time.Duration
	minRefresh time.Duration
	leeway     time.Duration
	onError    func(error)

	mu          sync.Mutex
	keys        map[string]signingKey
	expiresAt   time.Time
	lastRefresh time.Time
}

type signingKey struct {
	key any
	alg string
	kty string
}

type Claims struct {
	Subject   string
	Issuer    string
	Audience  []string
	Expiry    time.Time
	IssuedAt  time.Time
	NotBefore time.Time
	Nonce     string
	Email     string
	Name      string
	Raw       map[string]any
}

func NewVerifier(cfg VerifierConfig) (*Verifier, error) {
	if cfg.Issuer == "" {
		return nil, errors.New("oidc: issuer is required")
	}
	if cfg.JWKSURL == "" {
		return nil, errors.New("oidc: jwks url is required")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = 5 * time.Minute
	}
	if cfg.MinRefresh <= 0 {
		cfg.MinRefresh = 10 * time.Second
	}
	if cfg.Leeway <= 0 {
		cfg.Leeway = time.Minute
	}

	return &Verifier{
		issuer:     strings.TrimSuffix(cfg.Issuer, "/"),
		audience:   cfg.Audience,
		jwksURL:    cfg.JWKSURL,
		client:     cfg.HTTPClient,
		cacheTTL:   cfg.CacheTTL,
		minRefresh: cfg.MinRefresh,
		leeway:     cfg.Leeway,
		onError:    cfg.OnError,
		keys:       make(map[string]signingKey),
	}, nil
}

func (v *Verifier) Verify(ctx context.Context, raw string) (*Claims, error) {
	if raw == "" {
		return nil, ErrInvalidToken
	}

	claims := jwt.MapClaims{}
	options := []jwt.ParserOption{
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods(supportedAlgorithms),
		jwt.WithLeeway(v.leeway),
	}
	if v.issuer != "" {
		options = append(options, jwt.WithIssuer(v.issuer))
	}
	if v.audience != "" {
		options = append(options, jwt.WithAudience(v.audience))
	}

	token, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		return v.signingKey(ctx, token)
	}, options...)
	if err != nil {
		return nil, fmt.Errorf("oidc: verify token: %w", err)
	}
	if !token.Valid {
		return nil, ErrInvalidToken
	}
	if v.audience != "" {
		audiences, _ := claims.GetAudience()
		if len(audiences) > 1 {
			azp, _ := claims["azp"].(string)
			if azp != v.audience {
				return nil, errors.New("oidc: authorized party does not match audience")
			}
		}
	}
	return claimsFrom(claims), nil
}

func (v *Verifier) Middleware() core.Handler {
	return func(c *core.Context) {
		raw := bearerToken(c.Header("Authorization"))
		if raw == "" {
			unauthorized(c, false)
			return
		}

		claims, err := v.Verify(c.Ctx, raw)
		if err != nil {
			v.report(err)
			unauthorized(c, true)
			return
		}
		c.SetSession(ClaimsKey, claims)
		c.Next()
	}
}

func ClaimsFrom(c *core.Context) *Claims {
	claims, _ := c.GetSession(ClaimsKey).(*Claims)
	return claims
}

func ValidateNonce(claims *Claims, expected string) bool {
	if claims == nil || claims.Nonce == "" || expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(expected)) == 1
}

func (v *Verifier) signingKey(ctx context.Context, token *jwt.Token) (any, error) {
	alg := token.Method.Alg()
	if !isSupportedAlgorithm(alg) {
		return nil, fmt.Errorf("oidc: unsupported signing algorithm %q", alg)
	}

	kid, _ := token.Header["kid"].(string)
	if key, ok, fresh := v.lookup(kid); ok && fresh {
		if !compatible(alg, key.kty) || (key.alg != "" && key.alg != alg) {
			return nil, fmt.Errorf("oidc: signing key is not compatible with %s", alg)
		}
		return key.key, nil
	}

	if err := v.refresh(ctx); err != nil {
		return nil, err
	}

	key, ok, fresh := v.lookup(kid)
	if !ok || !fresh {
		return nil, fmt.Errorf("%w: kid %q", ErrKeyNotFound, kid)
	}
	if !compatible(alg, key.kty) || (key.alg != "" && key.alg != alg) {
		return nil, fmt.Errorf("oidc: signing key is not compatible with %s", alg)
	}
	return key.key, nil
}

func (v *Verifier) lookup(kid string) (signingKey, bool, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()

	fresh := time.Now().Before(v.expiresAt)
	if len(v.keys) == 0 {
		return signingKey{}, false, false
	}
	if kid != "" {
		key, ok := v.keys[kid]
		return key, ok, fresh
	}
	if len(v.keys) == 1 {
		for _, key := range v.keys {
			return key, true, fresh
		}
	}
	return signingKey{}, false, fresh
}

func (v *Verifier) refresh(ctx context.Context) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	now := time.Now()
	if len(v.keys) > 0 && now.Before(v.expiresAt) && !v.lastRefresh.IsZero() &&
		now.Sub(v.lastRefresh) < v.minRefresh {
		return nil
	}
	v.lastRefresh = now

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := v.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("oidc: jwks returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var set jwks
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&set); err != nil {
		return fmt.Errorf("oidc: decode jwks: %w", err)
	}
	keys := make(map[string]signingKey, len(set.Keys))
	for i, raw := range set.Keys {
		if raw.Use != "" && raw.Use != "sig" {
			continue
		}
		key, kty, err := parseJWK(raw)
		if err != nil {
			continue
		}
		id := raw.Kid
		if id == "" {
			id = fmt.Sprintf("#%d", i)
		}
		keys[id] = signingKey{key: key, alg: raw.Alg, kty: kty}
	}
	if len(keys) == 0 {
		return errors.New("oidc: jwks has no usable signing keys")
	}

	v.keys = keys
	v.expiresAt = time.Now().Add(cacheTTL(v.cacheTTL, resp.Header.Get("Cache-Control")))
	return nil
}

func (v *Verifier) report(err error) {
	if err != nil && v.onError != nil {
		v.onError(err)
	}
}

type jwks struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	Crv string `json:"crv"`
	N   string `json:"n"`
	E   string `json:"e"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func parseJWK(raw jwk) (any, string, error) {
	switch raw.Kty {
	case "RSA":
		n, err := decodeBigInt(raw.N)
		if err != nil || n.Sign() <= 0 {
			return nil, "", errors.New("oidc: invalid rsa modulus")
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(raw.E)
		if err != nil || len(eBytes) == 0 || len(eBytes) > 4 {
			return nil, "", errors.New("oidc: invalid rsa exponent")
		}
		e := 0
		for _, b := range eBytes {
			e = e<<8 | int(b)
		}
		if e < 3 {
			return nil, "", errors.New("oidc: invalid rsa exponent")
		}
		return &rsa.PublicKey{N: n, E: e}, "RSA", nil

	case "EC":
		curve, err := curveFor(raw.Crv)
		if err != nil {
			return nil, "", err
		}
		x, err := decodeBigInt(raw.X)
		if err != nil {
			return nil, "", err
		}
		y, err := decodeBigInt(raw.Y)
		if err != nil {
			return nil, "", err
		}
		if !curve.IsOnCurve(x, y) {
			return nil, "", errors.New("oidc: ec point is not on the curve")
		}
		return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, "EC", nil

	case "OKP":
		if raw.Crv != "Ed25519" {
			return nil, "", errors.New("oidc: unsupported okp curve")
		}
		x, err := base64.RawURLEncoding.DecodeString(raw.X)
		if err != nil || len(x) != ed25519.PublicKeySize {
			return nil, "", errors.New("oidc: invalid ed25519 key")
		}
		return ed25519.PublicKey(x), "OKP", nil
	}
	return nil, "", errors.New("oidc: unsupported key type")
}

func decodeBigInt(value string) (*big.Int, error) {
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(b), nil
}

func curveFor(name string) (elliptic.Curve, error) {
	switch name {
	case "P-256":
		return elliptic.P256(), nil
	case "P-384":
		return elliptic.P384(), nil
	case "P-521":
		return elliptic.P521(), nil
	}
	return nil, errors.New("oidc: unsupported ec curve")
}

func cacheTTL(fallback time.Duration, header string) time.Duration {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if !strings.HasPrefix(part, "max-age=") {
			continue
		}
		seconds, err := strconv.Atoi(strings.TrimPrefix(part, "max-age="))
		if err != nil || seconds <= 0 {
			continue
		}
		ttl := time.Duration(seconds) * time.Second
		if ttl < 30*time.Second {
			return 30 * time.Second
		}
		if ttl > time.Hour {
			return time.Hour
		}
		return ttl
	}
	return fallback
}

func compatible(alg, kty string) bool {
	switch {
	case strings.HasPrefix(alg, "RS"), strings.HasPrefix(alg, "PS"):
		return kty == "RSA"
	case strings.HasPrefix(alg, "ES"):
		return kty == "EC"
	case alg == "EdDSA":
		return kty == "OKP"
	}
	return false
}

func isSupportedAlgorithm(alg string) bool {
	for _, supported := range supportedAlgorithms {
		if alg == supported {
			return true
		}
	}
	return false
}

func claimsFrom(claims jwt.MapClaims) *Claims {
	out := &Claims{Raw: map[string]any(claims)}
	if value, err := claims.GetSubject(); err == nil {
		out.Subject = value
	}
	if value, err := claims.GetIssuer(); err == nil {
		out.Issuer = value
	}
	if value, err := claims.GetAudience(); err == nil {
		out.Audience = []string(value)
	}
	if value, err := claims.GetExpirationTime(); err == nil && value != nil {
		out.Expiry = value.Time
	}
	if value, err := claims.GetIssuedAt(); err == nil && value != nil {
		out.IssuedAt = value.Time
	}
	if value, err := claims.GetNotBefore(); err == nil && value != nil {
		out.NotBefore = value.Time
	}
	if value, ok := claims["nonce"].(string); ok {
		out.Nonce = value
	}
	if value, ok := claims["email"].(string); ok {
		out.Email = value
	}
	if value, ok := claims["name"].(string); ok {
		out.Name = value
	}
	return out
}

func bearerToken(header string) string {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

func unauthorized(c *core.Context, invalid bool) {
	if invalid {
		c.Ctx.Response.Header.Set("WWW-Authenticate", `Bearer error="invalid_token"`)
	} else {
		c.Ctx.Response.Header.Set("WWW-Authenticate", "Bearer")
	}
	c.Text(401, "unauthorized")
	c.Abort()
}
