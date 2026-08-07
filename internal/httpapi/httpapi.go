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
	// The composition root requires it whenever the listener is not loopback.
	AuthToken string
	// WebDir, when set, is a directory of built static assets served outside the API's
	// path, for local convenience. The API itself never depends on it: the same bundle
	// can be served by anything else without the API changing.
	WebDir string
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
	allowedHosts    map[string]struct{}
	allowedOrigins  map[string]bool
	authToken       string
	webDir          string
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
		authToken:       options.AuthToken,
		webDir:          options.WebDir,
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
	perSecond, burst := options.RequestsPerSecond, options.Burst
	if perSecond == 0 {
		perSecond, burst = DefaultRequestsPerSecond, DefaultBurst
	}
	s.limiter = newLimiter(perSecond, burst)
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
		{pattern: s.basePath + "/runs", methods: map[string]http.HandlerFunc{http.MethodGet: s.handleRuns}},
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
	return list
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
	for _, item := range page.Items {
		items = append(items, activeWorkResource{
			ID: item.ID, Type: item.Type, Status: item.Status, Running: item.Running,
		})
	}
	s.writeJSON(w, r, http.StatusOK, modelActiveWorkCollection,
		newActiveWorkCollection(items, query.Limit, s.nextPage(r, page.Next)))
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
	for _, fleet := range fleets {
		items = append(items, fleetFrom(fleet, false))
	}
	s.writeJSON(w, r, http.StatusOK, modelFleetCollection, newCollection(items, limit))
}

// handleFleet answers one fleet, with its plan's graph.
func (s *Server) handleFleet(w http.ResponseWriter, r *http.Request) {
	fleet, err := s.view.Fleet(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeServiceProblem(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, modelFleet, fleetFrom(fleet, true))
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
	for _, run := range runs {
		items = append(items, runFrom(run))
	}
	s.writeJSON(w, r, http.StatusOK, modelRunCollection, newCollection(items, limit))
}

// handleRun answers one run chain.
func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.view.Run(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeServiceProblem(w, r, err)
		return
	}
	s.writeJSON(w, r, http.StatusOK, modelRun, runFrom(run))
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
	for _, schedule := range schedules {
		items = append(items, scheduleFrom(schedule))
	}
	s.writeJSON(w, r, http.StatusOK, modelScheduleCollection, newCollection(items, limit))
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
	case errors.Is(err, agenthub.ErrInvalid):
		s.writeProblem(w, r, codeInvalidRequest, err.Error())
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
