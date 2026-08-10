import { describe, expect, it } from "vitest";
import { layoutOrbit, type OrbitSlot } from "./layout";
import { advanced, ORBIT_PERIOD_MS, positionAt } from "./orbit-motion";

const CANVAS = { width: 900, height: 640 };
const FULL_TURN = Math.PI * 2;

function constellation(count: number): { center: { x: number; y: number }; slots: OrbitSlot[] } {
  const layout = layoutOrbit(
    Array.from({ length: count }, (_, i) => ({
      id: `run-${i}`,
      kind: "run" as const,
      label: `Run ${i}`,
      status: "in-progress" as const,
      icon: "document" as const,
      placeId: "unknown",
    })),
    CANVAS,
  );
  return { center: layout.center, slots: layout.slots };
}

function distanceToCenter(p: { x: number; y: number }, c: { x: number; y: number }): number {
  return Math.hypot(p.x - c.x, p.y - c.y);
}

describe("advancing the rotation", () => {
  it("completes one turn in one period", () => {
    expect(advanced(0, ORBIT_PERIOD_MS / 4)).toBeCloseTo(FULL_TURN / 4, 10);
    expect(advanced(0, ORBIT_PERIOD_MS / 2)).toBeCloseTo(FULL_TURN / 2, 10);
  });

  it("comes back to the same angle after a full period", () => {
    expect(advanced(1, ORBIT_PERIOD_MS)).toBeCloseTo(1, 10);
  });

  it("stays within one turn however long the motion runs", () => {
    // An hour of motion at 60 frames per second, in one-frame steps.
    let rotation = 0;
    let highest = 0;
    let lowest = 0;
    for (let frame = 0; frame < 60 * 60 * 60; frame += 1) {
      rotation = advanced(rotation, 1000 / 60);
      highest = Math.max(highest, rotation);
      lowest = Math.min(lowest, rotation);
    }

    expect(lowest).toBeGreaterThanOrEqual(0);
    expect(highest).toBeLessThan(FULL_TURN);
  });
});

describe("positioning a satellite", () => {
  it("uses the slot's own place before any motion", () => {
    const { center, slots } = constellation(5);

    slots.forEach((slot) => {
      const at = positionAt(slot, center, 0);
      expect(at.x).toBeCloseTo(slot.x, 10);
      expect(at.y).toBeCloseTo(slot.y, 10);
    });
  });

  it("keeps every satellite on its own ring", () => {
    const { center, slots } = constellation(9);

    [0.3, 1.7, 4.2, 123.4].forEach((rotation) => {
      slots.forEach((slot) => {
        expect(distanceToCenter(positionAt(slot, center, rotation), center)).toBeCloseTo(
          slot.radius,
          10,
        );
      });
    });
  });

  it("turns the constellation as one rigid body", () => {
    const { center, slots } = constellation(6);
    const [a, b] = slots;
    const gapBefore = Math.hypot(a.x - b.x, a.y - b.y);

    const turned = [a, b].map((slot) => positionAt(slot, center, 1.1));

    expect(Math.hypot(turned[0].x - turned[1].x, turned[0].y - turned[1].y)).toBeCloseTo(
      gapBefore,
      10,
    );
  });
});
