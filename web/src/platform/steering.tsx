import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactNode,
} from "react";
import type { SteeringSessionDTO } from "../clients/api";
import {
  decideSteeringSession,
  loadSteeringSession,
  loadWaitingSessions,
  questionSteeringSession,
} from "../clients/steering";
import { connectConversation, connectHubEvents } from "../clients/streams";
import { waitingFor } from "./waiting-time";
import "./steering.css";

const GUIDANCE_LIMIT = 8 * 1024;
const REFRESH_INTERVAL_MS = 5_000;

type SteeringDecision = "guide" | "skip" | "stop" | "continue" | "accept";
type SteeringStep = "outcome" | "clarify" | "guidance" | "review";

const DECISION_LABELS: Record<SteeringDecision, string> = {
  guide: "Build with guidance",
  skip: "Proceed without guidance",
  stop: "Stop the loop",
  continue: "Continue with a fresh pass budget",
  accept: "Accept the work as finished",
};

const DECISION_CONSEQUENCES: Record<SteeringDecision, string> = {
  guide: "Implementation resumes with the guidance below.",
  skip: "Implementation resumes without operator guidance.",
  stop: "The review loop stops without another implementation pass.",
  continue: "The review loop receives a fresh pass budget.",
  accept: "The current work is accepted as finished.",
};

interface SteeringContextValue {
  sessions: SteeringSessionDTO[];
  open(sessionId: string): void;
  forItem(itemId: string): SteeringSessionDTO | undefined;
}

const NO_STEERING: SteeringContextValue = {
  sessions: [],
  open: () => undefined,
  forItem: () => undefined,
};

const SteeringContext = createContext<SteeringContextValue>(NO_STEERING);

export function SteeringProvider({ children }: { children: ReactNode }): ReactNode {
  const [sessions, setSessions] = useState<SteeringSessionDTO[]>([]);
  const [activeId, setActiveId] = useState<string | null>(null);
  const [notice, setNotice] = useState("");

  useEffect(() => {
    let cancelled = false;
    const refresh = async (): Promise<void> => {
      const result = await loadWaitingSessions();
      if (!cancelled && result.ok) setSessions(result.value);
    };
    void refresh();
    const timer = setInterval(() => void refresh(), REFRESH_INTERVAL_MS);
    const stream = typeof EventSource === "undefined"
      ? null
      : connectHubEvents(() => void refresh());
    return () => {
      cancelled = true;
      clearInterval(timer);
      stream?.close();
    };
  }, []);

  useEffect(() => {
    if (activeId !== null && !sessions.some((session) => session.id === activeId)) {
      setActiveId(null);
      setNotice("This review round was decided elsewhere. Its recorded decision won.");
    }
  }, [activeId, sessions]);

  const value = useMemo<SteeringContextValue>(() => ({
    sessions,
    open: setActiveId,
    forItem: (itemId) => sessions.find((session) => session.itemId === itemId),
  }), [sessions]);

  return (
    <SteeringContext.Provider value={value}>
      {children}
      {notice !== "" && (
        <div className="steering-notice ui-feedback ui-feedback--warning" role="status">
          <span>{notice}</span>
          <button type="button" onClick={() => setNotice("")}>Dismiss</button>
        </div>
      )}
      {activeId !== null && (
        <SteeringModal
          sessionId={activeId}
          onClose={() => setActiveId(null)}
          onDecided={() => {
            setSessions((current) => current.filter((session) => session.id !== activeId));
            setActiveId(null);
          }}
        />
      )}
    </SteeringContext.Provider>
  );
}

export function useSteering(): SteeringContextValue {
  return useContext(SteeringContext);
}

