// Package identity is the application core of authentication: who a request is
// made by, and how a browser comes to be signed in.
//
// It holds the rules and none of the machinery. There is no HTTP, no cookie, no
// SQL and no provider library here: an authorization-code exchange is expressed as
// a port (see ports.go), a session is a record a store keeps, and a sign-in is a
// sequence this package decides on. That is what allows a second, non-standard
// provider to be added as one more adapter, and what makes the rules that matter —
// request binding, single use, expiry — testable without a network or a database.
//
// The hub records identity and nothing else. There are no roles and no scopes: any
// authenticated principal is the operator. A principal exists so that a fact this
// hub creates can name who created it.
package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// The failures the core reports. They are few on purpose: a caller decides what to
// do about "not authenticated" and about "the sign-in did not complete", and it
// must never be able to branch on why a credential was refused, because the why is
// exactly what an attacker would like to learn.
var (
	// ErrUnauthenticated reports a request that carries no credential this hub
	// accepts. A missing, malformed, expired, revoked and forged credential are all
	// this error.
	ErrUnauthenticated = errors.New("the request is not authenticated")
	// ErrSignInFailed reports a callback that cannot be completed: it is unbound
	// from any sign-in this server started, it has already been used, it expired, or
	// the provider refused it.
	ErrSignInFailed = errors.New("the sign-in could not be completed")
	// ErrProviderUnavailable reports a provider that could not be reached. It is
	// separate because it is transient, and a caller may retry it.
	ErrProviderUnavailable = errors.New("the identity provider could not be reached")
	// ErrNoSession reports a session a store does not hold. It is the store port's
	// vocabulary; the service turns it into ErrUnauthenticated.
	ErrNoSession = errors.New("no such session")
	// ErrNoPendingSignIn reports a pending sign-in a store does not hold, for the
	// same reason.
	ErrNoPendingSignIn = errors.New("no such pending sign-in")
	// ErrNoPrincipal reports a principal a store does not hold.
	ErrNoPrincipal = errors.New("no such principal")
)

// LocalIssuer is the issuer recorded for the static automation token. It is a URN
// rather than a URL because nothing vouches for it over a network: the token is
// configuration of this deployment.
const LocalIssuer = "urn:temporal-agents:static-token"

// Principal is who a request is made by: a subject an issuer vouches for, plus
// whatever the issuer chose to disclose about them.
//
// Identity is (Issuer, Subject) and never the email address. An address can be
// reassigned inside an organisation, and a hub that keyed attribution on one would
// silently credit somebody else's work to whoever inherited the mailbox.
type Principal struct {
	// Issuer is who vouches for the subject: the provider's issuer identifier, or
	// LocalIssuer for the static token.
	Issuer string
	// Subject is the identifier the issuer uses for this person, stable across
	// sign-ins and across changes of name and address.
	Subject string
	// Name is what to show a human, when the provider disclosed one.
	Name string
	// Email is the address the provider disclosed, when it disclosed one. It is a
	// display field, never an identity.
	Email string
}

// ID is the principal's stable identity across both of its parts, in a form that
// can be written into a record that attributes work.
func (p Principal) ID() string { return p.Issuer + "|" + p.Subject }

// Valid reports whether the principal names somebody. A principal without both
// parts of its identity is not one this hub is willing to attribute anything to.
func (p Principal) Valid() bool {
	return strings.TrimSpace(p.Issuer) != "" && strings.TrimSpace(p.Subject) != ""
}

