package identitypg

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/identity"
	"temporal-agents/internal/pgtest"
)

// fixedNow anchors every timestamp, so an assertion about expiry is about the rule
// and not about how long the container took to start.
var fixedNow = time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)

// operator is the principal the suite signs in throughout.
var operator = identity.Principal{
	Issuer:  "https://issuer.test",
	Subject: "operator-1",
	Name:    "The Operator",
	Email:   "operator@example.test",
}

// TestOpenRejectsAnEmptyDSN pins the fail-fast contract: a hub must not start with
// an identity store it cannot reach, because the first thing an operator would learn
// about it is a sign-in that silently did nothing.
func TestOpenRejectsAnEmptyDSN(t *testing.T) {
	_, err := Open(context.Background(), "   ")
	require.Error(t, err)
}

// TestMigrateIsIdempotent pins that a restart is free.
func TestMigrateIsIdempotent(t *testing.T) {
	store := newTestStore(t)
	require.NoError(t, store.Migrate(context.Background()))
	require.NoError(t, store.Migrate(context.Background()))
}

// TestASessionSurvivesWithItsPrincipalAndTheProvidersTokens is the round trip the
// whole feature rests on: what was written at sign-in is what a later request reads,
// including the tokens that must never leave the server.
func TestASessionSurvivesWithItsPrincipalAndTheProvidersTokens(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	session := identity.Session{
		TokenHash: identity.HashToken("a-browser-token"),
		Principal: operator,
		IssuedAt:  fixedNow,
		ExpiresAt: fixedNow.Add(12 * time.Hour),
		Tokens: identity.Tokens{
			Access: "access-1", Refresh: "refresh-1", ExpiresAt: fixedNow.Add(time.Hour),
		},
	}
	require.NoError(t, store.CreateSession(ctx, session))

	got, err := store.Session(ctx, identity.HashToken("a-browser-token"))
	require.NoError(t, err)
	require.Equal(t, operator, got.Principal)
	require.Equal(t, "access-1", got.Tokens.Access)
	require.Equal(t, "refresh-1", got.Tokens.Refresh)
	require.True(t, got.ExpiresAt.Equal(session.ExpiresAt))
	require.True(t, got.Tokens.ExpiresAt.Equal(session.Tokens.ExpiresAt))

	recorded, err := store.Principal(ctx, operator.Issuer, operator.Subject)
	require.NoError(t, err)
	require.Equal(t, operator, recorded, "signing in records who signed in")
}

// TestASessionWithNoStatedTokenExpiryIsStoredAsHavingNone pins that a provider that
// does not say when its token expires does not get an expiry invented for it.
func TestASessionWithNoStatedTokenExpiryIsStoredAsHavingNone(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	require.NoError(t, store.CreateSession(ctx, identity.Session{
		TokenHash: identity.HashToken("token"),
		Principal: operator,
		IssuedAt:  fixedNow,
		ExpiresAt: fixedNow.Add(time.Hour),
		Tokens:    identity.Tokens{Access: "access-1"},
	}))

	got, err := store.Session(ctx, identity.HashToken("token"))
	require.NoError(t, err)
	require.True(t, got.Tokens.ExpiresAt.IsZero())
	require.False(t, got.Tokens.Expired(fixedNow.Add(100*time.Hour)),
		"a token with no stated expiry is bounded only by its session")
}

// TestEndingASessionTakesEffectOnTheNextRead is the revocation guarantee, at the
// store: nothing is left behind for a later query to have to filter out.
func TestEndingASessionTakesEffectOnTheNextRead(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	hash := identity.HashToken("a-browser-token")
	require.NoError(t, store.CreateSession(ctx, identity.Session{
		TokenHash: hash, Principal: operator, IssuedAt: fixedNow, ExpiresAt: fixedNow.Add(time.Hour),
	}))

	require.NoError(t, store.EndSession(ctx, hash))

	_, err := store.Session(ctx, hash)
	require.ErrorIs(t, err, identity.ErrNoSession)
	require.ErrorIs(t, store.EndSession(ctx, hash), identity.ErrNoSession,
		"ending a session that is not there is reported, so a caller can tell the two apart")
}

// TestASecondServerSeesTheSameSession pins that the session record is shared state
// and not process memory: the browser signed in against one process is signed in
// against the next.
func TestASecondServerSeesTheSameSession(t *testing.T) {
	dsn := pgtest.NewDatabase(t)
	first := openTestStore(t, dsn)
	ctx := context.Background()
	require.NoError(t, first.Migrate(ctx))
	require.NoError(t, first.CreateSession(ctx, identity.Session{
		TokenHash: identity.HashToken("a-browser-token"), Principal: operator,
		IssuedAt: fixedNow, ExpiresAt: fixedNow.Add(time.Hour),
	}))

	second := openTestStore(t, dsn)
	got, err := second.Session(ctx, identity.HashToken("a-browser-token"))
	require.NoError(t, err)
	require.Equal(t, operator, got.Principal)

	require.NoError(t, second.EndSession(ctx, identity.HashToken("a-browser-token")))
	_, err = first.Session(ctx, identity.HashToken("a-browser-token"))
	require.ErrorIs(t, err, identity.ErrNoSession, "ending a session ends it everywhere")
}

