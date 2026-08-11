package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"temporal-agents/internal/execstore"
)

// openReader opens the read side of the execution store, returning the port and
// the function that releases it. It is a parameter of historyCmd rather than a
// direct call so the command reads through the port instead of the adapter, and so
// a test can put the in-memory fake in its place (see openExecutionReader).
type openReader func(context.Context) (execstore.ExecutionReader, func(), error)

// historyCmd lists the durably recorded executions. It is the counterpart to
// `list`: `list` reads the current Agent Hub overview, while `history` reads the
// record that outlives Temporal's retention and a reset of its state.
// It returns its failures instead of exiting, so the whole command — including
// the "DATABASE_URL is unset" contract — is reachable from a test; main turns the
// error into the exit.
//
// It prints to out rather than to os.Stdout for the same reason: what an operator
// sees is then assertable at the command level, instead of only through the
// formatting helpers underneath it.
func historyCmd(args []string, out io.Writer, open openReader) error {
	if wantsHelp(args) {
		historyHelp(out)
		return nil
	}
	filter, err := parseHistoryFlags(args)
	if err != nil {
		return err
	}

	ctx := context.Background()
	reader, release, err := open(ctx)
	if err != nil {
		return err
	}
	defer release()

	execs, err := reader.ListExecutions(ctx, filter)
	if err != nil {
		return fmt.Errorf("could not read the execution history: %w", err)
	}
	fmt.Fprint(out, formatHistory(execs))
	return nil
}

// parseHistoryFlags reads the history filters: --kind <kind> keeps one command
// type, --limit <n> caps the number of rows, --workflow-id <id> shows a single
// execution together with its children, and --schedule-id <id> keeps the runs one
// schedule fired. Each also accepts the --flag=value form.
func parseHistoryFlags(args []string) (execstore.Filter, error) {
	var f execstore.Filter
	value := func(i int, flag string) (string, int, error) {
		if a := args[i]; strings.HasPrefix(a, flag+"=") {
			v := strings.TrimPrefix(a, flag+"=")
			if v == "" {
				return "", i, fmt.Errorf("%s requires a value", flag)
			}
			return v, i, nil
		}
		if i+1 >= len(args) {
			return "", i, fmt.Errorf("%s requires a value", flag)
		}
		// A value that looks like a flag is a forgotten value, not an ID: accepting it
		// would make "--workflow-id --kind" quietly search for the workflow "--kind".
		if v := args[i+1]; strings.HasPrefix(v, "--") {
			return "", i, fmt.Errorf("%s requires a value, got the flag %q", flag, v)
		}
		return args[i+1], i + 1, nil
	}

	for i := 0; i < len(args); i++ {
		a := args[i]
		flag := a
		if eq := strings.IndexByte(a, '='); eq > 0 {
			flag = a[:eq]
		}
		var v string
		var err error
		switch flag {
		case "--kind":
			if v, i, err = value(i, flag); err != nil {
				return execstore.Filter{}, err
			}
			f.Kind = execstore.Kind(v)
			if !execstore.ValidKind(f.Kind) {
				return execstore.Filter{}, fmt.Errorf("unknown kind %q (try: %s)", v, strings.Join(kindNames(), ", "))
			}
		case "--limit":
			if v, i, err = value(i, flag); err != nil {
				return execstore.Filter{}, err
			}
			n, cerr := strconv.Atoi(v)
			if cerr != nil || n <= 0 {
				return execstore.Filter{}, fmt.Errorf("--limit requires a positive number, got %q", v)
			}
			if n > execstore.MaxListLimit {
				return execstore.Filter{}, fmt.Errorf("--limit is capped at %d, got %d", execstore.MaxListLimit, n)
			}
			f.Limit = n
		case "--workflow-id":
			if v, i, err = value(i, flag); err != nil {
				return execstore.Filter{}, err
			}
			f.WorkflowID = v
		case "--schedule-id":
			if v, i, err = value(i, flag); err != nil {
				return execstore.Filter{}, err
			}
			f.ScheduleID = v
		default:
			return execstore.Filter{}, fmt.Errorf("unexpected argument %q", a)
		}
	}
	return f, nil
}

// kindNames renders the recorded kinds for help and error text.
func kindNames() []string {
	kinds := execstore.Kinds()
	names := make([]string, 0, len(kinds))
	for _, k := range kinds {
		names = append(names, string(k))
	}
	return names
}