// DisplayName is the best name available for a principal, falling back to the
// address and then to the subject, so a surface always has something to show.
func (p Principal) DisplayName() string {
	for _, candidate := range []string{p.Name, p.Email, p.Subject} {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// Tokens are the provider's tokens for one session. They stay on the server: the
// browser never receives one, which is the whole reason the API is the confidential
// client rather than the page.
type Tokens struct {
	// Access is the provider's access token.
	Access string
	// Refresh is the token that renews the access token, when the provider issued
	// one. A provider that issues none simply ends the session at its expiry.
	Refresh string
	// ExpiresAt is when the access token stops being usable, or the zero time when
	// the provider did not say.
	ExpiresAt time.Time
}

// Expired reports whether the access token is past its expiry at the given moment.
// Tokens with no stated expiry never expire on their own: the session's expiry is
// then the only bound, which is what a provider that omits the field is asking for.
func (t Tokens) Expired(now time.Time) bool {
	return !t.ExpiresAt.IsZero() && !now.Before(t.ExpiresAt)
}

// Session is a signed-in browser, as the server remembers it.
//
// The cookie's value is never stored — only its hash — so a copy of this table is
// not a set of usable credentials, and looking a session up is a comparison of
// digests rather than of secrets.
type Session struct {
	// TokenHash identifies the session: the digest of the value the browser holds.
	TokenHash []byte
	// Principal is who the session belongs to.
	Principal Principal
	// IssuedAt is when the session was created.
	IssuedAt time.Time
	// ExpiresAt is when it stops being accepted, whatever the provider's tokens say.
	ExpiresAt time.Time
	// Tokens are the provider's tokens held for this session.
	Tokens Tokens
}

// Expired reports whether the session is past its own expiry.
func (s Session) Expired(now time.Time) bool { return !now.Before(s.ExpiresAt) }

// PendingSignIn is a sign-in this server started and has not yet completed: the
// values that bind a callback to it.
//
// It is a server-side record rather than something carried in the redirect, because
// the two properties that matter cannot be had otherwise. Binding: a callback is
// accepted only when the browser also presents the pending sign-in's own token, so
// a code injected from elsewhere has nothing to bind to. Single use: completing a
// sign-in consumes the record, so replaying the same callback finds nothing.
type PendingSignIn struct {
	// TokenHash identifies the pending sign-in: the digest of the value the browser
	// holds while the provider is visited.
	TokenHash []byte
	// State is the value handed to the provider and expected back, unchanged.
	State string
	// Nonce binds the provider's ID token to this very request.
	Nonce string
	// CodeVerifier is the PKCE verifier whose challenge went to the provider, so an
	// intercepted code cannot be exchanged by anyone else.
	CodeVerifier string
	// ReturnTo is where the browser is sent once signed in. It is either a path
	// inside this application or an absolute URL on an allowlisted frontend origin.
	ReturnTo string
	// StartedAt is when the sign-in began.
	StartedAt time.Time
	// ExpiresAt is when it stops being completable, so an abandoned sign-in cannot
	// be finished days later.
	ExpiresAt time.Time
}

// Expired reports whether the pending sign-in can no longer be completed.
func (p PendingSignIn) Expired(now time.Time) bool { return !now.Before(p.ExpiresAt) }

// DefaultReturnPath is where a browser lands when it did not ask for anywhere in
// particular, or asked for somewhere this server will not send it.
const DefaultReturnPath = "/"

// ReturnTarget is a callback destination that cannot name an origin unless the
// operator explicitly allowed it.
type ReturnTarget struct{ value string }

// String returns the validated destination for a Location header.
func (t ReturnTarget) String() string {
	if t.value == "" {
		return DefaultReturnPath
	}
	return t.value
}

// DefaultReturnTarget is the safe destination used when a requested target is not
// valid or is not allowlisted.
func DefaultReturnTarget() ReturnTarget { return ReturnTarget{value: DefaultReturnPath} }

// NewReturnTarget accepts either an application-relative path or an absolute HTTP(S)
// URL on one of the exact allowed origins. It rejects every other absolute URL.
func NewReturnTarget(candidate string, allowedOrigins []string) (ReturnTarget, error) {
	target := strings.TrimSpace(candidate)
	if strings.HasPrefix(target, "/") {
		if safe := SafeReturnPath(target); safe == target {
			return ReturnTarget{value: safe}, nil
		}
		return ReturnTarget{}, fmt.Errorf("invalid relative return target %q", candidate)
	}
	parsed, err := url.Parse(target)
	if err != nil || !parsed.IsAbs() || parsed.Opaque != "" || parsed.User != nil || parsed.Host == "" {
		return ReturnTarget{}, fmt.Errorf("invalid absolute return target %q", candidate)
	}
	origin, err := ParseBrowserOrigin(parsed.Scheme + "://" + parsed.Host)
	if err != nil {
		return ReturnTarget{}, fmt.Errorf("invalid absolute return target %q", candidate)
	}
	for _, allowed := range allowedOrigins {
		if origin == allowed {
			return ReturnTarget{value: target}, nil
		}
	}
	return ReturnTarget{}, fmt.Errorf("return target origin %q is not allowed", origin)
}

// ParseBrowserOrigin validates and canonicalizes one browser origin. Only HTTP(S)
// origins are accepted; credentials, paths, queries, fragments, wildcard and opaque
// origins are not origins this service can trust.
func ParseBrowserOrigin(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "*" || strings.EqualFold(trimmed, "null") {
		return "", fmt.Errorf("invalid browser origin %q", raw)
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.ForceQuery ||
		parsed.Fragment != "" {
		return "", fmt.Errorf("invalid browser origin %q", raw)
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

// SafeReturnPath narrows a requested destination to a path inside this
// application. Anything else — an absolute URL, a scheme-relative "//host/path", a
// backslash form some clients normalise into one — becomes the default.
//
// The rule is a whitelist rather than a blacklist of hosts: a redirect target that
// a request can choose is an open redirect the moment it can name a host, and a
// sign-in flow is the most attractive possible place to find one.
func SafeReturnPath(candidate string) string {
	path := strings.TrimSpace(candidate)
	if path == "" {
		return DefaultReturnPath
	}
	if !strings.HasPrefix(path, "/") {
		return DefaultReturnPath
	}
	// "//host" and "/\host" are both read as scheme-relative URLs by browsers.
	if strings.HasPrefix(path, "//") || strings.HasPrefix(path, "/\\") {
		return DefaultReturnPath
	}
	// A control character in a Location header is a header-injection attempt, not a
	// destination.
	if strings.ContainsAny(path, "\r\n\x00") {
		return DefaultReturnPath
	}
	return path
}

// HashToken is how a browser-held secret becomes a stored identifier. It is a plain
// digest rather than a password hash on purpose: the value is 256 bits of entropy
// this server generated, so there is nothing to guess and nothing to slow down.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// tokenBytes is how much entropy a browser-held token carries. 256 bits makes
// guessing one irrelevant next to every other way of getting in.
const tokenBytes = 32

// NewToken mints a browser-held secret: URL-safe, unpadded, and never stored as
// issued.
func NewToken() (string, error) {
	buffer := make([]byte, tokenBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
