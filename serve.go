package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/agenthub/hubpg"
	"temporal-agents/internal/agenthub/hubrecords"
	"temporal-agents/internal/agenthub/hubtemporal"
	"temporal-agents/internal/httpapi"
)

// The serve command is the composition root for the Agent Hub API. The core and
// every adapter stay unaware of each other until here: the orchestration client is
// put behind the live ports, the execution store behind the record and plan ports,
// Postgres behind the dismissal port, and the application service behind the HTTP
// driving port. That is the only dependency direction a hexagonal application
// permits — adapters point inward, never the core outward.

// serveDefaults are safe for an unauthenticated API that contains workflow goals and
// prompts. A non-loopback address is possible, but it can only be selected with the
// explicit --addr option; there is no environment variable that can widen the bind by
// accident.
const (
	defaultServeAddress = "127.0.0.1:8973"
	defaultWebDirectory = "web/dist"
)

// HTTP server budgets. The handler has its own request deadline; these additionally
// protect the protocol edges before and after a handler runs.
const (
	serveReadHeaderTimeout = 5 * time.Second
	serveReadTimeout       = 35 * time.Second
	serveWriteTimeout      = 35 * time.Second
	serveIdleTimeout       = 2 * time.Minute
	serveShutdownTimeout   = 10 * time.Second
)

// serveOptions are the parsed command options.
type serveOptions struct {
	// address is the listener address. It defaults to loopback.
	address string
	// webDir is the independently-built static bundle to serve for local
	// convenience, or empty to serve only JSON.
	webDir string
	// allowedOrigins are the browser origins explicitly allowed to read the API.
	allowedOrigins []string
}

// stringList is a repeatable flag value, used for --allow-origin. Repetition is
// clearer than a comma-separated value because an origin itself has punctuation.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("the origin must not be empty")
	}
	*s = append(*s, value)
	return nil
}

