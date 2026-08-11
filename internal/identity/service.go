package identity

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"
)

// Service is the authentication core: it starts a sign-in, completes one, resolves
// a session credential to a principal, and ends a session.
//
// It is also an Authenticator, which is how the session credential reaches the
// transport through the same port as the static token.

// Defaults for how long the two kinds of record live. A session outlives a working
// day, so an operator is not signed out mid-task; a pending sign-in lives only as
// long as visiting a provider plausibly takes, because an unused one is an
// unfinished exchange nobody is waiting for.
const (
	DefaultSessionLifetime = 12 * time.Hour
	DefaultSignInLifetime  = 10 * time.Minute
)

// Dependencies are the ports a Service is built on, supplied by the composition
// root.
type Dependencies struct {
	// Provider is the identity provider the hub signs in against.
	Provider Provider
	// Sessions keeps the server-side session records.
	Sessions SessionStore
	// Principals records who has signed in.
	Principals PrincipalStore
	// PendingSignIns keeps the sign-ins in flight.
	PendingSignIns PendingSignInStore
	// RedirectURI is this deployment's own callback, as registered with the
	// provider. It travels in both exchanges and must be identical in each.
	RedirectURI string
	// AllowedReturnOrigins are the exact frontend origins an absolute callback
	// destination may name. Relative application paths need no entry.
	AllowedReturnOrigins []string
	// SessionLifetime and SignInLifetime override the defaults above.
	SessionLifetime time.Duration
	SignInLifetime  time.Duration
	// NewToken mints a browser-held secret. It defaults to the package's own
	// generator and exists so a test can make a sign-in reproducible.
	NewToken func() (string, error)
	// Now supplies the current time, defaulting to time.Now.
	Now func() time.Time
}

// Service implements the sign-in sequence over the ports.
type Service struct {
	provider             Provider
	sessions             SessionStore
	principals           PrincipalStore
	pending              PendingSignInStore
	redirectURI          string
	allowedReturnOrigins []string
	sessionLifetime      time.Duration
	signInLifetime       time.Duration
	newToken             func() (string, error)
	now                  func() time.Time
}

// Compile-time proof the service is usable through the transport's one question.
var _ Authenticator = (*Service)(nil)

// NewService builds the service, refusing anything it cannot work without. A hub
// that starts with half an authentication core would answer requests it cannot
// authenticate.
func NewService(dependencies Dependencies) (*Service, error) {
	switch {
	case dependencies.Provider == nil:
		return nil, errors.New("an identity provider is required")
	case dependencies.Sessions == nil:
		return nil, errors.New("a session store is required")
	case dependencies.Principals == nil:
		return nil, errors.New("a principal store is required")
	case dependencies.PendingSignIns == nil:
		return nil, errors.New("a pending sign-in store is required")
	case dependencies.RedirectURI == "":
		return nil, errors.New("the redirect URI is required")
	}
	allowedReturnOrigins := make([]string, 0, len(dependencies.AllowedReturnOrigins))
	for _, configured := range dependencies.AllowedReturnOrigins {
		origin, err := ParseBrowserOrigin(configured)
		if err != nil {
			return nil, err
		}
		allowedReturnOrigins = append(allowedReturnOrigins, origin)
	}
	service := &Service{
		provider:             dependencies.Provider,
		sessions:             dependencies.Sessions,
		principals:           dependencies.Principals,
		pending:              dependencies.PendingSignIns,
		redirectURI:          dependencies.RedirectURI,
		allowedReturnOrigins: allowedReturnOrigins,
		sessionLifetime:      dependencies.SessionLifetime,
		signInLifetime:       dependencies.SignInLifetime,
		newToken:             dependencies.NewToken,
		now:                  dependencies.Now,
	}
	if service.sessionLifetime <= 0 {
		service.sessionLifetime = DefaultSessionLifetime
	}
	if service.signInLifetime <= 0 {
		service.signInLifetime = DefaultSignInLifetime
	}
	if service.newToken == nil {
		service.newToken = NewToken
	}
	if service.now == nil {
		service.now = time.Now
	}
	return service, nil
}

// SignIn is what a started sign-in gives the transport: where to send the browser,
// and the secret that binds the callback back to this very request.
type SignIn struct {
	// AuthorizationURL is where the browser goes next.
	AuthorizationURL string
	// RequestToken is the value the browser must present at the callback. The
	// transport carries it in a short-lived cookie; nothing else may read it.
	RequestToken string
	// ExpiresAt is when the sign-in stops being completable.
	ExpiresAt time.Time
}

