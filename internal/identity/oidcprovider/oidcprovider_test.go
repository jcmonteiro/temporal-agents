package oidcprovider_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/identity"
	"temporal-agents/internal/identity/oidcprovider"
)

// The adapter's whole job is refusing an identity assertion that is not exactly what
// was asked for, so it is tested against an issuer this suite controls: one that can
// sign with the wrong key, claim the wrong audience, backdate an expiry, leave the
// token unsigned, or answer for a different sign-in. None of that can be provoked
// from a real provider, and every one of them is a way in if it is not refused.
//
// The issuer runs in-process (httptest), so these tests need no container. The
// container suite beside them proves the same adapter against a real provider.

// fakeIssuer is an OpenID provider this suite can misbehave on purpose.
type fakeIssuer struct {
	server *httptest.Server
	// mu guards everything a test changes while the provider is running.
	mu sync.Mutex
	// key is the current signing key, and kid its published identifier. Replacing
	// both is how a rotation is staged.
	key *rsa.PrivateKey
	kid string
	// idToken is what the token endpoint returns as the assertion, built per
	// exchange by the test's own function.
	idToken func(request tokenRequest) string
	// refresh decides what the token endpoint answers a refresh with.
	refresh func(request tokenRequest) (int, map[string]any)
}

// tokenRequest is what the token endpoint received.
type tokenRequest struct {
	GrantType    string
	Code         string
	CodeVerifier string
	RefreshToken string
}

// newFakeIssuer starts an issuer that signs correct assertions, which each test then
// spoils in exactly one way.
func newFakeIssuer(t *testing.T) *fakeIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	issuer := &fakeIssuer{key: key, kid: "key-1"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"issuer":                                issuer.url(),
			"authorization_endpoint":                issuer.url() + "/auth",
			"token_endpoint":                        issuer.url() + "/token",
			"jwks_uri":                              issuer.url() + "/keys",
			"end_session_endpoint":                  issuer.url() + "/end-session",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		issuer.mu.Lock()
		defer issuer.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"keys": []any{publicJWK(issuer.kid, &issuer.key.PublicKey)}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		request := tokenRequest{
			GrantType:    r.PostFormValue("grant_type"),
			Code:         r.PostFormValue("code"),
			CodeVerifier: r.PostFormValue("code_verifier"),
			RefreshToken: r.PostFormValue("refresh_token"),
		}
		issuer.mu.Lock()
		defer issuer.mu.Unlock()
		if request.GrantType == "refresh_token" && issuer.refresh != nil {
			status, body := issuer.refresh(request)
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token":  "access-1",
			"refresh_token": "refresh-1",
			"token_type":    "bearer",
			"expires_in":    3600,
			"id_token":      issuer.idToken(request),
		})
	})
	issuer.server = httptest.NewServer(mux)
	t.Cleanup(issuer.server.Close)
	issuer.idToken = func(tokenRequest) string { return issuer.sign(t, issuer.validClaims()) }
	return issuer
}

func (i *fakeIssuer) url() string { return i.server.URL }

// validClaims are the assertion a well-behaved provider would return.
func (i *fakeIssuer) validClaims() map[string]any {
	now := time.Now()
	return map[string]any{
		"iss":                i.url(),
		"sub":                "operator-1",
		"aud":                "the-hub",
		"exp":                now.Add(time.Hour).Unix(),
		"iat":                now.Unix(),
		"nonce":              "the-nonce",
		"name":               "The Operator",
		"email":              "operator@example.test",
		"preferred_username": "operator",
	}
}

// sign renders claims as an RS256 assertion under the issuer's current key.
func (i *fakeIssuer) sign(t *testing.T, claims map[string]any) string {
	t.Helper()
	return signRS256(t, i.key, i.kid, claims)
}

// rotate replaces the signing key, as a provider does periodically.
func (i *fakeIssuer) rotate(t *testing.T) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	i.mu.Lock()
	defer i.mu.Unlock()
	i.key, i.kid = key, "key-2"
}

