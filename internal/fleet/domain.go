// Package fleet implements fan-out orchestration: a parent "fleet" workflow
// that decomposes a larger change into a dependency graph of features and runs
// a child develop workflow per feature, respecting the graph so a dependent
// feature only starts once every feature it depends on has succeeded.
//
// Dependencies gate execution *ordering*, not code layering: every node
// develops on its own branch/worktree cut from the same repository base, so a
// node does not automatically inherit the commits of the nodes it depends on.
// An edge therefore sequences work (and skips a dependent when a prerequisite
// fails) rather than stacking one node's code on top of another's. Author each
// node's prompt as a self-contained instruction.
//
// It follows the same hexagonal split as the codereview package: this file
// holds the application core (pure domain types and logic — plan validation,
// dependency-graph ordering, prompt building, plan parsing, and result
// aggregation), while workflow.go orchestrates and activities.go/ports.go wire
// in the driven adapters (the Pi agent) from the edges. The orchestration reuses
// the existing codereview.DevelopWorkflow for each node rather than
// reimplementing the develop pipeline.
package fleet

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// FleetNode is one feature in a fleet plan: an isolated unit of work handed to
// a child develop workflow, plus the IDs of the features it must run after.
type FleetNode struct {
	// ID uniquely identifies the node within its plan. It must be a short slug
	// (letters, digits, '-', '_') so it can be embedded verbatim in a child
	// workflow ID.
	ID string `json:"id"`
	// Prompt is the instruction handed to the child develop workflow for this
	// node.
	Prompt string `json:"prompt"`
	// DependsOn lists the IDs of nodes that must complete successfully before
	// this node starts. An edge A in B.DependsOn means "B runs after A": it
	// sequences B after A (and skips B if A does not succeed), but B still
	// develops from the repository base without A's commits, so the ordering does
	// not stack B's code on top of A's.
	DependsOn []string `json:"dependsOn,omitempty"`
}

// FleetPlan is a dependency graph of features: the approved, deterministic
// prescription a fleet execute run orchestrates.
type FleetPlan struct {
	// Goal is the original high-level prompt the plan decomposes.
	Goal string `json:"goal"`
	// Nodes are the features and their dependencies.
	Nodes []FleetNode `json:"nodes"`
}

// nodeIDPattern constrains node IDs to a filesystem/workflow-id-safe slug so an
// ID can be embedded verbatim into a child workflow ID and (indirectly) a
// branch name without escaping.
var nodeIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ValidatePlan checks that a plan is a well-formed, acyclic dependency graph:
// it has at least one node, every ID is a unique non-empty slug, and every
// DependsOn edge references an existing node without self-loops or cycles. It
// returns a descriptive error for the first problem found so a plan can be
// rejected before any child workflow is started.
func ValidatePlan(plan FleetPlan) error {
	if len(plan.Nodes) == 0 {
		return fmt.Errorf("plan has no nodes")
	}
	seen := make(map[string]bool, len(plan.Nodes))
	for _, n := range plan.Nodes {
		if strings.TrimSpace(n.ID) == "" {
			return fmt.Errorf("node has an empty id")
		}
		if !nodeIDPattern.MatchString(n.ID) {
			return fmt.Errorf("node id %q must contain only letters, digits, '-' or '_'", n.ID)
		}
		if seen[n.ID] {
			return fmt.Errorf("duplicate node id %q", n.ID)
		}
		if strings.TrimSpace(n.Prompt) == "" {
			return fmt.Errorf("node %q has an empty prompt", n.ID)
		}
		seen[n.ID] = true
	}
	for _, n := range plan.Nodes {
		for _, dep := range n.DependsOn {
			if dep == n.ID {
				return fmt.Errorf("node %q depends on itself", n.ID)
			}
			if !seen[dep] {
				return fmt.Errorf("node %q depends on unknown node %q", n.ID, dep)
			}
		}
	}
	if cycle := findCycle(plan); cycle != "" {
		return fmt.Errorf("plan has a dependency cycle: %s", cycle)
	}
	return nil
}

