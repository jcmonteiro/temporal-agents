package scopedpg

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/pgtest"
	"temporal-agents/internal/scoped"
)

// reviewInstruction is a key the suite stores values under. The adapter is the
// mechanism, not a catalogue, so the suite names a key the way any catalogue would
// and never asks what it means.
const reviewInstruction scoped.Key = "review.perform"

// TestOpenRejectsAnEmptyDSN pins the fail-fast contract: a worker must not start
// with a configuration store it cannot reach, because the first thing an operator
// would learn about it is an agent run that failed for no visible reason.
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

// Publication runs at every startup, and two processes can start at once. Both facts
// together are what make this the test that matters: a shipped value must end up
// published exactly once whoever gets there first, because a second version per
// restart would make "which version produced this run?" meaningless within a day.
func TestTwoProcessesStartingAtOncePublishOneVersionBetweenThem(t *testing.T) {
	dsn := pgtest.NewDatabase(t)
	first := openTestStore(t, dsn)
	require.NoError(t, first.Migrate(context.Background()))
	second := openTestStore(t, dsn)
	ctx := context.Background()
	const shipped = "Perform a thorough code review of the current branch"

	var wait sync.WaitGroup
	failures := make(chan error, 2)
	for _, store := range []*Store{first, second} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			failures <- scoped.PublishDefault(ctx, store, reviewInstruction, shipped)
		}()
	}
	wait.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err, "publishing a shipped value must be safe to race")
	}

	// A third publication, this time serial, must add nothing either.
	require.NoError(t, scoped.PublishDefault(ctx, first, reviewInstruction, shipped))
	published, err := first.Version(ctx, reviewInstruction, scoped.FactoryScope, 1)
	require.NoError(t, err, "the shipped value was never published")
	require.Equal(t, shipped, published.Text)
	_, err = first.Version(ctx, reviewInstruction, scoped.FactoryScope, 2)
	require.ErrorIs(t, err, scoped.ErrNoSuchVersion, "one shipped text was published more than once")
}

// An upgrade that improves a shipped value must reach every place that never
// overrode it — and must not rewrite what the runs before the upgrade referenced.
// Both halves are one property: append, never update.
func TestAnUpgradedShippedValueIsAppendedBesideTheOneItReplaces(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	const previous = "Perform a review of the current branch"
	published, err := store.PublishFactory(ctx, reviewInstruction, previous)
	require.NoError(t, err)
	require.Equal(t, 1, published.Version)

	upgraded, err := store.PublishFactory(ctx, reviewInstruction, "Perform a thorough review, and say why")
	require.NoError(t, err)

	require.Equal(t, 2, upgraded.Version, "an upgraded default must be a new version")
	untouched, err := store.Version(ctx, reviewInstruction, scoped.FactoryScope, 1)
	require.NoError(t, err)
	require.Equal(t, previous, untouched.Text, "the version earlier runs referenced was rewritten")
	require.Equal(t, scoped.Hash(previous), untouched.Hash)
}

// This is the promise the whole feature makes to a past run: an operator who edits a
// value today cannot change what a run last week is recorded as having used. The run
// recorded a version; that version still reads as it did, while resolution moves on
// to the new one.
func TestAFinishedRunsValueStillResolvesAfterALaterEdit(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	const worktree = "/src/agents"
	chain := scoped.Chain(worktree, "")
	require.NoError(t, scoped.PublishDefault(ctx, store, reviewInstruction, "the shipped instruction"))

	// What a run resolved, and therefore recorded, at the time it ran.
	before, err := store.Current(ctx, []scoped.Key{reviewInstruction}, chain)
	require.NoError(t, err)
	used, ok := scoped.Winner(before, reviewInstruction, chain)
	require.True(t, ok)
	require.Equal(t, scoped.FactoryScope, used.Scope)

	// The operator then overrides the value for that place.
	saved, err := store.Set(ctx, reviewInstruction, scoped.DirectoryScope(worktree),
		"Review only the infrastructure changes", "operator-1")
	require.NoError(t, err)
	require.Equal(t, "operator-1", saved.SavedBy)

	after, err := store.Current(ctx, []scoped.Key{reviewInstruction}, chain)
	require.NoError(t, err)
	next, _ := scoped.Winner(after, reviewInstruction, chain)
	require.Equal(t, "Review only the infrastructure changes", next.Text, "the edit did not take effect")
	require.Equal(t, scoped.DirectoryScope(worktree), next.Scope, "the place's own value must win")

	recorded, err := store.Version(ctx, used.Key, used.Scope, used.Version)
	require.NoError(t, err, "the version a finished run recorded is no longer resolvable")
	require.Equal(t, used.Text, recorded.Text)
	require.Equal(t, used.Hash, recorded.Hash)
}

