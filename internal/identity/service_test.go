package identity_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/identity"
	"temporal-agents/internal/identity/identitytest"
)

// The core's rules are tested with no network and no database, because none of them
// is about either: a callback is bound to a sign-in or it is not, a session is
// current or it is not. The provider and the stores are in-memory fakes that behave
// like the real thing (a consuming take, an upsert), so a test asserts on what an
// operator observes — signed in, refused, signed out — rather than on which method
// was called.

// fakeProvider stands in for an identity provider that has already verified
// everything it is required to verify.
type fakeProvider struct {
	// issuer is what the provider vouches as.
	issuer string
	// identity is what a successful exchange yields.
	identity identity.Identity
	// exchangeErr, when set, is what the exchange reports instead.
	exchangeErr error
	// refreshed is what a refresh yields.
	refreshed identity.Tokens
	// refreshErr, when set, is what a refresh reports instead.
	refreshErr error
	// exchanges counts the exchanges the core actually asked for, so a test can pin
	// that an unbound callback never reaches the provider at all.
	exchanges int
	// lastRequest is the last exchange the core asked for.
	lastRequest identity.ExchangeRequest
}

func (p *fakeProvider) AuthorizationURL(_ context.Context, request identity.AuthorizationRequest) (string, error) {
	query := url.Values{
		"state":         {request.State},
		"nonce":         {request.Nonce},
		"redirect_uri":  {request.RedirectURI},
		"code_verifier": {request.CodeVerifier},
	}
	return p.issuer + "/authorize?" + query.Encode(), nil
}

func (p *fakeProvider) Exchange(_ context.Context, request identity.ExchangeRequest) (identity.Identity, error) {
	p.exchanges++
	p.lastRequest = request
	if p.exchangeErr != nil {
		return identity.Identity{}, p.exchangeErr
	}
	return p.identity, nil
}

func (p *fakeProvider) Refresh(context.Context, string) (identity.Tokens, error) {
	if p.refreshErr != nil {
		return identity.Tokens{}, p.refreshErr
	}
	return p.refreshed, nil
}

func (p *fakeProvider) Issuer() string { return p.issuer }

func (p *fakeProvider) SignOutURL(context.Context, string) string { return "" }

// signedIn is the principal the fake provider vouches for throughout.
var signedIn = identity.Principal{
	Issuer:  "https://issuer.test",
	Subject: "operator-1",
	Name:    "The Operator",
	Email:   "operator@example.test",
}

// testClock is a movable clock, so expiry is exercised without waiting for it.
type testClock struct{ at time.Time }

func (c *testClock) now() time.Time { return c.at }

// newService wires the core over the fakes, with a counting token generator so a
// test can name the secrets a sign-in minted.
func newService(t *testing.T, store *identitytest.Store, provider *fakeProvider, clock *testClock, mutate ...func(*identity.Dependencies)) *identity.Service {
	t.Helper()
	minted := 0
	dependencies := identity.Dependencies{
		Provider:       provider,
		Sessions:       store,
		Principals:     store,
		PendingSignIns: store,
		RedirectURI:    "https://hub.test/api/v1/auth/callback",
		Now:            clock.now,
		NewToken: func() (string, error) {
			minted++
			return "secret-" + strconv.Itoa(minted), nil
		},
	}
	for _, change := range mutate {
		change(&dependencies)
	}
	service, err := identity.NewService(dependencies)
	require.NoError(t, err)
	return service
}

// newFixture wires the usual arrangement: a provider that vouches for signedIn and
// a store that holds nothing yet.
func newFixture(t *testing.T, mutate ...func(*identity.Dependencies)) (*identity.Service, *identitytest.Store, *fakeProvider, *testClock) {
	t.Helper()
	clock := &testClock{at: time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)}
	provider := &fakeProvider{
		issuer: signedIn.Issuer,
		identity: identity.Identity{
			Principal: signedIn,
			Tokens: identity.Tokens{
				Access:    "access-1",
				Refresh:   "refresh-1",
				ExpiresAt: clock.at.Add(time.Hour),
			},
		},
		refreshed: identity.Tokens{Access: "access-2", Refresh: "refresh-2"},
	}
	store := identitytest.NewStore()
	return newService(t, store, provider, clock, mutate...), store, provider, clock
}

