package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"temporal-agents/internal/identity"
	"temporal-agents/internal/identity/identitytest"
)

// The transport's authentication tests run against the real identity core over an
// in-memory store, with only the provider replaced. That is deliberate: the
// behaviour worth pinning here spans both — a browser signs in, holds a cookie,
// reads the API, signs out and is refused — and a stubbed core would let the
// transport pass while binding, expiry or revocation were never exercised at all.

// stubProvider stands in for an identity provider that has verified everything it
// is required to verify. It is the only thing replaced, because a real provider
// needs a container and the container suite beside the adapter already runs one.
type stubProvider struct {
	principal identity.Principal
	// refuse, when set, is what the exchange reports instead of an identity.
	refuse error
}

func (p *stubProvider) AuthorizationURL(_ context.Context, request identity.AuthorizationRequest) (string, error) {
	return "https://issuer.test/authorize?" + url.Values{
		"state":        {request.State},
		"redirect_uri": {request.RedirectURI},
	}.Encode(), nil
}

func (p *stubProvider) Exchange(context.Context, identity.ExchangeRequest) (identity.Identity, error) {
	if p.refuse != nil {
		return identity.Identity{}, p.refuse
	}
	return identity.Identity{Principal: p.principal, Tokens: identity.Tokens{Access: "access-1"}}, nil
}

func (p *stubProvider) Refresh(context.Context, string) (identity.Tokens, error) {
	return identity.Tokens{}, identity.ErrUnauthenticated
}

func (p *stubProvider) Issuer() string { return "https://issuer.test" }

func (p *stubProvider) SignOutURL(context.Context, string) string { return "" }

// theOperator is who the stub provider vouches for.
var theOperator = identity.Principal{
	Issuer:  "https://issuer.test",
	Subject: "operator-1",
	Name:    "The Operator",
	Email:   "operator@example.test",
}

// newSignInService wires the real core over the in-memory store.
func newSignInService(t *testing.T, provider identity.Provider) (*identity.Service, *identitytest.Store) {
	t.Helper()
	store := identitytest.NewStore()
	service, err := identity.NewService(identity.Dependencies{
		Provider:       provider,
		Sessions:       store,
		Principals:     store,
		PendingSignIns: store,
		RedirectURI:    "http://localhost:8973" + BasePath + "/auth/callback",
		Now:            func() time.Time { return fixedNow },
	})
	if err != nil {
		t.Fatalf("wire the identity service: %v", err)
	}
	return service, store
}

// newAuthenticatedServer builds a server that requires a credential, signing in
// against the stub provider.
func newAuthenticatedServer(t *testing.T, provider identity.Provider, mutate ...func(*Options)) (*Server, *identity.Service) {
	t.Helper()
	service, _ := newSignInService(t, provider)
	options := append([]func(*Options){func(options *Options) {
		options.SignIn = service
		options.Authenticator = service
	}}, mutate...)
	return newTestServer(t, &viewStub{}, options...), service
}

// signInThroughTheBrowser plays the browser through a whole sign-in and returns the
// session cookie it ends up holding.
func signInThroughTheBrowser(t *testing.T, server *Server) *http.Cookie {
	t.Helper()
	started := request(t, server, http.MethodGet, BasePath+"/auth/sign-in?return=%2Fplaces%2Fone", nil)
	if started.Code != http.StatusSeeOther {
		t.Fatalf("sign-in status = %d, want 303", started.Code)
	}
	pending := cookieNamed(t, started.Result().Cookies(), signInCookieName)

	state := queryOf(t, started.Header().Get("Location")).Get("state")
	callback := newRequest(http.MethodGet,
		BasePath+"/auth/callback?code=the-code&state="+url.QueryEscape(state), nil)
	callback.AddCookie(pending)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, callback)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("callback status = %d, want 303 (body %s)", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Location"); got != "/places/one" {
		t.Fatalf("returned to %q, want the path the browser asked for", got)
	}
	return cookieNamed(t, response.Result().Cookies(), sessionCookieName)
}

