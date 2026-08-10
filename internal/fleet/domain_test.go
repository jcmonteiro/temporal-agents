package fleet

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidatePlan_AcceptsWellFormedGraph(t *testing.T) {
	plan := FleetPlan{
		Goal: "expose the core",
		Nodes: []FleetNode{
			{ID: "core", Prompt: "implement the domain core"},
			{ID: "rest", Prompt: "expose via REST", DependsOn: []string{"core"}},
			{ID: "grpc", Prompt: "expose via gRPC", DependsOn: []string{"core"}},
		},
	}
	require.NoError(t, ValidatePlan(plan))
}

func TestValidatePlan_Rejects(t *testing.T) {
	cases := map[string]FleetPlan{
		"no nodes":          {Goal: "x"},
		"empty id":          {Nodes: []FleetNode{{ID: "  ", Prompt: "p"}}},
		"bad id characters": {Nodes: []FleetNode{{ID: "has space", Prompt: "p"}}},
		"duplicate id": {Nodes: []FleetNode{
			{ID: "a", Prompt: "p"}, {ID: "a", Prompt: "q"},
		}},
		"empty prompt": {Nodes: []FleetNode{{ID: "a", Prompt: ""}}},
		"unknown dependency": {Nodes: []FleetNode{
			{ID: "a", Prompt: "p", DependsOn: []string{"ghost"}},
		}},
		"self dependency": {Nodes: []FleetNode{
			{ID: "a", Prompt: "p", DependsOn: []string{"a"}},
		}},
		"cycle": {Nodes: []FleetNode{
			{ID: "a", Prompt: "p", DependsOn: []string{"b"}},
			{ID: "b", Prompt: "q", DependsOn: []string{"a"}},
		}},
	}
	for name, plan := range cases {
		t.Run(name, func(t *testing.T) {
			require.Error(t, ValidatePlan(plan))
		})
	}
}

func TestTopoLayers_HorizontalThenParallel(t *testing.T) {
	// The issue's motivating example: a horizontal slice (core) followed by two
	// parallel vertical slices (rest, grpc).
	plan := FleetPlan{
		Nodes: []FleetNode{
			{ID: "rest", Prompt: "p", DependsOn: []string{"core"}},
			{ID: "grpc", Prompt: "p", DependsOn: []string{"core"}},
			{ID: "core", Prompt: "p"},
		},
	}
	layers, err := TopoLayers(plan)
	require.NoError(t, err)
	require.Equal(t, [][]string{{"core"}, {"grpc", "rest"}}, layers)
}

func TestTopoLayers_Diamond(t *testing.T) {
	plan := FleetPlan{
		Nodes: []FleetNode{
			{ID: "a", Prompt: "p"},
			{ID: "b", Prompt: "p", DependsOn: []string{"a"}},
			{ID: "c", Prompt: "p", DependsOn: []string{"a"}},
			{ID: "d", Prompt: "p", DependsOn: []string{"b", "c"}},
		},
	}
	layers, err := TopoLayers(plan)
	require.NoError(t, err)
	require.Equal(t, [][]string{{"a"}, {"b", "c"}, {"d"}}, layers)
}

func TestTopoLayers_RejectsInvalidPlan(t *testing.T) {
	_, err := TopoLayers(FleetPlan{})
	require.Error(t, err)
}

func TestParseTokenTotal(t *testing.T) {
	require.Equal(t, 1234567, ParseTokenTotal("Total token usage across all sessions: 1,234,567 tokens."))
	require.Equal(t, 500, ParseTokenTotal("Develop step token usage: 500 tokens. The review..."))
	require.Equal(t, 0, ParseTokenTotal("no usage line here"))
}

func TestSummarizeFleet_ReportsStatusesTotalsAndPRLinks(t *testing.T) {
	results := []NodeResult{
		{ID: "core", Status: StatusSucceeded, Tokens: 1000,
			Detail: "PR #7 https://github.com/acme/widgets/pull/7 is open."},
		{ID: "rest", Status: StatusFailed, Tokens: 0, Detail: "boom"},
		{ID: "grpc", Status: StatusSkipped, Detail: "skipped: dependency \"core\" did not succeed"},
	}
	out := SummarizeFleet("expose the core", results)
	require.Contains(t, out, "Goal: expose the core")
	require.Contains(t, out, "core: succeeded")
	require.Contains(t, out, "https://github.com/acme/widgets/pull/7")
	require.Contains(t, out, "rest: failed")
	require.Contains(t, out, "grpc: skipped")
	// The blocking dependency is surfaced on the skip line (with the redundant
	// "skipped:" prefix trimmed since the status already says skipped).
	require.Contains(t, out, `grpc: skipped (dependency "core" did not succeed)`)
	require.Contains(t, out, "3 node(s): 1 succeeded, 1 failed, 0 blocked, 1 skipped.")
	require.Contains(t, out, "Develop-step token usage across all nodes: 1,000 tokens.")
}

func TestBuildPlanPrompt_IncludesGoalAndForbidsCodeChanges(t *testing.T) {
	p := BuildPlanPrompt("  add multi-tenant support  ")
	require.Contains(t, p, "add multi-tenant support")
	require.Contains(t, p, "Do NOT make any code changes")
	require.Contains(t, p, `"dependsOn"`)
}

