package oidcprovider_test

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/dextest"
	"temporal-agents/internal/identity"
	"temporal-agents/internal/identity/identitytest"
	"temporal-agents/internal/identity/oidcprovider"
)

// The fake issuer beside this file proves what the adapter refuses. This suite
// proves the other half against a provider nobody here wrote: a real authorization
// request, a real code exchange, a real refresh, and a session that stops working
// the moment it is ended. A protocol implemented against a stand-in of one's own
// assumptions is a protocol tested against those assumptions.
//
// The provider is brought up by dextest, and therefore by testcontainers-go, so
// `go test ./...` runs the suite with no setup and no compose service.

func TestMain(m *testing.M) { os.Exit(dextest.Run(m)) }

// TestARealSignInEndsWithASessionAndNoTokensInTheBrowser is the slice's demo,
// asserted: a browser follows the provider's redirects, comes back with a code, and
// ends up holding a session and nothing else.
func TestARealSignInEndsWithASessionAndNoTokensInTheBrowser(t *testing.T) {
	service, store, _ := newSignedInWorld(t)
	ctx := context.Background()

	signIn, err := service.BeginSignIn(ctx, "/places/one")
	require.NoError(t, err)
	callback := followTheProvider(t, signIn.AuthorizationURL)

	grant, err := service.CompleteSignIn(ctx, identity.Callback{
		RequestToken: signIn.RequestToken,
		State:        callback.Get("state"),
		Code:         callback.Get("code"),
		Error:        callback.Get("error"),
	})
	require.NoError(t, err)
	require.NotEmpty(t, grant.Principal.Subject, "the provider vouched for somebody")
	require.Equal(t, "/places/one", grant.ReturnTo)

	principal, err := service.Authenticate(ctx, identity.Credential{SessionToken: grant.SessionToken})
	require.NoError(t, err)
	require.Equal(t, grant.Principal, principal)

	stored, err := store.Session(ctx, identity.HashToken(grant.SessionToken))
	require.NoError(t, err)
	require.NotEmpty(t, stored.Tokens.Access, "the provider's tokens are held on the server")
	require.NotContains(t, grant.SessionToken, stored.Tokens.Access)
}

// TestARealCodeCanBeExchangedOnlyOnce pins replay protection where it is finally
// decided: the second attempt is refused here, and the provider would refuse the
// code as well.
func TestARealCodeCanBeExchangedOnlyOnce(t *testing.T) {
	service, _, _ := newSignedInWorld(t)
	ctx := context.Background()

	signIn, err := service.BeginSignIn(ctx, "/")
	require.NoError(t, err)
	callback := followTheProvider(t, signIn.AuthorizationURL)
	answer := identity.Callback{
		RequestToken: signIn.RequestToken,
		State:        callback.Get("state"),
		Code:         callback.Get("code"),
	}

	_, err = service.CompleteSignIn(ctx, answer)
	require.NoError(t, err)

	_, err = service.CompleteSignIn(ctx, answer)
	require.ErrorIs(t, err, identity.ErrSignInFailed)
}

// TestARealSessionSurvivesTheProvidersTokenExpiring pins the refresh against a real
// token endpoint: the renewed tokens replace the old ones, and the browser is not
// asked to do anything about it.
func TestARealSessionSurvivesTheProvidersTokenExpiring(t *testing.T) {
	service, store, provider := newSignedInWorld(t)
	ctx := context.Background()
	grant := signIn(t, service)
	stored, err := store.Session(ctx, identity.HashToken(grant.SessionToken))
	require.NoError(t, err)
	require.NotEmpty(t, stored.Tokens.Refresh, "the provider issued a refresh token")

	renewed, err := provider.Refresh(ctx, stored.Tokens.Refresh)
	require.NoError(t, err)
	require.NotEmpty(t, renewed.Access)
	require.NotEqual(t, stored.Tokens.Access, renewed.Access, "the provider issued a new access token")
}

// TestARevokedRefreshTokenEndsTheSession pins what happens when the provider stops
// standing behind a session: the hub does too.
func TestARevokedRefreshTokenEndsTheSession(t *testing.T) {
	_, _, provider := newSignedInWorld(t)

	_, err := provider.Refresh(context.Background(), "a-refresh-token-this-provider-never-issued")
	require.ErrorIs(t, err, identity.ErrUnauthenticated)
}

// TestEndingARealSessionStopsTheBrowserImmediately is the revocation guarantee, end
// to end: signed in against a real provider, then not, on the very next request.
func TestEndingARealSessionStopsTheBrowserImmediately(t *testing.T) {
	service, store, _ := newSignedInWorld(t)
	ctx := context.Background()
	grant := signIn(t, service)

	require.NoError(t, service.SignOut(ctx, grant.SessionToken))

	_, err := service.Authenticate(ctx, identity.Credential{SessionToken: grant.SessionToken})
	require.ErrorIs(t, err, identity.ErrUnauthenticated)
	require.Zero(t, store.Sessions())
}

// newSignedInWorld wires the core over the real provider adapter and in-memory
// stores. The stores are fakes on purpose: what this suite is about is the protocol,
// and the Postgres adapter has a container suite of its own.
func newSignedInWorld(t *testing.T) (*identity.Service, *identitytest.Store, *oidcprovider.Provider) {
	t.Helper()
	running := dextest.Start(t)
	provider, err := oidcprovider.New(context.Background(), oidcprovider.Config{
		Issuer:       running.Issuer,
		ClientID:     running.ClientID,
		ClientSecret: running.ClientSecret,
	})
	require.NoError(t, err)
	store := identitytest.NewStore()
	service, err := identity.NewService(identity.Dependencies{
		Provider:       provider,
		Sessions:       store,
		Principals:     store,
		PendingSignIns: store,
		RedirectURI:    running.RedirectURI,
	})
	require.NoError(t, err)
	return service, store, provider
}

// signIn runs a whole sign-in against the provider, for the tests that are about
// what happens afterwards.
func signIn(t *testing.T, service *identity.Service) identity.Grant {
	t.Helper()
	ctx := context.Background()
	started, err := service.BeginSignIn(ctx, "/")
	require.NoError(t, err)
	callback := followTheProvider(t, started.AuthorizationURL)
	grant, err := service.CompleteSignIn(ctx, identity.Callback{
		RequestToken: started.RequestToken,
		State:        callback.Get("state"),
		Code:         callback.Get("code"),
		Error:        callback.Get("error"),
	})
	require.NoError(t, err)
	return grant
}

// followTheProvider plays the browser: it follows the provider's redirects and stops
// where a browser's address bar would end up — at the hub's own callback — reporting
// what the provider put in the query.
//
// Nothing listens on the callback address, and nothing needs to: the redirect itself
// is the answer.
func followTheProvider(t *testing.T, authorizationURL string) url.Values {
	t.Helper()
	var callback url.Values
	client := &http.Client{
		CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			if strings.HasPrefix(request.URL.String(), dextest.RedirectURI) {
				callback = request.URL.Query()
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	response, err := client.Get(authorizationURL)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	require.NotNil(t, callback, "the provider never sent the browser back to the hub")
	require.Empty(t, callback.Get("error"), "the provider refused the sign-in")
	require.NotEmpty(t, callback.Get("code"))
	return callback
}
