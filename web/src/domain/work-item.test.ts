import { describe, expect, it } from "vitest";
import { isDismissibleWorkItem, itemKey, sameItem, type WorkItem } from "./work-item";

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

describe("dismissible work items", () => {
  const item: WorkItem = {
    id: "work-1",
    kind: "run",
    label: "Reviewed work",
    status: "done",
    icon: "document",
    placeId: "unknown",
    dismissible: true,
    stateRevision: "revision-1",
  };

  it("requires a fleet or run with an exact revision", () => {
    expect(isDismissibleWorkItem(item)).toBe(true);
    expect(isDismissibleWorkItem({ ...item, kind: "fleet" })).toBe(true);
  });

  it("refuses schedules, missing revisions, and ineligible items", () => {
    expect(isDismissibleWorkItem({ ...item, kind: "schedule" })).toBe(false);
    expect(isDismissibleWorkItem({ ...item, stateRevision: undefined })).toBe(false);
    expect(isDismissibleWorkItem({ ...item, dismissible: false })).toBe(false);
  });
});
