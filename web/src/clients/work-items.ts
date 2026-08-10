import type { UpNextEntry } from "../domain/up-next";
import { registryOf, type Place, type PlaceRegistry } from "../domain/place";
import type { WorkItem } from "../domain/work-item";
import { err, ok, type Result } from "../utils/result";
import type {
  FleetDTO,
  LocatedCollection,
  RunDTO,
  ScheduleDTO,
} from "./api";
import { fetchJSON } from "./http";
import {
  fromFleet,
  fromLocation,
  fromRun,
  fromSchedule,
  upNextOf,
} from "./mapping";

export interface OverviewData {
  items: WorkItem[];
  upNext: UpNextEntry[];
  /** The places the items run in, as the three responses published them. */
  places: PlaceRegistry;
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
    fetchJSON<LocatedCollection<FleetDTO>>("/fleets"),
    fetchJSON<LocatedCollection<RunDTO>>("/runs"),
    fetchJSON<LocatedCollection<ScheduleDTO>>("/schedules"),
  ]);

  if (!fleets.ok) return err(fleets.error);
  if (!runs.ok) return err(runs.error);
  if (!schedules.ok) return err(schedules.error);

  const items: WorkItem[] = [
    ...fleets.value.items.map(fromFleet),
    ...runs.value.items.map(fromRun),
    ...schedules.value.items.map(fromSchedule),
  ];

  // Each response carries its own registry, closed under ancestry and ordered
  // parents-first. Identity is the server's, so concatenating the three keeps
  // that order and the registry collapses the copies of a shared place.
  const published: Place[] = [
    ...(fleets.value.locations ?? []),
    ...(runs.value.locations ?? []),
    ...(schedules.value.locations ?? []),
  ].map(fromLocation);

  return ok({
    items,
    upNext: upNextOf(fleets.value.items),
    places: registryOf(published),
  });
}
