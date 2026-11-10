package oidc

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/plygo/ply"
	"github.com/plygo/ply/core"
	"github.com/plygo/ply/oauth2"
)

type testProvider struct {
	server       *httptest.Server
	jwksRequests atomic.Int64
	mu           sync.RWMutex
	key          *rsa.PrivateKey
	kid          string
}

func newTestProvider(t *testing.T) *testProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tp := &testProvider{key: key, kid: "key-1"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 tp.server.URL,
			"authorization_endpoint": tp.server.URL + "/authorize",
			"token_endpoint":         tp.server.URL + "/token",
			"userinfo_endpoint":      tp.server.URL + "/userinfo",
			"jwks_uri":               tp.server.URL + "/jwks",
			"end_session_endpoint":   tp.server.URL + "/logout",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		tp.jwksRequests.Add(1)
		tp.mu.RLock()
		key := tp.key
		kid := tp.kid
		tp.mu.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=60")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA",
				"kid": kid,
				"use": "sig",
				"alg": "RS256",
				"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			}},
		})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"sub": "user-1", "email": "user@example.com"})
	})
	tp.server = httptest.NewServer(mux)
	t.Cleanup(tp.server.Close)
	return tp
}

func (tp *testProvider) sign(t *testing.T, audience string, expires time.Time) string {
	t.Helper()
	tp.mu.RLock()
	key := tp.key
	kid := tp.kid
	tp.mu.RUnlock()

	claims := jwt.MapClaims{
		"iss":   tp.server.URL,
		"aud":   audience,
		"sub":   "user-1",
		"iat":   time.Now().Add(-time.Minute).Unix(),
		"exp":   expires.Unix(),
		"nonce": "nonce-1",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func (tp *testProvider) provider(t *testing.T) *Provider {
	t.Helper()
	provider, err := NewProvider(context.Background(), ProviderConfig{
		Issuer:     tp.server.URL,
		ClientID:   "client-1",
		MinRefresh: time.Nanosecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func TestProvider_DiscoveryAndVerify(t *testing.T) {
	tp := newTestProvider(t)
	provider := tp.provider(t)

	if provider.AuthURL != tp.server.URL+"/authorize" {
		t.Fatalf("auth url = %q", provider.AuthURL)
	}
	if provider.JWKSURL != tp.server.URL+"/jwks" {
		t.Fatalf("jwks url = %q", provider.JWKSURL)
	}

	cfg := provider.OAuth2Config("https://app.example/callback")
	if cfg.ClientID != "client-1" || len(cfg.Scopes) != 1 || cfg.Scopes[0] != "openid" {
		t.Fatalf("oauth2 config = %+v", cfg)
	}
	authURL, err := neturl.Parse(provider.AuthCodeURL("state-1", "nonce-1", "verifier-1", "https://app.example/callback"))
	if err != nil {
		t.Fatal(err)
	}
	if authURL.Query().Get("nonce") != "nonce-1" || authURL.Query().Get("code_challenge") == "" {
		t.Fatalf("auth url = %s", authURL)
	}

	token := tp.sign(t, "client-1", time.Now().Add(time.Hour))
	claims, err := provider.Verifier().Verify(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "user-1" || claims.Email != "" {
		t.Fatalf("claims = %+v", claims)
	}
	if !ValidateNonce(claims, "nonce-1") || ValidateNonce(claims, "other") {
		t.Fatal("nonce validation failed")
	}

	var profile struct {
		Subject string `json:"sub"`
		Email   string `json:"email"`
	}
	if err := provider.UserInfo(context.Background(), &oauth2.Token{AccessToken: "access-1"}, &profile); err != nil {
		t.Fatal(err)
	}
	if profile.Subject != "user-1" || profile.Email != "user@example.com" {
		t.Fatalf("profile = %+v", profile)
	}

	if _, err := provider.Verifier().Verify(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if got := tp.jwksRequests.Load(); got != 1 {
		t.Fatalf("jwks requests = %d", got)
	}
}

func TestVerifier_Rejects(t *testing.T) {
	tp := newTestProvider(t)
	verifier := tp.provider(t).Verifier()

	if _, err := verifier.Verify(context.Background(), ""); err == nil {
		t.Fatal("expected empty token error")
	}
	if _, err := verifier.Verify(context.Background(), tp.sign(t, "other-client", time.Now().Add(time.Hour))); err == nil {
		t.Fatal("expected audience error")
	}
	if _, err := verifier.Verify(context.Background(), tp.sign(t, "client-1", time.Now().Add(-time.Hour))); err == nil {
		t.Fatal("expected expiry error")
	}

	algToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": tp.server.URL,
		"aud": "client-1",
		"sub": "user-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	algToken.Header["kid"] = "key-1"
	signed, err := algToken.SignedString([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(context.Background(), signed); err == nil {
		t.Fatal("expected algorithm error")
	}
}

func TestVerifier_RefreshesUnknownKey(t *testing.T) {
	tp := newTestProvider(t)
	verifier := tp.provider(t).Verifier()

	first := tp.sign(t, "client-1", time.Now().Add(time.Hour))
	if _, err := verifier.Verify(context.Background(), first); err != nil {
		t.Fatal(err)
	}

	newKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tp.mu.Lock()
	tp.key = newKey
	tp.kid = "key-2"
	tp.mu.Unlock()

	second := tp.sign(t, "client-1", time.Now().Add(time.Hour))
	if _, err := verifier.Verify(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if got := tp.jwksRequests.Load(); got < 2 {
		t.Fatalf("jwks requests = %d", got)
	}
}

func TestMiddleware(t *testing.T) {
	tp := newTestProvider(t)
	verifier := tp.provider(t).Verifier()

	app := ply.New()
	app.Use(verifier.Middleware())
	app.Get("/me", func(c *core.Context) {
		claims := ClaimsFrom(c)
		if claims == nil {
			c.Text(500, "missing claims")
			return
		}
		c.Text(200, claims.Subject)
	})

	token := tp.sign(t, "client-1", time.Now().Add(time.Hour))
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp := app.Test(req)
	if resp.StatusCode != 200 || readBody(t, resp) != "user-1" {
		t.Fatalf("status = %d, body = %q", resp.StatusCode, readBody(t, resp))
	}

	missing := app.Test(httptest.NewRequest(http.MethodGet, "/me", nil))
	if missing.StatusCode != 401 {
		t.Fatalf("missing token status = %d", missing.StatusCode)
	}
	if got := missing.Header.Get("WWW-Authenticate"); got != "Bearer" {
		t.Fatalf("challenge = %q", got)
	}

	bad := httptest.NewRequest(http.MethodGet, "/me", nil)
	bad.Header.Set("Authorization", "Bearer invalid")
	badResp := app.Test(bad)
	if badResp.StatusCode != 401 {
		t.Fatalf("invalid token status = %d", badResp.StatusCode)
	}
	if got := badResp.Header.Get("WWW-Authenticate"); got != `Bearer error="invalid_token"` {
		t.Fatalf("challenge = %q", got)
	}
}

func TestProvider_DiscoveryIssuerMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 "https://wrong.example",
			"authorization_endpoint": "https://wrong.example/authorize",
			"token_endpoint":         "https://wrong.example/token",
			"jwks_uri":               "https://wrong.example/jwks",
		})
	}))
	defer server.Close()

	if _, err := NewProvider(context.Background(), ProviderConfig{Issuer: server.URL, ClientID: "client"}); err == nil {
		t.Fatal("expected issuer mismatch")
	}
}

func TestVerifier_ECDSAAndEdDSA(t *testing.T) {
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	edPublic, edPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	jwks := map[string]any{
		"keys": []map[string]any{
			{
				"kty": "EC",
				"kid": "ec-key",
				"use": "sig",
				"alg": "ES256",
				"crv": "P-256",
				"x":   base64.RawURLEncoding.EncodeToString(ecKey.X.FillBytes(make([]byte, 32))),
				"y":   base64.RawURLEncoding.EncodeToString(ecKey.Y.FillBytes(make([]byte, 32))),
			},
			{
				"kty": "OKP",
				"kid": "ed-key",
				"use": "sig",
				"alg": "EdDSA",
				"crv": "Ed25519",
				"x":   base64.RawURLEncoding.EncodeToString(edPublic),
			},
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	defer server.Close()

	verifier, err := NewVerifier(VerifierConfig{
		Issuer:     "https://issuer.example",
		Audience:   "client-1",
		JWKSURL:    server.URL,
		MinRefresh: time.Nanosecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		kid    string
		method jwt.SigningMethod
		key    any
	}{
		{name: "ecdsa", kid: "ec-key", method: jwt.SigningMethodES256, key: ecKey},
		{name: "eddsa", kid: "ed-key", method: jwt.SigningMethodEdDSA, key: edPrivate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := jwt.NewWithClaims(tt.method, jwt.MapClaims{
				"iss": "https://issuer.example",
				"aud": "client-1",
				"sub": "user-1",
				"exp": time.Now().Add(time.Hour).Unix(),
			})
			token.Header["kid"] = tt.kid
			signed, err := token.SignedString(tt.key)
			if err != nil {
				t.Fatal(err)
			}
			claims, err := verifier.Verify(context.Background(), signed)
			if err != nil {
				t.Fatal(err)
			}
			if claims.Subject != "user-1" {
				t.Fatalf("subject = %q", claims.Subject)
			}
		})
	}
}

func TestNewVerifier_Validation(t *testing.T) {
	if _, err := NewVerifier(VerifierConfig{}); err == nil {
		t.Fatal("expected issuer error")
	}
	if _, err := NewVerifier(VerifierConfig{Issuer: "https://issuer.example"}); err == nil {
		t.Fatal("expected jwks error")
	}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	var buf [256]byte
	n, _ := resp.Body.Read(buf[:])
	return string(buf[:n])
}
