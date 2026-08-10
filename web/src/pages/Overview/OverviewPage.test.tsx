// @vitest-environment jsdom
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { fireEvent } from "@testing-library/dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  aDirectoryPlace,
  aFleet,
  aNode,
  aRun,
  aSchedule,
  FakeApi,
  theUnknownPlace,
} from "../../test/fake-api";
import { OverviewPage } from "./OverviewPage";

const REFRESH_INTERVAL_MS = 5_000;

let api: FakeApi;

beforeEach(() => {
  api = new FakeApi();
  api.install();
});

afterEach(() => {
  cleanup();
  api.restore();
  vi.useRealTimers();
});

/** Renders the page and waits for the first snapshot to arrive. */
async function showOverview(): Promise<void> {
  render(<OverviewPage />);
  await waitFor(() =>
    expect(screen.queryByText("Loading orbit…")).not.toBeTruthy(),
  );
}

/**
 * Same, but on a controlled clock, for the tests that need the refresh to
 * happen. Real timers cannot be used there, and waitFor cannot be used here:
 * it does not recognise Vitest's fake timers and would hang.
 */
async function showOverviewOnAFakeClock(): Promise<void> {
  vi.useFakeTimers();
  render(<OverviewPage />);
  await tick();
}

/** Lets the pending requests settle and React apply the result. */
async function tick(ms = 0): Promise<void> {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

/** The orbit exposes each satellite as a button named "<label>, <status>". */
function satelliteNames(): string[] {
  return Array.from(document.querySelectorAll(".satellite")).map(
    (satellite) => satellite.getAttribute("aria-label") ?? "",
  );
}

/** The places the canvas draws, as the operator hears them named. */
function placeNames(): string[] {
  return Array.from(document.querySelectorAll(".place")).map(
    (place) => place.getAttribute("aria-label") ?? "",
  );
}

function railSection(title: string): HTMLElement {
  const aside = screen.getByRole("complementary");
  const section = within(aside).getByText(title).closest("section");
  if (!section) throw new Error(`no rail section titled ${title}`);
  return section as HTMLElement;
}

describe("the Overview", () => {
  it("shows one satellite per fleet, run and schedule", async () => {
    api.fleets = [aFleet()];
    api.runs = [aRun()];
    api.schedules = [aSchedule()];

    await showOverview();

    expect(satelliteNames()).toEqual([
      "Fix the flaky test, Done",
      "Nightly triage, Waiting",
      "Checkout revamp, In Progress",
    ]);
  });

  it("lists what the fleets have not started yet", async () => {
    api.fleets = [aFleet({ upNext: [aNode({ label: "Write the migration" })] })];

    await showOverview();

    expect(
      within(railSection("Up Next")).getByText("Write the migration"),
    ).toBeTruthy();
  });

  it("counts the work of each status", async () => {
    api.runs = [aRun({ id: "run-1" }), aRun({ id: "run-2" })];
    api.schedules = [aSchedule()];

    await showOverview();

    expect(screen.getByTitle("Filter by Done").textContent).toContain("2");
    expect(screen.getByTitle("Filter by Waiting").textContent).toContain("1");
  });

  it("reports that the API cannot be reached", async () => {
    api.down = true;

    render(<OverviewPage />);

    await waitFor(() =>
      expect(screen.getByRole("status").textContent).toContain(
        "Could not reach the Agent Hub API",
      ),
    );
  });

  it("shows the work that appeared since the last refresh", async () => {
    api.runs = [aRun({ id: "run-1", label: "Fix the flaky test" })];
    await showOverviewOnAFakeClock();

    api.runs = [
      ...api.runs,
      aRun({ id: "run-2", label: "Upgrade the driver", status: "in-progress" }),
    ];
    await tick(REFRESH_INTERVAL_MS);

    expect(satelliteNames()).toContain("Upgrade the driver, In Progress");
  });

  it("keeps the last snapshot and reports the error when a refresh fails", async () => {
    api.runs = [aRun({ label: "Fix the flaky test" })];
    await showOverviewOnAFakeClock();

    api.down = true;
    await tick(REFRESH_INTERVAL_MS);

    expect(satelliteNames()).toEqual(["Fix the flaky test, Done"]);
    expect(screen.getByRole("status").textContent).toContain(
      "Could not reach the Agent Hub API",
    );
  });

  it("stops polling once the page goes away", async () => {
    api.runs = [aRun()];
    await showOverviewOnAFakeClock();
    const pollingTimers = vi.getTimerCount();

    cleanup();

    expect(pollingTimers).toBeGreaterThan(0);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("shows the details of the satellite the operator picks", async () => {
    api.fleets = [aFleet()];
    await showOverview();

    fireEvent.click(
      screen.getByRole("button", { name: "Checkout revamp, In Progress" }),
    );

    const selected = railSection("Selected");
    expect(within(selected).getByText("Checkout revamp")).toBeTruthy();
    expect(within(selected).getByText(/1\/4/)).toBeTruthy();
  });

  it("drops a selection that the filter hides", async () => {
    api.fleets = [aFleet()];
    await showOverview();
    fireEvent.click(
      screen.getByRole("button", { name: "Checkout revamp, In Progress" }),
    );

    fireEvent.click(screen.getByTitle("Filter by Done"));

    expect(
      within(railSection("Selected")).getByText(
        "Select a satellite or a place to see its details.",
      ),
    ).toBeTruthy();
  });

  it("shows only the statuses the filter keeps", async () => {
    api.fleets = [aFleet()];
    api.runs = [aRun()];
    await showOverview();

    fireEvent.click(screen.getByTitle("Filter by Done"));

    expect(satelliteNames()).toEqual(["Fix the flaky test, Done"]);
  });
});

describe("the places on the Overview", () => {
  // A repository with one worktree, and the place nothing is known about.
  beforeEach(() => {
    api.locations = [
      theUnknownPlace(),
      aDirectoryPlace({ id: "repo", label: "checkout", directory: "/srv/checkout" }),
      aDirectoryPlace({
        id: "tree",
        label: "feature",
        parentId: "repo",
        directory: "/srv/feature",
      }),
    ];
  });

  it("groups the work into the places the API published", async () => {
    api.runs = [
      aRun({ id: "run-1", label: "In the checkout", locationId: "repo" }),
      aRun({ id: "run-2", label: "In the worktree", locationId: "tree" }),
    ];

    await showOverview();

    expect(placeNames()).toEqual([
      "Unknown, place, 0 items",
      "checkout, place, 1 item",
      "feature, place, 1 item",
    ]);
  });

  it("folds the worktrees into their repository on request", async () => {
    api.runs = [
      aRun({ id: "run-1", locationId: "repo" }),
      aRun({ id: "run-2", locationId: "tree" }),
    ];
    await showOverview();

    fireEvent.click(screen.getByRole("button", { name: "Collapse every place" }));

    expect(placeNames()).toEqual([
      "Unknown, place, 0 items",
      "checkout, place, 2 items, 1 place folded in",
    ]);
  });

  it("details the place the operator picks, with its work and what is under it", async () => {
    api.runs = [
      aRun({ id: "run-1", status: "done", locationId: "repo" }),
      aRun({ id: "run-2", status: "in-progress", locationId: "tree" }),
    ];
    await showOverview();

    fireEvent.click(screen.getByRole("button", { name: "checkout, place, 1 item" }));

    const selected = railSection("Selected");
    expect(within(selected).getByText("checkout")).toBeTruthy();
    expect(within(selected).getByText("/srv/checkout")).toBeTruthy();
    // The place answers for the work under it too: one done here, one running
    // in its worktree.
    expect(within(selected).getByText("Done:")).toBeTruthy();
    expect(within(selected).getByText("In Progress:")).toBeTruthy();
    expect(within(selected).getByText("feature")).toBeTruthy();
  });

  it("drops the place when the operator picks a satellite", async () => {
    api.runs = [aRun({ id: "run-1", label: "Fix the flaky test", locationId: "repo" })];
    await showOverview();
    fireEvent.click(screen.getByRole("button", { name: "checkout, place, 1 item" }));

    fireEvent.click(screen.getByRole("button", { name: "Fix the flaky test, Done" }));

    const selected = railSection("Selected");
    expect(within(selected).getByText("Fix the flaky test")).toBeTruthy();
    expect(within(selected).queryByText("/srv/checkout")).toBeNull();
  });

  it("leads to the page of the place the operator picked", async () => {
    api.runs = [aRun({ id: "run-1", locationId: "repo" })];
    await showOverview();

    fireEvent.click(screen.getByRole("button", { name: "checkout, place, 1 item" }));

    expect(
      within(railSection("Selected"))
        .getByRole("link", { name: "Open this place" })
        .getAttribute("href"),
    ).toBe("#/places/repo");
  });

  it("keeps the places it draws across a refresh", async () => {
    api.runs = [aRun({ id: "run-1", locationId: "tree" })];
    await showOverviewOnAFakeClock();
    const before = placeNames();

    api.runs = [...api.runs, aRun({ id: "run-2", locationId: "tree" })];
    await tick(REFRESH_INTERVAL_MS);

    expect(placeNames().map((name) => name.split(",")[0])).toEqual(
      before.map((name) => name.split(",")[0]),
    );
  });
});
