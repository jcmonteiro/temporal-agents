package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The hub authenticates everybody, including the operator's own scripts. That would
// turn a one-command local tool into a configuration exercise if the token had to be
// invented, copied and exported by hand, so it is not: the server mints one on a
// loopback listener and leaves it where the CLI on the same machine can read it, and
// nowhere else.
//
// The file is the machine's own trust boundary, which is the boundary this feature
// works within: the hub runs agents on a trusted machine, and guarding the machine
// from itself is explicitly out of scope. What the file does buy is that the port is
// no longer open to everything that can reach it — a page in the operator's browser
// cannot read a file.

// localTokenFile is the file's name inside the tool's configuration directory.
const localTokenFile = "api-token"

// localTokenBytes is how much entropy a minted token carries.
const localTokenBytes = 32

// allowUnauthenticatedEnv is the one way to serve an open API. It is explicit, it is
// refused outside loopback, and a process running with it says so on every start —
// the same shape as the development migration switch, and for the same reason: a
// mode that can be left on unnoticed is a mode that will be.
const allowUnauthenticatedEnv = "AGENT_HUB_ALLOW_UNAUTHENTICATED"

// localTokenPath is where the loopback token lives.
func localTokenPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config dir: %w", err)
	}
	return filepath.Join(dir, "temporal-agents", localTokenFile), nil
}

// apiToken is the token a request carries, as this machine's CLI finds it: the
// configured one, or the one `serve` minted for local use.
func apiToken(configured string) string {
	if token := strings.TrimSpace(configured); token != "" {
		return token
	}
	path, err := localTokenPath()
	if err != nil {
		return ""
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
}

// ensureLocalToken returns the token the server accepts from automation, minting and
// storing one when a loopback deployment configured none.
//
// It never mints for a listener that is not loopback: a token written to a file is a
// credential shared with everything that can read that file, which is a sensible
// trade on one operator's machine and not one anybody should make for a shared
// deployment. There, the operator configures a token deliberately.
func ensureLocalToken(configured string, loopback bool) (string, error) {
	if token := strings.TrimSpace(configured); token != "" {
		return token, nil
	}
	if !loopback {
		return "", nil
	}
	path, err := localTokenPath()
	if err != nil {
		return "", err
	}
	if existing, readErr := os.ReadFile(path); readErr == nil {
		if token := strings.TrimSpace(string(existing)); token != "" {
			return token, nil
		}
	}
	token, err := newLocalToken()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create the configuration directory: %w", err)
	}
	// 0600: the token is a credential, and a credential a second account can read is
	// a credential that account holds.
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("store the local API token: %w", err)
	}
	return token, nil
}

// newLocalToken mints a token with enough entropy that guessing it is irrelevant
// next to every other way onto the machine.
func newLocalToken() (string, error) {
	buffer := make([]byte, localTokenBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("mint a local API token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

// unauthenticatedAllowed reports whether the operator asked for an open API, and
// refuses the combination that would put one on a network.
func unauthenticatedAllowed(lookup func(string) string, loopback bool) (bool, error) {
	value := strings.TrimSpace(lookup(allowUnauthenticatedEnv))
	switch strings.ToLower(value) {
	case "", "0", "false", "no", "off":
		return false, nil
	}
	if !loopback {
		return false, fmt.Errorf("%s is refused when --addr is not loopback: "+
			"an API without a credential must not be reachable from another host", allowUnauthenticatedEnv)
	}
	return true, nil
}

// errNoCredential is what a deployment that can authenticate nobody is stopped with.
var errNoCredential = errors.New("this hub would accept anything that can reach its port")
