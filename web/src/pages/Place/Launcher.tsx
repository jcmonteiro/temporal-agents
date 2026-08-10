import { useRef, useState, type ReactNode } from "react";
import { anIntentToStart, startWork, type StartKind } from "../../clients/start";
import { ApiError } from "../../clients/http";
import { addressOf, goTo } from "../../platform/route";
import type { Place } from "../../domain/place";

/**
 * Starting work in this place.
 *
 * It lives on a place page and nowhere else. The overview watches; the place is
 * where an operator has already decided *where*, so it is the only surface on
 * which the question "what shall it do here?" is answerable without asking them
 * to name a directory — which they never do here either: the directory is shown
 * as context and the request names the place.
 */
export function Launcher({ place }: { place: Place }): ReactNode {
  const [kind, setKind] = useState<StartKind>("develop");
  const [prompt, setPrompt] = useState("");
  const [starting, setStarting] = useState(false);
  const [refusal, setRefusal] = useState<ApiError | Error | null>(null);
  // The identity of the *intent*, minted once and kept until it succeeds. A
  // second click, or a retry after a refusal, is the same intent: it must not
  // start a second run.
  const intent = useRef<string | null>(null);

  const change = (apply: () => void): void => {
    // Different work is a different intent, so it gets an identity of its own.
    intent.current = null;
    setRefusal(null);
    apply();
  };

  const submit = async (): Promise<void> => {
    if (starting) return;
    setStarting(true);
    setRefusal(null);
    if (intent.current === null) intent.current = anIntentToStart();
    const started = await startWork({
      requestId: intent.current,
      kind,
      placeId: place.id,
      prompt: kind === "develop" ? prompt.trim() : undefined,
    });
    if (started.ok) {
      intent.current = null;
      goTo({ name: "run", runId: started.value.runId });
    } else {
      setRefusal(started.error);
    }
    setStarting(false);
  };

  const nothingToDo = kind === "develop" && prompt.trim() === "";
  return (
    <section
      aria-labelledby="launcher-heading"
      style={{
        display: "flex",
        flexDirection: "column",
        gap: "var(--space-3)",
        border: "1px solid var(--color-border)",
        borderRadius: "var(--radius-md)",
        background: "var(--color-surface)",
        padding: "var(--space-4)",
      }}
    >
      <h2
        id="launcher-heading"
        style={{ margin: 0, fontSize: "var(--font-size-lg)", fontWeight: 600 }}
      >
        Start work here
      </h2>
      {/* Where the work runs is context, not a field: it is this place, and the
          request names the place rather than the path. */}
      <p
        style={{
          margin: 0,
          color: "var(--color-text-subtle)",
          fontSize: "var(--font-size-sm)",
        }}
      >
        In {place.directory ?? place.ref ?? place.label}
      </p>

      <form
        onSubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
        style={{ display: "flex", flexDirection: "column", gap: "var(--space-3)" }}
      >
        <fieldset
          style={{ border: 0, margin: 0, padding: 0, display: "flex", gap: 16 }}
        >
          <legend
            style={{
              padding: 0,
              fontSize: "var(--font-size-sm)",
              color: "var(--color-text-muted)",
            }}
          >
            What to run
          </legend>
          <Choice
            checked={kind === "develop"}
            label="Develop"
            says="Implement something, then review it until the loop converges."
            onChoose={() => change(() => setKind("develop"))}
          />
          <Choice
            checked={kind === "review"}
            label="Review"
            says="Review what is already in the working tree."
            onChoose={() => change(() => setKind("review"))}
          />
        </fieldset>

        {kind === "develop" && (
          <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
            <label
              htmlFor="launch-prompt"
              style={{ fontSize: "var(--font-size-sm)", color: "var(--color-text-muted)" }}
            >
              What to do
            </label>
            <textarea
              id="launch-prompt"
              rows={3}
              value={prompt}
              onChange={(event) => {
                const next = event.target.value;
                change(() => setPrompt(next));
              }}
              placeholder="Make the flaky checkout test pass"
              style={{
                padding: "8px 10px",
                borderRadius: "var(--radius-sm)",
                border: "1px solid var(--color-border)",
                background: "var(--color-surface-2)",
                color: "var(--color-text)",
                fontSize: "var(--font-size-sm)",
                resize: "vertical",
              }}
            />
          </div>
        )}

        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <button
            type="submit"
            disabled={starting || nothingToDo}
            style={{
              padding: "8px 14px",
              borderRadius: "var(--radius-sm)",
              border: "1px solid var(--color-border-strong)",
              background: "var(--color-surface-2)",
              color: "var(--color-text)",
              fontSize: "var(--font-size-sm)",
              opacity: starting || nothingToDo ? 0.5 : 1,
            }}
          >
            {starting ? "Starting…" : "Start"}
          </button>
          {starting && (
            <span
              role="status"
              style={{
                color: "var(--color-text-muted)",
                fontSize: "var(--font-size-sm)",
              }}
            >
              Starting the work…
            </span>
          )}
        </div>

        {refusal !== null && <Refusal refusal={refusal} />}
      </form>
    </section>
  );
}

/** One of the kinds of work, with what it means. */
function Choice({
  checked,
  label,
  says,
  onChoose,
}: {
  checked: boolean;
  label: string;
  says: string;
  onChoose: () => void;
}): ReactNode {
  return (
    <label
      style={{
        display: "flex",
        alignItems: "center",
        gap: 6,
        fontSize: "var(--font-size-sm)",
        color: "var(--color-text)",
      }}
      title={says}
    >
      <input
        type="radio"
        name="kind"
        checked={checked}
        onChange={onChoose}
        value={label.toLowerCase()}
      />
      {label}
    </label>
  );
}

/**
 * Why the hub would not start the work.
 *
 * A refusal that collided with something says so and leads there: an operator
 * told only "that place is busy" then has to go and find what is in the way.
 */
function Refusal({ refusal }: { refusal: ApiError | Error }): ReactNode {
  const conflict = refusal instanceof ApiError ? refusal.conflictingRunId : "";
  const said =
    refusal instanceof ApiError && refusal.detail !== ""
      ? refusal.detail
      : refusal.message;
  return (
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
      {said}
      {conflict !== "" && (
        <a href={addressOf({ name: "run", runId: conflict })} style={{ color: "inherit" }}>
          Show the run in the way
        </a>
      )}
    </p>
  );
}
