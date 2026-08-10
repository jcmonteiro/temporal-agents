package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"temporal-agents/internal/httpapi"
	"temporal-agents/internal/identity"
	"temporal-agents/internal/identity/identitypg"
	"temporal-agents/internal/identity/oidcprovider"
)

// Signing in is configuration, not code. The issuer, the client and the URL the
// provider redirects back to arrive from the environment; a different provider is a
// different set of values, and a provider that is not OIDC would be one more adapter
// wired here. Nothing below decides anything about a session: that is the identity
// core's, and this file only assembles it.

// The environment the identity provider is configured from.
const (
	// oidcIssuerEnv points at the provider. Setting it is what turns signing in on.
	oidcIssuerEnv = "AGENT_HUB_OIDC_ISSUER"
	// oidcClientIDEnv and oidcClientSecretEnv are this deployment's registered
	// confidential client. The secret never leaves the server.
	oidcClientIDEnv     = "AGENT_HUB_OIDC_CLIENT_ID"
	oidcClientSecretEnv = "AGENT_HUB_OIDC_CLIENT_SECRET"
	// publicURLEnv is the URL a browser reaches this hub at, which is what the
	// provider redirects back to. It is configuration because only the deployment
	// knows what is in front of the process: a listener address is not a public URL
	// once a reverse proxy or a container is involved.
	publicURLEnv = "AGENT_HUB_PUBLIC_URL"
)

// The local compose stack's provider, which is also what an unconfigured loopback
// deployment signs in against. It is a default rather than something an operator
// must type, because the alternative to a working default here is either a
// configuration exercise before the first sign-in or an open API — and the second is
// what this whole feature exists to remove. The values are exactly the ones in
// deploy/dex/config.yaml.
const (
	localProviderIssuer       = "http://localhost:15556/dex"
	localProviderClientID     = "agent-hub"
	localProviderClientSecret = "agent-hub-local-secret"
)

// sessionSweepInterval is how often expired sessions and abandoned sign-ins are
// swept. It is generous: expiry is enforced on every read, so sweeping is
// housekeeping rather than a security control.
const sessionSweepInterval = time.Hour

// identityOptions are the values this deployment signs in with, or the zero value
// when no provider is configured.
type identityOptions struct {
	// provider is the discovered provider's configuration.
	provider oidcprovider.Config
	// redirectURI is this deployment's own callback, as registered with the
	// provider.
	redirectURI string
	// local marks the configuration as the local stack's default rather than an
	// operator's, so a provider that is not running can be reported with the command
	// that starts it.
	local bool
}

// configured reports whether this deployment can sign anybody in.
func (o identityOptions) configured() bool { return o.provider.Issuer != "" }

// identityConfiguration reads the provider's configuration from the environment and
// derives the callback URL.
//
// Half a configuration is refused rather than ignored: an operator who set an issuer
// and mistyped the client id must be told, not served a hub that silently cannot
// sign anybody in.
func identityConfiguration(options serveOptions, lookup func(string) string, useLocalDefault bool) (identityOptions, error) {
	issuer := strings.TrimSpace(lookup(oidcIssuerEnv))
	clientID := strings.TrimSpace(lookup(oidcClientIDEnv))
	clientSecret := strings.TrimSpace(lookup(oidcClientSecretEnv))
	local := false
	if issuer == "" && clientID == "" && clientSecret == "" && useLocalDefault {
		issuer, clientID, clientSecret = localProviderIssuer, localProviderClientID, localProviderClientSecret
		local = true
	}
	if issuer == "" {
		if clientID != "" || clientSecret != "" {
			return identityOptions{}, fmt.Errorf("%s is required when %s or %s is set",
				oidcIssuerEnv, oidcClientIDEnv, oidcClientSecretEnv)
		}
		return identityOptions{}, nil
	}
	if clientID == "" || clientSecret == "" {
		return identityOptions{}, fmt.Errorf("%s and %s are required when %s is set",
			oidcClientIDEnv, oidcClientSecretEnv, oidcIssuerEnv)
	}
	public, err := publicURL(options, lookup(publicURLEnv))
	if err != nil {
		return identityOptions{}, err
	}
	return identityOptions{
		provider:    oidcprovider.Config{Issuer: issuer, ClientID: clientID, ClientSecret: clientSecret},
		redirectURI: public + httpapi.BasePath + "/auth/callback",
		local:       local,
	}, nil
}

