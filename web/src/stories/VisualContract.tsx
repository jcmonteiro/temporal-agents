import { useState, type ReactNode } from "react";

const statuses = [
  ["todo", "To do"],
  ["in-progress", "In progress"],
  ["paused", "Paused"],
  ["waiting-input", "Waiting for input"],
  ["waiting", "Waiting"],
  ["done", "Done"],
  ["failed", "Failed"],
] as const;

interface Props {
  viewport?: "wide" | "narrow";
}

/** Storybook specimen for the shared application-wide visual language. */
export function VisualContract({ viewport = "wide" }: Props): ReactNode {
  const [compact, setCompact] = useState(false);

  return (
    <main
      className={`visual-contract visual-contract--${viewport}`}
      data-density={compact ? "compact" : "comfortable"}
    >
      <header className="visual-contract__hero">
        <div>
          <p className="ui-eyebrow">Agent Hub visual contract</p>
          <h1>Quiet focus for active work</h1>
          <p className="visual-contract__lede">
            Calm surfaces keep operational detail legible. Orbital depth, precise
            borders, and one blue accent connect every task to the live canvas.
          </p>
        </div>
        <div className="visual-contract__principles" aria-label="Design principles">
          <span>Clear hierarchy</span>
          <span>Compact, not crowded</span>
          <span>State never relies on color alone</span>
        </div>
      </header>

      <section className="visual-contract__section" aria-labelledby="hierarchy-title">
        <div className="visual-contract__section-heading">
          <div>
            <p className="ui-eyebrow">01 · Hierarchy and surfaces</p>
            <h2 id="hierarchy-title">One calm frame, distinct layers</h2>
          </div>
          <p>Page intent leads. Sections group. Borders separate. Shadows float only interactive overlays.</p>
        </div>
        <div className="visual-contract__surface-grid">
          <article className="ui-surface visual-contract__feature-card">
            <span className="ui-status ui-status--in-progress">In progress</span>
            <div>
              <p className="ui-kicker">Reliability fleet</p>
              <h3>Preserve the payment failure cause</h3>
              <p>Implementation is active in the checkout worktree.</p>
            </div>
            <dl className="visual-contract__metrics">
              <div><dt>Iteration</dt><dd>3 of 5</dd></div>
              <div><dt>Elapsed</dt><dd>18 min</dd></div>
              <div><dt>Location</dt><dd>checkout</dd></div>
            </dl>
          </article>
          <aside className="ui-surface ui-surface--raised visual-contract__layer-card">
            <p className="ui-kicker">Elevation rule</p>
            <h3>Depth means proximity</h3>
            <p>Raised surfaces are reserved for menus, focused decisions, and transient feedback.</p>
            <div className="visual-contract__orbit-mark" aria-hidden="true"><span /></div>
          </aside>
        </div>
      </section>

      <section className="visual-contract__section" aria-labelledby="controls-title">
        <div className="visual-contract__section-heading">
          <div>
            <p className="ui-eyebrow">02 · Controls and focus</p>
            <h2 id="controls-title">Actions read in priority order</h2>
          </div>
          <p>Every control has a visible keyboard focus ring and a minimum 40-pixel target.</p>
        </div>
        <div className="ui-surface visual-contract__control-grid">
          <form className="visual-contract__form" onSubmit={(event) => event.preventDefault()}>
            <div className="ui-field">
              <label htmlFor="contract-run-name">Run name</label>
              <input
                id="contract-run-name"
                aria-describedby="contract-run-name-help"
                defaultValue="Checkout reliability review"
              />
              <small id="contract-run-name-help">Use a name that remains clear in notifications.</small>
            </div>
            <div className="ui-field">
              <label htmlFor="contract-review-depth">Review depth</label>
              <select id="contract-review-depth" defaultValue="standard">
                <option value="fast">Fast</option>
                <option value="standard">Standard</option>
                <option value="thorough">Thorough</option>
              </select>
            </div>
          </form>
          <div className="visual-contract__actions" aria-label="Action styles">
            <button type="button" className="ui-button ui-button--primary">Start review</button>
            <button type="button" className="ui-button ui-button--secondary">Save draft</button>
            <button type="button" className="ui-button ui-button--danger">Delete run</button>
            <button type="button" className="ui-button ui-button--secondary" disabled>Unavailable action</button>
            <button
              type="button"
              className="ui-button ui-button--toggle"
              aria-pressed={compact}
              onClick={() => setCompact((selected) => !selected)}
            >
              Compact density
            </button>
          </div>
        </div>
      </section>

      <section className="visual-contract__section" aria-labelledby="feedback-title">
        <div className="visual-contract__section-heading">
          <div>
            <p className="ui-eyebrow">03 · Feedback and status</p>
            <h2 id="feedback-title">State is explicit and consistent</h2>
          </div>
          <p>Labels, icons, and language carry meaning before color reinforces it.</p>
        </div>
        <div className="visual-contract__feedback-grid">
          <div className="ui-feedback ui-feedback--success" role="status" aria-label="Success">
            <strong>Configuration saved</strong>
            <span>The next run will use these settings.</span>
          </div>
          <div className="ui-feedback ui-feedback--warning">
            <strong>Review needs guidance</strong>
            <span>A decision is required before work can continue.</span>
          </div>
          <div className="ui-feedback ui-feedback--error">
            <strong>Connection interrupted</strong>
            <span>Live updates will resume after reconnection.</span>
          </div>
        </div>
        <div className="visual-contract__statuses" aria-label="Run statuses">
          {statuses.map(([status, label]) => (
            <span key={status} className={`ui-status ui-status--${status}`}>
              <span aria-hidden="true" />{label}
            </span>
          ))}
        </div>
      </section>
    </main>
  );
}
