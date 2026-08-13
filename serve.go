package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"go.temporal.io/sdk/client"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/agenthub/hubpg"
	"temporal-agents/internal/agenthub/hubplace"
	"temporal-agents/internal/agenthub/hubrecords"
	"temporal-agents/internal/agenthub/hubtemporal"
	"temporal-agents/internal/gitcli"
	"temporal-agents/internal/hostpicker"
	"temporal-agents/internal/httpapi"
	"temporal-agents/internal/identity"
	"temporal-agents/internal/instruction"
	"temporal-agents/internal/notification"
	"temporal-agents/internal/notification/notificationpg"
	"temporal-agents/internal/promptconfig"
	"temporal-agents/internal/promptconfig/hubplaces"
	"temporal-agents/internal/scoped/scopedpg"
	"temporal-agents/internal/setting"
	"temporal-agents/internal/steering"
	"temporal-agents/internal/steering/steeringpg"
	"temporal-agents/internal/steering/steeringtemporal"
)

// The serve command is the composition root for the Agent Hub API. The core and
// every adapter stay unaware of each other until here: the orchestration client is
// put behind the live ports, the execution store behind the record and plan ports,
// Postgres behind the dismissal and place ports, and the application service behind the HTTP
// driving port. That is the only dependency direction a hexagonal application
// permits — adapters point inward, never the core outward.

// serveDefaults are safe for an API that contains workflow goals and prompts. A
// non-loopback address is possible only with an explicit --addr and a bearer token;
// no environment variable can widen the bind by accident.
const (
	defaultServeAddress   = "127.0.0.1:3000"
	defaultWebDirectory   = "web/dist"
	agentHubAuthTokenEnv  = "AGENT_HUB_AUTH_TOKEN"
	minimumAuthTokenBytes = 32
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
	// tlsCert and tlsKey are the PEM certificate chain and private key. Both are
	// mandatory for a non-loopback listener.
	tlsCert string
	tlsKey  string
	// allowedHosts are HTTP Host names accepted in addition to loopback names and
	// the concrete listener host.
	allowedHosts []string
	// allowedOrigins are the browser origins explicitly allowed to call the API.
	allowedOrigins []string
}

// stringList is a repeatable flag value, used for --allow-origin. Repetition is
// clearer than a comma-separated value because an origin itself has punctuation.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("the value must not be empty")
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
	set.StringVar(&options.tlsCert, "tls-cert", "", "PEM TLS certificate chain")
	set.StringVar(&options.tlsKey, "tls-key", "", "PEM TLS private key")
	set.Var((*stringList)(&options.allowedHosts), "allow-host",
		"HTTP Host name accepted by the API (repeatable)")
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
	for index, configured := range options.allowedOrigins {
		origin, err := identity.ParseBrowserOrigin(configured)
		if err != nil {
			return serveOptions{}, fmt.Errorf("--allow-origin: %w", err)
		}
		options.allowedOrigins[index] = origin
	}
	return options, nil
}

