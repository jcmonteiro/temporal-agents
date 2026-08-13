import {
  lazy,
  Suspense,
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import type { SteeringSessionDTO } from "../clients/api";
import { loadWaitingSessions } from "../clients/steering";
import { connectHubEvents } from "../clients/streams";
import { waitingFor } from "./waiting-time";

const SteeringModal = lazy(() => import("./steering-modal"));
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
        <Suspense fallback={null}>
          <SteeringModal
            sessionId={activeId}
            onClose={() => setActiveId(null)}
            onDecided={() => {
              setSessions((current) => current.filter((session) => session.id !== activeId));
              setActiveId(null);
            }}
          />
        </Suspense>
      )}
    </SteeringContext.Provider>
  );
}

export function useSteering(): SteeringContextValue {
  return useContext(SteeringContext);
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
