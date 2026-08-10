// A place is where work runs: the frontend's reading of the API's location
// registry (internal/agenthub/location.go, published by internal/httpapi).
//
// The server owns identity, the label and the parent edge. Nothing here parses a
// path, derives a name or invents a relation: this module only indexes what the
// server published, so a consumer can walk the tree in one pass.

export type PlaceKind = "unknown" | "directory" | "remote";

export interface Place {
  /** Server-issued, opaque identity. Never taken apart. */
  id: string;
  kind: PlaceKind;
  /** Server-computed display name. */
  label: string;
  /** The place this one is part of, or null for a root. */
  parentId: string | null;
  /** Absolute path, on the directory variant only. */
  directory?: string;
  /** Bounded reference, on the remote variant only. */
  ref?: string;
}

/**
 * Identity of the place work runs when nothing was recorded about where. The
 * value is the server's constant (pinned by the location contract tests in
 * internal/httpapi), not a client invention: an item that carries no location
 * reference belongs to this place.
 */
export const UNKNOWN_PLACE_ID = "unknown";

/**
 * The published registry, indexed. Ordering everywhere is the server's
 * published order — parents before children — so two identical responses index
 * identically.
 */
export interface PlaceRegistry {
  /** Every place, in published order. */
  places: Place[];
  /** The places with no parent, in published order. */
  roots: Place[];
  byId(id: string): Place | undefined;
  /** The children of a place, in published order. */
  childrenOf(id: string): Place[];
  /** How many ancestors a place has; a root is at depth 0. */
  depthOf(id: string): number;
  /**
   * The chain from the place's topmost ancestor down to the place itself. An
   * unknown id yields an empty chain.
   */
  ancestryOf(id: string): Place[];
}

/**
 * Indexes the places of one or more responses. Duplicates (the same place
 * published by two endpoints) collapse onto the first copy, which is safe
 * because identity is the server's.
 *
 * A parent reference the registry does not carry reads as "no parent", and a
 * cycle stops at the place it closes on. Neither can happen in a response the
 * server produced; handling them here keeps a malformed payload from hanging
 * the canvas.
 */
export function registryOf(published: Place[]): PlaceRegistry {
  const byId = new Map<string, Place>();
  for (const place of published) {
    if (!byId.has(place.id)) byId.set(place.id, place);
  }
  const places = [...byId.values()];

  const parentOf = (place: Place): Place | undefined =>
    place.parentId === null ? undefined : byId.get(place.parentId);

  const children = new Map<string, Place[]>();
  const roots: Place[] = [];
  for (const place of places) {
    const parent = parentOf(place);
    if (!parent) {
      roots.push(place);
      continue;
    }
    const siblings = children.get(parent.id) ?? [];
    siblings.push(place);
    children.set(parent.id, siblings);
  }

  const ancestries = new Map<string, Place[]>();
  for (const place of places) {
    const chain: Place[] = [];
    const seen = new Set<string>();
    let current: Place | undefined = place;
    while (current && !seen.has(current.id)) {
      seen.add(current.id);
      chain.unshift(current);
      current = parentOf(current);
    }
    ancestries.set(place.id, chain);
  }

  return {
    places,
    roots,
    byId: (id) => byId.get(id),
    childrenOf: (id) => children.get(id) ?? [],
    depthOf: (id) => Math.max(0, (ancestries.get(id)?.length ?? 1) - 1),
    ancestryOf: (id) => ancestries.get(id) ?? [],
  };
}
