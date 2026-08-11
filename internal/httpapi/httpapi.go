// Package httpapi is the driving adapter that publishes the Agent Hub read model as
// a versioned REST API.
//
// Everything about the API's shape lives here and nowhere else: the resources, their
// representations (dto.go), the failures they can report (problem.go), the posture
// every response is served under (middleware.go), and the specification and schemas
// the contract is published as (spec.go). The application core knows none of it, so
// the contract can hold still while the model and its sources move underneath — and
// the contract, not this implementation, is what a consumer builds against.
//
// The API is read-only except for one write: an operator dismissing a finished item
// from their overview, which is view state and never touches the work. It is
// unauthenticated only on its default loopback listener; an exposed listener requires
// bearer authentication. It is built as if exposed in either mode: bounded rate, bounded
// bodies, bounded time, no permissive cross-origin default, and no internal detail in
// any answer.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"temporal-agents/internal/agenthub"
	"temporal-agents/internal/identity"
	"temporal-agents/internal/instruction"
	"temporal-agents/internal/promptconfig"
	"temporal-agents/internal/setting"
)

// BasePath is where the API lives. The major version is in the path on purpose: a
// consumer moves to a future v2 by a deliberate change of URL, never by this server
// deciding to answer differently one day.
const BasePath = "/api/v1"

// Version is the API's major version, as reported by the service description. It is
// the "1" in BasePath.
const Version = "1"

// Defaults for the bounds every request is served under. They are generous for a
// local operator and small enough that no single request can exhaust the server.
const (
	// DefaultRequestTimeout bounds how long one request may take.
	DefaultRequestTimeout = 30 * time.Second
	// DefaultMaxBodyBytes bounds a write body. The only body this API accepts names
	// an item to hide: a few dozen bytes.
	DefaultMaxBodyBytes int64 = 64 << 10
	// DefaultRequestsPerSecond and DefaultBurst bound the accepted request rate, so a
	// consumer polling in a tight loop degrades itself rather than the server.
	DefaultRequestsPerSecond = 50
	DefaultBurst             = 100
)

// WorkView is the driving port the transport depends on: the read model of an
// operator's work, plus the single write. It is declared here, in terms of the core's
// model, so the HTTP layer is testable against a stand-in and cannot reach past the
// core into an adapter.
//
// *agenthub.Service implements it.
type WorkView interface {
	// Fleets returns the fleet satellites, newest first.
	Fleets(ctx context.Context, limit int) ([]agenthub.Fleet, error)
	// Fleet returns one fleet with its node graph.
	Fleet(ctx context.Context, id string) (agenthub.Fleet, error)
	// Runs returns the standalone run satellites, newest first.
	Runs(ctx context.Context, limit int) ([]agenthub.Run, error)
	// Run returns one run chain.
	Run(ctx context.Context, id string) (agenthub.Run, error)
	// Schedules returns the schedule satellites.
	Schedules(ctx context.Context, limit int) ([]agenthub.Schedule, error)
	// ActiveWork returns one bounded page of active top-level work.
	ActiveWork(ctx context.Context, query agenthub.PageQuery) (agenthub.Page[agenthub.ActiveWorkItem], error)
	// Dismissals returns the dismissals in force.
	Dismissals(ctx context.Context) ([]agenthub.Dismissal, error)
	// Dismiss hides a finished item from the overview.
	Dismiss(ctx context.Context, kind agenthub.ItemKind, itemID string) (agenthub.Dismissal, error)
	// Undismiss brings a dismissed item back.
	Undismiss(ctx context.Context, kind agenthub.ItemKind, itemID string) error
}

// PlaceView is the surface that answers "where may the hub work?", and the one
// mutation that changes the answer.
//
// It is a driving port of its own, next to the work view rather than inside it,
// because registering a place is a write against the operator's machine and not a
// read of work. A deployment that publishes no place registry serves no places
// resource at all, exactly as one with no identity provider serves no sign-in
// routes.
//
// *agenthub.Service implements it.
type PlaceView interface {
	// RegisteredPlaces returns the places an operator registered, whether or not any
	// work has ever run in them.
	RegisteredPlaces(ctx context.Context) ([]agenthub.RegisteredPlace, error)
	// RegisterPlace records that the hub may work in a directory, and returns the
	// place it registered. It is idempotent on the place the directory resolves to.
	RegisterPlace(ctx context.Context, directory, by string) (agenthub.RegisteredPlace, error)
}

// WorkStarter is the surface that starts agent work.
//
// It is a driving port entirely of its own, and deliberately not a method on the
// work view: everything the view offers reads what happened, while this runs an
// agent on the operator's machine. A deployment that publishes no starter serves no
// way to start work, and the read surface then remains read-only by construction.
//
// *agenthub.Service implements it.
type WorkStarter interface {
	// StartWork starts one unit of agent work in a place the hub knows, exactly once
	// per request identity.
	StartWork(ctx context.Context, request agenthub.StartRequest) (agenthub.StartedWork, error)
}