// stateOf reads back the state a sign-in handed the provider, which is the value a
// legitimate callback echoes.
func stateOf(t *testing.T, signIn identity.SignIn) string {
	t.Helper()
	parsed, err := url.Parse(signIn.AuthorizationURL)
	require.NoError(t, err)
	return parsed.Query().Get("state")
}

// TestSigningInGivesTheBrowserASessionAndNoTokens is the feature's primary
// behavior: a browser that completes a sign-in can read the API afterwards, and
// everything the provider issued stays on this side of the wire.
func TestSigningInGivesTheBrowserASessionAndNoTokens(t *testing.T) {
	service, store, provider, clock := newFixture(t)
	ctx := context.Background()

	signIn, err := service.BeginSignIn(ctx, "/places/one")
	require.NoError(t, err)

	grant, err := service.CompleteSignIn(ctx, identity.Callback{
		RequestToken: signIn.RequestToken,
		State:        stateOf(t, signIn),
		Code:         "code-from-provider",
	})
	require.NoError(t, err)
	require.Equal(t, signedIn, grant.Principal)
	require.Equal(t, "/places/one", grant.ReturnTo, "the browser returns to where it was going")

	principal, err := service.Authenticate(ctx, identity.Credential{SessionToken: grant.SessionToken})
	require.NoError(t, err)
	require.Equal(t, signedIn, principal)

	require.NotContains(t, grant.SessionToken, provider.identity.Tokens.Access,
		"the browser must hold nothing derived from the provider's tokens")
	stored, err := store.Session(ctx, identity.HashToken(grant.SessionToken))
	require.NoError(t, err)
	require.Equal(t, "access-1", stored.Tokens.Access, "the provider's tokens stay in the server's record")
	require.NotEqual(t, grant.SessionToken, string(stored.TokenHash),
		"the stored record must not be a usable credential")
	require.Equal(t, clock.at.Add(identity.DefaultSessionLifetime), grant.ExpiresAt)

	recorded, err := store.Principal(ctx, signedIn.Issuer, signedIn.Subject)
	require.NoError(t, err)
	require.Equal(t, signedIn, recorded, "who signed in is recorded, so work can name them later")
}

// TestACallbackThatIsNotBoundToThisBrowsersSignInIsRefused is the request-binding
// rule: a code delivered to a browser that never started this sign-in buys nothing,
// and the provider is not even asked.
func TestACallbackThatIsNotBoundToThisBrowsersSignInIsRefused(t *testing.T) {
	service, _, provider, _ := newFixture(t)
	ctx := context.Background()
	signIn, err := service.BeginSignIn(ctx, "/")
	require.NoError(t, err)

	for name, callback := range map[string]identity.Callback{
		"no request token": {State: stateOf(t, signIn), Code: "code"},
		"another browser's request token": {
			RequestToken: "someone-elses-token", State: stateOf(t, signIn), Code: "code",
		},
		"a state the sign-in never issued": {
			RequestToken: signIn.RequestToken, State: "forged-state", Code: "code",
		},
		"no state at all": {RequestToken: signIn.RequestToken, Code: "code"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := service.CompleteSignIn(ctx, callback)
			require.ErrorIs(t, err, identity.ErrSignInFailed)
		})
	}
	require.Zero(t, provider.exchanges, "an unbound callback must never reach the provider")
}

