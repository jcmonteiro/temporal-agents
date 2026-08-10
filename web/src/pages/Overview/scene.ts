import type { Place, PlaceRegistry } from "../../domain/place";
import { UNKNOWN_PLACE_ID } from "../../domain/place";
import type { WorkItem } from "../../domain/work-item";
import { layoutOrbit, type OrbitSlot } from "./layout";

/**
 * The overview scene: one body per place, with that place's work in orbit
 * around it (IB §4).
 *
 * This is a pure function of (items, registry, view state). It reads no clock,
 * draws no random number and mutates none of its inputs, so the same snapshot
 * always yields the same picture and a test can assert placements directly.
 *
 * Nothing here parses a path or invents a relation: the tree is the one the
 * server published, and grouping is by the location reference each item
 * carries.
 */

/** Radius of the neutral mark at the centre. It holds no work. */
export const CENTRE_RADIUS = 60;

/** Radius of a place's body. */
export const PLANET_RADIUS = 44;

// Room a body needs beyond its outermost ring, for its label and its badge.
const LABEL_MARGIN = 34;

// Clear space between two bodies' discs.
const BODY_GAP = 56;

// Orbit geometry of one place. A ring sits far enough outside the one within it
// that two satellites on neighbouring rings stay as clear of each other as two
// satellites on the same ring.
const PLACE_ORBIT = { innerRadius: 110, ringGap: 100, satelliteSpacing: 96 };

const FULL_TURN = Math.PI * 2;

/** Where the first body of a ring starts: straight up from its centre. */
const RING_START_ANGLE = -Math.PI / 2;

export interface PlaceBody {
  /** The place this body draws. */
  place: Place;
  /** Where the body sits on the canvas. */
  centre: { x: number; y: number };
  /** The planet's own radius. */
  radius: number;
  /** The outermost ring drawn around the planet. */
  reach: number;
  /** Ring radii, relative to the body's centre. */
  orbits: number[];
  /**
   * The body's work. A slot's angle and radius are relative to this body's
   * centre, which is what the motion turns them about.
   */
  slots: OrbitSlot[];
  /** How many places this body absorbed, folded into it. */
  foldedCount: number;
  /** True when the fold happened for legibility rather than for depth. */
  crowded: boolean;
  /**
   * The ancestors this body stands in for: places that hold this one place and
   * no work of their own, so the pair draws once instead of twice.
   */
  standingInFor: Place[];
  /** How deep the place sits in the published tree. */
  depth: number;
}

export interface Scene {
  /** The neutral mark. It carries no work. */
  centre: { x: number; y: number };
  /** The depth the view state settled on. */
  visibleDepth: number;
  /** The bodies, parents before children, in the server's published order. */
  bodies: PlaceBody[];
}

export interface SceneOptions {
  width: number;
  height: number;
  /** How deep a place may sit and still draw as its own body. */
  visibleDepth: number;
}

// The unknown place always exists in a response the API produced. A snapshot
// that lost it (an item referencing a place the payload does not carry) still
// needs a home for that work, and inventing one would hide the gap.
const UNKNOWN_PLACE: Place = {
  id: UNKNOWN_PLACE_ID,
  kind: "unknown",
  label: "Unknown",
  parentId: null,
};

// A node while the scene is being built. Everything it holds is derived; the
// values become a PlaceBody once the geometry is settled.
interface Node {
  place: Place;
  depth: number;
  items: WorkItem[];
  children: Node[];
  foldedCount: number;
  crowded: boolean;
  standingInFor: Place[];
  orbits: number[];
  slots: OrbitSlot[];
  reach: number;
  footprint: number;
  ringRadius: number;
  centre: { x: number; y: number };
}

function newNode(place: Place, depth: number): Node {
  return {
    place,
    depth,
    items: [],
    children: [],
    foldedCount: 0,
    crowded: false,
    standingInFor: [],
    orbits: [],
    slots: [],
    reach: 0,
    footprint: 0,
    ringRadius: 0,
    centre: { x: 0, y: 0 },
  };
}

