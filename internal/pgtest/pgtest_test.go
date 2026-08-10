package pgtest_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/pgtest"
)

// composeFile is the stack an operator brings up, relative to this package.
const composeFile = "../../docker-compose.yml"

func TestTheComposeStackRunsThePinnedPostgres(t *testing.T) {
	// The suites claim to test the database the tool really runs against. Nothing
	// enforces that claim except this: one digest, written once, and a compose file
	// that must agree with it — otherwise a bump lands in one place and the suites go
	// on testing the version nobody deploys.
	compose, err := os.ReadFile(composeFile)
	require.NoError(t, err)

	require.True(t, strings.Contains(string(compose), pgtest.Image),
		"the compose stack does not run %s, so the container suites test a different Postgres", pgtest.Image)
}
