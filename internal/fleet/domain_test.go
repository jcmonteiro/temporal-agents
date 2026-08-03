package fleet

import (
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
	require.Contains(t, out, "3 node(s): 1 succeeded, 1 failed, 1 skipped.")
	require.Contains(t, out, "Develop-step token usage across all nodes: 1,000 tokens.")
}

func TestBuildPlanPrompt_IncludesGoalAndForbidsCodeChanges(t *testing.T) {
	p := BuildPlanPrompt("  add multi-tenant support  ")
	require.Contains(t, p, "add multi-tenant support")
	require.Contains(t, p, "Do NOT make any code changes")
	require.Contains(t, p, `"dependsOn"`)
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