export function layoutScene(
  items: WorkItem[],
  places: PlaceRegistry,
  opts: SceneOptions,
): Scene {
  const depth = Math.max(0, Math.trunc(opts.visibleDepth));
  const centre = { x: opts.width / 2, y: opts.height / 2 };

  const roots = visibleTree(places, depth);
  const nodes = index(roots);
  absorbTheDeeperPlaces(places, depth, nodes);
  giveEachItemItsPlace(items, places, depth, nodes, roots);

  const drawn = roots.map(standInForPassThrough);
  drawn.forEach(measure);
  placeAroundTheCentre(drawn, centre);

  return { centre, visibleDepth: depth, bodies: bodiesOf(drawn) };
}

/** The places shallow enough to draw as their own body, as a tree. */
function visibleTree(places: PlaceRegistry, depth: number): Node[] {
  const nodes = new Map<string, Node>();
  const roots: Node[] = [];
  for (const place of places.places) {
    const at = places.depthOf(place.id);
    if (at > depth) continue;
    const node = newNode(place, at);
    nodes.set(place.id, node);
    const parent = place.parentId === null ? undefined : nodes.get(place.parentId);
    // The registry is closed under ancestry and ordered parents-first, so a
    // parent shallow enough to draw is already indexed here.
    if (parent) parent.children.push(node);
    else roots.push(node);
  }
  return roots;
}

function index(roots: Node[]): Map<string, Node> {
  const nodes = new Map<string, Node>();
  const walk = (node: Node): void => {
    nodes.set(node.place.id, node);
    node.children.forEach(walk);
  };
  roots.forEach(walk);
  return nodes;
}

/**
 * The body a place draws inside: the place itself when it is shallow enough,
 * otherwise its nearest visible ancestor.
 */
function hostOf(
  placeId: string,
  places: PlaceRegistry,
  depth: number,
): string | undefined {
  const ancestry = places.ancestryOf(placeId);
  if (ancestry.length === 0) return undefined;
  return ancestry[Math.min(depth, ancestry.length - 1)].id;
}

/** Every place deeper than the visible depth folds into its nearest visible ancestor. */
function absorbTheDeeperPlaces(
  places: PlaceRegistry,
  depth: number,
  nodes: Map<string, Node>,
): void {
  for (const place of places.places) {
    if (places.depthOf(place.id) <= depth) continue;
    const host = hostOf(place.id, places, depth);
    const node = host === undefined ? undefined : nodes.get(host);
    if (node) node.foldedCount += 1;
  }
}

function giveEachItemItsPlace(
  items: WorkItem[],
  places: PlaceRegistry,
  depth: number,
  nodes: Map<string, Node>,
  roots: Node[],
): void {
  let unknown = nodes.get(UNKNOWN_PLACE_ID);
  for (const item of items) {
    const host = hostOf(item.placeId, places, depth);
    const node = host === undefined ? undefined : nodes.get(host);
    if (node) {
      node.items.push(item);
      continue;
    }
    // The item references a place the response did not publish. It runs
    // somewhere unknown, which is a place, not a null branch.
    if (!unknown) {
      unknown = newNode(UNKNOWN_PLACE, 0);
      nodes.set(UNKNOWN_PLACE_ID, unknown);
      roots.unshift(unknown);
    }
    unknown.items.push(item);
  }
}

/**
 * A place that holds exactly one place and no work of its own draws once, not
 * twice: the child takes its slot and says which ancestors it stands in for.
 */
function standInForPassThrough(node: Node): Node {
  node.children = node.children.map(standInForPassThrough);
  const passesThrough =
    node.items.length === 0 && node.foldedCount === 0 && node.children.length === 1;
  if (!passesThrough) return node;
  const child = node.children[0];
  child.standingInFor = [node.place, ...child.standingInFor];
  return child;
}

