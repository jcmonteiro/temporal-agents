# Capturing a replay fixture

The `testdata/*_before_recording.json` files in `internal/codereview` and
`internal/fleet` are real Temporal histories of the workflows **as they were before
a change**. They exist so a version gate (`wfrecord.Enabled`, and any gate added
after it) is proved by a test instead of by an in-flight execution failing
nondeterministically after a worker upgrade.

They must be re-captured whenever a recorded workflow's command sequence changes
shape without a version gate to protect the executions already in flight. The
harness that produced them is deliberately not part of the repository — it is a
throwaway — so this is the recipe.

## Recipe

1. **Check out the pre-change revision** in a scratch worktree. The fixture must be
   produced by code that does *not* yet issue the new commands:

   ```sh
   git worktree add /tmp/capture <revision>   # e.g. origin/main at df3f7a3
   ```

2. **Write a throwaway test in that worktree**, inside the package of the workflow
   being captured (`internal/codereview` or `internal/fleet`), that does the
   following.

   - Start a local server:

     ```go
     srv, err := testsuite.StartDevServer(ctx, testsuite.DevServerOptions{})
     c := srv.Client()
     ```

   - Start a worker on a task queue of its own, register the workflow under test,
     and register **stub activities under the real activity names**:

     ```go
     w.RegisterWorkflow(DevelopWorkflow)
     w.RegisterActivityWithOptions(
         func(ctx context.Context, in AgentInput) (AgentResult, error) { return AgentResult{…}, nil },
         activity.RegisterOptions{Name: "RunDevelopAgent"})
     ```

     The name is what the history records, so a stub registered under any other
     name produces a history the real activity struct cannot match. Take the names
     from the production bundle (or from `wftest.ActivityName`), never by hand.
     No agent, no git repository and no `DATABASE_URL` are needed: the
     pre-recording code writes no records, and every side effect is stubbed.

   - Execute the workflow with the input that produces the *shape* the fixture is
     meant to pin (a node whose child failed, a review pass that continues as new,
     a planning run with no plan handle, …), and let the stubs return the values
     that steer it there. Use a **stable workflow ID** — the existing fixtures use
     `<workflow>-fixture` — because the ID is part of what has to be restored at
     replay time (see the trap below).

   - Read the history back and write it with **protojson**, not `encoding/json`:

     ```go
     iter := c.GetWorkflowHistory(ctx, id, runID, false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
     hist := &historypb.History{}
     for iter.HasNext() {
         ev, err := iter.Next()
         …
         hist.Events = append(hist.Events, ev)
     }
     data, err := protojson.Marshal(hist)
     ```

     The SDK reads a fixture back with `client.HistoryFromJSON`, which expects
     protojson's field names and enum spellings; `encoding/json` produces a file it
     cannot parse.

   - For a looping workflow (review, pilot), capture the **first run only**: pass
     that run's ID, so the fixture ends in the continue-as-new that the gate must
     not disturb.

3. **Copy the file** into `internal/<package>/testdata/` in the real worktree and
   remove the scratch worktree (`git worktree remove /tmp/capture`).

4. **Replay it through `wftest.ReplayHistoryFile`** with the workflow ID it was
   captured under.

## The one trap

The replayer substitutes the workflow ID `ReplayId` unless the original execution
is supplied. A workflow that derives a child workflow ID from its own — develop
derives its review child, fleet derives one child per node — then produces a child
command the history cannot match, and the replay fails for the substitution instead
of for a real nondeterminism. That is why every fixture except the review one is
replayed through `wftest.ReplayHistoryFile`, which restores the captured ID.
