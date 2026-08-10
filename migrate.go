package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"temporal-agents/internal/agenthub/hubpg"
	"temporal-agents/internal/execstore/execpg"
	"temporal-agents/internal/pgmigrate"
)

// Schema application is a deliberate step, not something a starting process does on
// the way to taking work. Two processes racing to apply DDL is a failure nobody can
// reconstruct from a log, and a schema change is an operational decision with a
// moment an operator chooses. So `migrate` applies, and `worker` and `serve` only
// *verify* and refuse to start against a database older than the build they are.
//
// Each bounded context keeps its own migrations inside its own adapter package and is
// listed here by name. This file is a composition root: it knows which contexts
// exist, and nothing about any context's SQL. No context reaches into another's
// migrations, and there are no foreign keys between them, so a context stays
// independently migratable and independently testable.

// devAutoMigrateEnv is the one documented way to get DDL at startup: an explicit,
// loud development mode. It exists so a contributor can iterate without a second
// command, and it is an environment variable with an unmistakable name rather than a
// default, so it cannot be switched on by accident and cannot be left on unnoticed —
// a process running with it says so on every start.
const devAutoMigrateEnv = "TEMPORAL_AGENTS_DEV_AUTO_MIGRATE"

// contextSchema is what a bounded context's adapter must offer for its schema to be
// managed from here: apply it, report what it is at, and release its connections.
// Both Postgres adapters satisfy it.
type contextSchema interface {
	// Migrate applies every migration this build carries that is not applied yet.
	Migrate(ctx context.Context) error
	// SchemaState reports what the database is at and what this build requires.
	SchemaState(ctx context.Context) (pgmigrate.State, error)
	// Close releases the adapter's connections.
	Close()
}

// schemaContext is one bounded context that owns a schema, as this composition root
// sees it: a name to report it under, and how to open it.
type schemaContext struct {
	// name is what the context is called in output and in failure messages. It is a
	// context's name, not a table's: an operator reads "execution-store", not "the
	// executions table".
	name string
	// open connects to this context's schema over the shared DSN.
	open func(ctx context.Context, dsn string) (contextSchema, error)
}

// executionStoreSchema is the schema behind the durable execution record and the
// stored plans, written by the worker's activities.
var executionStoreSchema = schemaContext{
	name: "execution-store",
	open: func(ctx context.Context, dsn string) (contextSchema, error) {
		return execpg.Open(ctx, dsn)
	},
}

// agentHubSchema is the schema behind the Agent Hub's dismissals, the one durable
// write the read API owns.
var agentHubSchema = schemaContext{
	name: "agent-hub",
	open: func(ctx context.Context, dsn string) (contextSchema, error) {
		return hubpg.Open(ctx, dsn)
	},
}

// allSchemaContexts lists every context whose schema lives in this database, in the
// order `migrate` reports them. Adding a context means adding it here and nowhere
// else.
func allSchemaContexts() []schemaContext {
	return []schemaContext{executionStoreSchema, agentHubSchema}
}

// workerSchemaContexts are the schemas the worker writes through. It never touches
// the hub's dismissals, so it must not be stopped by their version: a context a
// process does not use is not that process's dependency.
func workerSchemaContexts() []schemaContext {
	return []schemaContext{executionStoreSchema}
}

// serveSchemaContexts are the schemas the API server reads and writes through.
func serveSchemaContexts() []schemaContext {
	return []schemaContext{executionStoreSchema, agentHubSchema}
}

// migrateHelp writes the command's usage.
func migrateHelp(w io.Writer) {
	fmt.Fprintf(w, `temporal-agents migrate — apply the database schema

Applies every bounded context's own migrations, in that context's own order, and
prints the version each context ends at. It is idempotent: running it against an
up-to-date database applies nothing, and two invocations at once serialize on a
database lock rather than racing.

Run it before starting 'worker' or 'serve'. Both verify the schema at startup and
refuse to run against a database older than the build they are; neither applies DDL.

USAGE
  temporal-agents migrate

ENVIRONMENT
  DATABASE_URL  Postgres connection string (required)
  %s
                Development only: let 'worker' and 'serve' apply migrations at
                startup instead of refusing. Loud, opt-in, and never a default.

EXAMPLES
  docker compose up -d
  export DATABASE_URL=postgres://postgres:postgres@localhost:15432/temporal_agents?sslmode=disable
  temporal-agents migrate
  temporal-agents worker
`, devAutoMigrateEnv)
}

