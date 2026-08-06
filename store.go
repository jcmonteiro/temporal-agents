package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"temporal-agents/internal/execstore/execpg"
)

// databaseURLEnv names the environment variable carrying the execution store's
// connection string. It is required and has no default: an unset value is a
// fail-fast error in both the worker and the store-backed CLI commands, so a
// misconfiguration can never silently drop the durable record instead of writing
// it. Its value is never printed — a DSN commonly carries credentials, so this
// follows the worker's webhook precedent of reporting only that it is configured.
const databaseURLEnv = "DATABASE_URL"

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
// the worker does that at startup.
func openStore(ctx context.Context) (*execpg.Postgres, error) {
	dsn, err := databaseURL()
	if err != nil {
		return nil, err
	}
	store, err := execpg.Open(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("could not reach the execution store: %w", err)
	}
	return store, nil
}

// openMigratedStore connects the worker to the execution store and brings its
// schema up to date. Applying the embedded migrations at startup is what makes
// `docker compose up -d` plus a worker enough to run — no migrate binary, no psql
// step — and it is idempotent, so restarting a worker against an up-to-date
// database does nothing.
func openMigratedStore(ctx context.Context) *execpg.Postgres {
	store, err := openStore(ctx)
	if err != nil {
		fatalf("%v", err)
	}
	if err := store.Migrate(ctx); err != nil {
		store.Close()
		fatalf("Could not apply the execution store schema: %v", err)
	}
	return store
}
