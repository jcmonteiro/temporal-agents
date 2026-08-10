package instructionpg

import (
	"context"

	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/instruction"
	"temporal-agents/internal/pgtest"
)

// TestOpenRejectsAnEmptyDSN pins the fail-fast contract: a worker must not start
// with an instruction store it cannot reach, because the first thing an operator
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

// Publication runs at every startup, and two processes can start at once. Both
// facts together are what make this the test that matters: the defaults must end up
// published exactly once whoever gets there first, because a second version per
// restart would make "which version produced this run?" meaningless within a day.
func TestTwoProcessesStartingAtOncePublishOneVersionBetweenThem(t *testing.T) {
	dsn := pgtest.NewDatabase(t)
	first := openTestStore(t, dsn)
	require.NoError(t, first.Migrate(context.Background()))
	second := openTestStore(t, dsn)
	ctx := context.Background()

	var wait sync.WaitGroup
	failures := make(chan error, 2)
	for _, store := range []*Store{first, second} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			failures <- instruction.PublishDefaults(ctx, store)
		}()
	}
	wait.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err, "publishing the shipped defaults must be safe to race")
	}

	// A third publication, this time serial, must add nothing either.
	require.NoError(t, instruction.PublishDefaults(ctx, first))
	for _, key := range instruction.Keys() {
		record, err := first.Version(ctx, key, instruction.FactoryScope, 1)
		require.NoError(t, err, "the shipped default for %s was never published", key)
		require.Equal(t, specFor(t, key).Factory, record.Text)
		_, err = first.Version(ctx, key, instruction.FactoryScope, 2)
		require.ErrorIs(t, err, instruction.ErrNoSuchVersion,
			"%s was published more than once for one shipped text", key)
	}
}

// An upgrade that improves a shipped instruction must reach every place that never
// overrode it — and must not rewrite what the runs before the upgrade referenced.
// Both halves are one property: append, never update.
func TestAnUpgradedShippedInstructionIsAppendedBesideTheOneItReplaces(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	const previous = "Perform a review of the current branch"
	published, err := store.PublishFactory(ctx, instruction.KeyReviewPerform, previous)
	require.NoError(t, err)
	require.Equal(t, 1, published.Version)

	upgraded, err := store.PublishFactory(ctx, instruction.KeyReviewPerform, "Perform a thorough review, and say why")
	require.NoError(t, err)

	require.Equal(t, 2, upgraded.Version, "an upgraded default must be a new version")
	untouched, err := store.Version(ctx, instruction.KeyReviewPerform, instruction.FactoryScope, 1)
	require.NoError(t, err)
	require.Equal(t, previous, untouched.Text, "the version earlier runs referenced was rewritten")
	require.Equal(t, instruction.Hash(previous), untouched.Hash)
}

// This is the promise the whole feature makes to a past run: an operator who edits
// an instruction today cannot change what a run last week is recorded as having
// used. The run recorded a version; that version still reads as it did, while
// resolution moves on to the new one.
func TestAFinishedRunsInstructionStillResolvesAfterALaterEdit(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	const worktree = "/src/agents"
	require.NoError(t, instruction.PublishDefaults(ctx, store))
	activity := &instruction.Activity{Store: store}

	// What a run resolved, and therefore recorded, at the time it ran.
	before, err := activity.Resolve(ctx, instruction.Request{
		Keys:   []instruction.Key{instruction.KeyReviewPerform},
		Scopes: instruction.Chain(worktree, ""),
	})
	require.NoError(t, err)
	used, ok := before.Value(instruction.KeyReviewPerform)
	require.True(t, ok)

	// The operator then overrides the instruction for that place.
	saveOverride(t, store, instruction.KeyReviewPerform, instruction.DirectoryScope(worktree),
		"Review only the infrastructure changes")

	after, err := activity.Resolve(ctx, instruction.Request{
		Keys:   []instruction.Key{instruction.KeyReviewPerform},
		Scopes: instruction.Chain(worktree, ""),
	})
	require.NoError(t, err)
	next, _ := after.Value(instruction.KeyReviewPerform)
	require.Equal(t, "Review only the infrastructure changes", next.Text, "the edit did not take effect")

	recorded, err := store.Version(ctx, used.Key, used.Scope, used.Version)
	require.NoError(t, err, "the version a finished run recorded is no longer resolvable")
	require.Equal(t, used.Text, recorded.Text)
	require.Equal(t, used.Hash, recorded.Hash)
}

// A pointer that named a version nobody stored would resolve to nothing, and the
// failure would surface as an agent running with no instruction at all. The
// foreign key is what makes that unrepresentable rather than merely unlikely.
func TestAScopeCannotPointAtAVersionThatWasNeverStored(t *testing.T) {
	store := newTestStore(t)

	_, err := store.pool.Exec(context.Background(), pointSQL,
		string(instruction.KeyReviewPerform), string(instruction.GlobalScope), 7)

	require.Error(t, err, "a pointer to a version nobody stored was accepted")
}

// A store that answers nothing for a key is a normal, empty database, not a
// failure: resolution then falls back to what the build ships.
func TestAnEmptyStoreAnswersNothingRatherThanFailing(t *testing.T) {
	store := newTestStore(t)

	records, err := store.Current(context.Background(),
		[]instruction.Key{instruction.KeyReviewPerform}, instruction.Chain("/src/agents", ""))

	require.NoError(t, err)
	require.Empty(t, records)
}

// saveOverride appends a version at a scope and points that scope at it, which is
// what saving an override will do once the write surface exists. It is written here,
// against the same tables, so the read path is tested against rows that are shaped
// the way the writer will shape them.
func saveOverride(t *testing.T, store *Store, key instruction.Key, scope instruction.Scope, text string) {
	t.Helper()
	ctx := context.Background()
	tx, err := store.pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	var version int64
	require.NoError(t, tx.QueryRow(ctx,
		`INSERT INTO instruction_versions (key, scope, version, body, hash)
		 SELECT $1, $2, COALESCE(MAX(version), 0) + 1, $3, $4
		 FROM instruction_versions WHERE key = $1 AND scope = $2
		 RETURNING version`,
		string(key), string(scope), text, instruction.Hash(text)).Scan(&version))
	_, err = tx.Exec(ctx, pointSQL, string(key), string(scope), version)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
}

// specFor is the catalogue entry of one key, so a test states what it expects
// without copying the shipped text.
func specFor(t *testing.T, key instruction.Key) instruction.Spec {
	t.Helper()
	spec, ok := instruction.SpecFor(key)
	if !ok {
		t.Fatalf("the catalogue does not govern %s", key)
	}
	return spec
}
