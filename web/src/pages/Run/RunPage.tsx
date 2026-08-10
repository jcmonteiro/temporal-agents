import { useEffect, useState, type ReactNode } from "react";
import { loadRun, type RunView } from "../../clients/runs";
import { STATUS_LABEL } from "../../domain/work-item";
import { StatusDot } from "../../components/StatusDot";
import { Icon } from "../../components/Icon";
import { addressOf, OVERVIEW } from "../../platform/route";

// A run is live while it runs, so the page polls on the same cadence as the rest
// of the hub.
const REFRESH_INTERVAL_MS = 5_000;

// How long a run that the read path does not list yet is called "starting".
// A start returns before the orchestrator's state is observable, so the first
// reads answer "no such run" for work that is beginning perfectly well. After
// this long it is no longer a delay, and saying so is more honest than a page
// that waits forever.
const STARTING_GRACE_MS = 60_000;

// `view` holds the last successful read and `error` the last failure, so a failed
// refresh reports itself without emptying the page.
interface State {
  view: RunView | null;
  error: string | null;
  /** How long the hub has been asked about a run it does not list. */
  waitedMs: number;
}

/**
 * One run: what it is, where it runs, and how it stands.
 */
export function RunPage({ runId }: { runId: string }): ReactNode {
  const [state, setState] = useState<State>({ view: null, error: null, waitedMs: 0 });

  useEffect(() => {
    let cancelled = false;
    setState({ view: null, error: null, waitedMs: 0 });
    const refresh = async (): Promise<void> => {
      const result = await loadRun(runId);
      if (cancelled) return;
      setState((previous) => ({
        view: result.ok ? result.value : previous.view,
        error: result.ok ? null : result.error.message,
        waitedMs:
          result.ok && result.value.known
            ? 0
            : previous.waitedMs + REFRESH_INTERVAL_MS,
      }));
    };
    void refresh();
    const timer = setInterval(() => void refresh(), REFRESH_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [runId]);

  const { view, error, waitedMs } = state;
  return (
    <main
      style={{
        flex: 1,
        minWidth: 0,
        overflowY: "auto",
        padding: "var(--space-5)",
        display: "flex",
        flexDirection: "column",
        gap: "var(--space-4)",
      }}
    >
      <a
        href={addressOf(OVERVIEW)}
        style={{
          color: "var(--color-text-muted)",
          fontSize: "var(--font-size-sm)",
          textDecoration: "none",
        }}
      >
        ← Back to the overview
      </a>

      {error !== null && (
        <p role="status" style={{ margin: 0, color: "var(--status-failed)" }}>
          The Agent Hub API could not be reached: {error}
        </p>
      )}
      {view === null && error === null && (
        <p role="status" style={{ margin: 0, color: "var(--color-text-muted)" }}>
          Loading this run…
        </p>
      )}
      {view?.known === false &&
        (waitedMs < STARTING_GRACE_MS ? <Starting runId={runId} /> : <Missing runId={runId} />)}
      {view?.known === true && <Report runId={runId} view={view} />}
    </main>
  );
}

/**
 * The run has been started and the hub has not seen it yet.
 *
 * This is emphatically not an error: the operator has just started this work,
 * and telling them it does not exist is telling them their click failed.
 */
function Starting({ runId }: { runId: string }): ReactNode {
  return (
    <div role="status" style={{ display: "flex", flexDirection: "column", gap: 8 }}>
      <h1 style={{ margin: 0, fontSize: "var(--font-size-xl)", fontWeight: 600 }}>
        Starting…
      </h1>
      <p style={{ margin: 0, color: "var(--color-text-muted)" }}>
        The work has been submitted. It appears here as soon as the orchestrator
        reports it.
      </p>
      <code style={{ color: "var(--color-text-subtle)" }}>{runId}</code>
    </div>
  );
}

/** The hub does not know this run, and has waited long enough to say so. */
function Missing({ runId }: { runId: string }): ReactNode {
  return (
    <div role="status" style={{ display: "flex", flexDirection: "column", gap: 8 }}>
      <h1 style={{ margin: 0, fontSize: "var(--font-size-xl)", fontWeight: 600 }}>
        No such run
      </h1>
      <p style={{ margin: 0, color: "var(--color-text-muted)" }}>
        The hub knows no run {runId}. It never started, or the orchestrator no
        longer keeps it and nothing was recorded for it.
      </p>
    </div>
  );
}

/** What is known about a run that the hub reports. */
function Report({
  runId,
  view,
}: {
  runId: string;
  view: Extract<RunView, { known: true }>;
}): ReactNode {
  const { run, place } = view;
  return (
    <>
      <header style={{ display: "flex", flexDirection: "column", gap: 6 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <Icon name={run.icon} size={20} />
          <h1 style={{ margin: 0, fontSize: "var(--font-size-xl)", fontWeight: 600 }}>
            {run.label}
          </h1>
        </div>
        <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
          <StatusDot status={run.status} />
          <span
            style={{ color: "var(--color-text-muted)", fontSize: "var(--font-size-sm)" }}
          >
            {STATUS_LABEL[run.status]}
          </span>
        </div>
      </header>

      <dl
        style={{
          margin: 0,
          display: "grid",
          gridTemplateColumns: "auto 1fr",
          columnGap: 16,
          rowGap: 8,
          fontSize: "var(--font-size-sm)",
          color: "var(--color-text-muted)",
        }}
      >
        <dt>Run</dt>
        <dd style={{ margin: 0, color: "var(--color-text)" }}>{runId}</dd>
        <dt>Type</dt>
        <dd style={{ margin: 0, color: "var(--color-text)" }}>{run.runType ?? "run"}</dd>
        <dt>Place</dt>
        <dd style={{ margin: 0, color: "var(--color-text)" }}>
          {place ? (
            <a
              href={addressOf({ name: "place", placeId: place.id })}
              style={{ color: "inherit" }}
            >
              {place.label}
            </a>
          ) : (
            "Where this work runs was not recorded"
          )}
        </dd>
        <dt>Started</dt>
        <dd style={{ margin: 0, color: "var(--color-text)" }}>
          {view.startedAt ?? "—"}
        </dd>
        <dt>Ended</dt>
        <dd style={{ margin: 0, color: "var(--color-text)" }}>
          {view.endedAt ?? "Still running"}
        </dd>
        {typeof run.iterations === "number" && (
          <>
            <dt>Iterations</dt>
            <dd style={{ margin: 0, color: "var(--color-text)" }}>{run.iterations}</dd>
          </>
        )}
        {typeof view.tokens === "number" && view.tokens > 0 && (
          <>
            <dt>Tokens</dt>
            <dd style={{ margin: 0, color: "var(--color-text)" }}>{view.tokens}</dd>
          </>
        )}
      </dl>
    </>
  );
}
