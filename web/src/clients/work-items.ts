import type { WorkItem } from "../domain/work-item";
import { err, ok, type Result } from "../utils/result";
import type { Collection, FleetDTO, RunDTO, ScheduleDTO } from "./api";
import { fetchJSON } from "./http";
import { fromFleet, fromRun, fromSchedule, upNextOf } from "./mapping";

export interface OverviewData {
  items: WorkItem[];
  upNext: WorkItem[];
}

/**
 * Loads the Overview by calling the three resource endpoints in parallel and
 * merging the results into one list of satellites. This is the boundary
 * described in the implementation brief §3 — pages/components never call
 * `fetch` directly.
 *
 * The shell: it does the I/O and hands the payloads to the pure projections in
 * mapping.ts.
 */
export async function loadOverview(): Promise<Result<OverviewData, Error>> {
  const [fleets, runs, schedules] = await Promise.all([
    fetchJSON<Collection<FleetDTO>>("/fleets"),
    fetchJSON<Collection<RunDTO>>("/runs"),
    fetchJSON<Collection<ScheduleDTO>>("/schedules"),
  ]);

  if (!fleets.ok) return err(fleets.error);
  if (!runs.ok) return err(runs.error);
  if (!schedules.ok) return err(schedules.error);

  const items: WorkItem[] = [
    ...fleets.value.items.map(fromFleet),
    ...runs.value.items.map(fromRun),
    ...schedules.value.items.map(fromSchedule),
  ];

  return ok({ items, upNext: upNextOf(fleets.value.items) });
}