// TestACallbackCanBeCompletedOnlyOnce is the replay rule: the record a callback
// binds to is consumed by the first use, so the same code and state presented twice
// finds nothing to bind to.
func TestACallbackCanBeCompletedOnlyOnce(t *testing.T) {
	service, _, provider, _ := newFixture(t)
	ctx := context.Background()
	signIn, err := service.BeginSignIn(ctx, "/")
	require.NoError(t, err)
	callback := identity.Callback{
		RequestToken: signIn.RequestToken, State: stateOf(t, signIn), Code: "code",
	}

	_, err = service.CompleteSignIn(ctx, callback)
	require.NoError(t, err)

	_, err = service.CompleteSignIn(ctx, callback)
	require.ErrorIs(t, err, identity.ErrSignInFailed)
	require.Equal(t, 1, provider.exchanges, "the replay must not be exchanged a second time")
}

// TestAnAbandonedSignInCannotBeFinishedLater pins the pending record's expiry: a
// callback that arrives after the window is as good as no callback.
func TestAnAbandonedSignInCannotBeFinishedLater(t *testing.T) {
	service, _, provider, clock := newFixture(t)
	ctx := context.Background()
	signIn, err := service.BeginSignIn(ctx, "/")
	require.NoError(t, err)

	clock.at = clock.at.Add(identity.DefaultSignInLifetime + time.Second)

	_, err = service.CompleteSignIn(ctx, identity.Callback{
		RequestToken: signIn.RequestToken, State: stateOf(t, signIn), Code: "code",
	})
	require.ErrorIs(t, err, identity.ErrSignInFailed)
	require.Zero(t, provider.exchanges)
}

// TestAProviderThatRefusesTheExchangeSignsNobodyIn covers what the adapter refuses
// on the core's behalf — a wrong audience, an expired or unsigned assertion, a
// nonce from another request — plus a provider that returns an error instead of a
// code. None of them may produce a session.
func TestAProviderThatRefusesTheExchangeSignsNobodyIn(t *testing.T) {
	ctx := context.Background()

	t.Run("the provider rejects the assertion", func(t *testing.T) {
		service, store, provider, _ := newFixture(t)
		provider.exchangeErr = errors.New("the id token's audience is not this client")
		signIn, err := service.BeginSignIn(ctx, "/")
		require.NoError(t, err)

		_, err = service.CompleteSignIn(ctx, identity.Callback{
			RequestToken: signIn.RequestToken, State: stateOf(t, signIn), Code: "code",
		})
		require.ErrorIs(t, err, identity.ErrSignInFailed)
		require.Zero(t, store.Sessions())
	})

	t.Run("the provider sends an error instead of a code", func(t *testing.T) {
		service, store, provider, _ := newFixture(t)
		signIn, err := service.BeginSignIn(ctx, "/")
		require.NoError(t, err)

		_, err = service.CompleteSignIn(ctx, identity.Callback{
			RequestToken: signIn.RequestToken, State: stateOf(t, signIn), Error: "access_denied",
		})
		require.ErrorIs(t, err, identity.ErrSignInFailed)
		require.Zero(t, provider.exchanges)
		require.Zero(t, store.Sessions())
	})

	t.Run("the provider discloses no stable subject", func(t *testing.T) {
		service, store, provider, _ := newFixture(t)
		provider.identity = identity.Identity{Principal: identity.Principal{Name: "nobody"}}
		signIn, err := service.BeginSignIn(ctx, "/")
		require.NoError(t, err)

		_, err = service.CompleteSignIn(ctx, identity.Callback{
			RequestToken: signIn.RequestToken, State: stateOf(t, signIn), Code: "code",
		})
		require.ErrorIs(t, err, identity.ErrSignInFailed)
		require.Zero(t, store.Sessions())
	})
}

// TestSigningOutStopsTheBrowserImmediately is the revocation guarantee that server-
// side session records exist for: the next request is refused, not the next hour's.
func TestSigningOutStopsTheBrowserImmediately(t *testing.T) {
	service, _, _, _ := newFixture(t)
	ctx := context.Background()
	grant := completeSignIn(t, service)

	require.NoError(t, service.SignOut(ctx, grant.SessionToken))

	_, err := service.Authenticate(ctx, identity.Credential{SessionToken: grant.SessionToken})
	require.ErrorIs(t, err, identity.ErrUnauthenticated)

	require.NoError(t, service.SignOut(ctx, grant.SessionToken),
		"signing out twice leaves the browser exactly where it asked to be")
}

