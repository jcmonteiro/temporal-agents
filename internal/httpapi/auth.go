package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"temporal-agents/internal/identity"
)

// The browser's half of authentication, as HTTP: four routes and two cookies.
//
// Everything that decides anything lives in the identity core — what binds a
// callback, when a session ends, who a principal is. This file is transport: it
// finds the credential a request carries, sets and clears cookies under the
// strictest rules a provider redirect still allows, and turns the core's refusals
// into problem documents. A rule stated here as well as in the core would be a rule
// with two homes and one of them wrong.

// SignIn is the driving port for the browser's side of authentication. It is
// declared here, at the consumer, in the core's vocabulary, so the transport cannot
// reach past the core into a provider or a store.
//
// *identity.Service implements it.
type SignIn interface {
	// BeginSignIn starts a sign-in for a browser that wants to end up at returnTo.
	BeginSignIn(ctx context.Context, returnTo string) (identity.SignIn, error)
	// CompleteSignIn finishes one, or refuses it.
	CompleteSignIn(ctx context.Context, callback identity.Callback) (identity.Grant, error)
	// SignOut ends a session immediately.
	SignOut(ctx context.Context, sessionToken string) error
}

// The cookies the browser holds. Both are script-inaccessible and same-site; neither
// carries anything but an opaque server-issued token.
const (
	// sessionCookieName carries the session token: the whole of what a signed-in
	// browser holds. No provider token, no identity, nothing a page can read.
	sessionCookieName = "agent_hub_session"
	// signInCookieName carries the pending sign-in's token while the provider is
	// being visited. It is what binds the callback to this browser.
	signInCookieName = "agent_hub_sign_in"
)

// signInCookiePath scopes the sign-in cookie to the routes that use it, so it is not
// attached to every read of the API.
func (s *Server) signInCookiePath() string { return s.basePath + "/auth" }

// The auth routes' own rate limit. It is separate from, and far tighter than, the
// API's: these are the only routes that accept a credential, so they are the only
// ones where a caller can profit from trying repeatedly. A human signs in a few
// times a day.
const (
	signInAttemptsPerSecond = 2
	signInAttemptBurst      = 10
)

// authenticate is the one place a request's credential is resolved.
//
// It asks the port a single question — is this request authenticated, and as whom —
// and never branches on the kind of credential: a session cookie and a configured
// token are the same question with different answers, and a third kind must be
// addable without this function changing.
//
// A server with no authenticator at all exists only where an operator asked for one
// explicitly (see New), and it is loud about it. There is no path by which a
// deployment ends up unauthenticated because nobody thought about it.
func (s *Server) authenticate(next http.Handler) http.Handler {
	if s.authenticator == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.isPublicRoute(r.URL.Path) {
			// The routes that hand out a credential cannot themselves require one.
			next.ServeHTTP(w, r)
			return
		}
		principal, err := s.authenticator.Authenticate(r.Context(), credentialFrom(r))
		if err != nil {
			s.refuseCredential(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), principal)))
	})
}

// isPublicRoute reports the whole of what an unauthenticated request may reach.
//
// It is three things, and each one has to be reachable for the door to be usable at
// all: the two routes by which a browser obtains a credential, and the health
// resource, which a monitor probes with no credential and which discloses only
// whether dependencies answer.
//
// Everything else — every read, the contract, the schemas, the problem catalogue,
// the service description — is behind the door. A specification is not a secret, but
// it is also not worth a second, separate rule about who may read what: one rule,
// applied to everything, cannot be got wrong resource by resource.
//
// The application bundle is not an API route and is not covered here (see
// rootHandler): the page that offers the sign-in has to load before anybody can sign
// in, and it carries no data of its own.
func (s *Server) isPublicRoute(path string) bool {
	switch path {
	case s.basePath + "/auth/sign-in", s.basePath + "/auth/callback", s.basePath + "/health":
		return true
	default:
		return false
	}
}

