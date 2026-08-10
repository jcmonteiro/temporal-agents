import type { UpNextEntry } from "../domain/up-next";
import { registryOf, workIn, type Place, type PlaceRegistry } from "../domain/place";
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

/**
 * One place and the work that runs there. A place that the hub does not know
 * is answered as not found, not as an empty one: a stale address must read as
 * stale.
 */
export type PlaceView =
  | { found: false }
  | {
      found: true;
      place: Place;
      /** The chain from the topmost ancestor down to the place itself. */
      ancestry: Place[];
      /** The places directly under it. */
      children: Place[];
      /** Its work, and the work of every place under it. */
      items: WorkItem[];
    };

/**
 * Reads one place and its work.
 *
 * The API publishes work per collection, with the registry alongside it, and
 * has no per-place resource; the page therefore reads the same three
 * collections and keeps the work of one place. The grouping is still the
 * server's: the item's location reference and the published ancestry decide,
 * nothing here parses a path.
 */
export async function loadPlace(
  placeId: string,
): Promise<Result<PlaceView, Error>> {
  const overview = await loadOverview();
  if (!overview.ok) return err(overview.error);
  const { places, items } = overview.value;
  const place = places.byId(placeId);
  if (!place) return ok({ found: false });
  return ok({
    found: true,
    place,
    ancestry: places.ancestryOf(place.id),
    children: places.childrenOf(place.id),
    items: workIn(items, places, place.id),
  });
}
