// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { fetchJSON, onUnauthenticated, send, UnauthenticatedError } from "./http";

// The transport is where the credential is attached and where a refused one is
// noticed. Both are asserted here because both are contracts other code depends
// on without being able to see them: a page never attaches a credential, and a
// long-lived reader (a stream, later) never polls to discover it was signed out.

let calls: { url: string; init: RequestInit | undefined }[];
let answer: Response;
const originalFetch = globalThis.fetch;

beforeEach(() => {
  calls = [];
  answer = jsonResponse(200, { ok: true });
  globalThis.fetch = ((url: RequestInfo | URL, init?: RequestInit) => {
    calls.push({ url: String(url), init });
    return Promise.resolve(answer);
  }) as typeof globalThis.fetch;
});

afterEach(() => {
  globalThis.fetch = originalFetch;
  vi.unstubAllEnvs();
  vi.restoreAllMocks();
});

it("sends the browser's own credential, and never one of its own", async () => {
  await fetchJSON("/runs");

  expect(calls).toHaveLength(1);
  expect(calls[0].init?.credentials).toBe("include");
  const headers = (calls[0].init?.headers ?? {}) as Record<string, string>;
  expect(headers.Authorization).toBeUndefined();
});

it("calls a configured sibling API directly", async () => {
  vi.stubEnv("VITE_AGENT_HUB_API_URL", "http://127.0.0.1:3000/api/v1");
  vi.resetModules();
  const { fetchJSON: fetchFromSibling } = await import("./http");

  await fetchFromSibling("/runs");

  expect(calls[0].url).toBe("http://127.0.0.1:3000/api/v1/runs");
});

it("tells every listener once when the hub refuses the credential", async () => {
  const stream = vi.fn();
  const page = vi.fn();
  const stopStream = onUnauthenticated(stream);
  const stopPage = onUnauthenticated(page);
  answer = jsonResponse(401, { title: "Authentication is required" });

  const result = await fetchJSON("/runs");

  expect(result.ok).toBe(false);
  expect(result.ok === false && result.error instanceof UnauthenticatedError).toBe(true);
  expect(stream).toHaveBeenCalledTimes(1);
  expect(page).toHaveBeenCalledTimes(1);
  stopStream();
  stopPage();
});

it("stops telling a listener that has gone away", async () => {
  const listener = vi.fn();
  const stop = onUnauthenticated(listener);
  stop();
  answer = jsonResponse(401, {});

  await fetchJSON("/runs");

  expect(listener).not.toHaveBeenCalled();
});

it("keeps a failure that is not about the credential to itself", async () => {
  const listener = vi.fn();
  const stop = onUnauthenticated(listener);
  answer = jsonResponse(503, { title: "A dependency is unavailable" });

  const result = await fetchJSON("/runs");

  expect(result.ok).toBe(false);
  expect(result.ok === false && result.error instanceof UnauthenticatedError).toBe(false);
  expect(listener).not.toHaveBeenCalled();
  stop();
});

it("reads a write that answers with no body", async () => {
  answer = new Response(null, { status: 204 });

  const result = await send("/auth/session", "DELETE");

  expect(result.ok).toBe(true);
  expect(calls[0].init?.method).toBe("DELETE");
});

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}
