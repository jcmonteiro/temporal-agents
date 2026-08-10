import { describe, expect, it } from "vitest";
import { itemKey, sameItem } from "./work-item";

describe("work item identity", () => {
  it("tells a fleet and a run with the same id apart", () => {
    const fleet = { kind: "fleet" as const, id: "shared-id" };
    const run = { kind: "run" as const, id: "shared-id" };

    expect(sameItem(fleet, run)).toBe(false);
    expect(itemKey(fleet)).not.toBe(itemKey(run));
  });

  it("recognises the same item again", () => {
    const selected = { kind: "schedule" as const, id: "nightly" };

    expect(sameItem(selected, { kind: "schedule", id: "nightly" })).toBe(true);
  });

  it("matches nothing while no item is selected", () => {
    expect(sameItem({ kind: "fleet", id: "a" }, null)).toBe(false);
    expect(sameItem(null, null)).toBe(false);
  });
});
