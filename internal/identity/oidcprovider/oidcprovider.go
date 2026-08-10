// Package oidcprovider is the driven adapter that signs the hub in against an
// OpenID Connect provider, as a confidential client.
//
// Everything a protocol brings with it is confined here: metadata discovery, the
// signing keys and their rotation, the code exchange, PKCE, and the checks that make
// an identity assertion believable — signature, issuer, audience, expiry and the
// nonce of the very request that asked for it. The core sees none of it; it asks for
// an authorization URL and hands back a callback (see identity.Provider).
//
// The client secret and every token stay on this side. The browser is given a
// session cookie and nothing else, which is the reason the hub is a confidential
// client at all: one named provider offers no secret-less flow, browser-held refresh
// tokens are defeated by tracking protections, and a long-lived server-push stream
// must outlive an access token without the page doing anything about it.
package oidcprovider

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"temporal-agents/internal/identity"
)

// DefaultScopes are what the hub asks a provider for: the subject (openid), the
// display fields a surface shows (profile, email), and a refresh token so a session
// can outlive one access token (offline_access).
//
// Nothing here asks for authorization of any kind. The hub records identity and has
// no roles, so a scope beyond these would be access it does not need.
var DefaultScopes = []string{oidc.ScopeOpenID, "profile", "email", oidc.ScopeOfflineAccess}

// discoveryTimeout bounds reading a provider's metadata at startup. A provider that
// does not answer must stop the server with a message, not hang it before it logs a
// line.
const discoveryTimeout = 15 * time.Second

// Config is what an operator supplies to point the hub at a provider. It is
// configuration, never code: a different provider is a different issuer and client,
// not a different build.
type Config struct {
	// Issuer is the provider's issuer identifier, from which its metadata is
	// discovered.
	Issuer string
	// ClientID and ClientSecret are this deployment's registered client. The secret
	// never leaves the server.
	ClientID     string
	ClientSecret string
	// Scopes overrides DefaultScopes, for a provider that names things differently
	// or issues no refresh tokens.
	Scopes []string
	// HTTPClient is the client used for discovery, the key set and the token
	// endpoint. It defaults to a client with a bounded timeout.
	HTTPClient *http.Client
}

// Provider is the OIDC adapter. One instance serves every sign-in, so the key set is
// fetched once and rotated in the background rather than per request.
type Provider struct {
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
	issuer   string
	// endSession is the provider's end-session endpoint when it published one, so
	// signing out can also end the session at the provider.
	endSession string
	// client is the HTTP client every call to the provider uses.
	client *http.Client
}

// Compile-time proof the adapter satisfies the seam the core depends on.
var _ identity.Provider = (*Provider)(nil)

// New discovers the provider's metadata and builds the adapter.
//
// Discovery happens once, at startup, and a failure here stops the server: a hub
// that cannot reach its provider cannot sign anybody in, and finding that out on the
// operator's first click is strictly worse than finding it out on start.
func New(ctx context.Context, config Config) (*Provider, error) {
	switch {
	case strings.TrimSpace(config.Issuer) == "":
		return nil, errors.New("the identity provider's issuer is required")
	case strings.TrimSpace(config.ClientID) == "":
		return nil, errors.New("the identity provider's client id is required")
	case strings.TrimSpace(config.ClientSecret) == "":
		// A confidential client with no secret is a public client, which is exactly the
		// topology this adapter exists not to be.
		return nil, errors.New("the identity provider's client secret is required")
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: discoveryTimeout}
	}
	discoveryCtx, cancel := context.WithTimeout(oidc.ClientContext(ctx, client), discoveryTimeout)
	defer cancel()

	discovered, err := oidc.NewProvider(discoveryCtx, config.Issuer)
	if err != nil {
		return nil, fmt.Errorf("%w: discover %s: %w", identity.ErrProviderUnavailable, config.Issuer, err)
	}
	scopes := config.Scopes
	if len(scopes) == 0 {
		scopes = DefaultScopes
	}
	// The metadata's own claims carry the sign-out endpoint, which is optional and
	// which nothing here depends on: ending the hub's session never waits for the
	// provider.
	var metadata struct {
		EndSessionEndpoint string `json:"end_session_endpoint"`
	}
	_ = discovered.Claims(&metadata)

	return &Provider{
		oauth: oauth2.Config{
			ClientID:     config.ClientID,
			ClientSecret: config.ClientSecret,
			Endpoint:     discovered.Endpoint(),
			Scopes:       scopes,
		},
		// The verifier checks the signature against the provider's published keys
		// (re-fetched when a key rotates), the issuer, the audience and the expiry.
		// Nothing is switched off: a check retrofitted later is a check that was absent
		// for every sign-in in between.
		verifier: discovered.Verifier(&oidc.Config{ClientID: config.ClientID}),
		// The principal is attributed to the configured issuer, which discovery has just
		// confirmed the provider claims for itself.
		issuer:     config.Issuer,
		endSession: metadata.EndSessionEndpoint,
		client:     client,
	}, nil
}

// Issuer implements identity.Provider.
func (p *Provider) Issuer() string { return p.issuer }

