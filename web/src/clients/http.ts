import { err, ok, type Result } from "../utils/result";

// Transport adapter: the only place in the frontend that talks to the network.
// Base path is proxied by Vite in dev (see vite.config.ts) and served by the
// same origin in production. See internal/httpapi/httpapi.go.
const BASE = "/api/v1";

// One request bound; the Go server also enforces its own.
const DEFAULT_TIMEOUT_MS = 15_000;

/** GETs a JSON document below /api/v1 and never throws. */
export async function fetchJSON<T>(path: string): Promise<Result<T, Error>> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), DEFAULT_TIMEOUT_MS);
  try {
    const res = await fetch(BASE + path, {
      headers: { Accept: "application/json" },
      signal: controller.signal,
    });
    if (!res.ok) {
      return err(new Error(`GET ${path} → ${res.status}`));
    }
    const body = (await res.json()) as T;
    return ok(body);
  } catch (e) {
    return err(e instanceof Error ? e : new Error(String(e)));
  } finally {
    clearTimeout(timer);
  }
}