// skippedNodeKind labels a fleet node expanded from its parent's breakdown. It is
// deliberately not a kind name: only recorded kinds may appear in the KIND column
// as something `--kind` accepts, and a skipped node has no record of its own (a
// node that ran records itself as a develop row instead).
const skippedNodeKind = "node (skipped)"

// historyRow is one printed line of history: either a recorded execution or a
// fleet node expanded from its parent's per-node breakdown.
type historyRow struct {
	// Kind labels the command type, or skippedNodeKind for an expanded node.
	Kind string
	// Status is the recorded status.
	Status string
	// Started is when the execution began; it is zero for an expanded node, which
	// never ran.
	Started time.Time
	// Took is how long the execution took, or 0 when there is none to report yet: a
	// still-running execution, or an expanded node. It is reported instead of the end
	// stamp because "11m0s" is what an operator reads a settled row for, while the
	// exact end time of a row whose start is already printed adds little.
	Took time.Duration
	// Tokens is the row's own incremental token usage, so the printed total is a
	// true sum rather than a double-count of the fleet→node→review tree.
	Tokens int
	// ID is the workflow ID, or the node's would-be workflow ID for an expanded
	// node.
	ID string
	// Note carries extra context for the rightmost column: the schedule that fired
	// a run, why a node was skipped, or a failure's reason.
	Note string
}

// historyRows renders the recorded executions as printable rows, expanding each
// fleet parent's skipped nodes from its detail. A skipped node starts no child
// workflow, so it has no run ID and therefore no row of its own; expanding it
// here is what makes a skipped node visible in history at all. Expanded nodes
// follow their parent, so the newest-first order of real executions is preserved.
func historyRows(execs []execstore.Execution) []historyRow {
	// One row per record, plus one per skipped node. A parent's node count bounds its
	// expansion, so the slice is sized once instead of growing per fleet parent.
	capacity := len(execs)
	for _, e := range execs {
		capacity += len(e.Detail.Nodes)
	}
	rows := make([]historyRow, 0, capacity)
	for _, e := range execs {
		row := historyRow{
			Kind:    string(e.Kind),
			Status:  string(e.Status),
			Started: e.StartedAt,
			Tokens:  e.Tokens,
			ID:      e.WorkflowID,
			Note:    executionNote(e),
		}
		if !e.Running() {
			// An in-flight execution has not taken its final time yet, so it reports none
			// rather than a duration that grows every time the table is printed.
			row.Took = e.Duration()
		}
		rows = append(rows, row)
		for _, n := range e.Detail.Nodes {
			if n.Status != string(execstore.StatusSkipped) {
				// A node that actually ran recorded itself as a develop execution, so
				// printing it from the parent's breakdown too would duplicate it.
				continue
			}
			rows = append(rows, historyRow{
				Kind:   skippedNodeKind,
				Status: n.Status,
				Tokens: n.Tokens,
				ID:     e.WorkflowID + "-" + n.ID,
				// Shortened like every other note: a node's detail is bounded only at
				// wfrecord.MaxDetailText, so printing it whole would break the table the day a
				// skip reason grows past one short line.
				Note: truncate(firstLine(n.Detail), noteWidth),
			})
		}
	}
	return rows
}

// noteWidth is how much free text one note keeps. The note is a single column of a
// tabulated row, so text a record carries (a failure reason, an agent-written
// prompt) is shortened to keep the row on one line.
const noteWidth = 60

// noteSeparator joins the independent facts of one note.
const noteSeparator = " · "

