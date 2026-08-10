package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"temporal-agents/internal/execstore"
)

// These tests cover the observable behavior of the fleet subcommands' flag
// parsing and plan handling. Error paths call fatalf (os.Exit) and are
// intentionally not exercised here.

func TestValidateFleetPlan_AcceptsAValidFullyParallelPlan(t *testing.T) {
	// A plan with no edges is valid: the validator checks the schema and graph,
	// not whether the chosen decomposition should have introduced dependencies.
	candidate := `{"goal":"two independent changes","nodes":[` +
		`{"id":"docs","prompt":"update the docs","dependsOn":[]},` +
		`{"id":"metrics","prompt":"add metrics","dependsOn":[]}]}`
	var out bytes.Buffer

	err := validateFleetPlan(strings.NewReader(candidate), &out)

	require.NoError(t, err)
	require.Equal(t, "valid fleet plan: 2 nodes, 0 dependencies\n", out.String())
}

func TestValidateFleetPlan_RejectsTheSchemaErrorFromTheFailedSession(t *testing.T) {
	candidate := `{"goal":"g","nodes":[{"id":"a","prompt":"p","dependsOn":[]}],"dependsOnNote":null}`

	err := validateFleetPlan(strings.NewReader(candidate), &bytes.Buffer{})

	require.EqualError(t, err, `parse plan: parse plan JSON: json: unknown field "dependsOnNote"`)
}

func TestRunFleetPlanValidate_ReadsTheAgentsCandidateFile(t *testing.T) {
	candidate := `{"goal":"g","nodes":[{"id":"a","prompt":"p","dependsOn":[]}]}`
	path := t.TempDir() + "/candidate.json"
	require.NoError(t, os.WriteFile(path, []byte(candidate), 0o600))
	var out bytes.Buffer

	err := runFleetPlanValidate([]string{path}, strings.NewReader("not used"), &out)

	require.NoError(t, err)
	require.Contains(t, out.String(), "valid fleet plan")
}

func TestRunFleetPlanValidate_ReadsStdinForDash(t *testing.T) {
	candidate := `{"goal":"g","nodes":[{"id":"a","prompt":"p","dependsOn":[]}]}`
	var out bytes.Buffer

	err := runFleetPlanValidate([]string{"-"}, strings.NewReader(candidate), &out)

	require.NoError(t, err)
	require.Contains(t, out.String(), "valid fleet plan")
}

func TestValidateFleetPlan_RejectsAnInvalidDependencyGraph(t *testing.T) {
	candidate := `{"goal":"g","nodes":[` +
		`{"id":"a","prompt":"p","dependsOn":["b"]},` +
		`{"id":"b","prompt":"q","dependsOn":["a"]}]}`

	err := validateFleetPlan(strings.NewReader(candidate), &bytes.Buffer{})

	require.Contains(t, err.Error(), "validate plan: plan has a dependency cycle")
}

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
	// Short enough to retype on the command line, wide enough that a collision —
	// which the store's upsert would answer by overwriting a plan, silently — stays
	// out of reach.
	require.Len(t, first, len("plan-")+planHandleHexDigits)
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
	require.Contains(t, unknown, "No fleet plan with handle")
	require.Contains(t, unknown, "fleet plan list")

	outage := planReadError("plan-1", errors.New("connection refused"))
	require.Contains(t, outage, "Could not read fleet plan plan-1")
	require.Contains(t, outage, "connection refused")
}

func TestParseFleetPlanListFlags_OptionalLimit(t *testing.T) {
	// No flag leaves the cap to the store's default, which is what 0 means.
	require.Zero(t, parseFleetPlanListFlags(nil))
	require.Equal(t, 50, parseFleetPlanListFlags([]string{"--limit", "50"}))
	require.Equal(t, 50, parseFleetPlanListFlags([]string{"--limit=50"}))
}

func TestEffectivePlanLimit_ResolvesTheStoresDefault(t *testing.T) {
	// The "there may be more" hint has to compare against the cap the listing was
	// actually served under, not against 0.
	require.Equal(t, execstore.DefaultPlanLimit, effectivePlanLimit(0))
	require.Equal(t, 5, effectivePlanLimit(5))
}

func TestFleetPlanHelp_ExplainsValidationAndReservedGoalWords(t *testing.T) {
	// These words are dispatched on the first word alone, so a goal worded as
	// exactly one of them cannot be planned. That limit is only acceptable while
	// the help says it out loud.
	var b strings.Builder
	fleetPlanHelp(&b)

	help := b.String()
	require.Contains(t, help, `"list", "ls", "show" and "validate" are read as subcommands`)
	require.Contains(t, help, "temporal-agents fleet plan validate <file|->")
	require.Contains(t, help, "does not read or write Postgres")
}
