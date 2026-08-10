import type { OrbitSlot } from "./layout";

/** Time the constellation takes for one full turn. */
export const ORBIT_PERIOD_MS = 240_000;

const FULL_TURN = Math.PI * 2;

/**
 * The constellation's rotation after `elapsedMs` more of motion.
 *
 * The result is wrapped into a single turn, so the angle stays small however
 * long the canvas runs and never loses precision.
 */
export function advanced(rotation: number, elapsedMs: number): number {
  const next = (rotation + (elapsedMs / ORBIT_PERIOD_MS) * FULL_TURN) % FULL_TURN;
  return next < 0 ? next + FULL_TURN : next;
}

/**
 * Where a slot sits once the constellation has turned by `rotation` radians.
 *
 * The whole constellation turns as one rigid body, but a satellite only moves:
 * it never turns about its own centre, so its icon and label always stay
 * upright.
 */
export function positionAt(
  slot: OrbitSlot,
  center: { x: number; y: number },
  rotation: number,
): { x: number; y: number } {
  const angle = slot.angle + rotation;
  return {
    x: center.x + Math.cos(angle) * slot.radius,
    y: center.y + Math.sin(angle) * slot.radius,
  };
}
