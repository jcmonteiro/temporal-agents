import { describe, expect, it } from "vitest";
import { upNextKey } from "../domain/up-next";
import type { FleetDTO, LocationResource, RunDTO, ScheduleDTO } from "./api";
import {
  fromFleet,
  fromLocation,
  fromRun,
  fromSchedule,
  upNextOf,
} from "./mapping";

function aFleet(overrides: Partial<FleetDTO> = {}): FleetDTO {
  return {
    id: "fleet-1",
    kind: "fleet",
    label: "Checkout revamp",
    status: "in-progress",
    progress: { done: 1, total: 4, fraction: 0.25 },
    startedAt: null,
    endedAt: null,
    dismissible: false,
    ...overrides,
  };
}

function aRun(overrides: Partial<RunDTO> = {}): RunDTO {
  return {
    id: "run-1",
    kind: "run",
    type: "coder",
    label: "Fix the flaky test",
    status: "in-progress",
    startedAt: null,
    endedAt: null,
    iterations: 3,
    dismissible: false,
    ...overrides,
  };
}

function aSchedule(overrides: Partial<ScheduleDTO> = {}): ScheduleDTO {
  return {
    id: "schedule-1",
    kind: "schedule",
    label: "Nightly triage",
    spec: "0 2 * * *",
    status: "waiting",
    paused: false,
    runningActions: 0,
    lastRunAt: null,
    nextRunAt: null,
    dismissible: false,
    ...overrides,
  };
}

describe("the icon of a work item", () => {
  it("warns about failed work whatever its kind", () => {
    expect(fromFleet(aFleet({ status: "failed" })).icon).toBe("alert");
    expect(fromRun(aRun({ status: "failed" })).icon).toBe("alert");
    expect(fromSchedule(aSchedule({ status: "failed" })).icon).toBe("alert");
  });

  it("marks finished work as done", () => {
    expect(fromRun(aRun({ status: "done" })).icon).toBe("check");
  });

  it("shows work that waits for a person as people", () => {
    expect(fromFleet(aFleet({ status: "waiting-input" })).icon).toBe("users");
  });

  it("falls back to the icon of the kind", () => {
    expect(fromFleet(aFleet()).icon).toBe("rocket");
    expect(fromRun(aRun()).icon).toBe("document");
    expect(fromSchedule(aSchedule()).icon).toBe("clock");
  });
});

describe("the projection of a fleet", () => {
  it("keeps the identity, the status and the progress", () => {
    const item = fromFleet(aFleet());

    expect(item.kind).toBe("fleet");
    expect(item.id).toBe("fleet-1");
    expect(item.label).toBe("Checkout revamp");
    expect(item.status).toBe("in-progress");
    expect(item.progress).toEqual({ done: 1, total: 4, fraction: 0.25 });
  });

  it("falls back to the id when the label is empty", () => {
    expect(fromFleet(aFleet({ label: "" })).label).toBe("fleet-1");
  });

  it("carries no run or schedule fields", () => {
    const item = fromFleet(aFleet());

    expect(item.runType).toBeUndefined();
    expect(item.spec).toBeUndefined();
  });
});

describe("the projection of a run", () => {
  it("keeps the run type and the iteration count", () => {
    const item = fromRun(aRun());

    expect(item.kind).toBe("run");
    expect(item.runType).toBe("coder");
    expect(item.iterations).toBe(3);
    expect(item.progress).toBeUndefined();
  });

  it("falls back to the id when the label is empty", () => {
    expect(fromRun(aRun({ label: "" })).label).toBe("run-1");
  });
});

describe("the projection of a schedule", () => {
  it("keeps the schedule spec and the paused flag", () => {
    const item = fromSchedule(aSchedule({ paused: true }));

    expect(item.kind).toBe("schedule");
    expect(item.spec).toBe("0 2 * * *");
    expect(item.paused).toBe(true);
  });

  it("falls back to the id when the label is empty", () => {
    expect(fromSchedule(aSchedule({ label: "" })).label).toBe("schedule-1");
  });
});

describe("the place an item runs in", () => {
  it("keeps the reference the API published", () => {
    expect(fromFleet(aFleet({ locationId: "loc-a" })).placeId).toBe("loc-a");
    expect(fromRun(aRun({ locationId: "loc-b" })).placeId).toBe("loc-b");
    expect(fromSchedule(aSchedule({ locationId: "loc-c" })).placeId).toBe(
      "loc-c",
    );
  });

  it("reads an item with no reference as running in the unknown place", () => {
    expect(fromRun(aRun({ locationId: undefined })).placeId).toBe("unknown");
  });
});

describe("the projection of a place", () => {
  const directory: LocationResource = {
    id: "dir-1",
    kind: "directory",
    label: "checkout",
    parentId: "repo-1",
    directory: "/srv/checkout",
  };

  it("keeps the identity, the kind, the label and the parent", () => {
    expect(fromLocation(directory)).toEqual({
      id: "dir-1",
      kind: "directory",
      label: "checkout",
      parentId: "repo-1",
      directory: "/srv/checkout",
      ref: undefined,
    });
  });

  it("reads a place with no published parent as a root", () => {
    const orphan = { ...directory, parentId: null };

    expect(fromLocation(orphan).parentId).toBeNull();
  });
});

describe("up next", () => {
  it("lists the nodes that the fleets have not started", () => {
    const fleets = [
      aFleet({
        id: "fleet-a",
        upNext: [
          { id: "node-1", label: "Write the migration", status: "todo", execution: null },
          { id: "node-2", label: "", status: "waiting", execution: null },
        ],
      }),
      aFleet({ id: "fleet-b", upNext: [] }),
    ];

    const entries = upNextOf(fleets);

    expect(entries.map((e) => e.label)).toEqual([
      "Write the migration",
      "node-2",
    ]);
    expect(entries.map((e) => e.status)).toEqual(["todo", "waiting"]);
  });

  it("is empty when no fleet reports what comes next", () => {
    expect(upNextOf([aFleet()])).toEqual([]);
  });

  it("keeps entries of two fleets apart", () => {
    const shared = { id: "node-1", label: "Same name", status: "todo" as const, execution: null };
    const entries = upNextOf([
      aFleet({ id: "fleet-a", upNext: [shared] }),
      aFleet({ id: "fleet-b", upNext: [shared] }),
    ]);

    expect(entries).toHaveLength(2);
    expect(upNextKey(entries[0])).not.toBe(upNextKey(entries[1]));
  });

  it("names the fleet an entry belongs to", () => {
    const entries = upNextOf([
      aFleet({
        id: "fleet-a",
        upNext: [{ id: "node-1", label: "Write the migration", status: "todo", execution: null }],
      }),
    ]);

    expect(entries[0].fleetId).toBe("fleet-a");
    expect(entries[0].nodeId).toBe("node-1");
  });
});
