import { useRef, useState, type ReactNode } from "react";
import { ApiError } from "../../clients/http";
import { anIntentToStart, startWork, type StartKind } from "../../clients/start";
import type { Place } from "../../domain/place";
import { addressOf, goTo } from "../../platform/route";
import "./place.css";

/** Starts new work from the place the operator is already inspecting. */
export function Launcher({ place }: { place: Place }): ReactNode {
  const [kind, setKind] = useState<StartKind>("develop");
  const [prompt, setPrompt] = useState("");
  const [worktree, setWorktree] = useState(true);
  const [starting, setStarting] = useState(false);
  const [refusal, setRefusal] = useState<ApiError | Error | null>(null);
  // One intent survives repeated clicks and retries until the start succeeds.
  const intent = useRef<string | null>(null);

  const change = (apply: () => void): void => {
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
      worktree: kind === "develop" ? worktree : undefined,
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
  const location = place.directory ?? place.ref ?? place.label;

  return (
    <section className="place-launcher ui-surface" aria-labelledby="launcher-heading">
      <header className="place-launcher__header">
        <div>
          <p className="ui-kicker">New activity</p>
          <h2 id="launcher-heading">Start work here</h2>
          <p>Choose the operating mode and define the next task for this place.</p>
        </div>
        <span className="place-launcher__scope">Scoped to this place</span>
      </header>

      <form
        className="place-launcher__form"
        aria-busy={starting}
        onSubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        <fieldset className="place-launcher__choices">
          <legend>What to run</legend>
          <div>
            <Choice
              checked={kind === "develop"}
              label="Develop"
              says="Implement in a fresh worktree, then run local and Copilot review."
              onChoose={() => change(() => setKind("develop"))}
            />
            <Choice
              checked={kind === "review"}
              label="Review"
              says="Review what is already in the working tree."
              onChoose={() => change(() => setKind("review"))}
            />
          </div>
        </fieldset>

        <div className="place-launcher__context">
          <span>Execution context</span>
          <strong>
            {kind === "develop" && worktree ? "Starts from " : "In "}
            {location}
            {kind === "develop" && worktree && " in a fresh worktree"}
          </strong>
        </div>

        {kind === "develop" && (
          <div className="place-launcher__task">
            <label className="place-launcher__check">
              <input
                type="checkbox"
                aria-label="Use a fresh worktree"
                checked={worktree}
                onChange={(event) => {
                  const next = event.target.checked;
                  change(() => setWorktree(next));
                }}
              />
              <span>
                <strong>Use a fresh worktree</strong>
                <small>Keep implementation changes isolated from this checkout.</small>
              </span>
            </label>
            <label className="ui-field" htmlFor="launch-prompt">
              What to do
              <textarea
                id="launch-prompt"
                aria-label="What to do"
                rows={4}
                value={prompt}
                onChange={(event) => {
                  const next = event.target.value;
                  change(() => setPrompt(next));
                }}
                placeholder="Make the flaky checkout test pass"
              />
              <small>State the outcome and constraints for the development pass.</small>
            </label>
          </div>
        )}

        {refusal !== null && <Refusal refusal={refusal} />}

        <footer className="place-launcher__footer">
          <p>
            {kind === "develop"
              ? "Development runs local and Copilot review after implementation."
              : "Review inspects the current working tree without creating a worktree."}
          </p>
          <div>
            {starting && <span role="status">Starting the work…</span>}
            <button
              className="ui-button ui-button--primary"
              type="submit"
              disabled={starting || nothingToDo}
            >
              {starting ? "Starting…" : "Start"}
            </button>
          </div>
        </footer>
      </form>
    </section>
  );
}

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
    <label className="place-launcher__choice">
      <input
        type="radio"
        name="kind"
        aria-label={label}
        checked={checked}
        onChange={onChoose}
        value={label.toLowerCase()}
      />
      <span className="place-launcher__choice-mark" aria-hidden="true" />
      <span>
        <strong>{label}</strong>
        <small>{says}</small>
      </span>
    </label>
  );
}

/** Shows a start refusal and links to the conflicting run when one exists. */
function Refusal({ refusal }: { refusal: ApiError | Error }): ReactNode {
  const conflict = refusal instanceof ApiError ? refusal.conflictingRunId : "";
  const said =
    refusal instanceof ApiError && refusal.detail !== ""
      ? refusal.detail
      : refusal.message;
  return (
    <div className="ui-feedback ui-feedback--error" role="alert">
      <strong>Work not started</strong>
      <span>
        {said}
        {conflict !== "" && (
          <>
            {" "}
            <a href={addressOf({ name: "run", runId: conflict })}>
              Show the run in the way
            </a>
          </>
        )}
      </span>
    </div>
  );
}