// newProvider builds the adapter against the fake issuer.
func newProvider(t *testing.T, issuer *fakeIssuer) *oidcprovider.Provider {
	t.Helper()
	provider, err := oidcprovider.New(context.Background(), oidcprovider.Config{
		Issuer:       issuer.url(),
		ClientID:     "the-hub",
		ClientSecret: "the-secret",
	})
	require.NoError(t, err)
	return provider
}

// exchange asks the adapter to complete a callback for the usual request.
func exchange(provider *oidcprovider.Provider) (identity.Identity, error) {
	return provider.Exchange(context.Background(), identity.ExchangeRequest{
		Code:         "the-code",
		Nonce:        "the-nonce",
		CodeVerifier: "a-verifier-long-enough-to-be-a-pkce-verifier",
		RedirectURI:  "https://hub.test/api/v1/auth/callback",
	})
}

// TestAVerifiedAssertionBecomesAPrincipalAndServerHeldTokens is the adapter's
// primary behavior: what the provider vouched for becomes a principal, and what it
// issued stays as tokens the core keeps on the server.
func TestAVerifiedAssertionBecomesAPrincipalAndServerHeldTokens(t *testing.T) {
	issuer := newFakeIssuer(t)
	provider := newProvider(t, issuer)

	signedIn, err := exchange(provider)
	require.NoError(t, err)
	require.Equal(t, issuer.url(), signedIn.Principal.Issuer)
	require.Equal(t, "operator-1", signedIn.Principal.Subject)
	require.Equal(t, "The Operator", signedIn.Principal.Name)
	require.Equal(t, "operator@example.test", signedIn.Principal.Email)
	require.Equal(t, "access-1", signedIn.Tokens.Access)
	require.Equal(t, "refresh-1", signedIn.Tokens.Refresh)
	require.False(t, signedIn.Tokens.ExpiresAt.IsZero(), "the provider stated an expiry, so it is kept")
	require.Equal(t, issuer.url(), provider.Issuer())
}

// TestAnAssertionThatIsNotExactlyWhatWasAskedForIsRefused is the verification
// contract, one refusal at a time. Every case is a real attack: a token minted for
// another client, one that has expired, one nobody signed, one signed by a key the
// provider does not publish, one from another issuer, and one answering a different
// sign-in.
func TestAnAssertionThatIsNotExactlyWhatWasAskedForIsRefused(t *testing.T) {
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	for name, spoil := range map[string]func(t *testing.T, issuer *fakeIssuer){
		"minted for another client": spoiler{}.wrongAudience(),
		"already expired":           spoiler{}.expired(),
		"unsigned":                  spoiler{}.unsigned(),
		"signed by an unpublished key": func(t *testing.T, issuer *fakeIssuer) {
			issuer.idToken = func(tokenRequest) string {
				return signRS256(t, otherKey, issuer.kid, issuer.validClaims())
			}
		},
		"from another issuer": spoiler{}.wrongIssuer(),
		"missing altogether": func(_ *testing.T, issuer *fakeIssuer) {
			issuer.idToken = func(tokenRequest) string { return "" }
		},
	} {
		t.Run(name, func(t *testing.T) {
			issuer := newFakeIssuer(t)
			provider := newProvider(t, issuer)
			spoil(t, issuer)

			_, err := exchange(provider)
			require.Error(t, err)
		})
	}
}

// TestAnAssertionForAnotherSignInIsRefused is the request binding the provider
// itself carries: an assertion that verifies perfectly but answers a nonce this
// request never sent is somebody else's, and is refused for that reason alone.
func TestAnAssertionForAnotherSignInIsRefused(t *testing.T) {
	issuer := newFakeIssuer(t)
	provider := newProvider(t, issuer)
	issuer.idToken = func(tokenRequest) string {
		claims := issuer.validClaims()
		claims["nonce"] = "a-nonce-from-somebody-elses-sign-in"
		return issuer.sign(t, claims)
	}

	_, err := exchange(provider)
	require.ErrorContains(t, err, "belongs to a different sign-in")
}

