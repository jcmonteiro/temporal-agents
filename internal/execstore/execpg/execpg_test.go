package execpg

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/execstore"
)

// The two pure tests of the adapter, both about buildFilter's one non-obvious
// trick. Everything else the adapter does is asserted against a real Postgres
// (execpg_integration_test.go, plans_integration_test.go), where the outcome is
// observable rather than a string comparison against the SQL that produced it.

func TestBuildFilter_EmptyFilterConstrainsNothing(t *testing.T) {
	where, args := buildFilter(execstore.Filter{})

	require.Empty(t, where)
	require.Empty(t, args)
}

func TestBuildFilter_WorkflowIDMatchesTheWholeTreeWithOneArgument(t *testing.T) {
	// One workflow ID must select the execution itself (every continue-as-new
	// iteration) and its children, which the builder does by matching two columns
	// against a single positional parameter. Passing the value twice instead would
	// silently shift every later placeholder, so the count is what is pinned here —
	// not the wording of the clause.
	where, args := buildFilter(execstore.Filter{WorkflowID: "fleet-1"})

	require.Equal(t, []any{"fleet-1"}, args, "one value, however many columns it is matched against")
	require.Equal(t, 2, strings.Count(where, "$1"))
	require.NotContains(t, where, "$2")
	require.NotContains(t, where, filterPlaceholder, "every placeholder is rendered, none reaches Postgres")
}