// BeginSignIn starts a sign-in for a browser that wants to end up at returnTo.
//
// Every value that binds the exchange is minted here and kept here: the browser
// carries one opaque token and learns nothing about the state, the nonce or the
// verifier, so a page that can read a redirect URL still cannot complete a sign-in.
func (s *Service) BeginSignIn(ctx context.Context, returnTo string) (SignIn, error) {
	requestToken, state, nonce, verifier, err := s.mintSignInSecrets()
	if err != nil {
		return SignIn{}, err
	}
	target, err := NewReturnTarget(returnTo, s.allowedReturnOrigins)
	if err != nil {
		target = DefaultReturnTarget()
	}
	now := s.now()
	pending := PendingSignIn{
		TokenHash:    HashToken(requestToken),
		State:        state,
		Nonce:        nonce,
		CodeVerifier: verifier,
		ReturnTo:     target.String(),
		StartedAt:    now,
		ExpiresAt:    now.Add(s.signInLifetime),
	}
	url, err := s.provider.AuthorizationURL(ctx, AuthorizationRequest{
		State:        state,
		Nonce:        nonce,
		CodeVerifier: verifier,
		RedirectURI:  s.redirectURI,
	})
	if err != nil {
		return SignIn{}, fmt.Errorf("%w: %w", ErrProviderUnavailable, err)
	}
	// The record is written before the browser leaves, so a callback can never
	// arrive before the sign-in it belongs to exists.
	if err := s.pending.StartSignIn(ctx, pending); err != nil {
		return SignIn{}, err
	}
	return SignIn{AuthorizationURL: url, RequestToken: requestToken, ExpiresAt: pending.ExpiresAt}, nil
}

// mintSignInSecrets generates the four independent secrets one sign-in needs.
func (s *Service) mintSignInSecrets() (requestToken, state, nonce, verifier string, err error) {
	for _, target := range []*string{&requestToken, &state, &nonce, &verifier} {
		value, tokenErr := s.newToken()
		if tokenErr != nil {
			return "", "", "", "", fmt.Errorf("mint a sign-in secret: %w", tokenErr)
		}
		*target = value
	}
	return requestToken, state, nonce, verifier, nil
}

// Callback is a provider's answer, as the transport received it.
type Callback struct {
	// RequestToken is what the browser presented from the sign-in cookie.
	RequestToken string
	// State is what the provider echoed back.
	State string
	// Code is the authorization code, when the provider issued one.
	Code string
	// Error is what the provider reported instead of a code, when it refused.
	Error string
}

// Grant is a completed sign-in: the session the browser is given, and where it
// wanted to go.
type Grant struct {
	// SessionToken is the value the browser holds in its session cookie.
	SessionToken string
	// Principal is who signed in.
	Principal Principal
	// ExpiresAt is when the session ends by itself.
	ExpiresAt time.Time
	// ReturnTo is the validated application path or allowlisted frontend URL to
	// send the browser to.
	ReturnTo string
}

// CompleteSignIn finishes a sign-in, or refuses it.
//
// The order is the point. The pending record is taken (and thereby consumed) before
// anything is sent to the provider, so a replayed callback costs one failed lookup
// and no exchange at all; the state is compared in constant time; and the provider
// is only asked once the callback has proved it belongs to a sign-in this server
// started for this browser.
func (s *Service) CompleteSignIn(ctx context.Context, callback Callback) (Grant, error) {
	if callback.RequestToken == "" || callback.State == "" {
		return Grant{}, ErrSignInFailed
	}
	pending, err := s.pending.TakePendingSignIn(ctx, HashToken(callback.RequestToken))
	if err != nil {
		if errors.Is(err, ErrNoPendingSignIn) {
			return Grant{}, ErrSignInFailed
		}
		return Grant{}, err
	}
	if pending.Expired(s.now()) {
		return Grant{}, ErrSignInFailed
	}
	if subtle.ConstantTimeCompare([]byte(pending.State), []byte(callback.State)) != 1 {
		return Grant{}, ErrSignInFailed
	}
	if callback.Error != "" || callback.Code == "" {
		// The provider refused, or sent something that is not a callback. Either way
		// there is nothing to exchange; the provider's own wording goes no further than
		// the caller's log.
		return Grant{}, fmt.Errorf("%w: the provider returned no usable code", ErrSignInFailed)
	}

	identity, err := s.provider.Exchange(ctx, ExchangeRequest{
		Code:         callback.Code,
		Nonce:        pending.Nonce,
		CodeVerifier: pending.CodeVerifier,
		RedirectURI:  s.redirectURI,
	})
	if err != nil {
		return Grant{}, fmt.Errorf("%w: %w", ErrSignInFailed, err)
	}
	if !identity.Principal.Valid() {
		return Grant{}, fmt.Errorf("%w: the provider disclosed no stable subject", ErrSignInFailed)
	}
	if err := s.principals.UpsertPrincipal(ctx, identity.Principal); err != nil {
		return Grant{}, err
	}

	sessionToken, err := s.newToken()
	if err != nil {
		return Grant{}, fmt.Errorf("mint a session token: %w", err)
	}
	now := s.now()
	session := Session{
		TokenHash: HashToken(sessionToken),
		Principal: identity.Principal,
		IssuedAt:  now,
		ExpiresAt: now.Add(s.sessionLifetime),
		Tokens:    identity.Tokens,
	}
	if err := s.sessions.CreateSession(ctx, session); err != nil {
		return Grant{}, err
	}
	target, err := NewReturnTarget(pending.ReturnTo, s.allowedReturnOrigins)
	if err != nil {
		target = DefaultReturnTarget()
	}
	return Grant{
		SessionToken: sessionToken,
		Principal:    identity.Principal,
		ExpiresAt:    session.ExpiresAt,
		ReturnTo:     target.String(),
	}, nil
}