// TestAnExpiredSessionStopsWorkingAndIsForgotten pins that a session has a real end
// of life, and that presenting it again does not keep a dead record alive.
func TestAnExpiredSessionStopsWorkingAndIsForgotten(t *testing.T) {
	service, store, _, clock := newFixture(t)
	ctx := context.Background()
	grant := completeSignIn(t, service)

	clock.at = grant.ExpiresAt

	_, err := service.Authenticate(ctx, identity.Credential{SessionToken: grant.SessionToken})
	require.ErrorIs(t, err, identity.ErrUnauthenticated)
	require.Zero(t, store.Sessions(), "an expired session is ended rather than merely refused")
}

// TestALongSessionSurvivesTheProvidersTokenExpiring is what a long-lived stream
// depends on: the access token ageing out is the server's problem, not the
// browser's.
func TestALongSessionSurvivesTheProvidersTokenExpiring(t *testing.T) {
	service, store, _, clock := newFixture(t)
	ctx := context.Background()
	grant := completeSignIn(t, service)

	clock.at = clock.at.Add(2 * time.Hour) // past the access token, inside the session

	principal, err := service.Authenticate(ctx, identity.Credential{SessionToken: grant.SessionToken})
	require.NoError(t, err)
	require.Equal(t, signedIn, principal)

	stored, err := store.Session(ctx, identity.HashToken(grant.SessionToken))
	require.NoError(t, err)
	require.Equal(t, "access-2", stored.Tokens.Access, "the renewed tokens replace the expired ones")
}

// TestASessionTheProviderWillNotRenewEnds pins the other half: when the provider
// refuses the renewal, the hub stops honouring the session instead of carrying on
// with tokens nobody stands behind.
func TestASessionTheProviderWillNotRenewEnds(t *testing.T) {
	service, store, provider, clock := newFixture(t)
	ctx := context.Background()
	provider.refreshErr = errors.New("the refresh token was revoked")
	grant := completeSignIn(t, service)

	clock.at = clock.at.Add(2 * time.Hour)

	_, err := service.Authenticate(ctx, identity.Credential{SessionToken: grant.SessionToken})
	require.ErrorIs(t, err, identity.ErrUnauthenticated)
	require.Zero(t, store.Sessions())
}

// TestAnUnreachableProviderDoesNotSignTheOperatorOut separates a transient failure
// from a revoked session: the operator's session is still theirs, and the caller is
// told to try again rather than shown a sign-in page.
func TestAnUnreachableProviderDoesNotSignTheOperatorOut(t *testing.T) {
	service, store, provider, clock := newFixture(t)
	ctx := context.Background()
	provider.refreshErr = fmt.Errorf("%w: dial tcp", identity.ErrProviderUnavailable)
	grant := completeSignIn(t, service)

	clock.at = clock.at.Add(2 * time.Hour)

	_, err := service.Authenticate(ctx, identity.Credential{SessionToken: grant.SessionToken})
	require.ErrorIs(t, err, identity.ErrProviderUnavailable)
	require.NotErrorIs(t, err, identity.ErrUnauthenticated)
	require.Equal(t, 1, store.Sessions())
}

// TestAnUnknownOrMissingSessionIsRefused pins that a forged or stale cookie is
// simply not authenticated.
func TestAnUnknownOrMissingSessionIsRefused(t *testing.T) {
	service, _, _, _ := newFixture(t)
	ctx := context.Background()

	for name, credential := range map[string]identity.Credential{
		"nothing at all":  {},
		"a forged cookie": {SessionToken: "not-a-session"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := service.Authenticate(ctx, credential)
			require.ErrorIs(t, err, identity.ErrUnauthenticated)
		})
	}
}

