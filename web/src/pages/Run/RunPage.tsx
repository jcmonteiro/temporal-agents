import { useEffect, useRef, useState, type ReactNode } from "react";
import { loadRun, type RunView } from "../../clients/runs";
import { anIntentToStart, startWork, type StartKind } from "../../clients/start";
import { ApiError } from "../../clients/http";
import { STATUS_LABEL } from "../../domain/work-item";
import { StatusDot } from "../../components/StatusDot";
import { Icon } from "../../components/Icon";
import { addressOf, goTo, OVERVIEW } from "../../platform/route";
import { SteeringButton } from "../../platform/steering";
import "./run.css";

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

/** One run: what it is, where it runs, and how it stands. */
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
    <main className="run-page">
      <div className="run-page__content">
        <nav className="run-page__breadcrumb" aria-label="Breadcrumb">
          <a href={addressOf(OVERVIEW)}>
            <span aria-hidden="true">←</span> Back to overview
          </a>
        </nav>

        {error !== null && (
          <div className="ui-feedback ui-feedback--error run-page__feedback" role="status">
            <strong>Run information unavailable</strong>
            <span>The Agent Hub API could not be reached: {error}</span>
          </div>
        )}
        {view === null && error === null && <Loading />}
        {view?.known === false &&
          (waitedMs < STARTING_GRACE_MS ? <Starting runId={runId} /> : <Missing runId={runId} />)}
        {view?.known === true && <Report runId={runId} view={view} />}
      </div>
    </main>
  );
}

function Loading(): ReactNode {
  return (
    <div className="run-state ui-surface" role="status">
      <span className="run-state__spinner" aria-hidden="true" />
      <div>
        <p className="ui-kicker">Run details</p>
        <h1>Loading this run…</h1>
        <p>Reading its current state and operational record.</p>
      </div>
    </div>
  );
}

/** The run has been started and the hub has not seen it yet. */
function Starting({ runId }: { runId: string }): ReactNode {
  return (
    <div className="run-state ui-surface" role="status">
      <span className="run-state__orbit" aria-hidden="true" />
      <div>
        <p className="ui-kicker">Submission accepted</p>
        <h1>Starting…</h1>
        <p>
          The work has been submitted. It appears here as soon as the orchestrator
          reports it.
        </p>
        <code>{runId}</code>
      </div>
    </div>
  );
}

