package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/schema"
)

// These pin the command's contract without a database. What migrating actually does
// to one is pinned by the container suite in migrate_integration_test.go.

func TestMigrateHelpNamesTheDevelopmentModeItIsTheAlternativeTo(t *testing.T) {
	// The help text itself is prose and may be rewritten at will. What is contractual
	// is that --help succeeds and names the one environment variable that changes what
	// a starting process does, because an operator who cannot find that name cannot
	// tell why a process applied DDL.
	var out bytes.Buffer

	require.NoError(t, migrateCmd([]string{"--help"}, &out))

	require.Contains(t, out.String(), devAutoMigrateEnv)
}

func TestMigrateRefusesArgumentsItDoesNotUnderstand(t *testing.T) {
	// A mistyped argument must not be ignored: silently migrating everything when the
	// operator asked for something narrower is the wrong kind of helpful.
	err := migrateCmd([]string{"execution-store"}, &bytes.Buffer{})

	require.Error(t, err)
	require.Contains(t, err.Error(), "execution-store")
}

func TestTheDevelopmentAutoMigrateModeIsOffUnlessItIsSwitchedOn(t *testing.T) {
	for _, value := range []string{"", "  ", "0", "false", "no", "off", "OFF"} {
		t.Setenv(devAutoMigrateEnv, value)
		require.False(t, devAutoMigrateEnabled(), "%q switched startup DDL on", value)
	}
	for _, value := range []string{"1", "true", "yes", "please"} {
		t.Setenv(devAutoMigrateEnv, value)
		require.True(t, devAutoMigrateEnabled(), "%q did not switch startup DDL on", value)
	}
}

func TestEveryContextAProcessUsesIsListedForIt(t *testing.T) {
	// The worker writes execution records; the API server reads them and owns the
	// dismissals. Both lists must be subsets of the set `migrate` applies, or a
	// process could verify a schema nothing ever applies.
	all := map[string]bool{}
	for _, target := range allSchemaContexts() {
		all[target.name] = true
	}
	for _, contexts := range [][]schemaContext{workerSchemaContexts(), serveSchemaContexts()} {
		for _, target := range contexts {
			require.True(t, all[target.name], "%s is verified but never migrated", target.name)
		}
	}
}

// deadlineRecorder is a schema context that records the deadline it is opened and
// read under, which is the only thing an adapter can act on when a database accepts
// packets and never answers.
type deadlineRecorder struct {
	openDeadline time.Time
	readDeadline time.Time
	hadOpen      bool
	hadRead      bool
}

func (r *deadlineRecorder) context() schemaContext {
	return schemaContext{name: "recorder", open: func(ctx context.Context, _ string) (contextSchema, error) {
		r.openDeadline, r.hadOpen = ctx.Deadline()
		return r, nil
	}}
}

func (r *deadlineRecorder) Migrate(context.Context) error { return nil }

func (r *deadlineRecorder) SchemaState(ctx context.Context) (schema.State, error) {
	r.readDeadline, r.hadRead = ctx.Deadline()
	return schema.State{}, nil
}

func (r *deadlineRecorder) Close() {}

func TestReachingADatabaseIsBoundedOnEveryPathThatReachesOne(t *testing.T) {
	// A database that drops packets instead of refusing them makes connecting hang, and
	// connecting is the first thing every one of these paths does. Unbounded, `migrate`,
	// `worker` and `serve` would all wait forever before saying anything at all — the
	// opposite of the fail-fast startup this step exists to give.
	for name, run := range map[string]func(*deadlineRecorder) error{
		"migrate": func(r *deadlineRecorder) error {
			return migrateSchemas(context.Background(), "dsn", []schemaContext{r.context()}, &bytes.Buffer{})
		},
		"verify": func(r *deadlineRecorder) error {
			return verifySchemas(context.Background(), "dsn", []schemaContext{r.context()})
		},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := &deadlineRecorder{}

			// verify reports a stale schema here; the bound is what is under test.
			_ = run(recorder)

			require.True(t, recorder.hadOpen, "connecting to the database is unbounded")
			require.True(t, recorder.hadRead, "reading the schema version is unbounded")
			require.WithinDuration(t, time.Now().Add(storeConnectTimeout), recorder.openDeadline, time.Minute)
		})
	}
}