// refuseCredential answers a request whose credential was not accepted, or whose
// credential could not be checked at all. The two are different answers: one is the
// caller's problem, the other is the server's, and reporting an outage as
// "unauthenticated" would appear to sign every operator out of a working hub.
func (s *Server) refuseCredential(w http.ResponseWriter, r *http.Request, err error) {
	if !errors.Is(err, identity.ErrUnauthenticated) {
		s.logger.Error("a credential could not be checked",
			"requestId", requestIDFrom(r.Context()), "path", r.URL.EscapedPath(), "error", err.Error())
		s.writeProblem(w, r, codeDependencyUnavailable, "the credential could not be checked; try again")
		return
	}
	w.Header().Set("WWW-Authenticate", `Bearer realm="agent-hub"`)
	// The answer names where a browser signs in, so a frontend has one place to go
	// and does not have to know this API's route layout.
	w.Header().Add("Link", `<`+s.basePath+`/auth/sign-in>; rel="authenticate"`)
	s.writeProblem(w, r, codeAuthenticationRequired,
		"sign in at "+s.basePath+"/auth/sign-in, or send the configured bearer token")
}

// credentialFrom reads what a request presents. It reports what was there and
// decides nothing: which kind counts is the port's business.
func credentialFrom(r *http.Request) identity.Credential {
	credential := identity.Credential{Authorization: r.Header.Get("Authorization")}
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		credential.SessionToken = cookie.Value
	}
	return credential
}

// principalKey is the context key an authenticated principal travels under.
type principalKey struct{}

// withPrincipal carries the principal into the request's context, so a handler that
// creates a fact can attribute it without resolving the credential a second time.
func withPrincipal(ctx context.Context, principal identity.Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, principal)
}

// PrincipalFrom returns who a request was made by, and whether it was authenticated
// at all. It is exported because the handlers that will record attribution live
// beyond this package's own resources.
func PrincipalFrom(ctx context.Context) (identity.Principal, bool) {
	principal, ok := ctx.Value(principalKey{}).(identity.Principal)
	return principal, ok && principal.Valid()
}

// handleSignIn sends the browser to the provider.
//
// The requested destination is carried in the pending record rather than in the
// redirect, so nothing a consumer supplies is ever reflected into a Location header
// (see identity.SafeReturnPath for the narrowing the core applies to it).
func (s *Server) handleSignIn(w http.ResponseWriter, r *http.Request) {
	if !s.allowSignInAttempt(w, r) {
		return
	}
	query, ok := s.parseQuery(w, r)
	if !ok {
		return
	}
	started, err := s.signIn.BeginSignIn(r.Context(), query.Get("return"))
	if err != nil {
		s.writeSignInProblem(w, r, err)
		return
	}
	http.SetCookie(w, s.cookie(signInCookieName, started.RequestToken,
		s.signInCookiePath(), started.ExpiresAt.Sub(s.now())))
	// 303 rather than 302: the browser must follow with a GET whatever it sent here.
	http.Redirect(w, r, started.AuthorizationURL, http.StatusSeeOther)
}

// handleCallback completes a sign-in and hands the browser its session.
func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	if !s.allowSignInAttempt(w, r) {
		return
	}
	query, ok := s.parseQuery(w, r)
	if !ok {
		return
	}
	callback := identity.Callback{
		State: query.Get("state"),
		Code:  query.Get("code"),
		Error: query.Get("error"),
	}
	if cookie, err := r.Cookie(signInCookieName); err == nil {
		callback.RequestToken = cookie.Value
	}
	// The pending cookie has done its work whatever happens next, and a stale one
	// would only bind a later callback to a sign-in that is already finished.
	http.SetCookie(w, s.expiredCookie(signInCookieName, s.signInCookiePath()))

	grant, err := s.signIn.CompleteSignIn(r.Context(), callback)
	if err != nil {
		s.writeSignInProblem(w, r, err)
		return
	}
	http.SetCookie(w, s.cookie(sessionCookieName, grant.SessionToken, "/", grant.ExpiresAt.Sub(s.now())))
	s.logger.Info("a principal signed in",
		"requestId", requestIDFrom(r.Context()),
		"issuer", grant.Principal.Issuer,
		"subject", grant.Principal.Subject)
	http.Redirect(w, r, grant.ReturnTo, http.StatusSeeOther)
}