// SettingsView is the read the configuration surface answers from: what every
// governed setting is, and where each answer came from.
//
// The source travels with the value because a consumer must never re-derive
// inheritance: "off because the installation says so" and "off because nobody has
// ever set it" are different facts, and two consumers each deriving which is which
// would eventually disagree with the server and with each other.
type SettingsView interface {
	// Settings returns every governed setting as it applies to this installation.
	Settings(ctx context.Context) (setting.Resolution, error)
}

// PromptConfiguration is the driving port for prompt catalogue reads and mutations.
type PromptConfiguration interface {
	Catalogue(ctx context.Context, locationID string) (instruction.Catalogue, error)
	Set(ctx context.Context, locationID string, key instruction.Key, text, savedBy string) (instruction.Record, error)
	Reset(ctx context.Context, locationID string, key instruction.Key) error
}

// HealthCheck is one dependency the health resource reports on. The wiring supplies
// them, so the transport reports readiness without having to know what the API reads
// through.
type HealthCheck struct {
	// Name identifies the dependency in the health document.
	Name string
	// Check reports whether the dependency can currently be reached. It must be
	// cheap: it runs on every health request.
	Check func(ctx context.Context) error
}

// Options configure a server. Every field has a working default, so the zero value
// plus a view is a valid, safe server.
type Options struct {
	// Logger receives one structured line per request plus any internal failure. It
	// defaults to slog.Default().
	Logger *slog.Logger
	// RequestTimeout bounds a single request, defaulting to DefaultRequestTimeout.
	RequestTimeout time.Duration
	// MaxBodyBytes bounds a write body, defaulting to DefaultMaxBodyBytes.
	MaxBodyBytes int64
	// RequestsPerSecond and Burst bound the accepted request rate. Zero selects the
	// safe default; a negative RequestsPerSecond switches rate limiting off, which is
	// only sensible in a test.
	RequestsPerSecond float64
	Burst             int
	// AllowedHosts lists additional HTTP Host names accepted by the server. The
	// loopback names localhost, 127.0.0.1, and ::1 are accepted by default. Every
	// other name must be explicit to prevent DNS rebinding.
	AllowedHosts []string
	// AllowedOrigins lists the browser origins allowed to read the API
	// cross-origin. It is empty by default. A request that supplies any other Origin
	// is rejected.
	AllowedOrigins []string
	// AuthToken, when set, requires an Authorization: Bearer header with this value.
	// The composition root requires it whenever the listener is not loopback. It is a
	// convenience for the one credential that needs no ports: the server turns it into
	// the static-token authenticator and puts it behind the same seam as a session.
	AuthToken string
	// Authenticator resolves a request's credential to a principal. The composition
	// root supplies it when the deployment can sign a browser in; it is chained with
	// the static token above, so the transport asks one port whatever is configured.
	Authenticator identity.Authenticator
	// SignIn is the browser's side of authentication. It is nil for a deployment with
	// no identity provider, which then publishes no sign-in routes.
	SignIn SignIn
	// SecureCookies marks the cookies this API sets as Secure, so a credential issued
	// over TLS never travels in the clear. The composition root sets it when the
	// listener serves TLS.
	SecureCookies bool
	// AllowUnauthenticated serves the API with no credential at all. It exists for a
	// single-operator loopback machine and nothing else: New refuses to build a server
	// that neither authenticates nor was explicitly asked not to, so an open surface
	// is always somebody's decision rather than an oversight.
	AllowUnauthenticated bool
	// WebDir, when set, is a directory of built static assets served outside the API's
	// path, for local convenience. The API itself never depends on it: the same bundle
	// can be served by anything else without the API changing.
	WebDir string
	// Start is the surface that starts agent work. It is nil for a deployment that
	// only watches, which then offers no way to start anything.
	Start WorkStarter
	// Places is the registry of where the hub may work, and the write that adds to
	// it. It is nil for a deployment that publishes no registry, which then serves no
	// places resource.
	Places PlaceView
	// Settings is the read of what the tool is configured to do. It is nil for a
	// deployment that publishes no configuration, which then serves no settings
	// resource, exactly as a deployment with no identity provider serves no sign-in
	// routes.
	Settings SettingsView
	// Prompts is the editable instruction catalogue. It is nil for a deployment that
	// does not publish prompt configuration.
	Prompts PromptConfiguration
	// Steering is the surface for rounds that wait for an operator. It is nil for a
	// deployment that does not publish steering, which then serves no steering
	// resources.
	Steering SteeringView
	// Notifications is the signed-in principal's durable inbox.
	Notifications NotificationsView
	// StreamPollInterval controls how often long-lived event streams check their
	// durable source. Zero selects one second.
	StreamPollInterval time.Duration
	// HealthChecks are the dependencies the health resource probes.
	HealthChecks []HealthCheck
	// DeprecatedSince and SunsetAt announce the API's lifecycle when an operator sets
	// them. Both are zero by default: an API that is not deprecated must not say it is.
	DeprecatedSince time.Time
	SunsetAt        time.Time
	// Now supplies the current time, and defaults to time.Now. It is injectable so a
	// test can assert on a duration or a timestamp.
	Now func() time.Time
}

