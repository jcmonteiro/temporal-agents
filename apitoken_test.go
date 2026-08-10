package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The rules here are the last ones between the operator's work and whatever can
// reach the port, so each is tested for what it refuses as much as for what it
// allows.

// TestAutomationGetsAWorkingTokenWithoutTheOperatorInventingOne pins the local
// ergonomics: the CLI on this machine authenticates with no credential to copy, and
// reading the token twice reads the same one, so a restart does not sign the CLI
// out.
func TestAutomationGetsAWorkingTokenWithoutTheOperatorInventingOne(t *testing.T) {
	withTemporaryConfigDir(t)

	minted, err := ensureLocalToken("", true)
	require.NoError(t, err)
	require.NotEmpty(t, minted)

	again, err := ensureLocalToken("", true)
	require.NoError(t, err)
	require.Equal(t, minted, again)
	require.Equal(t, minted, apiToken(""), "the CLI reads what the server minted")
	require.Equal(t, "configured", apiToken("configured"),
		"a configured token is never overridden by the local one")
}

// TestTheLocalTokenIsReadableOnlyByItsOperator pins the one property that makes
// writing a credential to disk acceptable at all.
func TestTheLocalTokenIsReadableOnlyByItsOperator(t *testing.T) {
	withTemporaryConfigDir(t)
	_, err := ensureLocalToken("", true)
	require.NoError(t, err)

	path, err := localTokenPath()
	require.NoError(t, err)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

// TestNoCredentialIsWrittenForAListenerThatIsNotLoopback pins that a shared
// deployment's token is always a deliberate one: a file-borne credential is shared
// with everything that can read the file, which is a trade only a single-operator
// machine should make.
func TestNoCredentialIsWrittenForAListenerThatIsNotLoopback(t *testing.T) {
	withTemporaryConfigDir(t)

	token, err := ensureLocalToken("", false)
	require.NoError(t, err)
	require.Empty(t, token)

	path, err := localTokenPath()
	require.NoError(t, err)
	_, statErr := os.Stat(path)
	require.True(t, os.IsNotExist(statErr), "a token was written for a non-loopback listener")
}

// TestAnOpenAPIIsAskedForExplicitlyAndOnlyLocally pins the shape of the one
// remaining unauthenticated mode: explicit, loopback-only, and refused where it
// would put an open API on a network.
func TestAnOpenAPIIsAskedForExplicitlyAndOnlyLocally(t *testing.T) {
	for value, want := range map[string]bool{
		"":      false,
		"0":     false,
		"false": false,
		"off":   false,
		"1":     true,
		"yes":   true,
	} {
		t.Run("value "+value, func(t *testing.T) {
			allowed, err := unauthenticatedAllowed(environmentOf(map[string]string{
				allowUnauthenticatedEnv: value,
			}), true)

			require.NoError(t, err)
			require.Equal(t, want, allowed)
		})
	}

	_, err := unauthenticatedAllowed(environmentOf(map[string]string{
		allowUnauthenticatedEnv: "1",
	}), false)
	require.ErrorContains(t, err, allowUnauthenticatedEnv,
		"an open API must never be reachable from another host")
}

// TestALoopbackListenerIsTheOnlyOneThatNeedsNoTLSOrToken restates, at this level,
// what makes the rules above safe: exposure beyond this machine is refused unless it
// is protected.
func TestALoopbackListenerIsTheOnlyOneThatNeedsNoTLSOrToken(t *testing.T) {
	for address, loopback := range map[string]bool{
		"127.0.0.1:8973":        true,
		"localhost:8973":        true,
		"[::1]:8973":            true,
		"0.0.0.0:8973":          false,
		"hub.example.test:8973": false,
		"192.168.1.10:8973":     false,
	} {
		t.Run(address, func(t *testing.T) {
			got, err := isLoopbackAddress(address)
			require.NoError(t, err)
			require.Equal(t, loopback, got)
		})
	}
}

// withTemporaryConfigDir points the tool's configuration directory at a directory of
// this test's own, so a test never reads or writes the operator's real token.
func withTemporaryConfigDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	// macOS reads the home directory instead of XDG_CONFIG_HOME.
	t.Setenv("HOME", filepath.Join(dir, "home"))
}