// TestAStoreThatCannotBeReachedIsNotARefusedCredential pins the distinction that
// keeps an outage from looking like a mass sign-out.
func TestAStoreThatCannotBeReachedIsNotARefusedCredential(t *testing.T) {
	service, store, _, _ := newFixture(t)
	ctx := context.Background()
	grant := completeSignIn(t, service)
	store.FailWith = errors.New("the session store is unreachable")

	_, err := service.Authenticate(ctx, identity.Credential{SessionToken: grant.SessionToken})
	require.Error(t, err)
	require.NotErrorIs(t, err, identity.ErrUnauthenticated)
}

// TestTheBrowserIsOnlyEverSentSomewhereInsideTheApplication pins the open-redirect
// rule at the surface that would be most attractive to abuse.
func TestTheBrowserIsOnlyEverSentSomewhereInsideTheApplication(t *testing.T) {
	for _, requested := range []string{
		"https://evil.test/steal",
		"//evil.test/steal",
		"/\\evil.test",
		"javascript:alert(1)",
		"/places\r\nSet-Cookie: x=1",
		"",
	} {
		t.Run(requested, func(t *testing.T) {
			service, _, _, _ := newFixture(t)
			ctx := context.Background()
			signIn, err := service.BeginSignIn(ctx, requested)
			require.NoError(t, err)
			grant, err := service.CompleteSignIn(ctx, identity.Callback{
				RequestToken: signIn.RequestToken, State: stateOf(t, signIn), Code: "code",
			})
			require.NoError(t, err)
			require.Equal(t, identity.DefaultReturnPath, grant.ReturnTo)
		})
	}
}

// TestPurgingRemovesOnlyWhatHasAgedOut pins the housekeeping: neither store grows
// without bound, and nothing current is swept up with the expired.
func TestPurgingRemovesOnlyWhatHasAgedOut(t *testing.T) {
	service, store, _, clock := newFixture(t)
	ctx := context.Background()
	expiring := completeSignIn(t, service)
	_, err := service.BeginSignIn(ctx, "/") // abandoned
	require.NoError(t, err)

	clock.at = clock.at.Add(identity.DefaultSignInLifetime + time.Minute)
	current := completeSignIn(t, service)

	clock.at = expiring.ExpiresAt
	sessions, signIns, err := service.PurgeExpired(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, sessions)
	require.Equal(t, 1, signIns)

	_, err = store.Session(ctx, identity.HashToken(current.SessionToken))
	require.NoError(t, err, "a session that has not expired is left alone")
}

// TestBuildingTheCoreWithoutItsPortsIsRefused pins that a half-wired hub does not
// start: it would answer requests it cannot authenticate.
func TestBuildingTheCoreWithoutItsPortsIsRefused(t *testing.T) {
	store := identitytest.NewStore()
	complete := identity.Dependencies{
		Provider:       &fakeProvider{issuer: "https://issuer.test"},
		Sessions:       store,
		Principals:     store,
		PendingSignIns: store,
		RedirectURI:    "https://hub.test/api/v1/auth/callback",
	}
	for name, missing := range map[string]func(*identity.Dependencies){
		"no provider":     func(d *identity.Dependencies) { d.Provider = nil },
		"no sessions":     func(d *identity.Dependencies) { d.Sessions = nil },
		"no principals":   func(d *identity.Dependencies) { d.Principals = nil },
		"no pending":      func(d *identity.Dependencies) { d.PendingSignIns = nil },
		"no redirect URI": func(d *identity.Dependencies) { d.RedirectURI = "" },
	} {
		t.Run(name, func(t *testing.T) {
			dependencies := complete
			missing(&dependencies)
			_, err := identity.NewService(dependencies)
			require.Error(t, err)
		})
	}
}

// completeSignIn runs a whole sign-in and returns the grant, for the tests that are
// about what happens afterwards.
func completeSignIn(t *testing.T, service *identity.Service) identity.Grant {
	t.Helper()
	ctx := context.Background()
	signIn, err := service.BeginSignIn(ctx, "/")
	require.NoError(t, err)
	grant, err := service.CompleteSignIn(ctx, identity.Callback{
		RequestToken: signIn.RequestToken, State: stateOf(t, signIn), Code: "code",
	})
	require.NoError(t, err)
	return grant
}