// Server is the API's HTTP handler together with the state every response is served
// under.
type Server struct {
	view            WorkView
	basePath        string
	logger          *slog.Logger
	timeout         time.Duration
	maxBodyBytes    int64
	limiter         *rate.Limiter
	signInLimiter   *rate.Limiter
	allowedHosts    map[string]struct{}
	allowedOrigins  map[string]bool
	authenticator   identity.Authenticator
	signIn          SignIn
	secureCookies   bool
	webDir          string
	start           WorkStarter
	places          PlaceView
	settings        SettingsView
	prompts         PromptConfiguration
	steering        SteeringView
	notifications   NotificationsView
	streamPoll      time.Duration
	streams         *streamLimiter
	healthChecks    []HealthCheck
	deprecatedSince time.Time
	sunsetAt        time.Time
	retryAfter      time.Duration
	now             func() time.Time
	spec            specification
	handler         http.Handler
}

// New builds the server. It fails rather than starts when the view is missing or the
// embedded specification does not parse: a server that cannot publish its own
// contract is not one this API is willing to be.
func New(view WorkView, options Options) (*Server, error) {
	if view == nil {
		return nil, errors.New("the work view is required")
	}
	spec, err := loadSpecification()
	if err != nil {
		return nil, err
	}
	s := &Server{
		view:         view,
		basePath:     BasePath,
		logger:       options.Logger,
		timeout:      options.RequestTimeout,
		maxBodyBytes: options.MaxBodyBytes,
		allowedHosts: map[string]struct{}{
			"localhost": {},
			"127.0.0.1": {},
			"::1":       {},
		},
		allowedOrigins:  map[string]bool{},
		signIn:          options.SignIn,
		secureCookies:   options.SecureCookies,
		webDir:          options.WebDir,
		start:           options.Start,
		places:          options.Places,
		settings:        options.Settings,
		prompts:         options.Prompts,
		steering:        options.Steering,
		notifications:   options.Notifications,
		streamPoll:      options.StreamPollInterval,
		streams:         newStreamLimiter(4, 2),
		healthChecks:    options.HealthChecks,
		deprecatedSince: options.DeprecatedSince,
		sunsetAt:        options.SunsetAt,
		retryAfter:      retryAfterDefault,
		now:             options.Now,
		spec:            spec,
	}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	if s.timeout <= 0 {
		s.timeout = DefaultRequestTimeout
	}
	if s.maxBodyBytes <= 0 {
		s.maxBodyBytes = DefaultMaxBodyBytes
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.streamPoll <= 0 {
		s.streamPoll = time.Second
	}
	perSecond, burst := options.RequestsPerSecond, options.Burst
	if perSecond == 0 {
		perSecond, burst = DefaultRequestsPerSecond, DefaultBurst
	}
	s.limiter = newLimiter(perSecond, burst)
	s.signInLimiter = newSignInLimiter(s.limiter)
	authenticator, err := newAuthenticator(options)
	if err != nil {
		return nil, err
	}
	if authenticator == nil && !options.AllowUnauthenticated {
		// The one place an open surface can come from is an explicit request for one.
		// Everything else — a missing token, an unconfigured provider, a half-finished
		// deployment — stops here instead of serving the operator's work to whatever can
		// reach the port.
		return nil, errors.New("no credential is configured: supply an authentication " +
			"port or a token, or ask for an unauthenticated server explicitly")
	}
	s.authenticator = authenticator
	for _, host := range options.AllowedHosts {
		if canonical := canonicalHost(host); canonical != "" {
			s.allowedHosts[canonical] = struct{}{}
		}
	}
	for _, origin := range options.AllowedOrigins {
		if trimmed := strings.TrimSpace(origin); trimmed != "" && trimmed != "*" {
			// A wildcard is refused silently rather than honoured: it would expose an
			// unauthenticated API to every page a browser visits.
			s.allowedOrigins[trimmed] = true
		}
	}
	s.handler = s.buildHandler()
	return s, nil
}

// ServeHTTP makes the server the handler an HTTP server runs.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

// resource is one addressable path and the methods it offers. Routing is declared as
// data so the specification can be checked against it: a resource that is served but
// not documented, or documented but not served, fails the package's tests.
type resource struct {
	// pattern is the path, with {name} wildcards.
	pattern string
	// methods maps an HTTP method to its handler.
	methods map[string]http.HandlerFunc
	// undocumented marks a path that is deliberately outside the specification (the
	// well-known discovery document, which is defined by its own standard, and the
	// static assets, which are not part of the API).
	undocumented bool
}

// resources declares the whole API surface.
func (s *Server) resources() []resource {
	list := []resource{
		{pattern: s.basePath, methods: map[string]http.HandlerFunc{http.MethodGet: s.handleServiceDescription}},
		{pattern: s.basePath + "/openapi.json", methods: map[string]http.HandlerFunc{http.MethodGet: s.handleSpecification}},
		{pattern: s.basePath + "/schemas", methods: map[string]http.HandlerFunc{http.MethodGet: s.handleSchemaIndex}},
		{pattern: s.basePath + "/schemas/{model}", methods: map[string]http.HandlerFunc{http.MethodGet: s.handleSchema}},
		{pattern: s.basePath + "/problems", methods: map[string]http.HandlerFunc{http.MethodGet: s.handleProblemIndex}},
		{pattern: s.basePath + "/problems/{code}", methods: map[string]http.HandlerFunc{http.MethodGet: s.handleProblemType}},
		{pattern: s.basePath + "/health", methods: map[string]http.HandlerFunc{http.MethodGet: s.handleHealth}},
		{pattern: s.basePath + "/active-work", methods: map[string]http.HandlerFunc{http.MethodGet: s.handleActiveWork}},
		{pattern: s.basePath + "/fleets", methods: map[string]http.HandlerFunc{http.MethodGet: s.handleFleets}},
		{pattern: s.basePath + "/fleets/{id}", methods: map[string]http.HandlerFunc{http.MethodGet: s.handleFleet}},
		{pattern: s.basePath + "/runs", methods: s.runCollectionMethods()},
		{pattern: s.basePath + "/runs/{id}", methods: map[string]http.HandlerFunc{http.MethodGet: s.handleRun}},
		{pattern: s.basePath + "/schedules", methods: map[string]http.HandlerFunc{http.MethodGet: s.handleSchedules}},
		{pattern: s.basePath + "/dismissals", methods: map[string]http.HandlerFunc{
			http.MethodGet:  s.handleDismissals,
			http.MethodPost: s.handleDismiss,
		}},
		{pattern: s.basePath + "/dismissals/{id}", methods: map[string]http.HandlerFunc{
			http.MethodDelete: s.handleUndismiss,
		}},
		{
			pattern:      "/.well-known/api-catalog",
			methods:      map[string]http.HandlerFunc{http.MethodGet: s.handleAPICatalog},
			undocumented: true,
		},
	}
	if s.places != nil {
		list = append(list, resource{
			pattern: s.basePath + "/places",
			methods: map[string]http.HandlerFunc{
				http.MethodGet:  s.handlePlaces,
				http.MethodPost: s.handleRegisterPlace,
			},
		})
	}
	if s.settings != nil {
		list = append(list, resource{
			pattern: s.basePath + "/settings",
			methods: map[string]http.HandlerFunc{http.MethodGet: s.handleSettings},
		})
	}
	if s.prompts != nil {
		list = append(list,
			resource{
				pattern: s.basePath + "/prompts",
				methods: map[string]http.HandlerFunc{http.MethodGet: s.handlePrompts},
			},
			resource{
				pattern: s.basePath + "/prompts/{key}",
				methods: map[string]http.HandlerFunc{
					http.MethodPut:    s.handleSetPrompt,
					http.MethodDelete: s.handleResetPrompt,
				},
			},
		)
	}
	if s.notifications != nil {
		list = append(list,
			resource{pattern: s.basePath + "/notifications", methods: map[string]http.HandlerFunc{http.MethodGet: s.handleNotifications}},
			resource{pattern: s.basePath + "/notifications/read", methods: map[string]http.HandlerFunc{http.MethodDelete: s.handleNotificationClearRead}},
			resource{pattern: s.basePath + "/notifications/{id}/read", methods: map[string]http.HandlerFunc{http.MethodPost: s.handleNotificationRead}},
		)
	}
	if s.steering != nil {
		list = append(list,
			resource{
				pattern: s.basePath + "/steering/sessions",
				methods: map[string]http.HandlerFunc{http.MethodGet: s.handleSteeringSessions},
			},
			resource{
				pattern: s.basePath + "/steering/sessions/{id}",
				methods: map[string]http.HandlerFunc{http.MethodGet: s.handleSteeringSession},
			},
			resource{
				pattern: s.basePath + "/steering/sessions/{id}/events",
				methods: map[string]http.HandlerFunc{http.MethodGet: s.handleConversationEvents},
			},
			resource{
				pattern: s.basePath + "/events",
				methods: map[string]http.HandlerFunc{http.MethodGet: s.handleHubEvents},
			},
			resource{
				pattern: s.basePath + "/steering/sessions/{id}/question",
				methods: map[string]http.HandlerFunc{http.MethodPost: s.handleSteeringQuestion},
			},
			resource{
				pattern: s.basePath + "/steering/sessions/{id}/decision",
				methods: map[string]http.HandlerFunc{http.MethodPost: s.handleSteeringDecision},
			},
		)
	}
	return append(list, s.authRoutes()...)
}

// runCollectionMethods are the methods the run collection offers: reading the runs
// always, and starting one where the deployment configures a starter.
func (s *Server) runCollectionMethods() map[string]http.HandlerFunc {
	methods := map[string]http.HandlerFunc{http.MethodGet: s.handleRuns}
	if s.start != nil {
		methods[http.MethodPost] = s.handleStartWork
	}
	return methods
}

// buildHandler wires the routing table and the middleware chain. The order is
// deliberate: a panic is caught outside everything so nothing can escape unlogged,
// the access log wraps the work so every answer is recorded, and the rate limit sits
// ahead of the handlers so a refused request costs almost nothing.
func (s *Server) buildHandler() http.Handler {
	mux := http.NewServeMux()
	for _, res := range s.resources() {
		mux.Handle(res.pattern, s.dispatch(res))
	}
	// Anything under the API's path that matched no resource is a 404 from the API
	// itself, never the static application: an unknown endpoint must not answer with
	// an HTML page.
	mux.HandleFunc(s.basePath+"/", func(w http.ResponseWriter, r *http.Request) {
		s.writeProblem(w, r, codeNotFound, "no such endpoint")
	})
	mux.Handle("/", s.rootHandler())

	var handler http.Handler = mux
	handler = s.withTimeout(handler)
	handler = s.rateLimit(handler)
	handler = s.requireSameSite(handler)
	handler = s.authenticate(handler)
	handler = s.cors(handler)
	handler = s.requireHost(handler)
	handler = s.deprecation(handler)
	// Recovery is inside security and access logging: a recovered panic receives the
	// same headers as every answer, and the resulting 500 is included in the access
	// log instead of only in the diagnostic log.
	handler = s.recoverPanics(handler)
	handler = securityHeaders(handler)
	handler = s.accessLog(handler)
	handler = withRequestID(handler)
	return handler
}

// dispatch answers one resource, refusing a method it does not offer with a problem
// document and an Allow header rather than with a bare status.
func (s *Server) dispatch(res resource) http.Handler {
	allowed := make([]string, 0, len(res.methods)+1)
	for method := range res.methods {
		allowed = append(allowed, method)
	}
	if _, hasGet := res.methods[http.MethodGet]; hasGet {
		// A HEAD is answered by the GET handler, which writes the headers and omits the
		// body (see writeJSON).
		allowed = append(allowed, http.MethodHead)
	}
	sort.Strings(allowed)
	allowHeader := strings.Join(allowed, ", ")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.Method
		if method == http.MethodHead {
			method = http.MethodGet
		}
		handler, ok := res.methods[method]
		if !ok {
			w.Header().Set("Allow", allowHeader)
			s.writeProblem(w, r, codeMethodNotAllowed, "this resource offers: "+allowHeader)
			return
		}
		handler(w, r)
	})
}