// executionNote renders the context a record carries into its note column: which
// pass it is, whether the review converged, whether a pilot addressed comments, the
// plan it belongs to, why it failed, which schedule fired it, and the pull request
// it operated on. Every recorded field that describes a row is read here: a field
// no production path reads is not a record, it is weight.
//
// A failed scheduled run needs both of its facts: without the schedule the operator
// cannot see that a schedule keeps failing, and without the reason the row says
// nothing. A failure keeps the PR link for the same reason: a develop pipeline that
// failed *after* opening the PR is exactly the row an operator follows to the PR.
//
// The prompt is the fallback rather than a fact of its own, because it is long and a
// row with something more specific to say (it failed, it did not converge, it came
// from a plan) says that instead. But a plain run row is identified by nothing except
// "run-<uuid>", so without the prompt an operator cannot tell five runs apart.
func executionNote(e execstore.Execution) string {
	var parts []string
	if e.Detail.Pass > 0 {
		parts = append(parts, fmt.Sprintf("pass %d", e.Detail.Pass))
	}
	if e.Detail.Resets > 0 {
		parts = append(parts, fmt.Sprintf("budget reset %d time(s)", e.Detail.Resets))
	}
	if e.Detail.Ending != "" {
		parts = append(parts, "ended: "+e.Detail.Ending)
	} else if e.Detail.Converged != nil {
		parts = append(parts, convergedLabel(*e.Detail.Converged))
	}
	if e.Detail.Addressed != nil {
		parts = append(parts, addressedLabel(*e.Detail.Addressed))
	}
	if e.Detail.PlanID != "" {
		parts = append(parts, fmt.Sprintf("plan %s (%d node(s))", e.Detail.PlanID, e.Detail.PlanNodes))
	}
	switch {
	case e.Detail.Error != "":
		reason := truncate(firstLine(e.Detail.Error), noteWidth)
		if e.ScheduleID != "" {
			reason = "schedule " + e.ScheduleID + ": " + reason
		}
		parts = append(parts, reason)
	case e.ScheduleID != "":
		parts = append(parts, "schedule "+e.ScheduleID)
	}
	if e.Detail.PRURL != "" {
		parts = append(parts, e.Detail.PRURL)
	}
	if used := instructionsUsed(e.Detail.Instructions); used != "" {
		parts = append(parts, used)
	}
	// Nothing more specific to say: the prompt is what identifies the row.
	if len(parts) == 0 && e.Prompt != "" {
		parts = append(parts, truncate(firstLine(e.Prompt), noteWidth))
	}
	return strings.Join(parts, noteSeparator)
}

// instructionsUsed renders which instruction each governed key of an execution ran
// under: the key, where the value came from, which version it was, and the start of
// its content hash. It is what makes "which instruction produced this?" answerable
// from the history an operator already reads.
//
// It is not shortened like the free-text notes are. Every part of it is a structured
// value the tool produced itself, and a truncated version number or hash would name
// nothing at all — the same reason a pull request URL is printed whole.
//
// The scope is printed as its kind rather than as itself: a scope carries an
// absolute path, and this line is read for "where was this set", not for the
// machine's directory layout. The hash is shortened to its first bytes, which is
// enough to tell two instructions apart by eye; the whole hash stays in the record.
func instructionsUsed(uses []execstore.InstructionUse) string {
	if len(uses) == 0 {
		return ""
	}
	rendered := make([]string, 0, len(uses))
	for _, use := range uses {
		rendered = append(rendered, fmt.Sprintf("%s %s v%d %s",
			use.Key, scopeKind(use.Scope), use.Version, shortHash(use.Hash)))
	}
	return "instructions: " + strings.Join(rendered, ", ")
}

// scopeKind is the sort of scope a recorded value came from, read off the recorded
// scope itself. The record keeps the whole scope so a value can be found again; only
// what is printed is reduced.
func scopeKind(scope string) string {
	if kind, _, found := strings.Cut(scope, ":"); found {
		return kind
	}
	return scope
}

// shortHashLength is how much of a content hash one history line carries: enough to
// tell two instructions apart at a glance, short enough not to take the row over.
const shortHashLength = 8

// shortHash renders the start of a content hash, or nothing when there is none (an
// execution that used what its build ships before anything was published).
func shortHash(hash string) string {
	if len(hash) > shortHashLength {
		return hash[:shortHashLength]
	}
	return hash
}

// convergedLabel names how a review loop ended: because the agent found nothing
// left to change, or because it ran out of passes. Both states are printed, because
// "not converged" is the outcome an operator has to act on, and an absent label
// would read like a workflow that does not review at all.
func convergedLabel(converged bool) string {
	if converged {
		return "converged"
	}
	return "not converged"
}

// addressedLabel names whether a pilot pass actually changed anything in response
// to the review comments, printing both states for the same reason convergedLabel
// does.
func addressedLabel(addressed bool) string {
	if addressed {
		return "addressed comments"
	}
	return "no comments addressed"
}

