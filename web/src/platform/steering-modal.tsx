import DOMPurify from "dompurify";
import { marked } from "marked";
import {
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
  questionSteeringSession,
} from "../clients/steering";
import { connectConversation } from "../clients/streams";
import {
  CONFIRM_LABELS,
  DECISION_CONSEQUENCES,
  DECISION_LABELS,
  DecisionMaterialDisclosure,
  OutcomeChoices,
  PASS_LIMIT_OUTCOMES,
  REVIEW_OUTCOMES,
  SteeringProgress,
  stepsFor,
  type SteeringDecision,
  type SteeringStep,
} from "./steering-modal-view";
import { waitingFor } from "./waiting-time";
import "./steering.css";

const GUIDANCE_LIMIT = 8 * 1024;

function AgentResponse({ text }: { text: string }): ReactNode {
  const html = useMemo(
    () => DOMPurify.sanitize(marked.parse(text, { async: false }), {
      ALLOWED_TAGS: [
        "p",
        "br",
        "strong",
        "em",
        "code",
        "pre",
        "blockquote",
        "ul",
        "ol",
        "li",
        "a",
      ],
      ALLOWED_ATTR: ["href", "title"],
      ALLOW_ARIA_ATTR: false,
      ALLOW_DATA_ATTR: false,
    }),
    [text],
  );
  return <div className="steering-message__markdown" dangerouslySetInnerHTML={{ __html: html }} />;
}

export default function SteeringModal({
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
  const guidanceBytes = new TextEncoder().encode(guidance).byteLength;
  const guidanceValid = guidance.trim() !== "" && guidanceBytes <= GUIDANCE_LIMIT;

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
    const result = await questionSteeringSession(sessionId, answer);
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
    if (submitting.current || (decision === "guide" && !guidanceValid)) return;
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
  const visibleSteps = stepsFor(choice, passLimit);
  const outcomes = passLimit ? PASS_LIMIT_OUTCOMES : REVIEW_OUTCOMES;

  return (
    <div
      className="steering-backdrop"
      role="presentation"
      onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}
    >
      <section
        className={`steering-modal ui-surface ui-surface--raised${materialExpanded ? " steering-modal--material-expanded" : ""}`}
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

              {!materialExpanded && <SteeringProgress current={step} steps={visibleSteps} />}

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

                  {!materialExpanded && (
                    <section className="steering-outcome" aria-labelledby="steering-outcome-heading">
                      <div>
                        <h3 id="steering-outcome-heading">Choose what happens next</h3>
                        <p>The selected outcome determines which details are needed before confirmation.</p>
                      </div>
                      <OutcomeChoices firstRef={outcomeRef} onChoose={choose} options={outcomes} />
                    </section>
                  )}
                </>
              )}

              {step === "clarify" && (
                <>
                  <section className="steering-step-intro" aria-labelledby="steering-clarify-heading">
                    <div className="steering-step-intro__title">
                      <h3 id="steering-clarify-heading">Clarify the direction</h3>
                      <span>Optional</span>
                    </div>
                    <p>Ask questions about the review material before writing implementation guidance.</p>
                    <DecisionMaterialDisclosure material={session.material} />
                  </section>

                  <section
                    className="steering-section steering-conversation"
                    aria-label="Clarification conversation"
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
                              {message.role === "agent" ? "Clarification agent" : "Operator"}
                            </span>
                            {message.role === "agent"
                              ? <AgentResponse text={message.text} />
                              : <p>{message.text}</p>}
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
                      <span>{session.tokens ?? 0} clarification tokens</span>
                      <span>
                        Contributors: {session.contributors?.length ?? 0}
                      </span>
                    </div>
                  </section>

                  <fieldset className="steering-question" disabled={busy}>
                    <legend>Ask the clarification agent</legend>
                    <p>Each answer is recorded immediately. The final steering decision is not submitted yet.</p>
                    <div className="steering-question__controls">
                      <label className="ui-field">
                        <span className="steering-visually-hidden">Question for the clarification agent</span>
                        <input
                          ref={answerRef}
                          aria-label="Question for the clarification agent"
                          value={answer}
                          placeholder="Ask for one detail…"
                          onChange={(event) => setAnswer(event.target.value)}
                          onKeyDown={(event) => {
                            if (event.key !== "Enter" || event.nativeEvent.isComposing) return;
                            event.preventDefault();
                            void question();
                          }}
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
                    <h3 id="steering-guidance-step-heading">Prepare implementation guidance</h3>
                    <p>Give the implementing agent a clear direction. Guidance is required for this outcome.</p>
                    <DecisionMaterialDisclosure material={session.material} />
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
                        aria-invalid={guidanceBytes > GUIDANCE_LIMIT}
                        value={guidance}
                        onChange={(event) => setGuidance(event.target.value)}
                        rows={8}
                      />
                      <small>{guidanceBytes} / {GUIDANCE_LIMIT} bytes</small>
                    </label>
                  </section>
                </>
              )}

              {step === "review" && choice !== null && (
                <section className="steering-review" aria-labelledby="steering-review-heading">
                  <div className="steering-step-intro">
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
                  disabled={busy || !guidanceValid}
                  title={guidance.trim() === ""
                    ? "Enter guidance before reviewing"
                    : guidanceBytes > GUIDANCE_LIMIT
                      ? `Shorten guidance to ${GUIDANCE_LIMIT} bytes or fewer`
                      : undefined}
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
                  {CONFIRM_LABELS[choice]}
                </button>
              </div>
            )}
          </footer>
        )}
      </section>
    </div>
  );
}
