import { err, ok, type Result } from "../utils/result";

// Transport adapter: the only place in the frontend that talks to the network.
// Base path is proxied by Vite in dev (see vite.config.ts) and served by the
// same origin in production. See internal/httpapi/httpapi.go.
const BASE = "/api/v1";

// One request bound; the Go server also enforces its own.
const DEFAULT_TIMEOUT_MS = 15_000;

/**
 * How the browser proves who it is: the same-origin session cookie the API set,
 * and nothing else.
 *
 * This is the one place a credential is attached. There is no token in script
 * storage, no Authorization header assembled in a component, and no identity
 * library: the cookie is `HttpOnly`, so this code could not read it if it
 * wanted to. A component that wanted to authenticate a request differently
 * would have to change this line, which is the point.
 */
const CREDENTIALS: RequestCredentials = "same-origin";

/**
 * A failure the API answered with, carrying the status so a caller can tell
 * "you are not signed in" from "this hub cannot answer right now". The two lead
 * to opposite actions, and guessing between them is how an outage comes to look
 * like everybody being signed out.
 */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

/**
 * The failure a request reports when the hub does not accept the browser's
 * credential — no session, an expired one, or one that has been ended.
 *
 * It is a type of its own because it is the only failure a caller must not
 * retry: retrying is how a signed-out browser ends up in a redirect loop.
 */
export class UnauthenticatedError extends ApiError {
  constructor(path: string) {
    super(401, `${path} → not signed in`);
    this.name = "UnauthenticatedError";
  }
}

/** Somebody who wants to know that the session is gone. */
type Listener = () => void;

const listeners = new Set<Listener>();

/**
 * Subscribes to "this browser is no longer signed in".
 *
 * Handling it centrally is what keeps every surface honest: one place notices,
 * and everything that assumed a session — a page's state, and any long-lived
 * stream added later — is told once and can end cleanly instead of hanging or
 * retrying into a loop. Returns the unsubscribe.
 */
export function onUnauthenticated(listener: Listener): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

/** Tells every subscriber that the session is gone. */
function announceUnauthenticated(): void {
  for (const listener of [...listeners]) listener();
}

/** GETs a JSON document below /api/v1 and never throws. */
export async function fetchJSON<T>(path: string): Promise<Result<T, Error>> {
  const res = await request(path, { method: "GET" });
  if (!res.ok) return err(res.error);
  if (res.value.status === 204) return ok(undefined as T);
  try {
    return ok((await res.value.json()) as T);
  } catch (e) {
    return err(e instanceof Error ? e : new Error(String(e)));
  }
}

/** Sends a request that changes something and reads no body. */
export async function send(
  path: string,
  method: "POST" | "DELETE",
): Promise<Result<void, Error>> {
  const res = await request(path, { method });
  if (!res.ok) return err(res.error);
  return ok(undefined);
}

/**
 * One request, with the bound, the credential and the unauthenticated handling
 * every caller shares.
 */
async function request(
  path: string,
  init: { method: string },
): Promise<Result<Response, Error>> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), DEFAULT_TIMEOUT_MS);
  try {
    const res = await fetch(BASE + path, {
      method: init.method,
      credentials: CREDENTIALS,
      headers: { Accept: "application/json" },
      signal: controller.signal,
    });
    if (res.status === 401) {
      announceUnauthenticated();
      return err(new UnauthenticatedError(`${init.method} ${path}`));
    }
    if (!res.ok) {
      return err(new ApiError(res.status, `${init.method} ${path} → ${res.status}`));
    }
    return ok(res);
  } catch (e) {
    return err(e instanceof Error ? e : new Error(String(e)));
  } finally {
    clearTimeout(timer);
  }
}
