import type { StartedWorkDTO } from "./api";
import { postJSON } from "./http";
import { err, ok, type Result } from "../utils/result";

/**
 * Starting agent work.
 *
 * The request names a place and never a directory: the server resolves where the
 * work runs, from the places it knows. That is not a convention this client is
 * asked to keep — the contract has no field for a path — and it is why an
 * operator cannot start work anywhere the hub was not told about.
 */
export type StartKind = "develop" | "review";

/** What an operator asked to start. */
export interface StartWork {
  /**
   * The caller's identity for this request, which makes the request repeatable.
   * It belongs to the *intent*, not to the attempt: every retry of one intent
   * carries the same identity, so the hub answers with the run it already
   * started instead of starting a second one.
   */
  requestId: string;
  kind: StartKind;
  placeId: string;
  /** What the agent is told to do. A review takes none. */
  prompt?: string;
}

/** The work the hub started. It has no status yet: it has only just begun. */
export interface StartedWork {
  runId: string;
  kind: StartKind;
  placeId: string;
  startedAt: string | null;
  startedBy?: string;
}

/** Starts one unit of agent work, and reports the refusal where there is one. */
export async function startWork(work: StartWork): Promise<Result<StartedWork, Error>> {
  const body: Record<string, unknown> = {
    requestId: work.requestId,
    kind: work.kind,
    placeId: work.placeId,
  };
  // A review reviews what is already there, and the server refuses a prompt on
  // one. Sending an empty string would be sending a prompt.
  if (work.prompt !== undefined && work.prompt !== "") body.prompt = work.prompt;
  const response = await postJSON<StartedWorkDTO>("/runs", body);
  if (!response.ok) return err(response.error);
  const started = response.value;
  return ok({
    runId: started.id,
    kind: started.type as StartKind,
    placeId: started.locationId,
    startedAt: started.startedAt,
    startedBy: started.startedBy,
  });
}

/**
 * A fresh identity for one intent to start work.
 *
 * It is minted where the operator forms the intent, not where the request is
 * sent, because that is the difference between "start this again" and "try that
 * again".
 */
export function anIntentToStart(): string {
  return crypto.randomUUID();
}
