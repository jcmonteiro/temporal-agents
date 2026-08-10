package identity

import (
	"context"
	"time"
)

// The ports of the authentication core, stated here at the consumer's side and in
// the consumer's vocabulary. No port names a protocol, a library or a database, so
// a provider that is not OIDC, or a store that is not Postgres, is one more adapter
// rather than a change to any rule below.

// Provider is the identity provider seam: the two exchanges a sign-in needs, plus
// the renewal that keeps a long session usable.
//
// Everything about a protocol lives behind it — discovery, key rotation, the
// signature, the issuer, the audience, the expiry and the request's nonce are the
// adapter's obligations, and an adapter that returns an identity is asserting that
// it verified all of them. The core deliberately cannot check them: a rule that can
// only be enforced in one place should not be half-stated in two.
type Provider interface {
	// AuthorizationURL is where the browser is sent to sign in, for one request.
	AuthorizationURL(ctx context.Context, request AuthorizationRequest) (string, error)
	// Exchange trades an authorization code for a verified identity and the tokens
	// that come with it. It fails when anything about the response is not exactly
	// what the request asked for.
	Exchange(ctx context.Context, request ExchangeRequest) (Identity, error)
	// Refresh renews the provider's tokens for a session whose access token expired.
	// A provider that cannot refresh reports ErrUnauthenticated, which ends the
	// session rather than pretending it is still good.
	Refresh(ctx context.Context, refreshToken string) (Tokens, error)
	// Issuer identifies the provider, so a principal records who vouched for it.
	Issuer() string
	// SignOutURL is where the browser is sent to end the session at the provider
	// too, or "" when the provider offers no such endpoint. Ending the local session
	// never depends on it.
	SignOutURL(ctx context.Context, returnTo string) string
}

// AuthorizationRequest is one sign-in, as the provider must be told about it.
type AuthorizationRequest struct {
	// State is echoed back by the provider and checked against the pending sign-in.
	State string
	// Nonce is bound into the provider's identity assertion, so a replayed
	// assertion from another sign-in does not verify.
	Nonce string
	// CodeVerifier is the per-request secret whose challenge the provider is given,
	// so only the request that started a sign-in can finish it.
	CodeVerifier string
	// RedirectURI is where the provider sends the browser back to. It is the
	// deployment's own callback and is registered with the provider.
	RedirectURI string
}

// ExchangeRequest is a callback the provider sent back, as the adapter must
// complete it.
type ExchangeRequest struct {
	// Code is the authorization code the provider issued.
	Code string
	// Nonce is what the identity assertion must carry.
	Nonce string
	// CodeVerifier proves this is the request that started the sign-in.
	CodeVerifier string
	// RedirectURI must be the same value the authorization request carried.
	RedirectURI string
}

// Identity is what a provider vouched for, once the adapter has verified it.
type Identity struct {
	// Principal is who signed in.
	Principal Principal
	// Tokens are the provider's tokens, which stay on the server.
	Tokens Tokens
}

// SessionStore keeps the server-side session records. Sessions are records rather
// than self-describing cookies precisely so that ending one is immediate: a
// stateless credential can only be revoked by invalidating everybody's.
type SessionStore interface {
	// CreateSession stores a new session.
	CreateSession(ctx context.Context, session Session) error
	// Session returns the session a token hash identifies, or ErrNoSession.
	Session(ctx context.Context, tokenHash []byte) (Session, error)
	// UpdateSessionTokens replaces the provider tokens held for a session, after a
	// refresh.
	UpdateSessionTokens(ctx context.Context, tokenHash []byte, tokens Tokens) error
	// EndSession removes a session, reporting ErrNoSession when there was none.
	EndSession(ctx context.Context, tokenHash []byte) error
	// DeleteExpiredSessions removes every session that expired before the given
	// moment, and reports how many it removed.
	DeleteExpiredSessions(ctx context.Context, before time.Time) (int, error)
}

// PrincipalStore keeps who has signed in, so a recorded fact can name a principal
// by identity and still be rendered with a name later.
type PrincipalStore interface {
	// UpsertPrincipal records a principal, refreshing the display fields the
	// provider disclosed this time.
	UpsertPrincipal(ctx context.Context, principal Principal) error
	// Principal returns one principal by its issuer and subject, or ErrNoPrincipal.
	Principal(ctx context.Context, issuer, subject string) (Principal, error)
}

// PendingSignInStore keeps the sign-ins that have been started and not completed.
//
// Taking one is a consuming read: that single property is what makes a callback
// unreplayable, and it belongs in the store because only the store can make it
// atomic against a second, concurrent callback.
type PendingSignInStore interface {
	// StartSignIn stores a pending sign-in.
	StartSignIn(ctx context.Context, pending PendingSignIn) error
	// TakePendingSignIn returns and removes the pending sign-in a token hash
	// identifies, or ErrNoPendingSignIn. A second call with the same hash must
	// report ErrNoPendingSignIn even when it runs concurrently with the first.
	TakePendingSignIn(ctx context.Context, tokenHash []byte) (PendingSignIn, error)
	// DeleteExpiredSignIns removes abandoned sign-ins and reports how many it
	// removed.
	DeleteExpiredSignIns(ctx context.Context, before time.Time) (int, error)
}

// Credential is what a request presents, as the transport found it. It carries
// every kind at once, and it is deliberately not a tagged union: the transport must
// not decide which kind counts, so it hands over what was there and asks one
// question.
type Credential struct {
	// Authorization is the request's Authorization header, verbatim.
	Authorization string
	// SessionToken is the session cookie's value, or "" when there was none.
	SessionToken string
}

// Empty reports whether a request presented nothing at all.
func (c Credential) Empty() bool { return c.Authorization == "" && c.SessionToken == "" }

// Authenticator answers the only question the transport asks: is this request
// authenticated, and as whom. Every credential kind implements it, and a Chain
// puts several behind the one port.
type Authenticator interface {
	// Authenticate resolves a credential to a principal, or reports
	// ErrUnauthenticated. It must not disclose which part of a credential failed.
	Authenticate(ctx context.Context, credential Credential) (Principal, error)
}