// AuthorizationURL implements identity.Provider: where the browser is sent to sign
// in.
//
// The state and the nonce are the core's, and the PKCE challenge is derived from the
// core's per-request verifier, so an intercepted code cannot be exchanged by anybody
// who did not start the request.
func (p *Provider) AuthorizationURL(_ context.Context, request identity.AuthorizationRequest) (string, error) {
	if request.RedirectURI == "" {
		return "", errors.New("the redirect URI is required")
	}
	config := p.oauth
	config.RedirectURL = request.RedirectURI
	return config.AuthCodeURL(request.State,
		oidc.Nonce(request.Nonce),
		oauth2.S256ChallengeOption(request.CodeVerifier),
	), nil
}

// Exchange implements identity.Provider: trade the code for a verified identity.
//
// Every refusal is one error to the caller. Which check failed is a detail an
// attacker would like and an operator finds in the log, so the wording says what
// happened without saying which of the provider's claims was wrong.
func (p *Provider) Exchange(ctx context.Context, request identity.ExchangeRequest) (identity.Identity, error) {
	config := p.oauth
	config.RedirectURL = request.RedirectURI
	ctx = oidc.ClientContext(ctx, p.client)

	token, err := config.Exchange(ctx, request.Code, oauth2.VerifierOption(request.CodeVerifier))
	if err != nil {
		return identity.Identity{}, exchangeError("exchange the authorization code", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return identity.Identity{}, errors.New("the provider returned no id token")
	}
	// Signature, issuer, audience and expiry are checked here; the nonce is checked
	// below, because only the core knows which request this assertion has to belong
	// to.
	idToken, err := p.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return identity.Identity{}, fmt.Errorf("the provider's id token did not verify: %w", err)
	}
	if idToken.Nonce != request.Nonce {
		return identity.Identity{}, errors.New("the provider's id token belongs to a different sign-in")
	}
	principal, err := principalFrom(p.issuer, idToken)
	if err != nil {
		return identity.Identity{}, err
	}
	return identity.Identity{Principal: principal, Tokens: tokensFrom(token)}, nil
}

// Refresh implements identity.Provider: renew the tokens held for a session.
//
// A provider that refuses the refresh token ends the session (the core's rule); a
// provider that cannot be reached does not, which is why the two are distinguished
// here rather than collapsed into one failure.
func (p *Provider) Refresh(ctx context.Context, refreshToken string) (identity.Tokens, error) {
	if refreshToken == "" {
		return identity.Tokens{}, identity.ErrUnauthenticated
	}
	ctx = oidc.ClientContext(ctx, p.client)
	renewed, err := p.oauth.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken}).Token()
	if err != nil {
		if unreachable(err) {
			return identity.Tokens{}, fmt.Errorf("%w: %w", identity.ErrProviderUnavailable, err)
		}
		return identity.Tokens{}, fmt.Errorf("%w: the provider refused the refresh token", identity.ErrUnauthenticated)
	}
	return tokensFrom(renewed), nil
}

// SignOutURL implements identity.Provider. It is empty for a provider that publishes
// no end-session endpoint, and the hub signs the browser out locally either way.
func (p *Provider) SignOutURL(_ context.Context, returnTo string) string {
	if p.endSession == "" {
		return ""
	}
	if returnTo == "" {
		return p.endSession
	}
	separator := "?"
	if strings.Contains(p.endSession, "?") {
		separator = "&"
	}
	return p.endSession + separator + url.Values{"post_logout_redirect_uri": {returnTo}}.Encode()
}

// claims are the fields the hub reads from an identity assertion: a stable subject,
// and whatever the provider chose to disclose for display.
type claims struct {
	Subject           string `json:"sub"`
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
	Email             string `json:"email"`
}

// principalFrom maps a verified assertion onto a principal. The subject is the
// identity; a name and an address are display fields, and their absence is not a
// failure.
func principalFrom(issuer string, idToken *oidc.IDToken) (identity.Principal, error) {
	var parsed claims
	if err := idToken.Claims(&parsed); err != nil {
		return identity.Principal{}, fmt.Errorf("read the provider's claims: %w", err)
	}
	subject := parsed.Subject
	if subject == "" {
		subject = idToken.Subject
	}
	if subject == "" {
		return identity.Principal{}, errors.New("the provider disclosed no subject")
	}
	name := parsed.Name
	if name == "" {
		name = parsed.PreferredUsername
	}
	return identity.Principal{
		Issuer:  issuer,
		Subject: subject,
		Name:    name,
		Email:   parsed.Email,
	}, nil
}

// tokensFrom copies what the provider issued into the core's vocabulary. The access
// token's expiry is kept as the provider stated it, and left zero when it stated
// none, so the core is never told an expiry that was invented here.
func tokensFrom(token *oauth2.Token) identity.Tokens {
	tokens := identity.Tokens{Access: token.AccessToken, Refresh: token.RefreshToken}
	if !token.Expiry.IsZero() {
		tokens.ExpiresAt = token.Expiry
	}
	return tokens
}

// exchangeError distinguishes a provider that refused from a provider that could not
// be reached, so a transient outage is not reported as a failed sign-in the operator
// should read as suspicious.
func exchangeError(what string, err error) error {
	if unreachable(err) {
		return fmt.Errorf("%w: %s: %w", identity.ErrProviderUnavailable, what, err)
	}
	return fmt.Errorf("%s: %w", what, err)
}

// unreachable reports whether a failure is about reaching the provider rather than
// about what it answered.
func unreachable(err error) bool {
	var retrieve *oauth2.RetrieveError
	if errors.As(err, &retrieve) {
		// The provider answered, and said no.
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var urlErr *url.Error
	return errors.As(err, &urlErr) || errors.Is(err, context.DeadlineExceeded)
}