// TestAProviderThatRotatesItsKeysKeepsWorking pins that signing keys are re-read
// rather than pinned at startup: a rotation must not sign every operator out until
// somebody restarts the hub.
func TestAProviderThatRotatesItsKeysKeepsWorking(t *testing.T) {
	issuer := newFakeIssuer(t)
	provider := newProvider(t, issuer)
	_, err := exchange(provider)
	require.NoError(t, err)

	issuer.rotate(t)

	signedIn, err := exchange(provider)
	require.NoError(t, err)
	require.Equal(t, "operator-1", signedIn.Principal.Subject)
}

// TestTheBrowserIsSentToTheProviderWithTheChallengeAndNeverTheSecret pins what the
// redirect may carry: the state, the nonce, and the challenge derived from the
// verifier — never the verifier itself, which is the only thing that makes an
// intercepted code useless.
func TestTheBrowserIsSentToTheProviderWithTheChallengeAndNeverTheSecret(t *testing.T) {
	issuer := newFakeIssuer(t)
	provider := newProvider(t, issuer)
	const verifier = "a-verifier-long-enough-to-be-a-pkce-verifier"

	url, err := provider.AuthorizationURL(context.Background(), identity.AuthorizationRequest{
		State:        "the-state",
		Nonce:        "the-nonce",
		CodeVerifier: verifier,
		RedirectURI:  "https://hub.test/api/v1/auth/callback",
	})
	require.NoError(t, err)
	require.Contains(t, url, "state=the-state")
	require.Contains(t, url, "nonce=the-nonce")
	require.Contains(t, url, "code_challenge_method=S256")
	require.NotContains(t, url, verifier, "the verifier must never leave this server")
	require.Contains(t, url, "client_id=the-hub")
	require.NotContains(t, url, "the-secret", "the client secret must never leave this server")
}

// TestARefusedRefreshEndsTheSessionAndAnUnreachableProviderDoesNot pins the
// distinction the core acts on: a revoked grant signs the operator out, an outage
// does not.
func TestARefusedRefreshEndsTheSessionAndAnUnreachableProviderDoesNot(t *testing.T) {
	t.Run("the provider refuses the grant", func(t *testing.T) {
		issuer := newFakeIssuer(t)
		issuer.refresh = func(tokenRequest) (int, map[string]any) {
			return http.StatusBadRequest, map[string]any{"error": "invalid_grant"}
		}
		provider := newProvider(t, issuer)

		_, err := provider.Refresh(context.Background(), "refresh-1")
		require.ErrorIs(t, err, identity.ErrUnauthenticated)
	})

	t.Run("the provider cannot be reached", func(t *testing.T) {
		issuer := newFakeIssuer(t)
		provider := newProvider(t, issuer)
		issuer.server.Close()

		_, err := provider.Refresh(context.Background(), "refresh-1")
		require.ErrorIs(t, err, identity.ErrProviderUnavailable)
	})

	t.Run("there is nothing to refresh with", func(t *testing.T) {
		provider := newProvider(t, newFakeIssuer(t))

		_, err := provider.Refresh(context.Background(), "")
		require.ErrorIs(t, err, identity.ErrUnauthenticated)
	})
}

// TestARenewedTokenReplacesTheExpiredOne pins the successful half of a refresh.
func TestARenewedTokenReplacesTheExpiredOne(t *testing.T) {
	issuer := newFakeIssuer(t)
	issuer.refresh = func(request tokenRequest) (int, map[string]any) {
		if request.RefreshToken != "refresh-1" {
			return http.StatusBadRequest, map[string]any{"error": "invalid_grant"}
		}
		return http.StatusOK, map[string]any{
			"access_token":  "access-2",
			"refresh_token": "refresh-2",
			"token_type":    "bearer",
			"expires_in":    3600,
		}
	}
	provider := newProvider(t, issuer)

	tokens, err := provider.Refresh(context.Background(), "refresh-1")
	require.NoError(t, err)
	require.Equal(t, "access-2", tokens.Access)
	require.Equal(t, "refresh-2", tokens.Refresh)
	require.False(t, tokens.ExpiresAt.IsZero())
}

