import { describe, expect, it } from "vitest";
import type { WorkItem, WorkItemStatus } from "../../domain/work-item";
import { layoutOrbit, type OrbitLayout, type OrbitSlot } from "./layout";

const CANVAS = { width: 900, height: 640 };

// Design requirement, stated independently of the layout algorithm: a
// satellite is 60px across and carries a label, so two of them must never sit
// closer than this.
const MINIMUM_SATELLITE_DISTANCE = 90;

function items(status: WorkItemStatus, count: number): WorkItem[] {
  return Array.from({ length: count }, (_, i) => ({
    id: `${status}-${i}`,
    kind: "run" as const,
    label: `${status} ${i}`,
    status,
    icon: "document" as const,
    placeId: "unknown",
  }));
}

function slotOf(layout: OrbitLayout, id: string): OrbitSlot {
  const slot = layout.slots.find((s) => s.item.id === id);
  if (!slot) throw new Error(`no slot for ${id}`);
  return slot;
}

function closestPairDistance(layout: OrbitLayout): number {
  let closest = Infinity;
  layout.slots.forEach((a, i) => {
    layout.slots.slice(i + 1).forEach((b) => {
      closest = Math.min(closest, Math.hypot(a.x - b.x, a.y - b.y));
    });
  });
  return closest;
}

describe("the orbit layout", () => {
  it("gives every item exactly one slot", () => {
    const all = [...items("done", 3), ...items("waiting", 2), ...items("todo", 4)];

    const layout = layoutOrbit(all, CANVAS);

    expect(layout.slots).toHaveLength(9);
    expect(new Set(layout.slots.map((s) => s.item.id)).size).toBe(9);
  });

  it("puts settled work closer to the centre than active work", () => {
    const layout = layoutOrbit([...items("done", 1), ...items("todo", 1)], CANVAS);

    expect(slotOf(layout, "done-0").radius).toBeLessThan(
      slotOf(layout, "todo-0").radius,
    );
  });

  it("keeps satellites apart when a status holds far more than one ring fits", () => {
    const layout = layoutOrbit(items("in-progress", 40), CANVAS);

    expect(closestPairDistance(layout)).toBeGreaterThanOrEqual(
      MINIMUM_SATELLITE_DISTANCE,
    );
  });

  it("moves the items a ring cannot hold outwards to another ring", () => {
    const layout = layoutOrbit(items("in-progress", 40), CANVAS);

    const radii = [...new Set(layout.slots.map((s) => s.radius))].sort(
      (a, b) => a - b,
    );
    expect(radii.length).toBeGreaterThan(1);
    // The overflow ring is one of the rings the layout reports, so the canvas
    // draws a guide line for it.
    radii.forEach((r) => expect(layout.orbits).toContain(r));
  });

  it("leaves an item on its ring when another status empties", () => {
    const withSettled = layoutOrbit(
      [...items("done", 5), ...items("todo", 3)],
      CANVAS,
    );
    const withoutSettled = layoutOrbit(items("todo", 3), CANVAS);

    expect(slotOf(withoutSettled, "todo-0").radius).toBe(
      slotOf(withSettled, "todo-0").radius,
    );
  });

  it("places the same items in the same positions every time", () => {
    const all = [...items("failed", 2), ...items("paused", 7)];

    expect(layoutOrbit(all, CANVAS)).toEqual(layoutOrbit(all, CANVAS));
  });

  it("centres the constellation on the canvas", () => {
    const layout = layoutOrbit(items("todo", 1), { width: 800, height: 600 });

    expect(layout.center).toEqual({ x: 400, y: 300 });
  });
});