// firstLine returns s up to its first newline, so a multi-line error stays on one
// tabulated row.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// formatHistory renders the recorded executions as a table, newest first, with a
// trailing count and token total. The total sums each row's own incremental
// usage, so it is a true total across a fleet→node→review tree rather than a
// double-count.
func formatHistory(execs []execstore.Execution) string {
	if len(execs) == 0 {
		return "No recorded executions yet.\n"
	}
	rows := historyRows(execs)

	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "KIND\tSTATUS\tSTARTED\tTOOK\tTOKENS\tWORKFLOW-ID\tNOTE")
	fmt.Fprintln(tw, "────\t──────\t───────\t────\t──────\t───────────\t────")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Kind, r.Status, formatStamp(r.Started), formatDuration(r.Took),
			groupThousands(r.Tokens), r.ID, r.Note)
	}
	tw.Flush()
	// The count is of recorded executions, which is not the number of printed lines:
	// a fleet parent's skipped nodes are expanded into rows of their own and have no
	// record. Both are reported so the table and its summary line agree.
	fmt.Fprintf(&b, "\n%d execution(s)", len(execs))
	if skipped := len(rows) - len(execs); skipped > 0 {
		fmt.Fprintf(&b, " · %d skipped node(s)", skipped)
	}
	fmt.Fprintf(&b, " · %s tokens\n", groupThousands(sumTokens(execs)))
	return b.String()
}

// formatStamp renders a timestamp in local time, or "-" when it is unset (an
// expanded skipped node has no start time, because it never ran).
func formatStamp(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

// formatDuration renders how long an execution took, rounded to the second, or
// "-" when there is nothing to report (still running, or a node that never ran).
// A sub-second execution is reported as such rather than as "0s", which would read
// like a missing value.
func formatDuration(d time.Duration) string {
	switch {
	case d <= 0:
		return "-"
	case d < time.Second:
		return "<1s"
	default:
		return d.Round(time.Second).String()
	}
}

// sumTokens totals the token usage of the given records. Every record carries
// only its own incremental usage, so a fleet run's rows (the parent, its nodes,
// and their reviews) sum to the run's real cost with nothing counted twice.
func sumTokens(execs []execstore.Execution) int {
	var total int
	for _, e := range execs {
		total += e.Tokens
	}
	return total
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

func historyHelp(w io.Writer) {
	fmt.Fprint(w, `temporal-agents history — list durably recorded executions

Every run, code and fleet workflow records itself in Postgres when it starts and
again when it settles, so the history outlives Temporal's retention (and a reset
of Temporal's own state). In-flight executions are listed too, with status
"running".

Each row reports only its own token usage, never an inclusive total, so the
printed total is a true sum across a fleet run's parent, its nodes and their
reviews.

The NOTE column carries what the row was: its pass number, whether a review
converged or a pilot addressed comments, the plan a fleet run came from, the
schedule that fired it, the pull request it operated on, and why it failed. A row
with nothing more specific to say shows its prompt, which is what tells one run
from another.

A row that ran under stored instructions also names them, as
"<key> <scope> v<version> <hash>": which instruction, whether it came from the
place, the installation or the shipped default, which version of it, and the start
of its content hash. That is what makes a past result explainable — the version it
names still reads as it did, however the instruction was edited since.

USAGE
  temporal-agents history [--kind <kind>] [--limit <n>] [--workflow-id <id>]
                          [--schedule-id <id>]

A fleet node is recorded as a develop execution carrying its fleet run as its
parent, so "--kind develop" lists nodes together with standalone develop runs, and
"--workflow-id <fleet-id>" lists one fleet run with its nodes. A node that ended
blocked (an unresolved seed conflict, or a review that did not converge) is a
failed develop row; the parent fleet run's breakdown holds the "blocked" wording.
A node that was skipped never started a workflow, so it has no record of its own
and is listed under its parent as "node (skipped)".

A row stays "running" until its workflow settles, and every write is made by the
workflow itself. A terminated workflow, or a worker that never comes back,
therefore leaves its row at "running" for good: treat a "running" row that is
hours old as abandoned and check "list" for the live Agent Hub view.

FLAGS
  --kind <kind>        Keep one command type: run, develop, review, pilot, fleet,
                       fleet-plan (a fleet node is a develop record)
  --limit <n>          How many executions to list (default 20, at most 1000)
  --workflow-id <id>   Show one execution and its children (a fleet run's nodes,
                       a develop run's review)
  --schedule-id <id>   Keep only the runs a schedule fired

EXAMPLES
  temporal-agents history
  temporal-agents history --kind develop --limit 50
  temporal-agents history --workflow-id fleet-9d0f…
  temporal-agents history --schedule-id schedule-9d0f…

Requires DATABASE_URL (see the README's Run section). Use "list" for the live
Agent Hub view of active top-level work.
`)
}
