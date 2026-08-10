import type { ReactNode } from "react";
import { NotBuiltYet } from "../NotBuiltYet";

/**
 * One run.
 *
 * The route and the address exist now, so everything that knows a run can link
 * to it. What the page shows — the run's facts, its provenance, and repeating
 * it — is the work of a later slice, so the page names the run and says as
 * much rather than pretending to report on it.
 */
export function RunPage({ runId }: { runId: string }): ReactNode {
  return (
    <NotBuiltYet
      title="Run"
      says="This run has a page, but what it reports is not built yet."
      detail={
        <code style={{ color: "var(--color-text-subtle)" }}>{runId}</code>
      }
    />
  );
}
