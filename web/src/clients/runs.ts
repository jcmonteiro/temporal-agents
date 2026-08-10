import type { LocationResource, RunDTO } from "./api";
import { ApiError, fetchJSON } from "./http";
import { fromLocation, fromRun } from "./mapping";
import type { Place } from "../domain/place";
import type { WorkItem } from "../domain/work-item";
import { err, ok, type Result } from "../utils/result";

/**
 * One run, and where it runs.
 *
 * "Not listed yet" is a state of its own rather than a failure. A start returns
 * as soon as the orchestrator accepts the submission, and the read path answers
 * for the run a moment later; a page that called that gap an error would tell an
 * operator their work had failed at the very moment it began.
 */
export type RunView =
  | { known: false }
  | { known: true; run: WorkItem; place: Place | null; startedAt: string | null; endedAt: string | null; tokens?: number };

/** Reads one run. */
export async function loadRun(runId: string): Promise<Result<RunView, Error>> {
  const response = await fetchJSON<RunDTO & { locations?: LocationResource[] }>(
    `/runs/${encodeURIComponent(runId)}`,
  );
  if (!response.ok) {
    if (response.error instanceof ApiError && response.error.status === 404) {
      return ok({ known: false });
    }
    return err(response.error);
  }
  const dto = response.value;
  const registry = dto.locations ?? [];
  const place = registry.find((location) => location.id === dto.locationId);
  return ok({
    known: true,
    run: fromRun(dto),
    place: place ? fromLocation(place) : null,
    startedAt: dto.startedAt,
    endedAt: dto.endedAt,
    tokens: dto.tokens,
  });
}
