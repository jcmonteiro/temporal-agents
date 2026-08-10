package identity_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/identity"
)

// The transport asks one question of one port. These tests are about that promise:
// both credential kinds answer it, a caller cannot tell which adapter answered, and
// a failure that is not a refusal is never reported as one.

// TestBothCredentialKindsAnswerTheSameQuestion pins that automation and a browser
// reach the hub through the one port, each with a principal of its own.
func TestBothCredentialKindsAnswerTheSameQuestion(t *testing.T) {
	ctx := context.Background()
	service, _, _, _ := newFixture(t)
	grant := completeSignIn(t, service)
	token, err := identity.NewStaticToken("a-configured-token")
	require.NoError(t, err)
	chain := identity.Chain{token, service}

	fromBrowser, err := chain.Authenticate(ctx, identity.Credential{SessionToken: grant.SessionToken})
	require.NoError(t, err)
	require.Equal(t, signedIn, fromBrowser)

	fromScript, err := chain.Authenticate(ctx, identity.Credential{Authorization: "Bearer a-configured-token"})
	require.NoError(t, err)
	require.Equal(t, identity.LocalIssuer, fromScript.Issuer)
	require.Equal(t, identity.StaticTokenSubject, fromScript.Subject,
		"automation is attributable too")
}

// TestACredentialNoAdapterAcceptsIsRefused pins the closed door: anything that is
// not one of the accepted credentials is simply not authenticated.
func TestACredentialNoAdapterAcceptsIsRefused(t *testing.T) {
	ctx := context.Background()
	service, _, _, _ := newFixture(t)
	token, err := identity.NewStaticToken("a-configured-token")
	require.NoError(t, err)
	chain := identity.Chain{token, service}

	for name, credential := range map[string]identity.Credential{
		"nothing":                {},
		"the wrong token":        {Authorization: "Bearer nearly-the-configured-token"},
		"the token unprefixed":   {Authorization: "a-configured-token"},
		"another scheme":         {Authorization: "Basic a-configured-token"},
		"an unknown session":     {SessionToken: "not-a-session"},
		"both, both of them bad": {Authorization: "Bearer wrong", SessionToken: "wrong"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := chain.Authenticate(ctx, credential)
			require.ErrorIs(t, err, identity.ErrUnauthenticated)
		})
	}
}

// TestAnUnreachableAdapterStopsTheChain pins that an outage is reported as an
// outage. Reporting it as "unauthenticated" would sign every operator out of a hub
// whose store is merely restarting.
func TestAnUnreachableAdapterStopsTheChain(t *testing.T) {
	ctx := context.Background()
	unreachable := errors.New("the session store is unreachable")
	chain := identity.Chain{authenticatorFunc(func(context.Context, identity.Credential) (identity.Principal, error) {
		return identity.Principal{}, unreachable
	})}

	_, err := chain.Authenticate(ctx, identity.Credential{SessionToken: "whatever"})
	require.ErrorIs(t, err, unreachable)
	require.NotErrorIs(t, err, identity.ErrUnauthenticated)
}

// TestAnEmptyStaticTokenIsRefusedAtBuildTime pins the configuration mistake that
// would otherwise authenticate every request: a credential that accepts the empty
// string is no credential at all.
func TestAnEmptyStaticTokenIsRefusedAtBuildTime(t *testing.T) {
	_, err := identity.NewStaticToken("   ")
	require.Error(t, err)
}

// authenticatorFunc adapts a function to the port, for the one test that needs an
// adapter which only ever fails.
type authenticatorFunc func(context.Context, identity.Credential) (identity.Principal, error)

func (f authenticatorFunc) Authenticate(ctx context.Context, credential identity.Credential) (identity.Principal, error) {
	return f(ctx, credential)
}
