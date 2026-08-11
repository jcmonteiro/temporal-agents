import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
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
      {notice !== "" && <p role="status" onClick={() => setNotice("")}>{notice}</p>}
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
  const guidanceRef = useRef<HTMLTextAreaElement>(null);

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
    if (session !== null) guidanceRef.current?.focus();
  }, [session !== null]);

  useEffect(() => {
    const closeOnEscape = (event: KeyboardEvent): void => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", closeOnEscape);
    return () => window.removeEventListener("keydown", closeOnEscape);
  }, [onClose]);

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

  const decide = async (choice: "guide" | "skip" | "stop"): Promise<void> => {
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

  return (
    <div
      role="presentation"
      onMouseDown={(event) => { if (event.target === event.currentTarget) onClose(); }}
      style={{ position: "fixed", inset: 0, zIndex: 100, background: "rgba(0,0,0,.55)", display: "grid", placeItems: "center", padding: 24 }}
    >
      <section
        role="dialog"
        aria-modal="true"
        aria-labelledby="steering-title"
        style={{ width: "min(760px, 100%)", maxHeight: "90vh", overflowY: "auto", background: "var(--color-surface)", border: "1px solid var(--color-border-strong)", borderRadius: "var(--radius-md)", padding: 20, display: "flex", flexDirection: "column", gap: 14 }}
      >
        <div style={{ display: "flex", justifyContent: "space-between", gap: 16 }}>
          <div>
            <h2 id="steering-title" style={{ margin: 0 }}>Guide this review round</h2>
            {session?.waitingSince && <small>Waiting since {session.waitingSince}</small>}
          </div>
          <button type="button" aria-label="Close steering" onClick={onClose}>×</button>
        </div>
        {session === null ? <p role="status">Loading the waiting round…</p> : (
          <>
            <section>
              <h3>What needs a decision</h3>
              <pre style={{ whiteSpace: "pre-wrap", maxHeight: 180, overflowY: "auto" }}>{session.material}</pre>
            </section>
            {(session.messages?.length ?? 0) > 0 && (
              <section aria-label="Questioning conversation">
                <h3>Conversation</h3>
                <ol>{session.messages?.map((message) => (
                  <li key={message.sequence}><strong>{message.role === "agent" ? "Agent" : message.author ?? "Operator"}:</strong> {message.text}</li>
                ))}</ol>
              </section>
            )}
            <p style={{ margin: 0 }}>{session.tokens ?? 0} questioning tokens · Contributors: {(session.contributors ?? []).join(", ") || "none yet"}</p>
            <label>
              Guidance for the implementing agent
              <textarea
                ref={guidanceRef}
                value={guidance}
                maxLength={GUIDANCE_LIMIT}
                onChange={(event) => setGuidance(event.target.value)}
                rows={6}
                style={{ display: "block", width: "100%" }}
              />
            </label>
            <small>{guidance.length} / {GUIDANCE_LIMIT} bytes. Build needs non-empty guidance; use proceed without guidance otherwise.</small>
            <fieldset disabled={busy}>
              <legend>Optional questioning · no cost until started</legend>
              <input aria-label="Answer the questioning agent" value={answer} onChange={(event) => setAnswer(event.target.value)} />
              <button type="button" onClick={() => void question(false)}>Ask one question</button>
              <button type="button" onClick={() => void question(true)}>Finish into guidance</button>
            </fieldset>
            <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
              <button type="button" disabled={busy || guidance.trim() === ""} title={guidance.trim() === "" ? "Enter guidance before building" : undefined} onClick={() => void decide("guide")}>Build with guidance</button>
              <button type="button" disabled={busy} onClick={() => void decide("skip")}>Proceed without guidance</button>
              <button type="button" disabled={busy} onClick={() => void decide("stop")}>Stop the loop</button>
            </div>
          </>
        )}
        {error && <p role="alert">{error}</p>}
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
      Needs guidance · waiting since {session.waitingSince ?? "an unknown time"}
    </button>
  );
}