// findCycle returns a human-readable rendering of a dependency cycle, or "" when
// the graph is acyclic. It runs a depth-first search coloring nodes white
// (unvisited), gray (on the current path), and black (fully explored); an edge
// into a gray node closes a cycle.
func findCycle(plan FleetPlan) string {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	deps := make(map[string][]string, len(plan.Nodes))
	for _, n := range plan.Nodes {
		deps[n.ID] = n.DependsOn
	}
	color := make(map[string]int, len(plan.Nodes))
	var stack []string
	var visit func(id string) []string
	visit = func(id string) []string {
		color[id] = gray
		stack = append(stack, id)
		for _, dep := range deps[id] {
			switch color[dep] {
			case gray:
				// Found a back edge; render the cycle from where dep first appears.
				for i, s := range stack {
					if s == dep {
						return append(append([]string{}, stack[i:]...), dep)
					}
				}
			case white:
				if c := visit(dep); c != nil {
					return c
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = black
		return nil
	}
	// Visit in the plan's node order so the reported cycle is deterministic.
	for _, n := range plan.Nodes {
		if color[n.ID] == white {
			if c := visit(n.ID); c != nil {
				return strings.Join(c, " -> ")
			}
		}
	}
	return ""
}

// TopoLayers groups node IDs into dependency layers: layer i contains every node
// whose longest dependency chain has length i, so all nodes in a layer can run
// in parallel and every layer depends only on earlier ones. Within a layer IDs
// are sorted for a deterministic execution order. It assumes a valid, acyclic
// plan (see ValidatePlan) and returns an error otherwise.
func TopoLayers(plan FleetPlan) ([][]string, error) {
	if err := ValidatePlan(plan); err != nil {
		return nil, err
	}
	deps := make(map[string][]string, len(plan.Nodes))
	for _, n := range plan.Nodes {
		deps[n.ID] = n.DependsOn
	}
	depth := make(map[string]int, len(plan.Nodes))
	var compute func(id string) int
	compute = func(id string) int {
		if d, ok := depth[id]; ok {
			return d
		}
		max := -1
		for _, dep := range deps[id] {
			if d := compute(dep); d > max {
				max = d
			}
		}
		d := max + 1
		depth[id] = d
		return d
	}
	maxDepth := 0
	for _, n := range plan.Nodes {
		if d := compute(n.ID); d > maxDepth {
			maxDepth = d
		}
	}
	layers := make([][]string, maxDepth+1)
	for _, n := range plan.Nodes {
		d := depth[n.ID]
		layers[d] = append(layers[d], n.ID)
	}
	for i := range layers {
		sort.Strings(layers[i])
	}
	return layers, nil
}

// NodeStatus is the terminal outcome of a node within a fleet run.
type NodeStatus string

const (
	// StatusSucceeded means the node's child develop workflow completed without
	// error.
	StatusSucceeded NodeStatus = "succeeded"
	// StatusFailed means the node's child develop workflow returned an error.
	StatusFailed NodeStatus = "failed"
	// StatusSkipped means the node never ran because one of its dependencies did
	// not succeed, so running a node sequenced after a failed prerequisite would
	// be pointless.
	StatusSkipped NodeStatus = "skipped"
)

// NodeResult is the aggregated outcome of a single node, collected by the fleet
// workflow for the summary notification.
type NodeResult struct {
	// ID is the node this result belongs to.
	ID string
	// Status is the node's terminal outcome.
	Status NodeStatus
	// Detail is the child workflow's returned summary (on success) or the error
	// text (on failure); for a skipped node it names the dependency that blocked
	// it.
	Detail string
	// Tokens is the token usage parsed out of the child's summary, or 0 when the
	// node did not run or reported none.
	Tokens int
}

// tokenTotalPattern matches the token-usage line the develop/review/pilot
// summaries append (see codereview.FormatTokenTotal), e.g.
// "Total token usage across all sessions: 1,234 tokens." and the develop-step
// variant "Develop step token usage: 1,234 tokens." The captured group is the
// comma-grouped number.
//
// In both fleet modes the child DevelopWorkflow returns only its develop-step
// usage: the review loop is an abandoned child (default mode) or reports its own
// total (--with-remote), so the number parsed here is always the develop step's,
// which is why SummarizeFleet labels the aggregate as develop-step usage. This
// is a hidden coupling to codereview's human summary wording; a structured token
// count would be more robust but would change DevelopWorkflow's string result
// type and every caller, so the regex is kept deliberately.
var tokenTotalPattern = regexp.MustCompile(`token usage[^:]*:\s*([\d,]+)\s*tokens`)

// ParseTokenTotal extracts the token count reported in a child workflow's
// summary line, returning 0 when the summary carries no such line. It lets the
// fleet aggregate per-child usage into a single total without threading a
// structured token count through the child workflow's string result.
func ParseTokenTotal(summary string) int {
	m := tokenTotalPattern.FindStringSubmatch(summary)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(strings.ReplaceAll(m[1], ",", ""))
	if err != nil {
		return 0
	}
	return n
}

// SummarizeFleet renders the single aggregated summary for a fleet run: the
// goal, a per-node status line (with any PR link surfaced from a succeeded
// child's detail, or the blocking dependency surfaced for a skipped node), and
// the develop-step token usage summed across every node. Only the
// develop step's usage is aggregated because that is all a child DevelopWorkflow
// returns to the fleet (see tokenTotalPattern); the review and pilot stages
// report their own totals separately. Results are rendered in the given order
// (the execution order the workflow collected them in).
func SummarizeFleet(goal string, results []NodeResult) string {
	var b strings.Builder
	b.WriteString("Fleet run complete.\n")
	if g := strings.TrimSpace(goal); g != "" {
		b.WriteString("Goal: ")
		b.WriteString(g)
		b.WriteString("\n")
	}
	var succeeded, failed, skipped, total int
	b.WriteString("\nPer-node status:\n")
	for _, r := range results {
		total += r.Tokens
		switch r.Status {
		case StatusSucceeded:
			succeeded++
		case StatusFailed:
			failed++
		case StatusSkipped:
			skipped++
		}
		b.WriteString(fmt.Sprintf("  - %s: %s", r.ID, r.Status))
		switch {
		case r.Status == StatusSkipped && strings.TrimSpace(r.Detail) != "":
			// Surface why the node was skipped (its blocking dependency) so the
			// reader does not have to reconstruct the graph. The redundant
			// "skipped: " prefix the workflow records is trimmed since the status
			// already says "skipped".
			detail := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(r.Detail), "skipped:"))
			b.WriteString(" (")
			b.WriteString(detail)
			b.WriteString(")")
		default:
			if url := extractPRURL(r.Detail); url != "" {
				b.WriteString(" (")
				b.WriteString(url)
				b.WriteString(")")
			}
		}
		b.WriteString("\n")
	}
	b.WriteString(fmt.Sprintf("\n%d node(s): %d succeeded, %d failed, %d skipped.\n",
		len(results), succeeded, failed, skipped))
	b.WriteString(fmt.Sprintf("Develop-step token usage across all nodes: %s tokens. The review and pilot stages report their own token totals separately.", groupThousands(total)))
	return b.String()
}

// prURLPattern matches a GitHub pull-request URL embedded in a child's summary
// so SummarizeFleet can surface the PR link per node.
var prURLPattern = regexp.MustCompile(`https://github\.com/[^\s)]+/pull/\d+`)

// extractPRURL returns the first GitHub PR URL found in detail, or "".
func extractPRURL(detail string) string {
	return prURLPattern.FindString(detail)
}

// groupThousands formats n with comma thousands separators (1234567 ->
// "1,234,567").
func groupThousands(n int) string {
	s := strconv.Itoa(n)
	neg := ""
	if n < 0 {
		neg, s = "-", s[1:]
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return neg + b.String()
}

// BuildPlanPrompt renders the instruction handed to the Pi agent to decompose a
// high-level goal into a dependency graph. It asks for a strict JSON object
// matching FleetPlan so ParsePlan can read the agent's final message, and steers
// the decomposition toward small, independently reviewable slices with explicit
// dependencies.
func BuildPlanPrompt(goal string) string {
	return `Decompose the software change described below into a dependency graph of small, independently reviewable slices of work (a "fleet plan"). Prefer a horizontal slice that establishes shared/domain foundations first, followed by vertical slices, and use dependencies to order the foundational slice ahead of the slices that logically build on it.

IMPORTANT: dependencies control execution ORDER only. Each slice is developed on its own branch cut from the current repository base and does NOT automatically include the code produced by the slices it depends on. So every "prompt" must be a complete, standalone instruction that a coding agent can implement on its own branch from the base, without relying on another slice's uncommitted work being present. Use "dependsOn" only to sequence slices (e.g. so a foundation is reviewed and lands first) and to skip a slice when a prerequisite fails.

Do NOT make any code changes. Read the referenced code and relevant in-repo documentation to inform the decomposition, then output ONLY a single JSON object (no prose, no code fences) matching exactly this shape:

{
  "goal": "<restate the overall goal in one sentence>",
  "nodes": [
    {
      "id": "<short-slug-unique-id>",
      "prompt": "<self-contained instruction for this slice>",
      "dependsOn": ["<id-of-a-slice-this-must-run-after>"]
    }
  ]
}

Rules:
- Each "id" must be a unique short slug using only letters, digits, '-' or '_'.
- "dependsOn" lists the ids of slices that must succeed before this one starts; omit or use [] when the slice has no prerequisites.
- The graph must be acyclic.
- Each "prompt" must be a complete, standalone instruction that a coding agent can implement on its own branch from the repository base.

--- Goal ---
` + strings.TrimSpace(goal)
}

// ParsePlan extracts and parses the FleetPlan JSON object from the Pi agent's
// output. The agent is asked for bare JSON, but ParsePlan tolerates surrounding
// prose or a ```json code fence by extracting the outermost { ... } object. It
// does not validate the graph; callers pair it with ValidatePlan.
//
// The prompt requires the exact FleetPlan schema, so parsing rejects unknown
// fields (DisallowUnknownFields): a near-miss such as "depends_on" instead of
// "dependsOn" fails planning here rather than silently dropping the dependency
// and letting a node run before its prerequisites.
func ParsePlan(output string) (FleetPlan, error) {
	raw := extractJSONObject(output)
	if raw == "" {
		return FleetPlan{}, fmt.Errorf("no JSON object found in agent output")
	}
	var plan FleetPlan
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&plan); err != nil {
		return FleetPlan{}, fmt.Errorf("parse plan JSON: %w", err)
	}
	return plan, nil
}

// extractJSONObject returns the substring from the first '{' to its matching
// '}', ignoring braces inside JSON strings, or "" when no balanced object is
// found. This isolates the plan object from any surrounding prose or code fence
// the agent may add despite the instructions.
func extractJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}