// handleSession answers "who am I": the identity behind the request, and nothing
// about the credential that carried it.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFrom(r.Context())
	if !ok {
		// This is reachable only when the deployment has no authenticator configured at
		// all, because the middleware refuses everything else before it gets here.
		s.writeProblem(w, r, codeAuthenticationRequired, "this request carries no identity")
		return
	}
	s.writeJSON(w, r, http.StatusOK, modelSession, sessionResource{
		Principal: principalResource{
			ID:      principal.ID(),
			Issuer:  principal.Issuer,
			Subject: principal.Subject,
			Name:    principal.DisplayName(),
			Email:   principal.Email,
		},
	})
}

// handleSignOut ends the session and clears the cookie.
//
// It answers the same way whether or not there was a session to end: a browser
// asking to be signed out is entitled to be signed out, and telling it that its
// cookie was already unknown discloses something it cannot act on.
func (s *Server) handleSignOut(w http.ResponseWriter, r *http.Request) {
	if !s.allowSignInAttempt(w, r) {
		return
	}
	token := ""
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		token = cookie.Value
	}
	if err := s.signIn.SignOut(r.Context(), token); err != nil {
		s.logger.Error("a session could not be ended",
			"requestId", requestIDFrom(r.Context()), "error", err.Error())
		s.writeProblem(w, r, codeDependencyUnavailable, "the session could not be ended; try again")
		return
	}
	http.SetCookie(w, s.expiredCookie(sessionCookieName, "/"))
	w.WriteHeader(http.StatusNoContent)
}

// writeSignInProblem maps the core's refusals onto problem documents. Which check
// failed is deliberately not disclosed: it is the one thing a caller probing the
// callback would most like to learn, and it is in the log for the operator.
func (s *Server) writeSignInProblem(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, identity.ErrSignInFailed):
		s.logger.Warn("a sign-in was refused",
			"requestId", requestIDFrom(r.Context()), "error", err.Error())
		s.writeProblem(w, r, codeSignInFailed, "start again at "+s.basePath+"/auth/sign-in")
	case errors.Is(err, identity.ErrProviderUnavailable):
		s.logger.Error("the identity provider could not be reached",
			"requestId", requestIDFrom(r.Context()), "error", err.Error())
		s.writeProblem(w, r, codeDependencyUnavailable,
			"the identity provider could not be reached")
	case errors.Is(err, context.DeadlineExceeded):
		s.writeProblem(w, r, codeTimeout, "the request exceeded the server's time budget")
	default:
		s.logger.Error("a sign-in failed unexpectedly",
			"requestId", requestIDFrom(r.Context()), "error", err.Error())
		s.writeProblem(w, r, codeInternal, "")
	}
}

// allowSignInAttempt bounds how often the credential-accepting routes may be tried.
//
// The provider is where a password can be guessed, but the callback and the session
// routes are still worth limiting: each one costs an exchange or a store round trip,
// and an unbounded caller could spend the server's budget guessing at tokens.
func (s *Server) allowSignInAttempt(w http.ResponseWriter, r *http.Request) bool {
	if s.signInLimiter != nil && !s.signInLimiter.Allow() {
		s.writeProblem(w, r, codeTooManyRequests, "too many sign-in attempts; wait and try again")
		return false
	}
	return true
}

// cookie builds one of this API's cookies.
//
// Every flag here is load-bearing. HttpOnly keeps a script — including one injected
// into the application — from reading a credential. SameSite=Lax is the weakest
// relaxation the provider's redirect back needs: the callback arrives as a top-level
// navigation, which Lax allows, while nothing cross-site can carry the cookie into a
// request the application did not make. Secure is set whenever the deployment serves
// TLS, so a cookie issued over HTTPS never travels in the clear.
func (s *Server) cookie(name, value, path string, maxAge time.Duration) *http.Cookie {
	seconds := int(maxAge.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		MaxAge:   seconds,
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
	}
}

// expiredCookie is how a cookie is removed: the same name and path, with an expiry
// in the past and an empty value.
func (s *Server) expiredCookie(name, path string) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
	}
}

