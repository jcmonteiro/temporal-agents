package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/execstore"
)

// These tests cover the observable behavior of the fleet subcommands' flag
// parsing and plan handling. Error paths call fatalf (os.Exit) and are
// intentionally not exercised here.

func TestParseFleetPlanFlags_PromptAndOptionalName(t *testing.T) {
	prompt, name := parseFleetPlanFlags([]string{"expose the core", "--name", "core"})
	require.Equal(t, "expose the core", prompt)
	require.Equal(t, "core", name)

	prompt, name = parseFleetPlanFlags([]string{"--name=core", "expose the core"})
	require.Equal(t, "expose the core", prompt)
	require.Equal(t, "core", name)

	// A name is optional: the handle, which is always generated, is what selects a
	// plan.
	prompt, name = parseFleetPlanFlags([]string{"expose the core"})
	require.Equal(t, "expose the core", prompt)
	require.Empty(t, name)
}

func TestParseFleetExecuteFlags_PlanHandleAndSummary(t *testing.T) {
	planID, summary := parseFleetExecuteFlags([]string{"--plan-id", "plan-abcd1234", "--summary"})
	require.Equal(t, "plan-abcd1234", planID)
	require.True(t, summary)

	planID, summary = parseFleetExecuteFlags([]string{"--plan-id=plan-abcd1234"})
	require.Equal(t, "plan-abcd1234", planID)
	require.False(t, summary)
}

func TestNewPlanHandle_IsShortPrefixedAndUnique(t *testing.T) {
	first, second := newPlanHandle(), newPlanHandle()

	require.True(t, strings.HasPrefix(first, "plan-"))
	// Short enough to retype on the command line.
	require.Len(t, first, len("plan-")+12)
	require.NotEqual(t, first, second)
}

func TestDecodePlan_ReadsAStoredDocument(t *testing.T) {
	plan := decodePlan("plan-1", []byte(`{"goal":"expose the core","nodes":[
		{"id":"core","prompt":"implement the core"},
		{"id":"rest","prompt":"expose via REST","dependsOn":["core"]}]}`))

	require.Equal(t, "expose the core", plan.Goal)
	require.Len(t, plan.Nodes, 2)
	require.Equal(t, []string{"core"}, plan.Nodes[1].DependsOn)
}

func TestPlanReadError_TellsAnUnknownHandleFromAStoreFailure(t *testing.T) {
	// Both abort, but the operator needs to know which happened: a mistyped handle
	// is theirs to fix, a store outage is not.
	unknown := planReadError("plan-nope", execstore.ErrNoSuchPlan)
	require.Contains(t, unknown.Error(), "No fleet plan with handle")
	require.Contains(t, unknown.Error(), "fleet plan list")

	outage := planReadError("plan-1", errors.New("connection refused"))
	require.Contains(t, outage.Error(), "Could not read fleet plan plan-1")
	require.Contains(t, outage.Error(), "connection refused")
}
