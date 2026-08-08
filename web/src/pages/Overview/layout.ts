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
}

// Each ring holds a fixed set of statuses (ring 0 = closest to center):
//   0 (closest):  done, failed        — settled work
//   1 (middle):   paused, waiting-input, waiting — stalled / needs attention
//   2 (furthest): in-progress, todo   — active and upcoming work
const RING_OF_STATUS: Record<WorkItemStatus, number> = {
  done: 0,
  failed: 0,
  paused: 1,
  "waiting-input": 1,
  waiting: 1,
  "in-progress": 2,
  todo: 2,
};

const RING_COUNT = 3;

/**
 * Pure, deterministic orbit layout (IB §4a). Same input → same output.
 * Items are assigned to one of three concentric rings by their status group,
 * then spread evenly around that ring.
 */
export function layoutOrbit(items: WorkItem[], opts: Options): OrbitLayout {
  const { width, height, innerRadius = 180, ringGap = 120 } = opts;

  const center = { x: width / 2, y: height / 2 };

  const orbits: number[] = [];
  for (let ring = 0; ring < RING_COUNT; ring += 1) {
    orbits.push(innerRadius + ring * ringGap);
  }

  // Bucket items by their status ring.
  const buckets: WorkItem[][] = Array.from({ length: RING_COUNT }, () => []);
  for (const item of items) {
    buckets[RING_OF_STATUS[item.status]].push(item);
  }

  const slots: OrbitSlot[] = [];
  buckets.forEach((bucket, ringIdx) => {
    const radius = orbits[ringIdx];
    const count = bucket.length;
    if (count === 0) return;
    // Offset each ring so items don't line up radially between rings.
    const offset = ringIdx * (Math.PI / 6);
    bucket.forEach((item, i) => {
      const angle = offset + (i / count) * Math.PI * 2 - Math.PI / 2;
      slots.push({
        item,
        orbit: ringIdx,
        angle,
        radius,
        x: center.x + Math.cos(angle) * radius,
        y: center.y + Math.sin(angle) * radius,
      });
    });
  });

  return { center, orbits, slots };
}