/** The hub does not know this run, and has waited long enough to say so. */
function Missing({ runId }: { runId: string }): ReactNode {
  return (
    <div className="run-state run-state--missing ui-surface" role="status">
      <span className="run-state__mark" aria-hidden="true">?</span>
      <div>
        <p className="ui-kicker">Unavailable</p>
        <h1>No such run</h1>
        <p>
          The hub knows no run <code>{runId}</code>. It never started, or the
          orchestrator no longer keeps it and nothing was recorded for it.
        </p>
      </div>
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
  const statusLabel = STATUS_LABEL[run.status];
  return (
    <article className="run-report">
      <header className="run-hero ui-surface">
        <div className="run-hero__identity">
          <span className="run-hero__icon" aria-hidden="true">
            <Icon name={run.icon} size={22} />
          </span>
          <div>
            <p className="ui-eyebrow">{run.runType ?? "Run"} run</p>
            <h1>{run.label || "Untitled run"}</h1>
            <code className="run-hero__id">{runId}</code>
          </div>
        </div>
        <div
          className={`ui-status ui-status--${run.status} run-hero__status`}
          role="status"
          aria-label={`Run status: ${statusLabel}`}
        >
          <StatusDot status={run.status} size={8} filled />
          {statusLabel}
        </div>
        <div className="run-hero__summary" aria-label="Run summary">
          <SummaryItem label="Place">
            {place ? (
              <a href={addressOf({ name: "place", placeId: place.id, category: "overview" })}>
                  {place.label}
                </a>
            ) : (
              "Not recorded"
            )}
          </SummaryItem>
          <SummaryItem label="Started">{view.startedAt ?? "Not recorded"}</SummaryItem>
          <SummaryItem label="Ended">{view.endedAt ?? "Still running"}</SummaryItem>
        </div>
      </header>

      <div className="run-report__layout">
        <div className="run-report__main">
          <section
            className="run-section ui-surface"
            aria-labelledby="operational-details-heading"
          >
            <SectionHeader
              kicker="Execution record"
              heading="Operational details"
              id="operational-details-heading"
            />
            <dl className="run-details">
              <Detail label="Run"><code>{runId}</code></Detail>
              <Detail label="Type">{run.runType ?? "run"}</Detail>
              <Detail label="Started by">{view.startedBy ?? "Not started from the hub"}</Detail>
              {typeof run.iterations === "number" && (
                <Detail label="Iterations">{run.iterations}</Detail>
              )}
              {typeof view.tokens === "number" && view.tokens > 0 && (
                <Detail label="Tokens">{view.tokens}</Detail>
              )}
            </dl>
          </section>

          <Instructions view={view} />
        </div>

        <section
          className="run-actions ui-surface"
          aria-labelledby="available-actions-heading"
        >
          <SectionHeader
            kicker="Next step"
            heading="Available actions"
            id="available-actions-heading"
          />
          <p className="run-actions__intro">
            Guide work that needs input or start another run from this record.
          </p>
          <div className="run-actions__steering">
            <SteeringButton itemId={runId} />
          </div>
          <Repeat view={view} />
        </section>
      </div>
    </article>
  );
}

function SummaryItem({ label, children }: { label: string; children: ReactNode }): ReactNode {
  return (
    <div>
      <span>{label}</span>
      <strong>{children}</strong>
    </div>
  );
}

function SectionHeader({
  kicker,
  heading,
  id,
}: {
  kicker: string;
  heading: string;
  id: string;
}): ReactNode {
  return (
    <header className="run-section__header">
      <p className="ui-kicker">{kicker}</p>
      <h2 id={id}>{heading}</h2>
    </header>
  );
}

function Detail({ label, children }: { label: string; children: ReactNode }): ReactNode {
  return (
    <div>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  );
}

/** Which stored instruction the run resolved for each governed key. */
function Instructions({ view }: { view: Extract<RunView, { known: true }> }): ReactNode {
  if (view.instructions.length === 0) return null;
  return (
    <section className="run-section ui-surface" aria-labelledby="instructions-heading">
      <SectionHeader
        kicker="Governance"
        heading="Instructions it ran under"
        id="instructions-heading"
      />
      <ul className="run-instructions">
        {view.instructions.map((used) => (
          <li key={`${used.key}:${used.scope}:${used.version}`}>
            <strong>{used.key}</strong>
            <span>{used.scope}</span>
            <span>version {used.version}</span>
          </li>
        ))}
      </ul>
    </section>
  );
}

/** Running this run again. */
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
    <div className="run-repeat" aria-busy={repeating}>
      <button
        className="ui-button ui-button--secondary"
        type="button"
        onClick={() => void submit()}
        disabled={repeating || why !== null}
        title={why ?? "Start the same work again"}
      >
        {repeating ? "Starting…" : "Run this again"}
      </button>
      {why !== null && (
        <p className="run-repeat__note" role="status">
          {why}
        </p>
      )}
      {refusal !== null && (
        <div className="ui-feedback ui-feedback--error" role="alert">
          <strong>Run not started</strong>
          <span>
            {refusal instanceof ApiError && refusal.detail !== "" ? refusal.detail : refusal.message}
          </span>
          {refusal instanceof ApiError && refusal.conflictingRunId !== "" && (
            <a href={addressOf({ name: "run", runId: refusal.conflictingRunId })}>
              Show the run in the way
            </a>
          )}
        </div>
      )}
    </div>
  );
}

/** The kind of pass a recorded run type can be started again as, if any. */
function repeatableKind(runType: string | undefined): StartKind | null {
  if (runType === "develop" || runType === "review") return runType;
  return null;
}

/** Why this run cannot be repeated, or nothing. */
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
