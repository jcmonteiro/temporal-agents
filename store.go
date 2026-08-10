package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"temporal-agents/internal/execstore"
	"temporal-agents/internal/execstore/execpg"
	"temporal-agents/internal/instruction"
	"temporal-agents/internal/instruction/instructionpg"
)

// databaseURLEnv names the environment variable carrying the execution store's
// connection string. It is required and has no default: an unset value is a
// fail-fast error in both the worker and the store-backed CLI commands, so a
// misconfiguration can never silently drop the durable record instead of writing
// it. Its value is never printed — a DSN commonly carries credentials, so this
// follows the worker's webhook precedent of reporting only that it is configured.
const databaseURLEnv = "DATABASE_URL"

// storeConnectTimeout bounds reaching the store. Without it an unreachable or
// black-holed host would leave pool.Ping waiting indefinitely, so `history` would
// hang instead of reporting the failure its message promises — and fail-fast is the
// whole contract of a required DSN.
const storeConnectTimeout = 10 * time.Second

// storeMigrateTimeout bounds bringing the schema up to date. It is far longer than
// the connect budget because the advisory lock legitimately waits here: two migrate
// invocations at once serialize on it. It exists so a lock never released (a session
// that died mid-migration) surfaces as a failure rather than a command that hangs
// silently.
const storeMigrateTimeout = 2 * time.Minute

// databaseURL returns the configured DSN, or an error naming what to set when it
// is unset or blank.
//
// It reports rather than exits so the "unset DSN" contract is reachable from a
// test: an exit can only be asserted on by spawning a process.
func databaseURL() (string, error) {
	dsn := strings.TrimSpace(os.Getenv(databaseURLEnv))
	if dsn == "" {
		return "", fmt.Errorf("%s is not set. Start the stack with 'docker compose up -d' and export it, e.g.\n"+
			"  export %s=postgres://postgres:postgres@localhost:15432/temporal_agents?sslmode=disable",
			databaseURLEnv, databaseURLEnv)
	}
	return dsn, nil
}

// openStore connects the CLI to the execution store. The CLI only ever reads
// (every write is owned by a workflow activity), so it does not apply migrations:
// applying them is the explicit `migrate` step.
func openStore(ctx context.Context) (*execpg.Postgres, error) {
	dsn, err := databaseURL()
	if err != nil {
		return nil, err
	}
	// Bound the connect (see storeConnectTimeout). The deadline covers reaching the
	// store, not the pool's later life: the pool keeps working after this context is
	// released, because pgxpool uses it only to pre-warm idle connections (and
	// pool_min_conns defaults to 0, so there are none to pre-warm; a DSN that sets it
	// simply skips the pre-warming once the deadline is gone).
	cctx, cancel := context.WithTimeout(ctx, storeConnectTimeout)
	defer cancel()
	store, err := execpg.Open(cctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("could not reach the execution store: %w", err)
	}
	return store, nil
}

// openExecutionReader opens the store as the read-only port `history` consumes,
// together with the function that releases it.
//
// The command takes the port rather than the adapter, exactly as the workflows'
// activities take the writer: it keeps the read path free of any pgx type and makes
// the whole command reachable from a test with the in-memory fake in its place.
func openExecutionReader(ctx context.Context) (execstore.ExecutionReader, func(), error) {
	store, err := openStore(ctx)
	if err != nil {
		return nil, nil, err
	}
	return store, store.Close, nil
}

// openVerifiedStore connects the worker to the execution store and refuses to
// continue against a schema older than this build. It applies nothing: migrating is
// the explicit `migrate` step, so two workers starting together can never race to
// apply DDL, and a schema change happens at a moment an operator chose (see
// migrate.go).
func openVerifiedStore(ctx context.Context) *execpg.Postgres {
	if err := requireCurrentSchema(ctx, workerSchemaContexts(), slog.Default()); err != nil {
		fatalf("%v", err)
	}
	store, err := openStore(ctx)
	if err != nil {
		fatalf("%v", err)
	}
	return store
}

// openPublishedInstructions connects the worker to the instruction store and
// publishes the instructions this build ships into it, so an upgrade that improves
// one reaches every place that has not overridden it.
//
// Both halves are fail-fast, and for the same reason the execution store is: an
// instruction is what the agent is told, so a worker that cannot reach the store —
// or cannot publish what it ships — must not take work and resolve to something
// nobody chose. Publication applies no DDL (the schema is the explicit migrate
// step's) and is safe to run while another worker starts: it takes a lock per key
// and writes only when the shipped text actually changed.
func openPublishedInstructions(ctx context.Context) *instructionpg.Store {
	dsn, err := databaseURL()
	if err != nil {
		fatalf("%v", err)
	}
	connectCtx, cancel := context.WithTimeout(ctx, storeConnectTimeout)
	defer cancel()
	store, err := instructionpg.Open(connectCtx, dsn)
	if err != nil {
		fatalf("could not reach the instruction store: %v", err)
	}
	publishCtx, cancelPublish := context.WithTimeout(ctx, storeConnectTimeout)
	defer cancelPublish()
	if err := instruction.PublishDefaults(publishCtx, store); err != nil {
		store.Close()
		fatalf("could not publish the instructions this build ships: %v", err)
	}
	return store
}
