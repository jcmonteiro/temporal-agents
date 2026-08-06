package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"temporal-agents/internal/execstore"
)

// historyCmd lists the durably recorded executions. It is the counterpart to
// `list`: `list` shows what Temporal is running right now, while `history` reads
// the record that outlives Temporal's retention and a reset of its state.
func historyCmd(args []string) {
	if wantsHelp(args) {
		historyHelp(os.Stdout)
		return
	}
	filter, err := parseHistoryFlags(args)
	if err != nil {
		fatalf("%v", err)
	}

	ctx := context.Background()
	store := openStore(ctx)
	defer store.Close()

	execs, err := store.ListExecutions(ctx, filter)
	if err != nil {
		fatalf("Could not read the execution history: %v", err)
	}
	fmt.Print(formatHistory(execs))
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

// historyRow is one printed line of history: either a recorded execution or a
// fleet node expanded from its parent's per-node breakdown.
type historyRow struct {
	// Kind labels the command type, or "fleet-node" for an expanded node.
	Kind string
	// Status is the recorded status.
	Status string
	// Started and Ended are the execution's timestamps; either may be zero (still
	// running, or an expanded node that never ran).
	Started time.Time
	Ended   time.Time
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
	rows := make([]historyRow, 0, len(execs))
	for _, e := range execs {
		rows = append(rows, historyRow{
			Kind:    string(e.Kind),
			Status:  string(e.Status),
			Started: e.StartedAt,
			Ended:   e.EndedAt,
			Tokens:  e.Tokens,
			ID:      e.WorkflowID,
			Note:    executionNote(e),
		})
		for _, n := range e.Detail.Nodes {
			if n.Status != string(execstore.StatusSkipped) {
				// A node that actually ran recorded itself as a develop execution, so
				// printing it from the parent's breakdown too would duplicate it.
				continue
			}
			rows = append(rows, historyRow{
				Kind:   "fleet-node",
				Status: n.Status,
				Tokens: n.Tokens,
				ID:     e.WorkflowID + "-" + n.ID,
				Note:   n.Detail,
			})
		}
	}
	return rows
}

// executionNote picks the most useful bit of context for a record's note column:
// why it failed, or which schedule fired it.
func executionNote(e execstore.Execution) string {
	if e.Detail.Error != "" {
		return truncate(firstLine(e.Detail.Error), 60)
	}
	if e.ScheduleID != "" {
		return "schedule " + e.ScheduleID
	}
	if e.Detail.PRURL != "" {
		return e.Detail.PRURL
	}
	return ""
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
	fmt.Fprintln(tw, "KIND\tSTATUS\tSTARTED\tENDED\tTOKENS\tWORKFLOW-ID\tNOTE")
	fmt.Fprintln(tw, "────\t──────\t───────\t─────\t──────\t───────────\t────")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Kind, r.Status, formatStamp(r.Started), formatStamp(r.Ended),
			groupThousands(r.Tokens), r.ID, r.Note)
	}
	tw.Flush()
	fmt.Fprintf(&b, "\n%d execution(s) · %s tokens\n", len(execs), groupThousands(sumTokens(execs)))
	return b.String()
}

// formatStamp renders a timestamp in local time, or "-" when it is unset (a
// still-running execution has no end time, and an expanded skipped node has
// neither).
func formatStamp(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
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

USAGE
  temporal-agents history [--kind <kind>] [--limit <n>] [--workflow-id <id>]
                          [--schedule-id <id>]

FLAGS
  --kind <kind>        Keep one command type: run, develop, review, pilot, fleet,
                       fleet-plan
  --limit <n>          How many executions to list (default 20)
  --workflow-id <id>   Show one execution and its children (a fleet run's nodes,
                       a develop run's review)
  --schedule-id <id>   Keep only the runs a schedule fired

EXAMPLES
  temporal-agents history
  temporal-agents history --kind develop --limit 50
  temporal-agents history --workflow-id fleet-9d0f…
  temporal-agents history --schedule-id schedule-9d0f…

Requires DATABASE_URL (see the README's Run section). Use "list" for the live
Temporal view of what is running right now.
`)
}
