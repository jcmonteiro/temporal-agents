package dextest_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/dextest"
)

// composeFile is the stack an operator brings up, relative to this package.
const composeFile = "../../docker-compose.yml"

func TestTheComposeStackRunsThePinnedProvider(t *testing.T) {
	// The suites claim to test the provider the tool really signs in against. Nothing
	// enforces that claim except this: one digest, written once, and a compose file
	// that must agree with it — otherwise a bump lands in one place and the suites go
	// on testing a provider nobody runs.
	compose, err := os.ReadFile(composeFile)
	require.NoError(t, err)

	require.True(t, strings.Contains(string(compose), dextest.Image),
		"the compose stack does not run %s, so the container suite tests a different provider", dextest.Image)
}