// TestABrowserSignsInAndThenReadsTheAPI is the slice's demo at the transport: the
// redirect, the callback, the session cookie, and a read that now succeeds.
func TestABrowserSignsInAndThenReadsTheAPI(t *testing.T) {
	server, _ := newAuthenticatedServer(t, &stubProvider{principal: theOperator})

	refused := request(t, server, http.MethodGet, BasePath+"/runs", nil)
	requireProblem(t, refused, http.StatusUnauthorized, codeAuthenticationRequired)
	if !strings.Contains(refused.Header().Get("Link"), `rel="authenticate"`) {
		t.Errorf("Link = %q, want the sign-in endpoint", refused.Header().Get("Link"))
	}

	session := signInThroughTheBrowser(t, server)

	read := newRequest(http.MethodGet, BasePath+"/runs", nil)
	read.AddCookie(session)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, read)
	if response.Code != http.StatusOK {
		t.Fatalf("read with a session = %d, want 200", response.Code)
	}
}

// TestTheBrowserHoldsNothingButAnOpaqueSessionCookie pins the topology this whole
// feature exists for: no token, no identity, nothing a script can read.
func TestTheBrowserHoldsNothingButAnOpaqueSessionCookie(t *testing.T) {
	server, _ := newAuthenticatedServer(t, &stubProvider{principal: theOperator})

	session := signInThroughTheBrowser(t, server)

	if !session.HttpOnly {
		t.Error("the session cookie is readable by scripts")
	}
	if session.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax: the weakest relaxation the provider redirect needs", session.SameSite)
	}
	if session.Path != "/" {
		t.Errorf("path = %q, want the whole application", session.Path)
	}
	if session.Secure {
		t.Error("a cookie served without TLS must not claim to be secure")
	}
	for _, forbidden := range []string{theOperator.Subject, theOperator.Email, "access-1"} {
		if strings.Contains(session.Value, forbidden) {
			t.Errorf("the session cookie discloses %q", forbidden)
		}
	}
}

// TestCookiesAreSecureWhenTheDeploymentServesTLS pins that a credential issued over
// TLS never travels in the clear afterwards.
func TestCookiesAreSecureWhenTheDeploymentServesTLS(t *testing.T) {
	server, _ := newAuthenticatedServer(t, &stubProvider{principal: theOperator},
		func(options *Options) { options.SecureCookies = true })

	session := signInThroughTheBrowser(t, server)

	if !session.Secure {
		t.Error("the session cookie is not marked Secure")
	}
}

// TestACallbackThatDoesNotBelongToThisBrowserIsRefused pins request binding at the
// transport: without the sign-in cookie, a code buys nothing.
func TestACallbackThatDoesNotBelongToThisBrowserIsRefused(t *testing.T) {
	server, _ := newAuthenticatedServer(t, &stubProvider{principal: theOperator})
	started := request(t, server, http.MethodGet, BasePath+"/auth/sign-in", nil)
	state := queryOf(t, started.Header().Get("Location")).Get("state")

	response := request(t, server, http.MethodGet,
		BasePath+"/auth/callback?code=the-code&state="+url.QueryEscape(state), nil)

	requireProblem(t, response, http.StatusBadRequest, codeSignInFailed)
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == sessionCookieName && cookie.Value != "" {
			t.Fatal("a refused callback handed out a session")
		}
	}
}

// TestAProviderThatRefusesIsReportedWithoutSayingWhy pins that the callback does not
// explain which check failed: that is what a caller probing it would want to learn.
func TestAProviderThatRefusesIsReportedWithoutSayingWhy(t *testing.T) {
	provider := &stubProvider{principal: theOperator}
	server, _ := newAuthenticatedServer(t, provider)
	started := request(t, server, http.MethodGet, BasePath+"/auth/sign-in", nil)
	pending := cookieNamed(t, started.Result().Cookies(), signInCookieName)
	state := queryOf(t, started.Header().Get("Location")).Get("state")
	provider.refuse = errors.New("the id token's audience is not this client")

	callback := newRequest(http.MethodGet,
		BasePath+"/auth/callback?code=the-code&state="+url.QueryEscape(state), nil)
	callback.AddCookie(pending)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, callback)

	requireProblem(t, response, http.StatusBadRequest, codeSignInFailed)
	if strings.Contains(response.Body.String(), "audience") {
		t.Error("the refusal discloses which check failed")
	}
}

