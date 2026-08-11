import { useEffect, useRef, useState, type ReactNode } from "react";
import { loadRun, type RunView } from "../../clients/runs";
import { anIntentToStart, startWork, type StartKind } from "../../clients/start";
import { ApiError } from "../../clients/http";
import { STATUS_LABEL } from "../../domain/work-item";
import { StatusDot } from "../../components/StatusDot";
import { Icon } from "../../components/Icon";
import { addressOf, goTo, OVERVIEW } from "../../platform/route";
import { SteeringButton } from "../../platform/steering";

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
        <dt>Started by</dt>
        <dd style={{ margin: 0, color: "var(--color-text)" }}>
          {view.startedBy ?? "Not started from the hub"}
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

      <SteeringButton itemId={runId} />
      <Instructions view={view} />
      <Repeat view={view} />
    </>
  );
}

/**
 * Which stored instruction the run resolved for each governed key.
 *
 * The text is not here because it is not published: the version is named, so a
 * page cannot show an instruction that has since been edited as though it were
 * the one that ran.
 */
function Instructions({ view }: { view: Extract<RunView, { known: true }> }): ReactNode {
  if (view.instructions.length === 0) return null;
  return (
    <section
      aria-labelledby="instructions-heading"
      style={{ display: "flex", flexDirection: "column", gap: 6 }}
    >
      <h2
        id="instructions-heading"
        style={{
          margin: 0,
          fontSize: "var(--font-size-xs)",
          letterSpacing: "0.08em",
          color: "var(--color-text-subtle)",
          textTransform: "uppercase",
          fontWeight: 600,
        }}
      >
        Instructions it ran under
      </h2>
      <ul style={{ margin: 0, padding: 0, listStyle: "none", display: "flex", flexDirection: "column", gap: 4 }}>
        {view.instructions.map((used) => (
          <li
            key={`${used.key}:${used.scope}:${used.version}`}
            style={{ fontSize: "var(--font-size-sm)", color: "var(--color-text-muted)" }}
          >
            <span style={{ color: "var(--color-text)" }}>{used.key}</span> · {used.scope} ·
            version {used.version}
          </li>
        ))}
      </ul>
    </section>
  );
}

/**
 * Running this run again.
 *
 * A repeat asks for exactly what the record holds — the same kind of pass, the
 * same instruction, the same place — and it invents nothing: a run whose place
 * was never recorded, or a develop pass whose instruction the record does not
 * hold, cannot be repeated, and is said so rather than started somewhere else or
 * with something else.
 */
function Repeat({ view }: { view: Extract<RunView, { known: true }> }): ReactNode {
  const [repeating, setRepeating] = useState(false);
  const [refusal, setRefusal] = useState<ApiError | Error | null>(null);
  // The identity of the intent to repeat: a second click, or a retry after a
  // refusal, is the same intent and must not start a second run.
  const intent = useRef<string | null>(null);

  const kind = repeatableKind(view.run.runType);
  const why = whyNotRepeatable(view, kind);

  const submit = async (): Promise<void> => {
    if (repeating || kind === null || why !== null) return;
    setRepeating(true);
    setRefusal(null);
    if (intent.current === null) intent.current = anIntentToStart();
    const started = await startWork({
      requestId: intent.current,
      kind,
      placeId: view.place?.id ?? "",
      prompt: kind === "develop" ? view.run.label : undefined,
    });
    if (started.ok) {
      intent.current = null;
      goTo({ name: "run", runId: started.value.runId });
    } else {
      setRefusal(started.error);
    }
    setRepeating(false);
  };

  return (
    <section style={{ display: "flex", flexDirection: "column", gap: 8, alignItems: "flex-start" }}>
      <button
        type="button"
        onClick={() => void submit()}
        disabled={repeating || why !== null}
        title={why ?? "Start the same work again"}
        style={{
          padding: "8px 14px",
          borderRadius: "var(--radius-sm)",
          border: "1px solid var(--color-border-strong)",
          background: "var(--color-surface-2)",
          color: "var(--color-text)",
          fontSize: "var(--font-size-sm)",
          opacity: repeating || why !== null ? 0.5 : 1,
        }}
      >
        {repeating ? "Starting…" : "Run this again"}
      </button>
      {why !== null && (
        <p
          role="status"
          style={{ margin: 0, color: "var(--color-text-subtle)", fontSize: "var(--font-size-sm)" }}
        >
          {why}
        </p>
      )}
      {refusal !== null && (
        <p
          role="alert"
          style={{
            margin: 0,
            display: "flex",
            gap: 8,
            color: "var(--status-failed)",
            fontSize: "var(--font-size-sm)",
          }}
        >
          {refusal instanceof ApiError && refusal.detail !== "" ? refusal.detail : refusal.message}
          {refusal instanceof ApiError && refusal.conflictingRunId !== "" && (
            <a
              href={addressOf({ name: "run", runId: refusal.conflictingRunId })}
              style={{ color: "inherit" }}
            >
              Show the run in the way
            </a>
          )}
        </p>
      )}
    </section>
  );
}

/** The kind of pass a recorded run type can be started again as, if any. */
function repeatableKind(runType: string | undefined): StartKind | null {
  if (runType === "develop" || runType === "review") return runType;
  return null;
}

/**
 * Why this run cannot be repeated, or nothing. Each answer names a fact the
 * record does not hold, because that is the only reason a repeat is refused
 * here — everything else is the server's to refuse.
 */
function whyNotRepeatable(
  view: Extract<RunView, { known: true }>,
  kind: StartKind | null,
): string | null {
  if (kind === null) {
    return `A ${view.run.runType ?? "run"} is not started from the hub, so it cannot be repeated here.`;
  }
  if (view.place === null || view.place.kind !== "directory") {
    return "Where this ran was never recorded, so it cannot be repeated: it would have to run somewhere else.";
  }
  if (kind === "develop" && view.run.label.trim() === "") {
    return "The record does not say what this run was told to do, so it cannot be repeated.";
  }
  return null;
}