// TestAMisconfiguredClientIsRefusedAtStartup pins the fail-fast rule, including the
// one that would silently turn the hub into a public client.
func TestAMisconfiguredClientIsRefusedAtStartup(t *testing.T) {
	issuer := newFakeIssuer(t)
	for name, config := range map[string]oidcprovider.Config{
		"no issuer":        {ClientID: "the-hub", ClientSecret: "the-secret"},
		"no client id":     {Issuer: issuer.url(), ClientSecret: "the-secret"},
		"no client secret": {Issuer: issuer.url(), ClientID: "the-hub"},
		"an issuer that is not there": {
			Issuer: "http://127.0.0.1:1/nowhere", ClientID: "the-hub", ClientSecret: "the-secret",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := oidcprovider.New(context.Background(), config)
			require.Error(t, err)
		})
	}
}

// TestSigningOutCanAlsoEndTheSessionAtTheProvider pins that the published
// end-session endpoint is used when there is one.
func TestSigningOutCanAlsoEndTheSessionAtTheProvider(t *testing.T) {
	issuer := newFakeIssuer(t)
	provider := newProvider(t, issuer)

	url := provider.SignOutURL(context.Background(), "https://hub.test/")
	require.Contains(t, url, issuer.url()+"/end-session")
	require.Contains(t, url, "post_logout_redirect_uri=https%3A%2F%2Fhub.test%2F")
}

// spoiler carries the small assertion-spoiling helpers, so the table above reads as
// a list of attacks rather than as a wall of closures.
type spoiler struct{}

func (spoiler) wrongAudience() func(*testing.T, *fakeIssuer) {
	return func(t *testing.T, issuer *fakeIssuer) {
		issuer.idToken = func(tokenRequest) string {
			claims := issuer.validClaims()
			claims["aud"] = "another-application"
			return issuer.sign(t, claims)
		}
	}
}

func (spoiler) expired() func(*testing.T, *fakeIssuer) {
	return func(t *testing.T, issuer *fakeIssuer) {
		issuer.idToken = func(tokenRequest) string {
			claims := issuer.validClaims()
			claims["exp"] = time.Now().Add(-time.Minute).Unix()
			claims["iat"] = time.Now().Add(-time.Hour).Unix()
			return issuer.sign(t, claims)
		}
	}
}

func (spoiler) unsigned() func(*testing.T, *fakeIssuer) {
	return func(_ *testing.T, issuer *fakeIssuer) {
		issuer.idToken = func(tokenRequest) string {
			header := encodeSegment(map[string]any{"alg": "none", "typ": "JWT"})
			return header + "." + encodeSegment(issuer.validClaims()) + "."
		}
	}
}

func (spoiler) wrongIssuer() func(*testing.T, *fakeIssuer) {
	return func(t *testing.T, issuer *fakeIssuer) {
		issuer.idToken = func(tokenRequest) string {
			claims := issuer.validClaims()
			claims["iss"] = "https://another-issuer.test"
			return issuer.sign(t, claims)
		}
	}
}

// signRS256 renders a JWT the way a provider does, so the adapter's verification is
// exercised against real bytes rather than against a stub.
func signRS256(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	signingInput := encodeSegment(map[string]any{"alg": "RS256", "typ": "JWT", "kid": kid}) +
		"." + encodeSegment(claims)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	require.NoError(t, err)
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// encodeSegment renders one JWT segment.
func encodeSegment(value map[string]any) string {
	body, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(body)
}

// publicJWK publishes a signing key the way a provider's key set does: the modulus
// and the exponent as unpadded big-endian, URL-safe base64.
func publicJWK(kid string, key *rsa.PublicKey) map[string]any {
	exponent := big.NewInt(int64(key.E)).Bytes()
	return map[string]any{
		"kty": "RSA",
		"kid": kid,
		"alg": "RS256",
		"use": "sig",
		"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(exponent),
	}
}

// writeJSON answers one of the fake issuer's documents.
func writeJSON(w http.ResponseWriter, status int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