// publicURL is where a browser reaches this hub. It is the configured value when
// there is one, and otherwise derived from the listener, which is correct for the
// local case and for nothing else — hence the environment variable.
func publicURL(options serveOptions, configured string) (string, error) {
	if trimmed := strings.TrimSpace(configured); trimmed != "" {
		parsed, err := url.Parse(trimmed)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return "", fmt.Errorf("%s must be an absolute URL, for example https://hub.example.test", publicURLEnv)
		}
		return strings.TrimSuffix(trimmed, "/"), nil
	}
	host, port, err := net.SplitHostPort(options.address)
	if err != nil {
		return "", fmt.Errorf("parse --addr: %w", err)
	}
	if host == "" || net.ParseIP(host) != nil && net.ParseIP(host).IsUnspecified() {
		// A listener on every interface says nothing about how it is reached, so the
		// operator has to say.
		return "", fmt.Errorf("%s is required when --addr does not name a host", publicURLEnv)
	}
	scheme := "http"
	if options.tlsCert != "" {
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(host, port), nil
}

// signInService is what the composition root hands the transport: the identity core,
// its store, and the sweep that keeps the store bounded.
type signInService struct {
	// service is the identity core, which is both the sign-in port and one of the
	// authenticators.
	service *identity.Service
	// store is the Postgres adapter, closed with the process.
	store *identitypg.Store
}

// openIdentity wires the identity context: the provider adapter, the Postgres store
// and the core over them. It returns nil when this deployment configures no
// provider, which is the mode that still serves an open (or token-only) API.
func openIdentity(ctx context.Context, dsn string, options identityOptions) (*signInService, error) {
	if !options.configured() {
		return nil, nil
	}
	store, err := identitypg.Open(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("could not reach the identity store: %w", err)
	}
	provider, err := oidcprovider.New(ctx, options.provider)
	if err != nil {
		store.Close()
		if options.local {
			// Nobody asked for this provider by name, so the failure has to say where it
			// came from and how to start it, or it reads as the tool being broken.
			return nil, fmt.Errorf("could not reach the local identity provider at %s "+
				"(start it with 'docker compose up -d dex', point %s at another provider, "+
				"or set %s=1 on a loopback listener): %w",
				options.provider.Issuer, oidcIssuerEnv, allowUnauthenticatedEnv, err)
		}
		return nil, fmt.Errorf("could not reach the identity provider: %w", err)
	}
	service, err := identity.NewService(identity.Dependencies{
		Provider:       provider,
		Sessions:       store,
		Principals:     store,
		PendingSignIns: store,
		RedirectURI:    options.redirectURI,
	})
	if err != nil {
		store.Close()
		return nil, err
	}
	return &signInService{service: service, store: store}, nil
}

// Close releases the identity store's connections.
func (s *signInService) Close() {
	if s != nil && s.store != nil {
		s.store.Close()
	}
}

// authenticator is the port the transport asks, or nil when nobody can sign in.
func (s *signInService) authenticator() identity.Authenticator {
	if s == nil {
		return nil
	}
	return s.service
}

// signIn is the transport's sign-in port, or nil when nobody can sign in.
func (s *signInService) signIn() httpapi.SignIn {
	if s == nil {
		return nil
	}
	return s.service
}

// healthCheck reports whether the identity store can be reached, so a hub that
// cannot authenticate says so where a monitor looks rather than at an operator's
// first click.
func (s *signInService) healthCheck() []httpapi.HealthCheck {
	if s == nil {
		return nil
	}
	return []httpapi.HealthCheck{{
		Name: "identity-store",
		Check: func(ctx context.Context) error {
			_, err := s.store.Session(ctx, identity.HashToken("health-check"))
			if err != nil && !errors.Is(err, identity.ErrNoSession) {
				return err
			}
			return nil
		},
	}}
}

// sweepExpired removes expired sessions and abandoned sign-ins until the context is
// cancelled. It runs in the API process because that is the process that creates
// them; nothing depends on it for correctness, since every read enforces expiry.
func (s *signInService) sweepExpired(ctx context.Context, logger *slog.Logger) {
	if s == nil {
		return
	}
	ticker := time.NewTicker(sessionSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sessions, signIns, err := s.service.PurgeExpired(ctx)
			if err != nil {
				logger.Warn("could not sweep the expired sessions", "error", err.Error())
				continue
			}
			if sessions+signIns > 0 {
				logger.Info("swept what had expired", "sessions", sessions, "signIns", signIns)
			}
		}
	}
}

// environment reads a variable, and exists so the configuration can be exercised
// without setting process-wide state in a test.
func environment(name string) string { return os.Getenv(name) }