// handleActiveWork answers the additive paged resource used by the CLI.
func (s *Server) handleActiveWork(w http.ResponseWriter, r *http.Request) {
	query, ok := s.activeWorkQuery(w, r)
	if !ok {
		return
	}
	page, err := s.view.ActiveWork(r.Context(), query)
	if err != nil {
		s.writeServiceProblem(w, r, err)
		return
	}
	items := make([]activeWorkResource, 0, len(page.Items))
	locations := make([]agenthub.Location, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, activeWorkResource{
			ID: item.ID, Type: item.Type, Status: item.Status, Running: item.Running,
			LocationID: item.Location.ID(),
		})
		locations = append(locations, item.Location)
	}
	s.writeJSON(w, r, http.StatusOK, modelActiveWorkCollection,
		newActiveWorkCollection(items, query.Limit, s.nextPage(r, page.Next),
			agenthub.NewLocationRegistry(locations...)))
}

// handleFleets answers the fleet collection.
func (s *Server) handleFleets(w http.ResponseWriter, r *http.Request) {
	limit, ok := s.limitParam(w, r)
	if !ok {
		return
	}
	fleets, err := s.view.Fleets(r.Context(), limit)
	if err != nil {
		s.writeServiceProblem(w, r, err)
		return
	}
	items := make([]fleetResource, 0, len(fleets))
	// Each fleet refers to its own place and to its up-next nodes' places, so the
	// registry's input is at least one entry per fleet.
	locations := make([]agenthub.Location, 0, len(fleets))
	for _, fleet := range fleets {
		resource, referred := fleetFrom(fleet, false)
		items = append(items, resource)
		locations = append(locations, referred...)
	}
	s.writeJSON(w, r, http.StatusOK, modelFleetCollection,
		newLocatedCollection(items, limit, agenthub.NewLocationRegistry(locations...)))
}

