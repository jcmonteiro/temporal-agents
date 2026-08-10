import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import {
  endSession,
  loadPrincipal,
  signInAddress,
  type PrincipalDTO,
} from "../clients/session";
import { ApiError, onUnauthenticated } from "../clients/http";

/**
 * Whether this browser may use the hub, and as whom.
 *
 * This is the one place the answer lives. A page never asks "am I signed in?"
 * by looking at a response of its own: it renders, and the shell decides what
 * to render it inside. The states are four because the hub really has four
 * answers, and collapsing any two of them misleads the operator:
 *
 * - `unknown` — the first read has not come back yet.
 * - `signed-in` — with the principal to show.
 * - `signed-out` — the credential was refused; the way forward is the provider.
 * - `not-required` — this deployment configures no sign-in at all, so there is
 *   nobody to be, and offering a sign-in button that leads nowhere would be a
 *   lie.
 * - `unavailable` — the hub could not answer. Emphatically not `signed-out`:
 *   sending the operator to a provider because a store is restarting solves
 *   nothing and loses their place.
 */
export type SessionState =
  | { status: "unknown" }
  | { status: "signed-in"; principal: PrincipalDTO }
  | { status: "signed-out" }
  | { status: "not-required" }
  | { status: "unavailable"; message: string };

/** What a surface can do about the session. */
export interface Session {
  state: SessionState;
  /** Re-reads who the operator is, for the retry after an outage. */
  refresh: () => Promise<void>;
  /** Ends the session and leaves the browser signed out. */
  signOut: () => Promise<void>;
  /** Where to send the browser to sign in and come back here. */
  signInHref: () => string;
}

const SessionContext = createContext<Session | null>(null);

/**
 * Reads the session once, and keeps it current by listening at the one place
 * that notices a refused credential.
 *
 * The listener is what makes an expired session mid-use land on the sign-in
 * page instead of on a page of failed reads. It flips the state once: every
 * later 401 finds the state already `signed-out`, so nothing loops.
 */
export function SessionProvider({ children }: { children: ReactNode }): ReactNode {
  const [state, setState] = useState<SessionState>({ status: "unknown" });

  const refresh = useCallback(async (): Promise<void> => {
    setState(await readSession());
  }, []);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const next = await readSession();
      if (!cancelled) setState(next);
    })();
    const stop = onUnauthenticated(() => {
      setState((previous) =>
        previous.status === "signed-out" ? previous : { status: "signed-out" },
      );
    });
    return () => {
      cancelled = true;
      stop();
    };
  }, []);

  const signOut = useCallback(async (): Promise<void> => {
    await endSession();
    // The server has already forgotten the session, so the view stops claiming
    // otherwise whatever the request reported: a browser that asked to be signed
    // out is signed out.
    setState({ status: "signed-out" });
  }, []);

  const value: Session = {
    state,
    refresh,
    signOut,
    signInHref: () => signInAddress(intendedDestination()),
  };
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

/** The session, for a surface inside the provider. */
export function useSession(): Session {
  const session = useContext(SessionContext);
  if (session === null) {
    throw new Error("useSession is only usable inside a SessionProvider");
  }
  return session;
}

/** Reads who the operator is and maps the failures onto the four answers. */
async function readSession(): Promise<SessionState> {
  const principal = await loadPrincipal();
  if (principal.ok) return { status: "signed-in", principal: principal.value };
  const error = principal.error;
  if (error instanceof ApiError && error.status === 401) {
    return { status: "signed-out" };
  }
  if (error instanceof ApiError && error.status === 404) {
    // The route is published only where somebody can sign in, so its absence is
    // this deployment saying it has no identity provider.
    return { status: "not-required" };
  }
  return { status: "unavailable", message: error.message };
}

/**
 * Where the operator was going, as a path this application can be returned to.
 *
 * The hub is one document with a fragment route, so the fragment is the page:
 * dropping it would sign the operator in and then land them on the overview,
 * which is exactly the small daily annoyance this preserves them from.
 */
export function intendedDestination(): string {
  const { pathname, search, hash } = window.location;
  return `${pathname || "/"}${search}${hash}`;
}