// parseServeFlags parses the additive serve command without touching process-global
// flags, so the whole CLI contract is reachable from a unit test.
func parseServeFlags(args []string) (serveOptions, error) {
	var options serveOptions
	set := flag.NewFlagSet("serve", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	set.StringVar(&options.address, "addr", defaultServeAddress,
		"listener address (non-loopback is an explicit exposure opt-in)")
	set.StringVar(&options.webDir, "web-dir", defaultWebDirectory,
		"built static bundle to serve, or empty for JSON only")
	set.Var((*stringList)(&options.allowedOrigins), "allow-origin",
		"browser origin allowed to call the API (repeatable; no wildcard)")
	if err := set.Parse(args); err != nil {
		return serveOptions{}, err
	}
	if set.NArg() != 0 {
		return serveOptions{}, fmt.Errorf("serve accepts no positional arguments: %q", set.Arg(0))
	}
	if strings.TrimSpace(options.address) == "" {
		return serveOptions{}, errors.New("--addr must not be empty")
	}
	return options, nil
}

// serveHelp writes the command's usage without exiting, so help is testable and the
// caller decides the process outcome.
func serveHelp(out io.Writer) {
	fmt.Fprint(out, `temporal-agents serve — serve the Agent Hub REST API

USAGE
  temporal-agents serve [--addr <host:port>] [--web-dir <path>]
                          [--allow-origin <origin>]...

The API is served under /api/v1. Its OpenAPI contract is available at
/api/v1/openapi.json and each versioned model schema under /api/v1/schemas.

The API is unauthenticated and can expose workflow goals and prompts, so it binds to
127.0.0.1:8973 by default. Setting --addr to a non-loopback address is an explicit
exposure opt-in. No cross-origin browser access is allowed by default; list each
trusted frontend origin with --allow-origin.

OPTIONS
  --addr <host:port>       Listener address (default 127.0.0.1:8973)
  --web-dir <path>         Built SPA directory for local convenience (default web/dist;
                           use --web-dir= for JSON only)
  --allow-origin <origin>  Browser origin allowed to call the API (repeatable)

ENVIRONMENT
  TEMPORAL_ADDRESS  Temporal server address (default localhost:17233)
  DATABASE_URL      Postgres connection string for execution records, plans, and
                    durable dismissals (required)

EXAMPLES
  temporal-agents serve
  temporal-agents serve --web-dir=
  temporal-agents serve --allow-origin http://localhost:5173
  temporal-agents serve --addr 0.0.0.0:8973
`)
}

// serveCmd runs the API until the process is interrupted. It returns setup and
// shutdown failures instead of exiting, so main owns the process boundary and tests
// can reach the parsing separately.
func serveCmd(args []string) error {
	if wantsHelp(args) {
		serveHelp(os.Stdout)
		return nil
	}
	options, err := parseServeFlags(args)
	if err != nil {
		return err
	}
	return runAPIServer(options)
}

// runAPIServer wires every port and runs the HTTP server.
func runAPIServer(options serveOptions) error {
	ctx := context.Background()

	// Open the execution record as read-only. Its schema remains worker-owned: if no
	// worker has applied it yet, health and resource reads report the dependency as
	// unavailable rather than the API silently creating workflow-owned tables.
	recordStore, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer recordStore.Close()

	// Dismissals are API-owned, so this process applies their schema at startup.
	dsn, err := databaseURL()
	if err != nil {
		return err
	}
	dismissals, err := hubpg.Open(ctx, dsn)
	if err != nil {
		return fmt.Errorf("could not reach the dismissal store: %w", err)
	}
	defer dismissals.Close()
	migrateCtx, cancelMigrate := context.WithTimeout(ctx, storeMigrateTimeout)
	defer cancelMigrate()
	if err := dismissals.Migrate(migrateCtx); err != nil {
		return fmt.Errorf("could not apply the dismissal store schema: %w", err)
	}

	orchestrator, err := connectTemporal()
	if err != nil {
		return err
	}
	defer orchestrator.Close()
	live, err := hubtemporal.NewExecutions(orchestrator)
	if err != nil {
		return err
	}
	schedules, err := hubtemporal.NewSchedules(orchestrator.ScheduleClient())
	if err != nil {
		return err
	}
	records, err := hubrecords.New(recordStore, recordStore)
	if err != nil {
		return err
	}
	service, err := agenthub.NewService(agenthub.Dependencies{
		Live:       live,
		Records:    records,
		Plans:      records,
		Schedules:  schedules,
		Dismissals: dismissals,
	})
	if err != nil {
		return err
	}

	api, err := httpapi.New(service, httpapi.Options{
		Logger:         slog.Default(),
		AllowedOrigins: options.allowedOrigins,
		WebDir:         options.webDir,
		HealthChecks: []httpapi.HealthCheck{
			{
				Name: "temporal",
				Check: func(ctx context.Context) error {
					_, err := live.RunningExecutions(ctx, 1)
					return err
				},
			},
			{
				Name: "execution-store",
				Check: func(ctx context.Context) error {
					_, err := records.RecordedExecutions(ctx, agenthub.RecordQuery{Limit: 1})
					return err
				},
			},
			{
				Name: "dismissal-store",
				Check: func(ctx context.Context) error {
					_, err := dismissals.Dismissals(ctx)
					return err
				},
			},
		},
	})
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              options.address,
		Handler:           api,
		ReadHeaderTimeout: serveReadHeaderTimeout,
		ReadTimeout:       serveReadTimeout,
		WriteTimeout:      serveWriteTimeout,
		IdleTimeout:       serveIdleTimeout,
	}

	// A signal cancels the serve context and starts a bounded graceful shutdown, so
	// an in-flight read finishes but the process can never wait forever.
	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdownDone := make(chan error, 1)
	go func() {
		<-signalCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), serveShutdownTimeout)
		defer cancel()
		shutdownDone <- server.Shutdown(shutdownCtx)
	}()

	slog.Info("serving the Agent Hub API",
		"address", options.address,
		"basePath", httpapi.BasePath,
		"webDir", options.webDir)
	err = server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve the Agent Hub API: %w", err)
	}
	if signalCtx.Err() != nil {
		if shutdownErr := <-shutdownDone; shutdownErr != nil {
			return fmt.Errorf("shut down the Agent Hub API: %w", shutdownErr)
		}
	}
	return nil
}
