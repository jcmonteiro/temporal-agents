import type { LocatedCollection, PlaceDTO } from "./api";
import { fetchJSON, postJSON } from "./http";
import { fromLocation } from "./mapping";
import type { Place } from "../domain/place";
import { err, ok, type Result } from "../utils/result";

/**
 * Where the hub may work.
 *
 * A place with work in it is observed — every work collection publishes it —
 * while a place with none exists only because an operator registered it. This
 * client reads that registry and adds to it; the places it returns carry the
 * server's own identity, label and parent, exactly as the work collections do.
 */
export interface RegisteredPlace {
  /** The place, as the server published it in the response's registry. */
  place: Place;
  /** When it was registered, as the server rendered it, or null. */
  registeredAt: string | null;
  /** Which principal registered it, absent where nobody is authenticated. */
  registeredBy?: string;
}

/** Reads the places the hub may work in. */
export async function loadRegisteredPlaces(): Promise<Result<RegisteredPlace[], Error>> {
  const response = await fetchJSON<LocatedCollection<PlaceDTO>>("/places");
  if (!response.ok) return err(response.error);
  return ok(placesOf(response.value.items, response.value.locations ?? []));
}

/**
 * Registers a directory as a place the hub may work in.
 *
 * The refusal is returned, not thrown: what the server said is wrong with the
 * directory is the only useful thing to put in front of the operator, and it is
 * carried on the error as the problem document's detail.
 */
export async function registerPlace(
  directory: string,
): Promise<Result<RegisteredPlace, Error>> {
  const response = await postJSON<PlaceDTO>("/places", { directory });
  if (!response.ok) return err(response.error);
  const registered = placesOf([response.value], response.value.locations ?? []);
  if (registered.length === 0) {
    return err(new Error("the hub registered a place it did not publish"));
  }
  return ok(registered[0]);
}

/**
 * Joins the registrations to the registry their references resolve against. A
 * reference the registry does not carry is dropped rather than drawn as a place
 * with no name: the server always publishes both together, so an entry without
 * its place would be a payload that contradicts itself.
 */
function placesOf(items: PlaceDTO[], locations: LocatedCollection<PlaceDTO>["locations"] = []): RegisteredPlace[] {
  const byId = new Map((locations ?? []).map((location) => [location.id, location]));
  const places: RegisteredPlace[] = [];
  for (const item of items) {
    const location = byId.get(item.locationId);
    if (!location) continue;
    places.push({
      place: fromLocation(location),
      registeredAt: item.registeredAt,
      registeredBy: item.registeredBy,
    });
  }
  return places;
}