// serveHelp writes the command's usage without exiting, so help is testable and the
// caller decides the process outcome.
func serveHelp(out io.Writer) {
	fmt.Fprint(out, `temporal-agents serve — serve the Agent Hub REST API

USAGE
  temporal-agents serve [--addr <host:port>] [--web-dir <path>]
                          [--tls-cert <path> --tls-key <path>]
                          [--allow-host <host>]... [--allow-origin <origin>]...

The API is served under /api/v1. Its OpenAPI contract is available at
/api/v1/openapi.json and each versioned model schema under /api/v1/schemas.

The API can expose workflow goals and prompts, so it binds to 127.0.0.1:3000 by
default. A non-loopback --addr requires TLS and a strong AGENT_HUB_AUTH_TOKEN.
Requests must use a loopback Host, the concrete listener host, or a name listed with
--allow-host. No cross-origin browser access is allowed by default. A session-based
frontend on another same-site origin must be listed with --allow-origin and must use
fetch with credentials: 'include'. The API then answers with credentialed CORS. The
bundled UI uses that fetch mode; set VITE_AGENT_HUB_API_URL to the versioned API
endpoint when building it for another origin.

Every route needs a credential, except signing in, the provider's callback, and the
health probe. A person signs in with an identity provider: the browser is redirected
to it and comes back holding a session cookie only, while every token the provider
issues stays on the server. On a loopback listener an unconfigured hub signs in
against the local compose stack's provider, so 'docker compose up -d' plus this
command is a working sign-in.

A script authenticates with AGENT_HUB_AUTH_TOKEN and no browser. On a loopback
listener the token is minted on first start and stored, readable only by this user,
so 'list' on this machine needs no configuration.

OPTIONS
  --addr <host:port>       Listener address (default 127.0.0.1:3000)
  --web-dir <path>         Built SPA directory for local convenience (default web/dist;
                           use --web-dir= for JSON only)
  --tls-cert <path>        PEM TLS certificate chain (required outside loopback)
  --tls-key <path>         PEM TLS private key (required outside loopback)
  --allow-host <host>      HTTP Host name accepted by the API (repeatable)
  --allow-origin <origin>  Browser origin allowed to call the API (repeatable)

ENVIRONMENT
  TEMPORAL_ADDRESS  Temporal server address (default localhost:17233)
  DATABASE_URL          Postgres connection string for execution records, plans,
                        the hub's own state, and sessions (required)
  AGENT_HUB_AUTH_TOKEN  Bearer token of at least 32 characters (required outside
                        loopback). Generate one with: openssl rand -base64 32
  AGENT_HUB_OIDC_ISSUER Identity provider's issuer URL. Setting it turns signing in
                        on; the local compose stack runs one at
                        http://localhost:15556/dex
  AGENT_HUB_OIDC_CLIENT_ID
  AGENT_HUB_OIDC_CLIENT_SECRET
                        This deployment's registered confidential client. Both are
                        required when the issuer is set; the secret stays server-side.
  AGENT_HUB_PUBLIC_URL  The URL a browser reaches this hub at, which the provider
                        redirects back to. Derived from --addr when it names a host;
                        required behind a proxy or on 0.0.0.0.
  AGENT_HUB_ALLOW_UNAUTHENTICATED
                        Serve with no credential at all. Loopback only, refused
                        anywhere else, and announced on every start. It is the only
                        way to get an open API, so an open API is always somebody's
                        decision.

The schema is applied by 'temporal-agents migrate'. This server verifies it at
startup and refuses to run against a database older than the build it is.

EXAMPLES
  temporal-agents serve
  temporal-agents serve --web-dir=
  temporal-agents serve --allow-origin http://127.0.0.1:3001
  AGENT_HUB_OIDC_ISSUER=http://localhost:15556/dex \
    AGENT_HUB_OIDC_CLIENT_ID=agent-hub \
    AGENT_HUB_OIDC_CLIENT_SECRET=agent-hub-local-secret \
    temporal-agents serve
  AGENT_HUB_AUTH_TOKEN="$(openssl rand -base64 32)" temporal-agents serve \
    --addr 0.0.0.0:3000 --tls-cert hub.crt --tls-key hub.key \
    --allow-host hub.example.test
  curl -H "Authorization: Bearer $AGENT_HUB_AUTH_TOKEN" \
    https://hub.example.test:3000/api/v1
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

// serveSecurity derives the HTTP Host allowlist and enforces transport security
// and authentication when the listener is not loopback.
func serveSecurity(options serveOptions, configuredToken string) ([]string, string, error) {
	host, _, err := net.SplitHostPort(options.address)
	if err != nil {
		return nil, "", fmt.Errorf("parse --addr: %w", err)
	}
	token := strings.TrimSpace(configuredToken)
	loopback := loopbackHost(host)
	if (options.tlsCert == "") != (options.tlsKey == "") {
		return nil, "", errors.New("--tls-cert and --tls-key must be configured together")
	}
	if !loopback && (options.tlsCert == "" || options.tlsKey == "") {
		return nil, "", errors.New("--tls-cert and --tls-key are required when --addr is not loopback")
	}
	if !loopback && token == "" {
		return nil, "", fmt.Errorf("%s is required when --addr is not loopback", agentHubAuthTokenEnv)
	}
	if !loopback && len(token) < minimumAuthTokenBytes {
		return nil, "", fmt.Errorf("%s must contain at least %d characters", agentHubAuthTokenEnv, minimumAuthTokenBytes)
	}
	hosts := append([]string(nil), options.allowedHosts...)
	if host != "" {
		if ip := net.ParseIP(host); ip == nil || !ip.IsUnspecified() {
			hosts = append(hosts, host)
		}
	}
	return hosts, token, nil
}

// loopbackHost reports whether a listener host is this machine and nothing else.
// It is one function because three rules depend on the same answer: whether TLS and
// a token are required, whether a token may be minted on disk, and whether an open
// API may be asked for at all.
func loopbackHost(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return strings.EqualFold(host, "localhost")
}

// isLoopbackAddress answers the same question for a listener address.
func isLoopbackAddress(address string) (bool, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false, fmt.Errorf("parse --addr: %w", err)
	}
	return loopbackHost(host), nil
}

// localOrigins derives the server origins that browsers can use for the bundled
// same-origin UI. They are explicit consequences of the listener and Host allowlist.
func localOrigins(address string, allowedHosts []string, tls bool) []string {
	listenerHost, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil
	}
	hosts := append([]string(nil), allowedHosts...)
	if ip := net.ParseIP(listenerHost); ip != nil && ip.IsLoopback() || strings.EqualFold(listenerHost, "localhost") {
		hosts = append(hosts, "localhost", "127.0.0.1", "::1")
	}
	scheme := "http"
	if tls {
		scheme = "https"
	}
	seen := map[string]bool{}
	origins := make([]string, 0, len(hosts))
	for _, host := range hosts {
		if parsedHost, _, splitErr := net.SplitHostPort(host); splitErr == nil {
			host = parsedHost
		}
		host = strings.Trim(strings.TrimSpace(host), "[]")
		if host == "" {
			continue
		}
		origin := scheme + "://" + net.JoinHostPort(host, port)
		if !seen[origin] {
			seen[origin] = true
			origins = append(origins, origin)
		}
	}
	return origins
}

// runAPIServer wires every port and runs the HTTP server.
func runAPIServer(options serveOptions) error {
	allowedHosts, authToken, err := serveSecurity(options, os.Getenv(agentHubAuthTokenEnv))
	if err != nil {
		return err
	}
	allowedOrigins := append(
		localOrigins(options.address, allowedHosts, options.tlsCert != ""),
		options.allowedOrigins...,
	)
	loopback, err := isLoopbackAddress(options.address)
	if err != nil {
		return err
	}
	// An open API is possible, and only as an answer to a question somebody asked.
	openForLocalUse, err := unauthenticatedAllowed(environment, loopback)
	if err != nil {
		return err
	}
	identityOptions, err := identityConfiguration(options, environment, loopback && !openForLocalUse)
	if err != nil {
		return err
	}
	// Automation authenticates with a token, and on a loopback machine it is minted
	// rather than invented by the operator: the alternative to a working default is
	// either an open port or a configuration exercise before the first command.
	if !openForLocalUse {
		authToken, err = ensureLocalToken(authToken, loopback)
		if err != nil {
			return err
		}
		if authToken == "" && !identityOptions.configured() {
			return fmt.Errorf("%w: configure %s, an identity provider (%s), "+
				"or ask for an open API explicitly with %s on a loopback listener",
				errNoCredential, agentHubAuthTokenEnv, oidcIssuerEnv, allowUnauthenticatedEnv)
		}
	}
	if openForLocalUse {
		slog.Warn("serving the API without any credential because it was asked for — "+
			"anything that can reach this port can read every goal, prompt and failure, "+
			"and can hide work from the overview",
			"env", allowUnauthenticatedEnv)
	}
	ctx := context.Background()

	// Both schemas this process reads and writes through are verified before anything
	// is opened for use. Neither is applied: migrating is the explicit `migrate` step,
	// so the API server can never create a table the worker owns, and a stale database
	// stops the server here instead of surfacing as a failed read later (see
	// migrate.go).
	if err := requireCurrentSchema(ctx, serveSchemaContexts(), slog.Default()); err != nil {
		return err
	}

	// Open the execution record as read-only.
	recordStore, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer recordStore.Close()

	// What the operator hid, and where the operator allows the hub to work, are the
	// durable writes this process owns.
	dsn, err := databaseURL()
	if err != nil {
		return err
	}
	hubStore, err := hubpg.Open(ctx, dsn)
	if err != nil {
		return fmt.Errorf("could not reach the hub store: %w", err)
	}
	defer hubStore.Close()

	// What the tool is configured to do is read from the same database, through the
	// catalogue that owns the rules: the API answers the effective value and the scope
	// it came from, so no consumer re-derives inheritance.
	config, err := scopedpg.Open(ctx, dsn)
	if err != nil {
		return fmt.Errorf("could not reach the configuration store: %w", err)
	}
	defer config.Close()
	settings := &setting.Resolver{Store: config}

	// A waiting round and its conversation belong to the steering context. The API
	// reads and decides through that context's store, not through execution records.
	steeringStore, err := steeringpg.Open(ctx, dsn)
	if err != nil {
		return fmt.Errorf("could not reach the steering store: %w", err)
	}
	defer steeringStore.Close()
	notificationStore, err := notificationpg.Open(ctx, dsn)
	if err != nil {
		return fmt.Errorf("could not reach the notification store: %w", err)
	}
	defer notificationStore.Close()
	inbox := &notification.Inbox{Store: notificationStore}

	// Signing in is opened before anything is served, so a hub configured with a
	// provider it cannot reach stops here with a message instead of failing at an
	// operator's first click.
	signIn, err := openIdentity(ctx, dsn, identityOptions, allowedOrigins)
	if err != nil {
		return err
	}
	defer signIn.Close()

	orchestrator, err := connectTemporal()
	if err != nil {
		return err
	}
	defer orchestrator.Close()
	live, err := hubtemporal.NewExecutions(orchestrator)
	if err != nil {
		return err
	}
	schedules, err := hubtemporal.NewSchedules(orchestrator.WorkflowService(), client.DefaultNamespace)
	if err != nil {
		return err
	}
	steeringSignals, err := steeringtemporal.New(orchestrator)
	if err != nil {
		return err
	}
	steeringService, err := steering.NewService(steeringStore, steeringSignals)
	if err != nil {
		return err
	}
	steeringQuestioner, err := steeringtemporal.NewQuestioner(orchestrator, TaskQueue)
	if err != nil {
		return err
	}
	steeringService.Questioner = steeringQuestioner
	// Starting work is the one thing this process submits rather than reads. It goes
	// to the very queue the worker listens on. Develop runs use the same managed
	// worktree base as the CLI and fleet, so the selected checkout remains untouched.
	worktrees, err := worktreesDir()
	if err != nil {
		return fmt.Errorf("locate the worktrees directory: %w", err)
	}
	launcher, err := hubtemporal.NewLauncher(orchestrator, TaskQueue, worktrees)
	if err != nil {
		return err
	}
	records, err := hubrecords.New(recordStore, recordStore)
	if err != nil {
		return err
	}
	service, err := agenthub.NewService(agenthub.Dependencies{
		Live:        live,
		Collections: records,
		Plans:       records,
		Schedules:   schedules,
		Dismissals:  hubStore,
		Places:      hubStore,
		// A place an operator names is checked against this machine, through the same
		// probe the workflows record their place with: the API server runs beside the
		// worker, so what it can see is what the work will run in.
		Inspector: hubplace.Inspector{Prober: gitcli.New()},
		Launcher:  launcher,
		Launches:  hubStore,
	})
	if err != nil {
		return err
	}
	prompts := &promptconfig.Service{
		Configuration: &instruction.Configuration{Store: config},
		Places:        hubplaces.Adapter{Registry: service},
	}

	api, err := httpapi.New(service, httpapi.Options{
		Logger:               slog.Default(),
		AllowedHosts:         allowedHosts,
		AllowedOrigins:       allowedOrigins,
		AuthToken:            authToken,
		Authenticator:        signIn.authenticator(),
		SignIn:               signIn.signIn(),
		SecureCookies:        options.tlsCert != "",
		AllowUnauthenticated: openForLocalUse,
		WebDir:               options.webDir,
		Start:                service,
		Places:               service,
		DirectoryPicker:      hostpicker.Picker{},
		Settings:             settings,
		Prompts:              prompts,
		Steering:             steeringService,
		Notifications:        inbox,
		HealthChecks: append([]httpapi.HealthCheck{
			{
				Name: "temporal",
				Check: func(ctx context.Context) error {
					if _, err := live.RunningExecutions(ctx, 1); err != nil {
						return err
					}
					_, err := schedules.Schedules(ctx, 1)
					return err
				},
			},
			{
				Name: "execution-store",
				Check: func(ctx context.Context) error {
					if _, err := records.RecordedExecutions(ctx, agenthub.RecordQuery{Limit: 1}); err != nil {
						return err
					}
					_, err := recordStore.ListPlans(ctx, 1)
					return err
				},
			},
			{
				Name: "hub-store",
				Check: func(ctx context.Context) error {
					_, err := hubStore.Dismissals(ctx, agenthub.LocalViewerID)
					return err
				},
			},
			{
				Name: "scoped-config",
				Check: func(ctx context.Context) error {
					_, err := prompts.Catalogue(ctx, "")
					return err
				},
			},
			{
				Name: "notification-store",
				Check: func(ctx context.Context) error {
					_, err := inbox.Unread(ctx, "health-check")
					return err
				},
			},
			{
				Name: "steering-store",
				Check: func(ctx context.Context) error {
					_, err := steeringStore.WaitingSessions(ctx)
					return err
				},
			},
		}, signIn.healthCheck()...),
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
	// Housekeeping runs for as long as the server does, and stops with it.
	go signIn.sweepExpired(signalCtx, slog.Default())
	shutdownDone := make(chan error, 1)
	go func() {
		<-signalCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), serveShutdownTimeout)
		defer cancel()
		shutdownDone <- server.Shutdown(shutdownCtx)
	}()

	slog.Info("serving the Agent Hub API",
		"address", options.address,
		"tls", options.tlsCert != "",
		"basePath", httpapi.BasePath,
		"signIn", identityOptions.configured(),
		"webDir", options.webDir)
	if options.tlsCert != "" {
		err = server.ListenAndServeTLS(options.tlsCert, options.tlsKey)
	} else {
		err = server.ListenAndServe()
	}
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