// The two ways a plan document has been observed to go wrong are an omitted
// "dependsOn" (which silently turns a layered plan into a flat fan-out no
// validation can catch, because a fully parallel plan is legal) and an invented
// extra key (which the strict decoder refuses outright). Both are spelled out as
// rules, so the prompt states them rather than leaving them to be inferred from
// the shape block.
func TestBuildPlanPrompt_DemandsDependsOnEverywhereAndNoExtraKeys(t *testing.T) {
	p := BuildPlanPrompt("add multi-tenant support")
	require.Contains(t, p, `Include "dependsOn" on EVERY node`)
	require.Contains(t, p, "never leave the key out")
	require.Contains(t, p, "Output exactly the keys shown")
	require.Contains(t, p, "Any other key makes the plan unusable")
	require.Contains(t, p, "Nothing downstream can recover an edge you leave out")
}

// A schema shown only as a shape leaves the value types, the JSON escaping of a
// long prompt string, and the place for commentary unsaid. The last one matters
// most: an agent with something to qualify and nowhere to put it invents a key,
// which is how the observed run emitted "dependsOnNote".
func TestBuildPlanPrompt_DeclaresTypesEscapingAndWhereCommentaryGoes(t *testing.T) {
	p := BuildPlanPrompt("add multi-tenant support")
	require.Contains(t, p, `"dependsOn" is an array of strings`)
	require.Contains(t, p, `write ["core"], not "core"`)
	require.Contains(t, p, "There is no field for commentary")
	require.Contains(t, p, `escape a newline as \n`)
	require.Contains(t, p, "List DIRECT prerequisites only")
	// The shape block shows one node; the contract has to say that is not the
	// expected node count.
	require.Contains(t, p, "non-empty array of objects, one per slice")
}

func TestBuildPlanPrompt_RequiresTheAgentToValidateAndRepairItsExactAnswer(t *testing.T) {
	p := BuildPlanPrompt("add multi-tenant support")
	require.Contains(t, p, "MUST validate your exact candidate")
	require.Contains(t, p, `temporal-agents fleet plan validate "$candidate"`)
	require.Contains(t, p, "If validation fails, correct the candidate and run the command again")
	require.Contains(t, p, "Do not finish until validation exits successfully")
	require.Contains(t, p, "output the exact JSON that passed validation")
	require.Contains(t, p, "Do not include the validator output")
}

func TestBuildPlanPrompt_ExampleIsAPlanTheParserAccepts(t *testing.T) {
	p := BuildPlanPrompt("add multi-tenant support")

	// The example is the compact object embedded in the prompt.
	var example string
	for _, line := range strings.Split(p, "\n") {
		if strings.HasPrefix(line, `{"goal":`) {
			example = line
		}
	}
	require.NotEmpty(t, example, "the prompt must carry a worked example object")

	// An example the parser would reject teaches the wrong schema, so it goes
	// through the same gate as real agent output.
	plan, err := ParsePlan(example)
	require.NoError(t, err)
	require.NoError(t, ValidatePlan(plan))
	// It shows both dependency shapes: an explicit empty list and a real edge.
	require.Len(t, plan.Nodes, 2)
	require.Contains(t, example, `"dependsOn":[]`)
	require.Equal(t, []string{"pricing-domain"}, plan.Nodes[1].DependsOn)
}

func TestParsePlan_BareJSON(t *testing.T) {
	plan, err := ParsePlan(`{"goal":"g","nodes":[{"id":"a","prompt":"p"}]}`)
	require.NoError(t, err)
	require.Equal(t, "g", plan.Goal)
	require.Len(t, plan.Nodes, 1)
	require.Equal(t, "a", plan.Nodes[0].ID)
}

func TestParsePlan_ToleratesProseAndCodeFence(t *testing.T) {
	out := "Here is the plan:\n```json\n{\n  \"goal\": \"g\",\n  \"nodes\": [{\"id\": \"a\", \"prompt\": \"do {a}\", \"dependsOn\": []}]\n}\n```\nHope that helps!"
	plan, err := ParsePlan(out)
	require.NoError(t, err)
	require.Equal(t, "g", plan.Goal)
	require.Equal(t, "do {a}", plan.Nodes[0].Prompt)
}

func TestParsePlan_NoObject(t *testing.T) {
	_, err := ParsePlan("no json here")
	require.Error(t, err)
}

func TestNodeBranch_NamespacesNodeUnderRun(t *testing.T) {
	require.Equal(t, "fleet-abc-rest", NodeBranch("fleet-abc", "rest"))
}

func TestDependencyBranches_MapsAndSortsDependencies(t *testing.T) {
	node := FleetNode{ID: "api", DependsOn: []string{"grpc", "core"}}
	// Dependencies are mapped to their branches and returned in sorted order so a
	// dependent seeds deterministically regardless of DependsOn ordering.
	require.Equal(t,
		[]string{"fleet-abc-core", "fleet-abc-grpc"},
		DependencyBranches("fleet-abc", node))
}

func TestDependencyBranches_NoDependencies_ReturnsEmpty(t *testing.T) {
	require.Empty(t, DependencyBranches("fleet-abc", FleetNode{ID: "core"}))
}