// handleFleet answers one fleet, with its plan's graph.
func (s *Server) handleFleet(w http.ResponseWriter, r *http.Request) {
	fleet, err := s.view.Fleet(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeServiceProblem(w, r, err)
		return
	}
	resource, _ := fleetFrom(fleet, true)
	s.writeJSON(w, r, http.StatusOK, modelFleet, resource)
}

// handleRuns answers the run collection.
func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	limit, ok := s.limitParam(w, r)
	if !ok {
		return
	}
	runs, err := s.view.Runs(r.Context(), limit)
	if err != nil {
		s.writeServiceProblem(w, r, err)
		return
	}
	items := make([]runResource, 0, len(runs))
	locations := make([]agenthub.Location, 0, len(runs))
	for _, run := range runs {
		items = append(items, runFrom(run, false))
		locations = append(locations, run.Location)
	}
	s.writeJSON(w, r, http.StatusOK, modelRunCollection,
		newLocatedCollection(items, limit, agenthub.NewLocationRegistry(locations...)))
}

// handleStartWork starts agent work and answers with the run it started.
//
// It answers 201 for a repeated request too, with the same resource: the request
// identity names the work, so a retry addresses the run it already created rather
// than a second one, and reporting a conflict where the caller got exactly what it
// asked for would be a refusal of nothing.
func (s *Server) handleStartWork(w http.ResponseWriter, r *http.Request) {
	var request startWorkRequest
	if !s.decodeJSONBody(w, r, &request) {
		return
	}
	by := ""
	if principal, ok := PrincipalFrom(r.Context()); ok {
		by = principal.ID()
	}
	started, err := s.start.StartWork(r.Context(), agenthub.StartRequest{
		RequestID: request.RequestID,
		Kind:      request.Kind,
		PlaceID:   request.PlaceID,
		Prompt:    request.Prompt,
		Worktree:  request.Worktree,
		StartedBy: by,
	})
	if err != nil {
		s.writeServiceProblem(w, r, err)
		return
	}
	w.Header().Set("Location", s.basePath+"/runs/"+started.RunID)
	s.writeJSON(w, r, http.StatusCreated, modelStartedWork, startedWorkFrom(started))
}