// migrateCmd is the explicit schema step. It reports failures instead of exiting, so
// main owns the process boundary.
func migrateCmd(args []string, out io.Writer) error {
	if wantsHelp(args) {
		migrateHelp(out)
		return nil
	}
	if len(args) != 0 {
		return fmt.Errorf("migrate accepts no arguments: %q", args[0])
	}
	dsn, err := databaseURL()
	if err != nil {
		return err
	}
	return migrateSchemas(context.Background(), dsn, allSchemaContexts(), out)
}

// migrateSchemas applies every listed context's migrations and reports the version
// each ends at, together with how many migrations this run applied.
//
// The contexts are applied one after another, each through its own adapter, so a
// context's SQL never leaves its package. A failure stops there: the contexts already
// applied stay applied (each migration commits with its own tracking row), so
// re-running continues rather than starting over.
func migrateSchemas(ctx context.Context, dsn string, contexts []schemaContext, out io.Writer) error {
	report := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, target := range contexts {
		line, err := migrateOne(ctx, dsn, target)
		if err != nil {
			// Flush what already succeeded: an operator needs to see which contexts were
			// applied before the one that failed.
			_ = report.Flush()
			return err
		}
		fmt.Fprintln(report, line)
	}
	return report.Flush()
}

// migrateOne applies one context's schema and renders what it did.
func migrateOne(ctx context.Context, dsn string, target schemaContext) (string, error) {
	schema, err := target.open(ctx, dsn)
	if err != nil {
		return "", fmt.Errorf("could not reach the %s schema: %w", target.name, err)
	}
	defer schema.Close()

	before, err := schema.SchemaState(ctx)
	if err != nil {
		return "", fmt.Errorf("could not read the %s schema version: %w", target.name, err)
	}
	migrateCtx, cancel := context.WithTimeout(ctx, storeMigrateTimeout)
	defer cancel()
	if err := schema.Migrate(migrateCtx); err != nil {
		return "", fmt.Errorf("could not apply the %s schema: %w", target.name, err)
	}
	after, err := schema.SchemaState(ctx)
	if err != nil {
		return "", fmt.Errorf("could not read the %s schema version: %w", target.name, err)
	}
	applied := "already up to date"
	if count := len(before.Missing); count > 0 {
		applied = fmt.Sprintf("applied %d migration(s)", count)
	}
	return fmt.Sprintf("%s\t%s\tschema %s", target.name, applied, after.Version()), nil
}

// verifySchemas checks every listed context's schema and refuses a database older
// than this build. The failure names the context, both versions, and the command that
// fixes it: a startup failure an operator cannot act on is only half a check.
//
// It applies nothing. That is the point of splitting migrate from start — except in
// the explicit development mode, where it says loudly what it is doing.
func verifySchemas(ctx context.Context, dsn string, contexts []schemaContext, out io.Writer) error {
	if devAutoMigrateEnabled() {
		fmt.Fprintf(out, "warning: %s is set — applying migrations at startup. Never do this outside development.\n",
			devAutoMigrateEnv)
		return migrateSchemas(ctx, dsn, contexts, out)
	}
	for _, target := range contexts {
		if err := verifyOne(ctx, dsn, target); err != nil {
			return err
		}
	}
	return nil
}

// verifyOne verifies one context's schema.
func verifyOne(ctx context.Context, dsn string, target schemaContext) error {
	schema, err := target.open(ctx, dsn)
	if err != nil {
		return fmt.Errorf("could not reach the %s schema: %w", target.name, err)
	}
	defer schema.Close()

	state, err := schema.SchemaState(ctx)
	if err != nil {
		return fmt.Errorf("could not read the %s schema version: %w", target.name, err)
	}
	if state.UpToDate() {
		return nil
	}
	return fmt.Errorf("%w: the %s schema is at %s, this build requires %s (%d migration(s) missing: %s). "+
		"Run 'temporal-agents migrate' first",
		pgmigrate.ErrStale, target.name, state.Version(), state.RequiredVersion(),
		len(state.Missing), strings.Join(state.Missing, ", "))
}

// devAutoMigrateEnabled reports whether the documented development mode is on. Any
// non-empty value other than an explicit off switches it on: someone who set it meant
// it.
func devAutoMigrateEnabled() bool {
	value := strings.TrimSpace(os.Getenv(devAutoMigrateEnv))
	switch strings.ToLower(value) {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// requireCurrentSchema is what a starting process calls: verify, or fail with the
// remedy in the message. It is separate from verifySchemas so the error a process
// exits on is reported once, in one voice.
func requireCurrentSchema(ctx context.Context, contexts []schemaContext, out io.Writer) error {
	dsn, err := databaseURL()
	if err != nil {
		return err
	}
	return verifySchemas(ctx, dsn, contexts, out)
}
