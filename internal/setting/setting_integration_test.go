package setting_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/pgtest"
	"temporal-agents/internal/scoped"
	"temporal-agents/internal/scoped/scopedpg"
	"temporal-agents/internal/setting"
)

// The in-memory fake proves the rule; this proves the rule against the store the
// rule actually runs on, because inheritance is decided from rows a database
// returned and the shape of those rows is the adapter's business. The container is
// started by pgtest and therefore by testcontainers-go, so the suite needs no setup
// and no compose service.
func TestMain(m *testing.M) { os.Exit(pgtest.Run(m)) }

// The demo of scoped settings: one value, set at three levels, read for a worktree.
// Unset everywhere it is the shipped default; set on the installation it applies
// everywhere; set on the repository it covers the worktree; set on the worktree it
// applies only there. Every step also reports where the answer came from, because
// that is what an interface shows instead of re-deriving inheritance.
func TestASettingResolvesThroughTheRealStoreAtEveryLevelOfTheChain(t *testing.T) {
	const repository, worktree = "/src/agents", "/src/agents-feature"
	ctx := context.Background()
	store := openStore(t)
	require.NoError(t, setting.PublishDefaults(ctx, store))
	resolver := &setting.Resolver{Store: store}
	forWorktree := setting.Request{Scopes: setting.Chain(worktree, repository)}

	shipped, err := resolver.Resolve(ctx, forWorktree)
	require.NoError(t, err)
	require.False(t, shipped.Enabled(setting.KeySteeringEnabled), "steering ships switched off")
	requireSource(t, shipped, setting.FactoryScope)

	save(t, store, setting.GlobalScope, true)
	installation, err := resolver.Resolve(ctx, forWorktree)
	require.NoError(t, err)
	require.True(t, installation.Enabled(setting.KeySteeringEnabled))
	requireSource(t, installation, setting.GlobalScope)

	save(t, store, setting.DirectoryScope(repository), false)
	fromRepository, err := resolver.Resolve(ctx, forWorktree)
	require.NoError(t, err)
	require.False(t, fromRepository.Enabled(setting.KeySteeringEnabled),
		"a worktree inherits the repository it belongs to")
	requireSource(t, fromRepository, setting.DirectoryScope(repository))

	save(t, store, setting.DirectoryScope(worktree), true)
	fromWorktree, err := resolver.Resolve(ctx, forWorktree)
	require.NoError(t, err)
	require.True(t, fromWorktree.Enabled(setting.KeySteeringEnabled))
	requireSource(t, fromWorktree, setting.DirectoryScope(worktree))

	// The worktree's own value is its own; the repository keeps answering for itself.
	forRepository, err := resolver.Resolve(ctx, setting.Request{Scopes: setting.Chain(repository, "")})
	require.NoError(t, err)
	require.False(t, forRepository.Enabled(setting.KeySteeringEnabled),
		"a worktree's override must not leak up into the repository")
}

// requireSource asserts which scope answered, which is the half of the read an
// interface shows as "inherited from …".
func requireSource(t *testing.T, resolution setting.Resolution, want setting.Scope) {
	t.Helper()
	value, ok := resolution.Value(setting.KeySteeringEnabled)
	require.True(t, ok)
	require.Equal(t, want, value.Scope)
}

// openStore gives the test a database of its own with the schema applied.
func openStore(t *testing.T) *scopedpg.Store {
	t.Helper()
	store, err := scopedpg.Open(context.Background(), pgtest.NewDatabase(t))
	require.NoError(t, err)
	t.Cleanup(store.Close)
	require.NoError(t, store.Migrate(context.Background()))
	return store
}

// save stores a setting at one scope, through the port an override is saved with.
// What may be saved is checked first, by the catalogue, exactly as the write surface
// will: the store keeps text, and only this side knows what the text has to mean.
func save(t *testing.T, store *scopedpg.Store, scope scoped.Scope, enabled bool) {
	t.Helper()
	spec, ok := setting.SpecFor(setting.KeySteeringEnabled)
	require.True(t, ok)
	text := setting.Format(enabled)
	require.NoError(t, spec.Validate(text))
	_, err := store.Set(context.Background(), setting.KeySteeringEnabled, scope, text)
	require.NoError(t, err)
}