// handleRun answers one run chain.
func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.view.Run(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeServiceProblem(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, modelRun, runFrom(run, true))
}

// handleSchedules answers the schedule collection.
func (s *Server) handleSchedules(w http.ResponseWriter, r *http.Request) {
	limit, ok := s.limitParam(w, r)
	if !ok {
		return
	}
	schedules, err := s.view.Schedules(r.Context(), limit)
	if err != nil {
		s.writeServiceProblem(w, r, err)
		return
	}
	items := make([]scheduleResource, 0, len(schedules))
	locations := make([]agenthub.Location, 0, len(schedules))
	for _, schedule := range schedules {
		items = append(items, scheduleFrom(schedule))
		locations = append(locations, schedule.Location)
	}
	s.writeJSON(w, r, http.StatusOK, modelScheduleCollection,
		newLocatedCollection(items, limit, agenthub.NewLocationRegistry(locations...)))
}

// handleDismissals answers the dismissal collection: what the operator has hidden.
func (s *Server) handleDismissals(w http.ResponseWriter, r *http.Request) {
	dismissals, err := s.view.Dismissals(r.Context())
	if err != nil {
		s.writeServiceProblem(w, r, err)
		return
	}
	items := make([]dismissalResource, 0, len(dismissals))
	for _, dismissal := range dismissals {
		items = append(items, dismissalFrom(dismissal))
	}
	s.writeJSON(w, r, http.StatusOK, modelDismissalCollection, newCollection(items, len(items)))
}

// handleDismiss hides a finished item from the overview.
//
// The dismissal's identity is derived from the item it refers to, so posting the same
// item twice addresses the same resource: a client that retries a lost response gets
// the same dismissal back rather than creating a second one.
func (s *Server) handleDismiss(w http.ResponseWriter, r *http.Request) {
	var request dismissalRequest
	if !s.decodeJSONBody(w, r, &request) {
		return
	}
	dismissal, err := s.view.Dismiss(r.Context(), request.Kind, request.ItemID)
	if err != nil {
		s.writeServiceProblem(w, r, err)
		return
	}
	w.Header().Set("Location", s.basePath+"/dismissals/"+dismissal.ID())
	s.writeJSON(w, r, http.StatusCreated, modelDismissal, dismissalFrom(dismissal))
}