/** The orbits of a body's own work, relative to its centre. */
function layoutWork(node: Node): void {
  if (node.items.length === 0) {
    node.orbits = [];
    node.slots = [];
    node.reach = PLANET_RADIUS + LABEL_MARGIN;
    return;
  }
  const layout = layoutOrbit(node.items, { width: 0, height: 0, ...PLACE_ORBIT });
  node.orbits = layout.orbits;
  node.slots = layout.slots;
  node.reach = Math.max(...layout.orbits) + LABEL_MARGIN;
}

/**
 * Sizes a body and decides whether its children can be drawn beside it.
 *
 * Children ride a ring outside the parent's own work. When their discs cannot
 * share that ring without overlapping, legibility wins over fidelity: the
 * parent folds every place under it and says so. Folded work costs no extra
 * width — it joins the parent's orbits, which grow outwards ring by ring —
 * whereas overlapping bodies cost the picture.
 */
function measure(node: Node): void {
  node.children.forEach(measure);
  layoutWork(node);
  if (node.children.length === 0) {
    node.footprint = node.reach;
    return;
  }
  const widest = Math.max(...node.children.map((c) => c.footprint));
  const ring = node.reach + widest + BODY_GAP;
  if (arcNeededBy(node.children) > FULL_TURN * ring) {
    fold(node);
    layoutWork(node);
    node.crowded = true;
    node.footprint = node.reach;
    return;
  }
  node.ringRadius = ring;
  node.footprint = ring + widest;
}

/** The arc length a row of bodies needs on a ring. */
function arcNeededBy(nodes: Node[]): number {
  return nodes.reduce((total, node) => total + 2 * (node.footprint + BODY_GAP), 0);
}

/** Folds every place under this one into it, work and fold counts included. */
function fold(node: Node): void {
  const absorb = (child: Node): void => {
    node.items.push(...child.items);
    node.foldedCount += 1 + child.foldedCount;
    child.children.forEach(absorb);
  };
  node.children.forEach(absorb);
  node.children = [];
}

/** Places the root bodies around the neutral mark, then each body's children around it. */
function placeAroundTheCentre(roots: Node[], centre: { x: number; y: number }): void {
  if (roots.length === 0) return;
  const widest = Math.max(...roots.map((r) => r.footprint));
  const ring = Math.max(
    CENTRE_RADIUS + widest + BODY_GAP,
    arcNeededBy(roots) / FULL_TURN,
  );
  placeOnRing(roots, centre, ring);
}

/**
 * Spreads bodies over a ring, each taking a share of the turn proportional to
 * the room it needs, and recurses into their children.
 */
function placeOnRing(
  nodes: Node[],
  centre: { x: number; y: number },
  ring: number,
): void {
  const total = arcNeededBy(nodes);
  let consumed = 0;
  for (const node of nodes) {
    const need = 2 * (node.footprint + BODY_GAP);
    const angle = RING_START_ANGLE + FULL_TURN * ((consumed + need / 2) / total);
    consumed += need;
    node.centre = {
      x: centre.x + Math.cos(angle) * ring,
      y: centre.y + Math.sin(angle) * ring,
    };
    if (node.children.length > 0) {
      placeOnRing(node.children, node.centre, node.ringRadius);
    }
  }
}

/** The bodies, parents before children, in the server's published order. */
function bodiesOf(roots: Node[]): PlaceBody[] {
  const bodies: PlaceBody[] = [];
  const walk = (node: Node): void => {
    bodies.push({
      place: node.place,
      centre: node.centre,
      radius: PLANET_RADIUS,
      reach: node.reach,
      orbits: node.orbits,
      slots: node.slots,
      foldedCount: node.foldedCount,
      crowded: node.crowded,
      standingInFor: node.standingInFor,
      depth: node.depth,
    });
    node.children.forEach(walk);
  };
  roots.forEach(walk);
  return bodies;
}