function SteeringModal({
  sessionId,
  onClose,
  onDecided,
}: {
  sessionId: string;
  onClose(): void;
  onDecided(): void;
}): ReactNode {
  const [session, setSession] = useState<SteeringSessionDTO | null>(null);
  const [guidance, setGuidance] = useState("");
  const [answer, setAnswer] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [step, setStep] = useState<SteeringStep>("outcome");
  const [choice, setChoice] = useState<SteeringDecision | null>(null);
  const [materialExpanded, setMaterialExpanded] = useState(false);
  const submitting = useRef(false);
  const closeRef = useRef<HTMLButtonElement>(null);
  const outcomeRef = useRef<HTMLButtonElement>(null);
  const answerRef = useRef<HTMLInputElement>(null);
  const guidanceRef = useRef<HTMLTextAreaElement>(null);
  const confirmRef = useRef<HTMLButtonElement>(null);
  const returnFocusRef = useRef<HTMLElement | null>(
    typeof document !== "undefined" && document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null,
  );

  useEffect(() => {
    closeRef.current?.focus();
    return () => returnFocusRef.current?.focus();
  }, []);

  useEffect(() => {
    let cancelled = false;
    void loadSteeringSession(sessionId).then((result) => {
      if (cancelled) return;
      if (result.ok) {
        setSession(result.value);
        setGuidance(result.value.guidance ?? "");
      } else {
        setError(result.error.message);
      }
    });
    const stream = typeof EventSource === "undefined"
      ? null
      : connectConversation(sessionId, (message) => {
        setSession((current) => current === null ? current : {
          ...current,
          messages: [...(current.messages ?? []).filter((item) => item.sequence !== message.sequence), message]
            .sort((left, right) => left.sequence - right.sequence),
          tokens: (current.tokens ?? 0) + (message.tokens ?? 0),
        });
      });
    return () => {
      cancelled = true;
      stream?.close();
    };
  }, [sessionId]);

  const loaded = session !== null;

  useEffect(() => {
    if (!loaded) return;
    const narrow = typeof window.matchMedia === "function"
      && window.matchMedia("(max-width: 620px)").matches;
    const stepTarget = step === "outcome"
      ? outcomeRef.current
      : step === "clarify"
        ? answerRef.current
        : step === "guidance"
          ? guidanceRef.current
          : confirmRef.current;
    (narrow && step === "outcome" ? closeRef.current : stepTarget ?? closeRef.current)
      ?.focus({ preventScroll: true });
  }, [loaded, step]);

  useEffect(() => {
    const closeOnEscape = (event: KeyboardEvent): void => {
      if (event.key !== "Escape") return;
      if (materialExpanded) setMaterialExpanded(false);
      else onClose();
    };
    window.addEventListener("keydown", closeOnEscape);
    return () => window.removeEventListener("keydown", closeOnEscape);
  }, [materialExpanded, onClose]);

  const containFocus = (event: ReactKeyboardEvent<HTMLElement>): void => {
    if (event.key !== "Tab") return;
    const focusable = Array.from(event.currentTarget.querySelectorAll<HTMLElement>(
      'button:not(:disabled), input:not(:disabled), textarea:not(:disabled), select:not(:disabled), a[href], [tabindex]:not([tabindex="-1"])',
    ));
    const first = focusable[0];
    const last = focusable.at(-1);
    if (first === undefined || last === undefined) return;
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  };

  const question = async (): Promise<void> => {
    if (busy || answer.trim() === "") return;
    setBusy(true);
    setError("");
    const result = await questionSteeringSession(sessionId, answer, false);
    if (result.ok) {
      setSession(result.value);
      setAnswer("");
    } else {
      setError(result.error.message);
    }
    setBusy(false);
  };

  const choose = (decision: SteeringDecision): void => {
    setChoice(decision);
    setStep(decision === "guide" ? "clarify" : "review");
  };

  const backFromReview = (): void => {
    setStep(choice === "guide" ? "guidance" : "outcome");
  };

  const decide = async (decision: SteeringDecision): Promise<void> => {
    if (submitting.current || (decision === "guide" && guidance.trim() === "")) return;
    submitting.current = true;
    setBusy(true);
    setError("");
    const result = await decideSteeringSession(
      sessionId,
      decision,
      decision === "guide" ? guidance : undefined,
    );
    if (result.ok) onDecided();
    else {
      setError(result.error.message);
      submitting.current = false;
      setBusy(false);
    }
  };

  const passLimit = session?.round === "pass-limit";
  const messages = session?.messages ?? [];
  const visibleSteps: SteeringStep[] = passLimit || (choice !== null && choice !== "guide")
    ? ["outcome", "review"]
    : ["outcome", "clarify", "guidance", "review"];
  const currentStepIndex = visibleSteps.indexOf(step);
  const stepLabels: Record<SteeringStep, string> = {
    outcome: "Outcome",
    clarify: "Clarify",
    guidance: "Guidance",
    review: "Review",
  };

  return (
    <div
      className="steering-backdrop"
      role="presentation"
      onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}
    >
      <section
        className="steering-modal ui-surface ui-surface--raised"
        role="dialog"
        aria-modal="true"
        aria-labelledby="steering-title"
        aria-describedby="steering-purpose"
        aria-busy={busy}
        onKeyDown={containFocus}
      >
        <header className="steering-modal__header">
          <div className="steering-modal__heading">
            <span className="steering-modal__orbit" aria-hidden="true"><span /></span>
            <div>
              <p className="ui-eyebrow">Operator steering</p>
              <h2 id="steering-title">
                {passLimit ? "Review pass limit reached" : "Guide this review round"}
              </h2>
              <p id="steering-purpose">
                {passLimit
                  ? "Choose whether this run receives another review budget or finishes here."
                  : "Choose an outcome, provide only the details it needs, then review the decision."}
              </p>
            </div>
          </div>
          <button
            ref={closeRef}
            className="steering-modal__close"
            type="button"
            aria-label="Close steering"
            onClick={onClose}
          >
            <span aria-hidden="true">×</span>
          </button>
        </header>

        <div className={`steering-modal__body${materialExpanded ? " steering-modal__body--material-expanded" : ""}`}>
          {session === null && error === "" && (
            <div className="steering-loading" role="status">
              <span className="steering-loading__spinner" aria-hidden="true" />
              <div>
                <strong>Loading the waiting round…</strong>
                <span>Reading its context, conversation, and available decisions.</span>
              </div>
            </div>
          )}

          {session !== null && (
            <>
              {!materialExpanded && (
                <div className="steering-context-bar" aria-label="Run context">
                  <span><strong>Run</strong> {session.itemId}</span>
                  <span><strong>State</strong> Waiting for input</span>
                  <span><strong>Elapsed</strong> {waitingFor(session.waitingSince)}</span>
                </div>
              )}

              {!materialExpanded && <nav className="steering-steps" aria-label="Steering progress">
                <ol>
                  {visibleSteps.map((item, index) => (
                    <li
                      key={item}
                      aria-current={item === step ? "step" : undefined}
                      data-complete={index < currentStepIndex || undefined}
                    >
                      <span>{index + 1}</span>
                      <strong>{stepLabels[item]}</strong>
                      {item === "clarify" && <small>Optional</small>}
                    </li>
                  ))}
                </ol>
              </nav>}

              {step === "outcome" && (
                <>
                  <section
                    className={`steering-section steering-decision${materialExpanded ? " steering-decision--expanded" : ""}`}
                    aria-labelledby="steering-decision-heading"
                  >
                    <div className="steering-section__heading">
                      <div>
                        <p className="ui-kicker">Decision context</p>
                        <h3 id="steering-decision-heading">What needs a decision</h3>
                      </div>
                      <div className="steering-decision__actions">
                        {!materialExpanded && (
                          <span className="ui-status ui-status--waiting-input">
                            <span aria-hidden="true" /> Input needed
                          </span>
                        )}
                        <button
                          className="steering-section__maximize"
                          type="button"
                          aria-expanded={materialExpanded}
                          aria-controls="steering-review-outcome"
                          onClick={() => setMaterialExpanded((expanded) => !expanded)}
                        >
                          <span aria-hidden="true">{materialExpanded ? "↙" : "↗"}</span>
                          {materialExpanded ? "Restore review outcome" : "Maximize review outcome"}
                        </button>
                      </div>
                    </div>
                    <pre id="steering-review-outcome">{session.material || "No review material was supplied for this round."}</pre>
                  </section>

                  {!materialExpanded && <section className="steering-outcome" aria-labelledby="steering-outcome-heading">
                    <div>
                      <p className="ui-kicker">Step 1</p>
                      <h3 id="steering-outcome-heading">Choose what happens next</h3>
                      <p>The selected outcome determines which details are needed before confirmation.</p>
                    </div>
                    <div className="steering-outcome__choices">
                      {passLimit ? (
                        <>
                          <button ref={outcomeRef} type="button" aria-label="Continue with a fresh pass budget" onClick={() => choose("continue")}>
                            <strong>Continue with a fresh pass budget</strong>
                            <span>Allow another bounded review pass.</span>
                          </button>
                          <button type="button" aria-label="Accept the work as finished" onClick={() => choose("accept")}>
                            <strong>Accept the work as finished</strong>
                            <span>Finish with the current implementation.</span>
                          </button>
                          <button className="steering-outcome__danger" type="button" aria-label="Stop the loop" onClick={() => choose("stop")}>
                            <strong>Stop the loop</strong>
                            <span>End the review without another pass.</span>
                          </button>
                        </>
                      ) : (
                        <>
                          <button ref={outcomeRef} type="button" aria-label="Build with guidance" onClick={() => choose("guide")}>
                            <strong>Build with guidance</strong>
                            <span>Clarify if needed, then prepare implementation direction.</span>
                          </button>
                          <button type="button" aria-label="Proceed without guidance" onClick={() => choose("skip")}>
                            <strong>Proceed without guidance</strong>
                            <span>Resume implementation without operator direction.</span>
                          </button>
                          <button className="steering-outcome__danger" type="button" aria-label="Stop the loop" onClick={() => choose("stop")}>
                            <strong>Stop the loop</strong>
                            <span>End the review without another implementation pass.</span>
                          </button>
                        </>
                      )}
                    </div>
                  </section>}
                </>
              )}

              {step === "clarify" && (
                <>
                  <section className="steering-step-intro" aria-labelledby="steering-clarify-heading">
                    <p className="ui-kicker">Optional step</p>
                    <h3 id="steering-clarify-heading">Clarify the direction</h3>
                    <p>Ask questions about the review material before writing implementation guidance.</p>
                    <details>
                      <summary>Review the decision material</summary>
                      <pre>{session.material || "No review material was supplied for this round."}</pre>
                    </details>
                  </section>

                  <section
                    className="steering-section steering-conversation"
                    aria-label="Questioning conversation"
                    aria-live="polite"
                  >
                    <div className="steering-section__heading">
                      <div>
                        <p className="ui-kicker">Clarification</p>
                        <h3>Conversation</h3>
                      </div>
                      <span className="steering-section__count">
                        {messages.length} {messages.length === 1 ? "turn" : "turns"}
                      </span>
                    </div>
                    {messages.length === 0 ? (
                      <p className="steering-conversation__empty">No questions asked yet.</p>
                    ) : (
                      <ol className="steering-conversation__list" aria-label="Conversation turns" tabIndex={0}>
                        {messages.map((message) => (
                          <li className={`steering-message steering-message--${message.role}`} key={message.sequence}>
                            <span className="steering-message__author">
                              {message.role === "agent" ? "Questioning agent" : message.author ?? "Operator"}
                            </span>
                            <p>{message.text}</p>
                            {message.role === "agent" && (
                              <button
                                className="steering-message__use"
                                type="button"
                                onClick={() => {
                                  setGuidance(message.text);
                                  setStep("guidance");
                                }}
                              >
                                Use this answer as draft guidance
                              </button>
                            )}
                          </li>
                        ))}
                      </ol>
                    )}
                    <div className="steering-conversation__meta">
                      <span>{session.tokens ?? 0} questioning tokens</span>
                      <span>Contributors: {(session.contributors ?? []).join(", ") || "none yet"}</span>
                    </div>
                  </section>

                  <fieldset className="steering-question" disabled={busy}>
                    <legend>Ask the questioning agent</legend>
                    <p>Each answer is recorded immediately. The final steering decision is not submitted yet.</p>
                    <div className="steering-question__controls">
                      <label className="ui-field">
                        <span className="steering-visually-hidden">Question for the questioning agent</span>
                        <input
                          ref={answerRef}
                          aria-label="Question for the questioning agent"
                          value={answer}
                          placeholder="Ask for one detail…"
                          onChange={(event) => setAnswer(event.target.value)}
                        />
                      </label>
                      <button
                        className="ui-button ui-button--secondary"
                        type="button"
                        disabled={busy || answer.trim() === ""}
                        onClick={() => void question()}
                      >
                        Ask question
                      </button>
                    </div>
                  </fieldset>
                </>
              )}

              {step === "guidance" && (
                <>
                  <section className="steering-step-intro" aria-labelledby="steering-guidance-step-heading">
                    <p className="ui-kicker">Step 3</p>
                    <h3 id="steering-guidance-step-heading">Prepare implementation guidance</h3>
                    <p>Give the implementing agent a clear direction. Guidance is required for this outcome.</p>
                    <details>
                      <summary>Review the decision material</summary>
                      <pre>{session.material || "No review material was supplied for this round."}</pre>
                    </details>
                  </section>
                  <section className="steering-section steering-guidance" aria-labelledby="steering-guidance-heading">
                    <div className="steering-section__heading">
                      <div>
                        <p className="ui-kicker">Implementation direction</p>
                        <h3 id="steering-guidance-heading">Guidance</h3>
                      </div>
                      {messages.length > 0 && (
                        <span className="steering-section__count">
                          {messages.length} clarification {messages.length === 1 ? "turn" : "turns"}
                        </span>
                      )}
                    </div>
                    <label className="ui-field steering-guidance__field">
                      Guidance for the implementing agent
                      <textarea
                        ref={guidanceRef}
                        aria-label="Guidance for the implementing agent"
                        value={guidance}
                        maxLength={GUIDANCE_LIMIT}
                        onChange={(event) => setGuidance(event.target.value)}
                        rows={8}
                      />
                      <small>{guidance.length} / {GUIDANCE_LIMIT} bytes</small>
                    </label>
                  </section>
                </>
              )}

              {step === "review" && choice !== null && (
                <section className="steering-review" aria-labelledby="steering-review-heading">
                  <div className="steering-step-intro">
                    <p className="ui-kicker">Final step</p>
                    <h3 id="steering-review-heading">Review the decision</h3>
                    <p>No terminal decision has been submitted. Confirm the summary below to continue.</p>
                  </div>
                  <dl className="steering-review__summary">
                    <div><dt>Run</dt><dd>{session.itemId}</dd></div>
                    <div><dt>Outcome</dt><dd>{DECISION_LABELS[choice]}</dd></div>
                    {choice === "guide" && (
                      <>
                        <div><dt>Clarification</dt><dd>{messages.length} {messages.length === 1 ? "turn" : "turns"}</dd></div>
                        <div className="steering-review__guidance"><dt>Guidance</dt><dd>{guidance}</dd></div>
                      </>
                    )}
                    <div><dt>Result</dt><dd>{DECISION_CONSEQUENCES[choice]}</dd></div>
                  </dl>
                </section>
              )}
            </>
          )}
        </div>

        {(error !== "" || busy || (session !== null && step !== "outcome")) && (
          <footer className="steering-modal__footer">
            <div className="steering-modal__feedback" aria-live="assertive">
              {busy && error === "" && (
                <div className="steering-progress" role="status">
                  <span className="steering-progress__dot" aria-hidden="true" />
                  <div>
                    <strong>Steering update in progress…</strong>
                    <span>Keep this dialog open while the run records the update.</span>
                  </div>
                </div>
              )}
              {error !== "" && (
                <div className="ui-feedback ui-feedback--error" role="alert">
                  <strong>Steering action unavailable</strong>
                  <span>{error}</span>
                </div>
              )}
            </div>
            {session !== null && step === "clarify" && (
              <div className="steering-decisions" aria-label="Clarification navigation">
                <button className="ui-button ui-button--secondary" type="button" disabled={busy} onClick={() => setStep("outcome")}>Back</button>
                <button className="ui-button ui-button--primary" type="button" disabled={busy} onClick={() => setStep("guidance")}>
                  {messages.length === 0 ? "Skip clarification" : "Continue to guidance"}
                </button>
              </div>
            )}
            {session !== null && step === "guidance" && (
              <div className="steering-decisions" aria-label="Guidance navigation">
                <button className="ui-button ui-button--secondary" type="button" disabled={busy} onClick={() => setStep("clarify")}>Back to clarification</button>
                <button
                  className="ui-button ui-button--primary"
                  type="button"
                  disabled={busy || guidance.trim() === ""}
                  title={guidance.trim() === "" ? "Enter guidance before reviewing" : undefined}
                  onClick={() => setStep("review")}
                >
                  Continue to review
                </button>
              </div>
            )}
            {session !== null && step === "review" && choice !== null && (
              <div className="steering-decisions" aria-label="Decision confirmation">
                <button className="ui-button ui-button--secondary" type="button" disabled={busy} onClick={backFromReview}>Back</button>
                <button
                  ref={confirmRef}
                  className={choice === "stop" ? "ui-button ui-button--danger" : "ui-button ui-button--primary"}
                  type="button"
                  disabled={busy}
                  onClick={() => void decide(choice)}
                >
                  {choice === "guide"
                    ? "Confirm and build"
                    : choice === "skip"
                      ? "Confirm and proceed"
                      : choice === "stop"
                        ? "Confirm stop"
                        : choice === "continue"
                          ? "Confirm fresh pass budget"
                          : "Confirm as finished"}
                </button>
              </div>
            )}
          </footer>
        )}
      </section>
    </div>
  );
}

export function SteeringButton({ itemId }: { itemId: string }): ReactNode {
  const steering = useSteering();
  const session = steering.forItem(itemId);
  if (!session) return null;
  return (
    <button type="button" onClick={() => steering.open(session.id)}>
      Needs guidance · {waitingFor(session.waitingSince)}
    </button>
  );
}