// handleUndismiss brings a dismissed item back onto the overview.
func (s *Server) handleUndismiss(w http.ResponseWriter, r *http.Request) {
	kind, itemID, err := agenthub.ParseDismissalID(r.PathValue("id"))
	if err != nil {
		s.writeProblem(w, r, codeInvalidRequest, err.Error())
		return
	}
	if err := s.view.Undismiss(r.Context(), kind, itemID); err != nil {
		s.writeServiceProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePlaces answers where the hub may work: the places an operator registered,
// whether or not anything has ever run in them.
//
// The registry is the whole point of the read, so it carries the same registry every
// work collection does: a consumer resolves a place's label, its path and its parent
// the one way, wherever it read the reference.
func (s *Server) handlePlaces(w http.ResponseWriter, r *http.Request) {
	places, err := s.places.RegisteredPlaces(r.Context())
	if err != nil {
		s.writeServiceProblem(w, r, err)
		return
	}
	items := make([]placeResource, 0, len(places))
	locations := make([]agenthub.Location, 0, len(places))
	for _, place := range places {
		items = append(items, placeFrom(place, false))
		locations = append(locations, place.Location)
	}
	s.writeJSON(w, r, http.StatusOK, modelPlaceCollection,
		newLocatedCollection(items, len(items), agenthub.NewLocationRegistry(locations...)))
}

// handleRegisterPlace records that the hub may work in a directory.
//
// It answers 201 for a place that was already registered too. The registration's
// identity is the place the directory resolves to, so a retried request, a double
// click and a second operator all address the same resource; answering a conflict
// would report a problem where there is none.
func (s *Server) handleRegisterPlace(w http.ResponseWriter, r *http.Request) {
	var request placeRegistrationRequest
	if !s.decodeJSONBody(w, r, &request) {
		return
	}
	by := ""
	if principal, ok := PrincipalFrom(r.Context()); ok {
		by = principal.ID()
	}
	place, err := s.places.RegisterPlace(r.Context(), request.Directory, by)
	if err != nil {
		s.writeServiceProblem(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusCreated, modelPlace, placeFrom(place, true))
}

// handleSettings answers what the tool is configured to do, and where each answer
// came from. It is a read of configuration, not of work, so it carries no location
// registry: a setting resolved for one place arrives with the surface that addresses
// places.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	resolved, err := s.settings.Settings(r.Context())
	if err != nil {
		s.writeServiceProblem(w, r, err)
		return
	}
	items := make([]settingResource, 0, len(resolved))
	for _, value := range resolved {
		items = append(items, settingFrom(value))
	}
	s.writeJSON(w, r, http.StatusOK, modelSettingCollection, newCollection(items, len(items)))
}

func (s *Server) handlePrompts(w http.ResponseWriter, r *http.Request) {
	locationID, ok := s.promptLocationID(w, r)
	if !ok {
		return
	}
	catalogue, err := s.prompts.Catalogue(r.Context(), locationID)
	if err != nil {
		s.writePromptProblem(w, r, err)
		return
	}
	items := make([]promptResource, 0, len(catalogue))
	for _, configured := range catalogue {
		items = append(items, promptFrom(configured))
	}
	s.writeJSON(w, r, http.StatusOK, modelPromptCollection, newCollection(items, len(items)))
}

func (s *Server) handleSetPrompt(w http.ResponseWriter, r *http.Request) {
	locationID, ok := s.promptLocationID(w, r)
	if !ok {
		return
	}
	var request promptRequest
	if !s.decodeJSONBody(w, r, &request) {
		return
	}
	savedBy := ""
	if principal, authenticated := PrincipalFrom(r.Context()); authenticated {
		savedBy = principal.ID()
	}
	if _, err := s.prompts.Set(r.Context(), locationID, instruction.Key(r.PathValue("key")), request.Text, savedBy); err != nil {
		s.writePromptProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleResetPrompt(w http.ResponseWriter, r *http.Request) {
	locationID, ok := s.promptLocationID(w, r)
	if !ok {
		return
	}
	if err := s.prompts.Reset(r.Context(), locationID, instruction.Key(r.PathValue("key"))); err != nil {
		s.writePromptProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) promptLocationID(w http.ResponseWriter, r *http.Request) (string, bool) {
	query, ok := s.parseQuery(w, r)
	if !ok {
		return "", false
	}
	for key := range query {
		if key != "locationId" {
			s.writeProblem(w, r, codeInvalidRequest, "unknown query parameter: "+key)
			return "", false
		}
	}
	if len(query["locationId"]) > 1 {
		s.writeProblem(w, r, codeInvalidRequest, "locationId must be supplied once")
		return "", false
	}
	return query.Get("locationId"), true
}

func (s *Server) writePromptProblem(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, instruction.ErrInvalidText):
		s.writeProblem(w, r, codeInvalidPrompt, err.Error())
	case errors.Is(err, instruction.ErrUnknownKey), errors.Is(err, promptconfig.ErrPlaceNotFound):
		s.writeProblem(w, r, codeNotFound, "no such prompt configuration resource")
	case errors.Is(err, context.DeadlineExceeded):
		s.writeProblem(w, r, codeTimeout, "the request exceeded the server's time budget")
	case errors.Is(err, promptconfig.ErrUnavailable):
		s.logger.Error("prompt configuration is unavailable",
			"requestId", requestIDFrom(r.Context()), "path", r.URL.EscapedPath(), "error", err.Error())
		s.writeProblem(w, r, codeDependencyUnavailable, "prompt configuration could not be reached")
	default:
		s.logger.Error("an unclassified prompt configuration failure reached the transport",
			"requestId", requestIDFrom(r.Context()), "path", r.URL.EscapedPath(), "error", err.Error())
		s.writeProblem(w, r, codeInternal, "")
	}
}

// handleHealth reports whether the server can reach what it reads through. It probes
// on every request because the probes are single cheap reads, and a health answer
// that is not current is worse than none.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	document := healthDocument{Status: "pass", Version: Version, Checks: map[string]healthCheckResult{}}
	for _, check := range s.healthChecks {
		result := healthCheckResult{Status: "pass"}
		if err := check.Check(r.Context()); err != nil {
			result.Status = "fail"
			// The consumer is told which dependency failed, never how: the cause goes to
			// the log.
			s.logger.Warn("a health check failed",
				"requestId", requestIDFrom(r.Context()), "check", check.Name, "error", err.Error())
			document.Status = "fail"
		}
		document.Checks[check.Name] = result
	}
	status := http.StatusOK
	if document.Status == "fail" {
		status = http.StatusServiceUnavailable
	}
	body, err := marshalJSON(document)
	if err != nil {
		s.writeProblem(w, r, codeInternal, "")
		return
	}
	// Health is the one answer that must never be served from a cache.
	w.Header().Set("Content-Type", "application/health+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// healthDocument is the health resource's representation.
type healthDocument struct {
	// Status is "pass" when every check passed and "fail" otherwise.
	Status string `json:"status"`
	// Version is the API's major version.
	Version string `json:"version"`
	// Checks reports each dependency by name.
	Checks map[string]healthCheckResult `json:"checks"`
}

// healthCheckResult is one dependency's outcome.
type healthCheckResult struct {
	// Status is "pass" or "fail".
	Status string `json:"status"`
}

// writeServiceProblem maps a failure from the core onto its problem document. The
// mapping is exhaustive by intent: an error the core does not classify is a defect
// here, so it is logged and reported as one rather than guessed at.
func (s *Server) writeServiceProblem(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, agenthub.ErrNotFound):
		s.writeProblem(w, r, codeNotFound, "no such resource")
	case errors.Is(err, agenthub.ErrNotDismissible):
		s.writeProblem(w, r, codeNotDismissible,
			"only an item that has finished can be dismissed")
	case errors.Is(err, agenthub.ErrPlaceIsBusy):
		// The request is fine and will succeed once the work in that place settles, so
		// the refusal names what it collided with — in the sentence for a person, and
		// as an identity for the surface that offers to take them there.
		var busy agenthub.PlaceIsBusy
		errors.As(err, &busy)
		s.writeProblemAbout(w, r, codePlaceIsBusy, err.Error(), busy.RunID)
	case errors.Is(err, agenthub.ErrNoSuchDirectory), errors.Is(err, agenthub.ErrNotARepository):
		// The request is well formed; the machine it names does not hold what it says.
		// The detail names the mistake, because "no such directory" and "no repository
		// holds it" have different fixes, and the consumer sent the path it is told
		// about.
		s.writeProblem(w, r, codeNotAPlace, err.Error())
	case errors.Is(err, agenthub.ErrInvalid):
		s.writeProblem(w, r, codeInvalidRequest, err.Error())
	case errors.Is(err, agenthub.ErrInvalidLocation):
		// A location is built from a recorded fact, never from the request, so this is a
		// defect here rather than something a consumer can fix. The message names the
		// recorded place, so it goes to the log and not into the problem document.
		s.logger.Error("a recorded place could not be expressed as a location",
			"requestId", requestIDFrom(r.Context()), "path", r.URL.EscapedPath(), "error", err.Error())
		s.writeProblem(w, r, codeInternal, "")
	case errors.Is(err, context.DeadlineExceeded):
		s.writeProblem(w, r, codeTimeout, "the request exceeded the server's time budget")
	case errors.Is(err, agenthub.ErrUnavailable):
		// The cause names a dependency and may carry a driver's message, so it goes to
		// the log while the consumer is told what to do about it.
		s.logger.Error("a dependency of the read path is unavailable",
			"requestId", requestIDFrom(r.Context()), "path", r.URL.EscapedPath(), "error", err.Error())
		s.writeProblem(w, r, codeDependencyUnavailable,
			"a source this answer needs could not be reached")
	default:
		s.logger.Error("an unclassified failure reached the transport",
			"requestId", requestIDFrom(r.Context()), "path", r.URL.EscapedPath(), "error", err.Error())
		s.writeProblem(w, r, codeInternal, "")
	}
}

// marshalJSON encodes a payload without escaping HTML, so a prompt that contains
// "<" or "&" is transported as written rather than as escape sequences a consumer
// has to undo.
func marshalJSON(payload any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return nil, err
	}
	// Encode appends a newline; keep it, so a body written to a terminal or a file
	// ends in one.
	return buffer.Bytes(), nil
}

// newStrictJSONDecoder builds a decoder that refuses unknown fields, so a consumer
// that misspells one is told rather than silently ignored.
func newStrictJSONDecoder(body io.Reader) *json.Decoder {
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	return decoder
}

// validatedLimit resolves a limit through the core's contract, reporting a refusal as
// a boolean for the caller that has already written the problem.
func validatedLimit(value int) (int, bool) {
	limit, err := agenthub.ValidateLimit(value)
	return limit, err == nil
}

// validatedLimitOrError resolves a limit and returns the core's own explanation of a
// refusal, so the message a consumer reads is the contract's, not the transport's.
func validatedLimitOrError(value int) (int, error) {
	return agenthub.ValidateLimit(value)
}

// String makes a resource readable in a test failure.
func (r resource) String() string {
	methods := make([]string, 0, len(r.methods))
	for method := range r.methods {
		methods = append(methods, method)
	}
	sort.Strings(methods)
	return fmt.Sprintf("%s %s", strings.Join(methods, "|"), r.pattern)
}
