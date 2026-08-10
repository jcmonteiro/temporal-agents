package identity

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"strings"
)

// The two credential kinds live behind one port, so the transport can ask its one
// question and a third kind (a device-code grant, say) can be added without the
// transport learning about it.

// StaticToken authenticates the programmatic caller: a token an operator
// configured, presented as an HTTP bearer credential. It is what keeps scripts and
// the CLI working with no browser and no interactive step.
type StaticToken struct {
	// want is the digest of the whole expected header value, so the comparison is
	// over fixed-length inputs and cannot leak the token's length.
	want [sha256.Size]byte
	// principal is who a request with this token is attributed to. Automation is a
	// principal like any other: a fact it creates must be attributable.
	principal Principal
}

// Compile-time proof it answers the transport's question.
var _ Authenticator = (*StaticToken)(nil)

// StaticTokenSubject is the subject recorded for the configured token. It names the
// credential rather than a person, which is exactly what it is.
const StaticTokenSubject = "automation"

// NewStaticToken builds the static-token authenticator, refusing an empty token: a
// credential that accepts the empty string authenticates every request.
func NewStaticToken(token string) (*StaticToken, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("the static token must not be empty")
	}
	return &StaticToken{
		want: sha256.Sum256([]byte(bearerPrefix + token)),
		principal: Principal{
			Issuer:  LocalIssuer,
			Subject: StaticTokenSubject,
			Name:    "Automation",
		},
	}, nil
}

// bearerPrefix is the authentication scheme the header must use.
const bearerPrefix = "Bearer "

// Authenticate accepts exactly the configured token, compared in constant time so
// that a caller cannot learn a prefix of it from how long the refusal took.
func (t *StaticToken) Authenticate(_ context.Context, credential Credential) (Principal, error) {
	got := sha256.Sum256([]byte(credential.Authorization))
	if subtle.ConstantTimeCompare(got[:], t.want[:]) != 1 {
		return Principal{}, ErrUnauthenticated
	}
	return t.principal, nil
}

// Chain puts several authenticators behind the one port, trying each in turn. The
// first that accepts the credential wins; a request is refused only when none does.
//
// Order is not a policy: an authenticator only ever accepts the kind of credential
// it understands, so a session cannot be resolved by the token authenticator or the
// other way round.
type Chain []Authenticator

// Compile-time proof a chain is itself an authenticator, so a caller cannot tell
// one from a single adapter.
var _ Authenticator = (Chain)(nil)

// Authenticate returns the first principal an authenticator in the chain resolves.
//
// A failure that is not "this credential is not accepted" — a store that cannot be
// reached, say — stops the chain and is reported, because answering
// "unauthenticated" when the truth is "the session store is down" would sign every
// operator out of a working hub.
func (c Chain) Authenticate(ctx context.Context, credential Credential) (Principal, error) {
	for _, authenticator := range c {
		principal, err := authenticator.Authenticate(ctx, credential)
		switch {
		case err == nil:
			return principal, nil
		case errors.Is(err, ErrUnauthenticated):
			continue
		default:
			return Principal{}, err
		}
	}
	return Principal{}, ErrUnauthenticated
}
