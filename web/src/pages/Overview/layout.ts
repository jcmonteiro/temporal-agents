import type { WorkItem, WorkItemStatus } from "../../domain/work-item";

export interface OrbitSlot {
  item: WorkItem;
  orbit: number; // 0-indexed ring, 0 = closest to center
  angle: number; // base radians, 0 = up
  radius: number; // distance from center
  x: number; // position at rotation 0
  y: number;
}

export interface OrbitLayout {
  center: { x: number; y: number };
  orbits: number[]; // radii, index matches slot.orbit (0 = innermost)
  slots: OrbitSlot[];
}

interface Options {
  width: number;
  height: number;
  innerRadius?: number;
  ringGap?: number;
  satelliteSpacing?: number;
}

// Each status group holds a fixed band of rings (band 0 = closest to center):
//   0 (closest):  done, failed        — settled work
//   1 (middle):   paused, waiting-input, waiting — stalled / needs attention
//   2 (furthest): in-progress, todo   — active and upcoming work
const BAND_OF_STATUS: Record<WorkItemStatus, number> = {
  done: 0,
  failed: 0,
  paused: 1,
  "waiting-input": 1,
  waiting: 1,
  "in-progress": 2,
  todo: 2,
};

const BAND_COUNT = 3;

// Minimum arc length between two satellite centres on the same ring. A
// satellite is 60px across and carries a label underneath, so this leaves a
// visible gap instead of letting neighbours touch.
const DEFAULT_SATELLITE_SPACING = 96;

// A ring never holds fewer than this, however tight it is; below three the
// innermost ring would spawn an unreasonable number of overflow rings.
const MIN_RING_CAPACITY = 3;

/** How many satellites fit on a ring of this radius without overlapping. */
function ringCapacity(radius: number, spacing: number): number {
  const circumference = 2 * Math.PI * radius;
  return Math.max(MIN_RING_CAPACITY, Math.floor(circumference / spacing));
}

/**
 * Pure, deterministic orbit layout (IB §4a). Same input → same output.
 *
 * Items are grouped into three status bands, innermost band first, so settled
 * work always sits closer to the body than active work. A band that holds
 * nothing takes no ring: several places share the canvas, and a ring reserved
 * for work that is not there would push every place's satellites outwards and
 * cost the picture more than it buys. Every band fills as many rings as it
 * needs: each ring takes only as many satellites as its circumference can hold,
 * and the rest overflow onto the next ring outwards.
 */
export function layoutOrbit(items: WorkItem[], opts: Options): OrbitLayout {
  const {
    width,
    height,
    innerRadius = 180,
    ringGap = 120,
    satelliteSpacing = DEFAULT_SATELLITE_SPACING,
  } = opts;

  const center = { x: width / 2, y: height / 2 };

  // Bucket items by their status band.
  const buckets: WorkItem[][] = Array.from({ length: BAND_COUNT }, () => []);
  for (const item of items) {
    buckets[BAND_OF_STATUS[item.status]].push(item);
  }

  const orbits: number[] = [];
  const slots: OrbitSlot[] = [];

  buckets.forEach((bucket) => {
    let remaining = bucket;
    while (remaining.length > 0) {
      const ringIdx = orbits.length;
      const radius = innerRadius + ringIdx * ringGap;
      orbits.push(radius);

      const onRing = remaining.slice(0, ringCapacity(radius, satelliteSpacing));
      remaining = remaining.slice(onRing.length);

      // Offset each ring so items don't line up radially between rings.
      const offset = ringIdx * (Math.PI / 6);
      onRing.forEach((item, i) => {
        const angle = offset + (i / onRing.length) * Math.PI * 2 - Math.PI / 2;
        slots.push({
          item,
          orbit: ringIdx,
          angle,
          radius,
          x: center.x + Math.cos(angle) * radius,
          y: center.y + Math.sin(angle) * radius,
        });
      });
    }
  });

  return { center, orbits, slots };
}