// TestSigningOutStopsTheBrowserOnTheNextRequest is the revocation guarantee, as a
// browser experiences it.
func TestSigningOutStopsTheBrowserOnTheNextRequest(t *testing.T) {
	server, _ := newAuthenticatedServer(t, &stubProvider{principal: theOperator})
	session := signInThroughTheBrowser(t, server)

	signOut := newRequest(http.MethodDelete, BasePath+"/auth/session", nil)
	signOut.AddCookie(session)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, signOut)
	if response.Code != http.StatusNoContent {
		t.Fatalf("sign-out status = %d, want 204", response.Code)
	}
	cleared := cookieNamed(t, response.Result().Cookies(), sessionCookieName)
	if cleared.Value != "" || cleared.MaxAge >= 0 {
		t.Errorf("the session cookie was not cleared: %+v", cleared)
	}

	read := newRequest(http.MethodGet, BasePath+"/runs", nil)
	read.AddCookie(session)
	after := httptest.NewRecorder()
	server.ServeHTTP(after, read)
	requireProblem(t, after, http.StatusUnauthorized, codeAuthenticationRequired)
}

// TestWhoAmIReportsTheIdentityAndNeverTheCredential pins what the frontend reads to
// show who is signed in.
func TestWhoAmIReportsTheIdentityAndNeverTheCredential(t *testing.T) {
	server, _ := newAuthenticatedServer(t, &stubProvider{principal: theOperator})
	session := signInThroughTheBrowser(t, server)

	read := newRequest(http.MethodGet, BasePath+"/auth/session", nil)
	read.AddCookie(session)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, read)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	var document struct {
		Principal struct {
			ID      string `json:"id"`
			Issuer  string `json:"issuer"`
			Subject string `json:"subject"`
			Name    string `json:"name"`
			Email   string `json:"email"`
		} `json:"principal"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if document.Principal.Subject != theOperator.Subject || document.Principal.Name != theOperator.Name {
		t.Errorf("principal = %+v, want the operator", document.Principal)
	}
	if strings.Contains(response.Body.String(), session.Value) {
		t.Error("the answer echoes the credential back")
	}
}

// TestAScriptAndABrowserReachTheSameAPIThroughOnePort pins that the transport asks
// one question: both credential kinds work, and neither is special-cased.
func TestAScriptAndABrowserReachTheSameAPIThroughOnePort(t *testing.T) {
	server, _ := newAuthenticatedServer(t, &stubProvider{principal: theOperator},
		func(options *Options) { options.AuthToken = "correct horse battery staple" })

	withToken := newRequest(http.MethodGet, BasePath+"/runs", nil)
	withToken.Header.Set("Authorization", "Bearer correct horse battery staple")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, withToken)
	if response.Code != http.StatusOK {
		t.Fatalf("the CLI's token = %d, want 200", response.Code)
	}

	session := signInThroughTheBrowser(t, server)
	withSession := newRequest(http.MethodGet, BasePath+"/runs", nil)
	withSession.AddCookie(session)
	browser := httptest.NewRecorder()
	server.ServeHTTP(browser, withSession)
	if browser.Code != http.StatusOK {
		t.Fatalf("the browser's session = %d, want 200", browser.Code)
	}
}

// TestACredentialThatCannotBeCheckedIsNotARefusal pins that an outage reads as an
// outage: answering 401 would appear to sign every operator out of a working hub,
// and a frontend would send them all to a sign-in page they do not need.
func TestACredentialThatCannotBeCheckedIsNotARefusal(t *testing.T) {
	server := newTestServer(t, &viewStub{}, func(options *Options) {
		options.Authenticator = authenticatorFunc(func(context.Context, identity.Credential) (identity.Principal, error) {
			return identity.Principal{}, errors.New("the session store is unreachable")
		})
	})

	read := newRequest(http.MethodGet, BasePath+"/runs", nil)
	read.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "a-session"})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, read)

	requireProblem(t, response, http.StatusServiceUnavailable, codeDependencyUnavailable)
}

// TestTheRoutesThatHandOutACredentialDoNotRequireOne pins the obvious circularity: a
// browser cannot be asked to sign in before it may reach the sign-in route.
func TestTheRoutesThatHandOutACredentialDoNotRequireOne(t *testing.T) {
	server, _ := newAuthenticatedServer(t, &stubProvider{principal: theOperator})

	for _, target := range []string{BasePath + "/auth/sign-in", BasePath + "/auth/callback?error=access_denied"} {
		response := request(t, server, http.MethodGet, target, nil)
		if response.Code == http.StatusUnauthorized {
			t.Errorf("%s answered 401; it is how a credential is obtained", target)
		}
	}
}

// TestSignInAttemptsAreBounded pins the attempt limit on the only routes where
// trying repeatedly could pay.
func TestSignInAttemptsAreBounded(t *testing.T) {
	server, _ := newAuthenticatedServer(t, &stubProvider{principal: theOperator},
		func(options *Options) { options.RequestsPerSecond = 0 })

	limited := false
	for range signInAttemptBurst + 5 {
		response := request(t, server, http.MethodGet, BasePath+"/auth/callback?code=x&state=y", nil)
		if response.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("the callback accepted an unbounded number of attempts")
	}
}

// TestADeploymentWithNoProviderPublishesNoSignInRoutes pins that an endpoint exists
// only where it means something: a hub with no provider offers no sign-in.
func TestADeploymentWithNoProviderPublishesNoSignInRoutes(t *testing.T) {
	server := newTestServer(t, &viewStub{}, func(options *Options) { options.SignIn = nil })

	response := request(t, server, http.MethodGet, BasePath+"/auth/sign-in", nil)

	requireProblem(t, response, http.StatusNotFound, codeNotFound)
}

// cookieNamed finds one cookie in a response, failing the test when it is absent.
func cookieNamed(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("no %s cookie was set", name)
	return nil
}

// queryOf reads the query of a redirect's Location.
func queryOf(t *testing.T, location string) url.Values {
	t.Helper()
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatalf("Location %q is not a URL: %v", location, err)
	}
	return parsed.Query()
}

// authenticatorFunc adapts a function to the port, for the test that needs an
// authenticator which only ever fails.
type authenticatorFunc func(context.Context, identity.Credential) (identity.Principal, error)

func (f authenticatorFunc) Authenticate(ctx context.Context, credential identity.Credential) (identity.Principal, error) {
	return f(ctx, credential)
}

// TestTheSessionResourceKeepsTheFieldNamesTheWebClientReads pins the wire names the
// frontend depends on, for the same reason the overview's are pinned:
// web/src/clients/session.ts is a hand-written copy of this resource, so a rename
// here would silently leave the hub showing nobody as signed in, and the frontend's
// own tests — written from the same assumption — could not see the drift.
func TestTheSessionResourceKeepsTheFieldNamesTheWebClientReads(t *testing.T) {
	server, _ := newAuthenticatedServer(t, &stubProvider{principal: theOperator})
	session := signInThroughTheBrowser(t, server)

	read := newRequest(http.MethodGet, BasePath+"/auth/session", nil)
	read.AddCookie(session)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, read)

	var document map[string]any
	decodeResponse(t, response, &document)
	principal, ok := document["principal"].(map[string]any)
	if !ok {
		t.Fatalf("the session document has no principal: %v", sortedKeys(document))
	}
	for _, key := range []string{"id", "issuer", "subject", "name", "email"} {
		if _, present := principal[key]; !present {
			t.Errorf("the principal has no %q; the web client reads %v", key, sortedKeys(principal))
		}
	}
}

// TestAServerThatNeitherAuthenticatesNorWasAskedNotToDoesNotBuild pins that an open
// surface is always a decision. A missing token, an unconfigured provider, a
// half-finished deployment: every one of them stops here rather than serving the
// operator's work to whatever can reach the port.
func TestAServerThatNeitherAuthenticatesNorWasAskedNotToDoesNotBuild(t *testing.T) {
	_, err := New(&viewStub{}, Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})

	if err == nil {
		t.Fatal("a server with no credential and no explicit opt-out was built")
	}
}

// TestEveryRouteButTheDoorAndTheHealthProbeNeedsACredential is the closed door,
// asserted as a matrix rather than as a sample: a route added later is refused by
// default, and one deliberately left open has to be named here.
func TestEveryRouteButTheDoorAndTheHealthProbeNeedsACredential(t *testing.T) {
	server, _ := newAuthenticatedServer(t, &stubProvider{principal: theOperator})

	open := map[string]bool{
		BasePath + "/auth/sign-in":  true,
		BasePath + "/auth/callback": true,
		BasePath + "/health":        true,
	}
	for _, res := range server.resources() {
		if strings.Contains(res.pattern, "{") || !strings.HasPrefix(res.pattern, BasePath) {
			// A wildcard pattern is exercised through a concrete path below, and a path
			// outside the API carries no data (see isPublicRoute).
			continue
		}
		for method := range res.methods {
			t.Run(method+" "+res.pattern, func(t *testing.T) {
				response := request(t, server, method, res.pattern, nil)
				refused := response.Code == http.StatusUnauthorized
				if refused == open[res.pattern] {
					t.Fatalf("status = %d, want %s", response.Code,
						map[bool]string{true: "401", false: "anything but 401"}[!open[res.pattern]])
				}
			})
		}
	}
	for _, path := range []string{
		BasePath + "/fleets/fleet-1",
		BasePath + "/runs/run-1",
		BasePath + "/schemas/run.v1",
		BasePath + "/problems/not-found",
	} {
		t.Run("GET "+path, func(t *testing.T) {
			response := request(t, server, http.MethodGet, path, nil)
			requireProblem(t, response, http.StatusUnauthorized, codeAuthenticationRequired)
		})
	}
}

// TestAnotherSitesPageCannotChangeAnything pins the rule loopback binding cannot
// provide: a page the operator happens to be visiting must not be able to start,
// stop or hide anything here, even though it can reach the port.
func TestAnotherSitesPageCannotChangeAnything(t *testing.T) {
	server, _ := newAuthenticatedServer(t, &stubProvider{principal: theOperator})
	session := signInThroughTheBrowser(t, server)

	for site, refused := range map[string]bool{
		"cross-site":  true,
		"same-site":   true,
		"same-origin": false,
		"none":        false,
		"":            false,
	} {
		t.Run("Sec-Fetch-Site: "+site, func(t *testing.T) {
			mutation := newRequest(http.MethodDelete, BasePath+"/dismissals/run:run-1", nil)
			mutation.AddCookie(session)
			if site != "" {
				mutation.Header.Set("Sec-Fetch-Site", site)
			}
			response := httptest.NewRecorder()
			server.ServeHTTP(response, mutation)

			if refused {
				requireProblem(t, response, http.StatusForbidden, codeCrossSiteRequest)
				return
			}
			if response.Code == http.StatusForbidden {
				t.Fatalf("a request from %q was refused as cross-site", site)
			}
		})
	}
}

// TestOnlyAConfiguredSameSiteSiblingCanChangeAnything pins every part of the
// exception: the request must name an Origin, that exact Origin must be configured,
// and the browser must classify it as same-site rather than cross-site.
func TestOnlyAConfiguredSameSiteSiblingCanChangeAnything(t *testing.T) {
	const allowedOrigin = "http://127.0.0.1:3001"
	server, _ := newAuthenticatedServer(t, &stubProvider{principal: theOperator},
		func(options *Options) { options.AllowedOrigins = []string{allowedOrigin} })
	session := signInThroughTheBrowser(t, server)

	cases := map[string]struct {
		origin string
		site   string
	}{
		"same-site without Origin":  {site: "same-site"},
		"unlisted same-site Origin": {origin: "http://127.0.0.1:3002", site: "same-site"},
		"listed cross-site Origin":  {origin: allowedOrigin, site: "cross-site"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			mutation := newRequest(http.MethodDelete, BasePath+"/dismissals/run:run-1", nil)
			mutation.AddCookie(session)
			mutation.Header.Set("Origin", testCase.origin)
			mutation.Header.Set("Sec-Fetch-Site", testCase.site)
			response := httptest.NewRecorder()

			server.ServeHTTP(response, mutation)

			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", response.Code)
			}
		})
	}
}

// TestReadingIsUnaffectedByTheCrossSiteRule pins the other half: the rule guards
// changes, and does not turn an ordinary read into a browser-only endpoint.
func TestReadingIsUnaffectedByTheCrossSiteRule(t *testing.T) {
	server, _ := newAuthenticatedServer(t, &stubProvider{principal: theOperator})
	session := signInThroughTheBrowser(t, server)

	read := newRequest(http.MethodGet, BasePath+"/runs", nil)
	read.AddCookie(session)
	read.Header.Set("Sec-Fetch-Site", "cross-site")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, read)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
}

// TestASignedOutBrowserCanStillLoadThePageThatOffersTheSignIn pins the one thing the
// closed door must not close: the application itself. A bundle behind the credential
// it exists to obtain is a hub nobody can ever sign in to.
func TestASignedOutBrowserCanStillLoadThePageThatOffersTheSignIn(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "index.html"),
		[]byte("<!doctype html><title>Agent Hub</title>"), 0o600); err != nil {
		t.Fatalf("write the bundle: %v", err)
	}
	server, _ := newAuthenticatedServer(t, &stubProvider{principal: theOperator},
		func(options *Options) { options.WebDir = directory })

	for _, path := range []string{"/", "/places/dir-1"} {
		response := request(t, server, http.MethodGet, path, nil)
		if response.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want the application", path, response.Code)
		}
	}

	// The data behind it stays closed.
	requireProblem(t, request(t, server, http.MethodGet, BasePath+"/runs", nil),
		http.StatusUnauthorized, codeAuthenticationRequired)
}