// Authenticate resolves a session cookie to the principal it belongs to.
//
// A session whose own lifetime is over is ended rather than merely refused, so a
// browser that keeps presenting it stops holding a record open. A session whose
// provider tokens expired is renewed when the provider issued a refresh token, so a
// long-lived stream does not have to survive a token expiry by itself; when it
// cannot be renewed the session ends, because a session the provider no longer
// stands behind is not one this hub will keep honouring.
func (s *Service) Authenticate(ctx context.Context, credential Credential) (Principal, error) {
	if credential.SessionToken == "" {
		return Principal{}, ErrUnauthenticated
	}
	hash := HashToken(credential.SessionToken)
	session, err := s.sessions.Session(ctx, hash)
	if err != nil {
		if errors.Is(err, ErrNoSession) {
			return Principal{}, ErrUnauthenticated
		}
		return Principal{}, err
	}
	now := s.now()
	if session.Expired(now) {
		_ = s.sessions.EndSession(ctx, hash)
		return Principal{}, ErrUnauthenticated
	}
	if session.Tokens.Expired(now) {
		if err := s.renew(ctx, session); err != nil {
			return Principal{}, err
		}
	}
	return session.Principal, nil
}

// renew refreshes a session's provider tokens, ending the session when the provider
// will not renew it.
func (s *Service) renew(ctx context.Context, session Session) error {
	if session.Tokens.Refresh == "" {
		_ = s.sessions.EndSession(ctx, session.TokenHash)
		return ErrUnauthenticated
	}
	tokens, err := s.provider.Refresh(ctx, session.Tokens.Refresh)
	if err != nil {
		if errors.Is(err, ErrProviderUnavailable) {
			// The provider being unreachable is not the operator being signed out: the
			// session stays, and the caller decides whether to retry.
			return err
		}
		_ = s.sessions.EndSession(ctx, session.TokenHash)
		return ErrUnauthenticated
	}
	return s.sessions.UpdateSessionTokens(ctx, session.TokenHash, tokens)
}

// SignOut ends a session immediately. Ending one that is not there is not a
// failure: signing out twice, or with a stale cookie, leaves the browser in exactly
// the state it asked for.
func (s *Service) SignOut(ctx context.Context, sessionToken string) error {
	if sessionToken == "" {
		return nil
	}
	if err := s.sessions.EndSession(ctx, HashToken(sessionToken)); err != nil && !errors.Is(err, ErrNoSession) {
		return err
	}
	return nil
}

// SignOutURL is where a browser may be sent to end the session at the provider as
// well, or "" when the provider offers nothing of the kind.
func (s *Service) SignOutURL(ctx context.Context, returnTo string) string {
	return s.provider.SignOutURL(ctx, returnTo)
}

// PurgeExpired removes the records that have aged out, so neither store grows
// without bound. It reports how many of each it removed.
func (s *Service) PurgeExpired(ctx context.Context) (sessions, signIns int, err error) {
	now := s.now()
	sessions, err = s.sessions.DeleteExpiredSessions(ctx, now)
	if err != nil {
		return 0, 0, err
	}
	signIns, err = s.pending.DeleteExpiredSignIns(ctx, now)
	if err != nil {
		return sessions, 0, err
	}
	return sessions, signIns, nil
}
