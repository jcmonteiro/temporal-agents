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
  const submitting = useRef(false);
  const closeRef = useRef<HTMLButtonElement>(null);
  const guidanceRef = useRef<HTMLTextAreaElement>(null);
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

  useEffect(() => {
    if (session !== null) {
      const narrow = typeof window.matchMedia === "function"
        && window.matchMedia("(max-width: 620px)").matches;
      (narrow ? closeRef.current : guidanceRef.current ?? closeRef.current)
        ?.focus({ preventScroll: true });
    }
  }, [session !== null]);

  useEffect(() => {
    const closeOnEscape = (event: KeyboardEvent): void => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", closeOnEscape);
    return () => window.removeEventListener("keydown", closeOnEscape);
  }, [onClose]);

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

  const question = async (finish: boolean): Promise<void> => {
    if (busy || answer.trim() === "") return;
    setBusy(true);
    setError("");
    const result = await questionSteeringSession(sessionId, answer, finish);
    if (result.ok) {
      setSession(result.value);
      setGuidance(result.value.guidance ?? guidance);
      setAnswer("");
    } else {
      setError(result.error.message);
    }
    setBusy(false);
  };

  const decide = async (choice: "guide" | "skip" | "stop" | "continue" | "accept"): Promise<void> => {
    if (submitting.current || (choice === "guide" && guidance.trim() === "")) return;
    submitting.current = true;
    setBusy(true);
    setError("");
    const result = await decideSteeringSession(
      sessionId,
      choice,
      choice === "guide" ? guidance : undefined,
    );
    if (result.ok) onDecided();
    else {
      setError(result.error.message);
      submitting.current = false;
      setBusy(false);
    }
  };

  const passLimit = session?.round === "pass-limit";

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
                  : "Review the issue, refine the guidance, then choose how this run continues."}
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

        <div className="steering-modal__body">
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
              <div className="steering-context-bar" aria-label="Run context">
                <span><strong>Run</strong> {session.itemId}</span>
                <span><strong>State</strong> Waiting for input</span>
                <span><strong>Elapsed</strong> {waitingFor(session.waitingSince)}</span>
              </div>

              <section className="steering-section steering-decision" aria-labelledby="steering-decision-heading">
                <div className="steering-section__heading">
                  <div>
                    <p className="ui-kicker">Decision context</p>
                    <h3 id="steering-decision-heading">What needs a decision</h3>
                  </div>
                  <span className="ui-status ui-status--waiting-input">
                    <span aria-hidden="true" /> Input needed
                  </span>
                </div>
                <pre>{session.material || "No review material was supplied for this round."}</pre>
              </section>

              <section
                className="steering-section steering-conversation"
                aria-label="Questioning conversation"
                aria-live="polite"
              >
                <div className="steering-section__heading">
                  <div>
                    <p className="ui-kicker">Optional guidance flow</p>
                    <h3>Conversation</h3>
                  </div>
                  <span className="steering-section__count">
                    {(session.messages ?? []).length} {(session.messages ?? []).length === 1 ? "turn" : "turns"}
                  </span>
                </div>
                {(session.messages ?? []).length === 0 ? (
                  <p className="steering-conversation__empty">No questions asked yet.</p>
                ) : (
                  <ol
                    className="steering-conversation__list"
                    aria-label="Conversation turns"
                    tabIndex={0}
                  >
                    {session.messages?.map((message) => (
                      <li className={`steering-message steering-message--${message.role}`} key={message.sequence}>
                        <span className="steering-message__author">
                          {message.role === "agent" ? "Questioning agent" : message.author ?? "Operator"}
                        </span>
                        <p>{message.text}</p>
                      </li>
                    ))}
                  </ol>
                )}
                <div className="steering-conversation__meta">
                  <span>{session.tokens ?? 0} questioning tokens</span>
                  <span>Contributors: {(session.contributors ?? []).join(", ") || "none yet"}</span>
                </div>
              </section>

              {!passLimit && (
                <section className="steering-section steering-guidance" aria-labelledby="steering-guidance-heading">
                  <div className="steering-section__heading">
                    <div>
                      <p className="ui-kicker">Implementation direction</p>
                      <h3 id="steering-guidance-heading">Guidance</h3>
                    </div>
                  </div>
                  <label className="ui-field steering-guidance__field">
                    Guidance for the implementing agent
                    <textarea
                      ref={guidanceRef}
                      aria-label="Guidance for the implementing agent"
                      value={guidance}
                      maxLength={GUIDANCE_LIMIT}
                      onChange={(event) => setGuidance(event.target.value)}
                      rows={6}
                    />
                    <small>
                      {guidance.length} / {GUIDANCE_LIMIT} bytes. Build needs non-empty guidance;
                      proceed without guidance when no direction is needed.
                    </small>
                  </label>
                </section>
              )}

              <fieldset className="steering-question" disabled={busy}>
                <legend>Question the review before deciding</legend>
                <p>Ask for one more detail, or turn the answer into implementation guidance.</p>
                <div className="steering-question__controls">
                  <label className="ui-field">
                    <span className="steering-visually-hidden">Answer the questioning agent</span>
                    <input
                      aria-label="Answer the questioning agent"
                      value={answer}
                      placeholder="Add a question or clarification…"
                      onChange={(event) => setAnswer(event.target.value)}
                    />
                  </label>
                  <button
                    className="ui-button ui-button--secondary"
                    type="button"
                    disabled={busy || answer.trim() === ""}
                    onClick={() => void question(false)}
                  >
                    Ask one question
                  </button>
                  <button
                    className="ui-button ui-button--secondary"
                    type="button"
                    disabled={busy || answer.trim() === ""}
                    onClick={() => void question(true)}
                  >
                    Finish into guidance
                  </button>
                </div>
              </fieldset>
            </>
          )}
        </div>

        {(session !== null || error !== "") && (
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
            {session !== null && (
              <div className="steering-decisions" aria-label="Steering decisions">
                {passLimit ? (
                  <>
                    <button className="ui-button ui-button--primary" type="button" disabled={busy} onClick={() => void decide("continue")}>Continue with a fresh pass budget</button>
                    <button className="ui-button ui-button--secondary" type="button" disabled={busy} onClick={() => void decide("accept")}>Accept the work as finished</button>
                    <button className="ui-button ui-button--danger" type="button" disabled={busy} onClick={() => void decide("stop")}>Stop the loop</button>
                  </>
                ) : (
                  <>
                    <button
                      className="ui-button ui-button--primary"
                      type="button"
                      disabled={busy || guidance.trim() === ""}
                      title={guidance.trim() === "" ? "Enter guidance before building" : undefined}
                      onClick={() => void decide("guide")}
                    >
                      Build with guidance
                    </button>
                    <button className="ui-button ui-button--secondary" type="button" disabled={busy} onClick={() => void decide("skip")}>Proceed without guidance</button>
                    <button className="ui-button ui-button--danger" type="button" disabled={busy} onClick={() => void decide("stop")}>Stop the loop</button>
                  </>
                )}
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
