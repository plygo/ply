// Package oidc discovers OpenID Connect providers and verifies ID tokens.
package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/plygo/ply/oauth2"
)

type ProviderConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	AuthStyle    oauth2.AuthStyle
	Scopes       []string
	HTTPClient   *http.Client
	CacheTTL     time.Duration
	MinRefresh   time.Duration
	Leeway       time.Duration
	OnError      func(error)
}

type Provider struct {
	Issuer        string
	ClientID      string
	AuthURL       string
	TokenURL      string
	UserInfoURL   string
	JWKSURL       string
	EndSessionURL string
	Scopes        []string
	HTTPClient    *http.Client
	clientSecret  string
	authStyle     oauth2.AuthStyle
	verifier      *Verifier
}

type Discovery struct {
	Issuer                           string   `json:"issuer"`
	AuthorizationEndpoint            string   `json:"authorization_endpoint"`
	TokenEndpoint                    string   `json:"token_endpoint"`
	UserInfoEndpoint                 string   `json:"userinfo_endpoint,omitempty"`
	JWKSURI                          string   `json:"jwks_uri"`
	EndSessionEndpoint               string   `json:"end_session_endpoint,omitempty"`
	ScopesSupported                  []string `json:"scopes_supported,omitempty"`
	CodeChallengeMethodsSupported    []string `json:"code_challenge_methods_supported,omitempty"`
	IDTokenSigningAlgValuesSupported []string `json:"id_token_signing_alg_values_supported,omitempty"`
}

func Discover(ctx context.Context, issuer string, client *http.Client) (*Discovery, error) {
	issuer = strings.TrimSuffix(issuer, "/")
	if issuer == "" {
		return nil, errors.New("oidc: issuer is required")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		issuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("oidc: discovery returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var doc Discovery
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("oidc: decode discovery: %w", err)
	}
	if strings.TrimSuffix(doc.Issuer, "/") != issuer {
		return nil, fmt.Errorf("oidc: issuer mismatch: got %q, want %q", doc.Issuer, issuer)
	}
	doc.AuthorizationEndpoint = resolveEndpoint(issuer, doc.AuthorizationEndpoint)
	doc.TokenEndpoint = resolveEndpoint(issuer, doc.TokenEndpoint)
	doc.UserInfoEndpoint = resolveEndpoint(issuer, doc.UserInfoEndpoint)
	doc.JWKSURI = resolveEndpoint(issuer, doc.JWKSURI)
	doc.EndSessionEndpoint = resolveEndpoint(issuer, doc.EndSessionEndpoint)
	if doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" || doc.JWKSURI == "" {
		return nil, errors.New("oidc: discovery is missing required endpoints")
	}
	return &doc, nil
}

func NewProvider(ctx context.Context, cfg ProviderConfig) (*Provider, error) {
	cfg.Issuer = strings.TrimSuffix(cfg.Issuer, "/")
	if cfg.Issuer == "" {
		return nil, errors.New("oidc: issuer is required")
	}
	if cfg.ClientID == "" {
		return nil, errors.New("oidc: client id is required")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}

	doc, err := Discover(ctx, cfg.Issuer, cfg.HTTPClient)
	if err != nil {
		return nil, err
	}
	verifier, err := NewVerifier(VerifierConfig{
		Issuer:     cfg.Issuer,
		Audience:   cfg.ClientID,
		JWKSURL:    doc.JWKSURI,
		HTTPClient: cfg.HTTPClient,
		CacheTTL:   cfg.CacheTTL,
		MinRefresh: cfg.MinRefresh,
		Leeway:     cfg.Leeway,
		OnError:    cfg.OnError,
	})
	if err != nil {
		return nil, err
	}

	scopes := append([]string(nil), cfg.Scopes...)
	if len(scopes) == 0 {
		scopes = []string{"openid"}
	}

	return &Provider{
		Issuer:        cfg.Issuer,
		ClientID:      cfg.ClientID,
		AuthURL:       doc.AuthorizationEndpoint,
		TokenURL:      doc.TokenEndpoint,
		UserInfoURL:   doc.UserInfoEndpoint,
		JWKSURL:       doc.JWKSURI,
		EndSessionURL: doc.EndSessionEndpoint,
		Scopes:        scopes,
		HTTPClient:    cfg.HTTPClient,
		clientSecret:  cfg.ClientSecret,
		authStyle:     cfg.AuthStyle,
		verifier:      verifier,
	}, nil
}

func (p *Provider) OAuth2Config(redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     p.ClientID,
		ClientSecret: p.clientSecret,
		RedirectURL:  redirectURL,
		Scopes:       append([]string(nil), p.Scopes...),
		AuthStyle:    p.authStyle,
		Endpoint: oauth2.Endpoint{
			AuthURL:  p.AuthURL,
			TokenURL: p.TokenURL,
		},
		HTTPClient: p.HTTPClient,
	}
}

func (p *Provider) AuthCodeURL(state, nonce, verifier, redirectURL string) string {
	extra := url.Values{}
	if nonce != "" {
		extra.Set("nonce", nonce)
	}
	return p.OAuth2Config(redirectURL).AuthCodeURLWithParams(state, verifier, extra)
}

func (p *Provider) Exchange(ctx context.Context, code, verifier, redirectURL string) (*oauth2.Token, error) {
	return p.OAuth2Config(redirectURL).Exchange(ctx, code, verifier)
}

func (p *Provider) UserInfo(ctx context.Context, token *oauth2.Token, dest any) error {
	if p.UserInfoURL == "" {
		return errors.New("oidc: provider has no userinfo endpoint")
	}
	return p.OAuth2Config("").UserInfo(ctx, token, p.UserInfoURL, dest)
}

func (p *Provider) Verifier() *Verifier {
	return p.verifier
}

func resolveEndpoint(base, endpoint string) string {
	if endpoint == "" {
		return ""
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	if u.IsAbs() {
		return endpoint
	}
	b, err := url.Parse(base)
	if err != nil {
		return endpoint
	}
	if !strings.HasSuffix(b.Path, "/") {
		b.Path += "/"
	}
	return b.ResolveReference(u).String()
}