// newSignInLimiter builds the auth routes' own limiter. It is nil when the server's
// rate limiting is switched off, which only a test does.
func newSignInLimiter(apiLimiter *rate.Limiter) *rate.Limiter {
	if apiLimiter == nil {
		return nil
	}
	return rate.NewLimiter(signInAttemptsPerSecond, signInAttemptBurst)
}

// authRoutes declares the four routes the browser's sign-in needs. They live under
// the API's own base path so one origin, one cookie scope and one host allowlist
// cover the application and its authentication alike.
func (s *Server) authRoutes() []resource {
	if s.signIn == nil {
		// A deployment with no sign-in service publishes no sign-in routes: an endpoint
		// that exists only to answer "not configured" is a surface with no purpose.
		return nil
	}
	return []resource{
		{pattern: s.basePath + "/auth/sign-in", methods: map[string]http.HandlerFunc{
			http.MethodGet: s.handleSignIn,
		}},
		{pattern: s.basePath + "/auth/callback", methods: map[string]http.HandlerFunc{
			http.MethodGet: s.handleCallback,
		}},
		{pattern: s.basePath + "/auth/session", methods: map[string]http.HandlerFunc{
			http.MethodGet: s.handleSession,
			// Signing out is a DELETE rather than a POST of a form: a non-simple method
			// cannot be sent cross-origin without a preflight, so no other page can sign
			// this browser out.
			http.MethodDelete: s.handleSignOut,
		}},
	}
}

// sessionResource is what "who am I" answers with.
type sessionResource struct {
	// Principal is who the request was made by.
	Principal principalResource `json:"principal"`
}

// principalResource is a principal on the wire. It carries identity and display
// fields, and no authority of any kind: there are no roles in this hub.
type principalResource struct {
	// ID is the principal's stable identity across issuer and subject, which is what
	// an attributed fact records.
	ID string `json:"id"`
	// Issuer is who vouches for the subject.
	Issuer string `json:"issuer"`
	// Subject is the identifier the issuer uses, stable across sign-ins.
	Subject string `json:"subject"`
	// Name is the best name available to show a human. It is never empty: it falls
	// back to the address and then to the subject.
	Name string `json:"name"`
	// Email is the address the provider disclosed, or empty when it disclosed none.
	Email string `json:"email,omitempty"`
}

// newAuthenticator builds the port the transport asks, out of whatever credentials
// the deployment configured. It returns nil when none were, which is the remaining
// unauthenticated mode.
//
// The chain is built here rather than in each composition root because the
// static token needs no ports at all: it is a configured string, and asking every
// caller to assemble it would spread one rule over several places.
func newAuthenticator(options Options) (identity.Authenticator, error) {
	var chain identity.Chain
	if token := strings.TrimSpace(options.AuthToken); token != "" {
		static, err := identity.NewStaticToken(token)
		if err != nil {
			return nil, err
		}
		chain = append(chain, static)
	}
	if options.Authenticator != nil {
		chain = append(chain, options.Authenticator)
	}
	if len(chain) == 0 {
		return nil, nil
	}
	return chain, nil
}

// requireSameSite refuses a mutation that a page on another site caused the browser
// to make.
//
// Loopback binding is not a defence here: any page an operator visits can send a
// request to 127.0.0.1, and once this hub can start agent work, a cross-site request
// that mutates is somebody else's page starting work on the operator's machine. The
// cookie's SameSite attribute already blocks most of it; this is the second, explicit
// rule, because a defence that depends only on a browser honouring a cookie
// attribute is one browser bug away from nothing.
//
// A request that declares nothing — a script, the CLI, curl — is allowed through:
// those carry no ambient credential a third party could borrow, and refusing them
// would break automation to protect it from an attack it cannot suffer.
func (s *Server) requireSameSite(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !mutates(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		switch strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))) {
		case "", "same-origin", "none":
			// "none" is a user-initiated navigation, "same-origin" is this application,
			// and an absent header is a client that is not a browser.
			next.ServeHTTP(w, r)
		default:
			s.writeProblem(w, r, codeCrossSiteRequest,
				"a change must be requested by this application, from this site")
		}
	})
}

// mutates reports whether a method changes something.
func mutates(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