// Reset removes only the current pointer. Every version stays recoverable for past
// executions, and resolution immediately returns to the broader scope.
func TestResetKeepsHistoryAndReturnsResolutionToTheInheritedValue(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	scope := scoped.DirectoryScope("/src/agents")
	require.NoError(t, scoped.PublishDefault(ctx, store, reviewInstruction, "shipped"))
	saved, err := store.Set(ctx, reviewInstruction, scope, "local", "operator-1")
	require.NoError(t, err)

	require.NoError(t, store.Reset(ctx, reviewInstruction, scope))

	current, err := store.Current(ctx, []scoped.Key{reviewInstruction}, scoped.Chain("/src/agents", ""))
	require.NoError(t, err)
	winner, ok := scoped.Winner(current, reviewInstruction, scoped.Chain("/src/agents", ""))
	require.True(t, ok)
	require.Equal(t, scoped.FactoryScope, winner.Scope)
	historical, err := store.Version(ctx, reviewInstruction, scope, saved.Version)
	require.NoError(t, err)
	require.Equal(t, "local", historical.Text)
	require.Equal(t, "operator-1", historical.SavedBy)
}

// Writers serialize per key. Each save therefore gets a distinct dense version and
// the pointer always selects the greatest committed version, independent of which
// caller acquired the lock first.
func TestConcurrentSavesProduceDistinctVersionsAndPointAtTheLatest(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	scope := scoped.DirectoryScope("/src/agents")

	var wait sync.WaitGroup
	results := make(chan scoped.Record, 2)
	failures := make(chan error, 2)
	for _, save := range []struct {
		text string
		by   string
	}{{"first candidate", "operator-1"}, {"second candidate", "operator-2"}} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			record, err := store.Set(ctx, reviewInstruction, scope, save.text, save.by)
			results <- record
			failures <- err
		}()
	}
	wait.Wait()
	close(results)
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}

	versions := map[int]scoped.Record{}
	for record := range results {
		versions[record.Version] = record
	}
	require.Len(t, versions, 2)
	require.Contains(t, versions, 1)
	require.Contains(t, versions, 2)
	current, err := store.Current(ctx, []scoped.Key{reviewInstruction}, []scoped.Scope{scope})
	require.NoError(t, err)
	require.Len(t, current, 1)
	require.Equal(t, versions[2], current[0])
}

// A pointer that named a version nobody stored would resolve to nothing, and the
// failure would surface as an agent running with no instruction at all. The foreign
// key is what makes that unrepresentable rather than merely unlikely.
func TestAScopeCannotPointAtAVersionThatWasNeverStored(t *testing.T) {
	store := newTestStore(t)

	_, err := store.pool.Exec(context.Background(), pointSQL,
		string(reviewInstruction), string(scoped.GlobalScope), 7)

	require.Error(t, err, "a pointer to a version nobody stored was accepted")
}

// A store that answers nothing for a key is a normal, empty database, not a failure:
// resolution then falls back to what the build ships.
func TestAnEmptyStoreAnswersNothingRatherThanFailing(t *testing.T) {
	store := newTestStore(t)

	records, err := store.Current(context.Background(),
		[]scoped.Key{reviewInstruction}, scoped.Chain("/src/agents", ""))

	require.NoError(t, err)
	require.Empty(t, records)
}

// saveOverride appends a version at a scope and points that scope at it, which is
// what saving an override will do once the write surface exists. It is written here,
// against the same tables, so the read path is tested against rows that are shaped
// the way the writer will shape them.
func saveOverride(t *testing.T, store *Store, key scoped.Key, scope scoped.Scope, text string) {
	t.Helper()
	ctx := context.Background()
	tx, err := store.pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	var version int64
	require.NoError(t, tx.QueryRow(ctx,
		`INSERT INTO scoped_values (key, scope, version, body, hash)
		 SELECT $1, $2, COALESCE(MAX(version), 0) + 1, $3, $4
		 FROM scoped_values WHERE key = $1 AND scope = $2
		 RETURNING version`,
		string(key), string(scope), text, scoped.Hash(text)).Scan(&version))
	_, err = tx.Exec(ctx, pointSQL, string(key), string(scope), version)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
}
