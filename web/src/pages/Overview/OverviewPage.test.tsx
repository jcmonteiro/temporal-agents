// @vitest-environment jsdom
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { fireEvent } from "@testing-library/dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { aFleet, aNode, aRun, aSchedule, FakeApi } from "../../test/fake-api";
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
  return screen
    .getAllByRole("button")
    .map((b) => b.getAttribute("aria-label"))
    .filter((name): name is string => name !== null && name.includes(","));
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
        "Select a satellite to see its details.",
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