// TestRenewingTheProvidersTokensLeavesTheSessionsOwnLifetimeAlone pins the
// distinction the two expiries carry: a refresh renews what the provider stands
// behind, it does not extend how long the browser stays signed in.
func TestRenewingTheProvidersTokensLeavesTheSessionsOwnLifetimeAlone(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	hash := identity.HashToken("a-browser-token")
	expiresAt := fixedNow.Add(12 * time.Hour)
	require.NoError(t, store.CreateSession(ctx, identity.Session{
		TokenHash: hash, Principal: operator, IssuedAt: fixedNow, ExpiresAt: expiresAt,
		Tokens: identity.Tokens{Access: "access-1", Refresh: "refresh-1", ExpiresAt: fixedNow.Add(time.Hour)},
	}))

	require.NoError(t, store.UpdateSessionTokens(ctx, hash, identity.Tokens{
		Access: "access-2", Refresh: "refresh-2", ExpiresAt: fixedNow.Add(2 * time.Hour),
	}))

	got, err := store.Session(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, "access-2", got.Tokens.Access)
	require.Equal(t, "refresh-2", got.Tokens.Refresh)
	require.True(t, got.ExpiresAt.Equal(expiresAt))

	require.ErrorIs(t, store.UpdateSessionTokens(ctx, identity.HashToken("gone"), identity.Tokens{}),
		identity.ErrNoSession)
}

// TestASessionCannotNameAnIdentityNobodyRecorded pins that attribution is always
// resolvable: a session exists only alongside the principal it belongs to.
func TestASessionCannotNameAnIdentityNobodyRecorded(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	require.Error(t, store.CreateSession(ctx, identity.Session{
		TokenHash: identity.HashToken("token"),
		Principal: identity.Principal{Name: "nobody"},
		IssuedAt:  fixedNow, ExpiresAt: fixedNow.Add(time.Hour),
	}))
}

// TestSigningInAgainRefreshesWhatTheProviderDiscloses pins that a renamed operator
// is shown under their new name without their identity, or their history, changing.
func TestSigningInAgainRefreshesWhatTheProviderDiscloses(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	require.NoError(t, store.UpsertPrincipal(ctx, operator))

	renamed := operator
	renamed.Name = "The Operator, Renamed"
	renamed.Email = "renamed@example.test"
	require.NoError(t, store.UpsertPrincipal(ctx, renamed))

	got, err := store.Principal(ctx, operator.Issuer, operator.Subject)
	require.NoError(t, err)
	require.Equal(t, renamed, got)

	_, err = store.Principal(ctx, operator.Issuer, "somebody-else")
	require.ErrorIs(t, err, identity.ErrNoPrincipal)
}

// TestAPendingSignInCanBeTakenOnlyOnce is the replay protection, proved where it
// actually has to hold: two callbacks racing on one browser token, of which exactly
// one may be completed.
func TestAPendingSignInCanBeTakenOnlyOnce(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	hash := identity.HashToken("a-sign-in-token")
	require.NoError(t, store.StartSignIn(ctx, identity.PendingSignIn{
		TokenHash: hash, State: "state-1", Nonce: "nonce-1", CodeVerifier: "verifier-1",
		ReturnTo: "/places/one", StartedAt: fixedNow, ExpiresAt: fixedNow.Add(10 * time.Minute),
	}))

	const racers = 8
	var (
		wait      sync.WaitGroup
		mutex     sync.Mutex
		taken     []identity.PendingSignIn
		refusals  int
		otherErrs []error
	)
	wait.Add(racers)
	for range racers {
		go func() {
			defer wait.Done()
			pending, err := store.TakePendingSignIn(ctx, hash)
			mutex.Lock()
			defer mutex.Unlock()
			switch {
			case err == nil:
				taken = append(taken, pending)
			case errors.Is(err, identity.ErrNoPendingSignIn):
				refusals++
			default:
				otherErrs = append(otherErrs, err)
			}
		}()
	}
	wait.Wait()

	require.Empty(t, otherErrs)
	require.Len(t, taken, 1, "exactly one callback may complete a sign-in")
	require.Equal(t, racers-1, refusals)
	require.Equal(t, "state-1", taken[0].State)
	require.Equal(t, "nonce-1", taken[0].Nonce)
	require.Equal(t, "verifier-1", taken[0].CodeVerifier)
	require.Equal(t, "/places/one", taken[0].ReturnTo)
}

// TestSweepingRemovesOnlyWhatHasAgedOut pins the housekeeping: neither table grows
// without bound, and nothing current is swept up with the expired.
func TestSweepingRemovesOnlyWhatHasAgedOut(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	require.NoError(t, store.CreateSession(ctx, identity.Session{
		TokenHash: identity.HashToken("expired"), Principal: operator,
		IssuedAt: fixedNow.Add(-13 * time.Hour), ExpiresAt: fixedNow.Add(-time.Hour),
	}))
	require.NoError(t, store.CreateSession(ctx, identity.Session{
		TokenHash: identity.HashToken("current"), Principal: operator,
		IssuedAt: fixedNow, ExpiresAt: fixedNow.Add(time.Hour),
	}))
	require.NoError(t, store.StartSignIn(ctx, identity.PendingSignIn{
		TokenHash: identity.HashToken("abandoned"), State: "s", Nonce: "n", CodeVerifier: "v",
		StartedAt: fixedNow.Add(-time.Hour), ExpiresAt: fixedNow.Add(-50 * time.Minute),
	}))
	require.NoError(t, store.StartSignIn(ctx, identity.PendingSignIn{
		TokenHash: identity.HashToken("in-flight"), State: "s", Nonce: "n", CodeVerifier: "v",
		StartedAt: fixedNow, ExpiresAt: fixedNow.Add(10 * time.Minute),
	}))

	sessions, err := store.DeleteExpiredSessions(ctx, fixedNow)
	require.NoError(t, err)
	require.Equal(t, 1, sessions)
	signIns, err := store.DeleteExpiredSignIns(ctx, fixedNow)
	require.NoError(t, err)
	require.Equal(t, 1, signIns)

	_, err = store.Session(ctx, identity.HashToken("current"))
	require.NoError(t, err)
	_, err = store.TakePendingSignIn(ctx, identity.HashToken("in-flight"))
	require.NoError(t, err)
}
