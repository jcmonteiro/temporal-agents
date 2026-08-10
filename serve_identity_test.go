package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/httpapi"
)

// The composition root's own rules are few, and each one is a way an operator can be
// misled: a half-configured provider that silently signs nobody in, and a callback
// URL derived from a listener that says nothing about how the hub is reached.

// environmentOf turns a map into the lookup the configuration reads through, so a
// test states its environment instead of setting the process's.
func environmentOf(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

// TestSigningInIsOffUntilAnIssuerIsConfigured pins the default: a hub with no
// provider is a hub with no sign-in, and nothing about that is implicit.
func TestSigningInIsOffUntilAnIssuerIsConfigured(t *testing.T) {
	options, err := identityConfiguration(serveOptions{address: defaultServeAddress}, environmentOf(nil))

	require.NoError(t, err)
	require.False(t, options.configured())
}

// TestHalfAProviderConfigurationIsRefused pins the mistake worth failing on: an
// operator who set an issuer and mistyped a client id must be told at startup, not
// served a hub that cannot sign anybody in.
func TestHalfAProviderConfigurationIsRefused(t *testing.T) {
	for name, environment := range map[string]map[string]string{
		"an issuer with no client": {
			oidcIssuerEnv: "http://localhost:15556/dex",
		},
		"an issuer and a client with no secret": {
			oidcIssuerEnv:   "http://localhost:15556/dex",
			oidcClientIDEnv: "agent-hub",
		},
		"a client with no issuer": {
			oidcClientIDEnv:     "agent-hub",
			oidcClientSecretEnv: "agent-hub-local-secret",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := identityConfiguration(serveOptions{address: defaultServeAddress}, environmentOf(environment))
			require.Error(t, err)
		})
	}
}

// TestTheCallbackIsDerivedFromWhereTheBrowserReachesTheHub pins the one value the
// provider must agree with, and the case where only the operator can know it.
func TestTheCallbackIsDerivedFromWhereTheBrowserReachesTheHub(t *testing.T) {
	provider := map[string]string{
		oidcIssuerEnv:       "http://localhost:15556/dex",
		oidcClientIDEnv:     "agent-hub",
		oidcClientSecretEnv: "agent-hub-local-secret",
	}

	t.Run("derived from a listener that names a host", func(t *testing.T) {
		options, err := identityConfiguration(
			serveOptions{address: "127.0.0.1:8973"}, environmentOf(provider))

		require.NoError(t, err)
		require.Equal(t, "http://127.0.0.1:8973"+httpapi.BasePath+"/auth/callback", options.redirectURI)
	})

	t.Run("https when the listener serves TLS", func(t *testing.T) {
		options, err := identityConfiguration(
			serveOptions{address: "hub.example.test:8973", tlsCert: "hub.crt", tlsKey: "hub.key"},
			environmentOf(provider))

		require.NoError(t, err)
		require.Equal(t, "https://hub.example.test:8973"+httpapi.BasePath+"/auth/callback", options.redirectURI)
	})

	t.Run("stated when the hub is behind something else", func(t *testing.T) {
		behindAProxy := map[string]string{publicURLEnv: "https://hub.example.test/"}
		for key, value := range provider {
			behindAProxy[key] = value
		}

		options, err := identityConfiguration(serveOptions{address: "127.0.0.1:8973"}, environmentOf(behindAProxy))

		require.NoError(t, err)
		require.Equal(t, "https://hub.example.test"+httpapi.BasePath+"/auth/callback", options.redirectURI)
	})

	t.Run("required when the listener names no host", func(t *testing.T) {
		_, err := identityConfiguration(serveOptions{address: "0.0.0.0:8973"}, environmentOf(provider))

		require.ErrorContains(t, err, publicURLEnv)
	})

	t.Run("refused when it is not a URL", func(t *testing.T) {
		notAURL := map[string]string{publicURLEnv: "hub.example.test"}
		for key, value := range provider {
			notAURL[key] = value
		}

		_, err := identityConfiguration(serveOptions{address: "127.0.0.1:8973"}, environmentOf(notAURL))

		require.ErrorContains(t, err, publicURLEnv)
	})
}
