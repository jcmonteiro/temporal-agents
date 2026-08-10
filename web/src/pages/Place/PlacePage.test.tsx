// @vitest-environment jsdom
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { fireEvent } from "@testing-library/dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  aDirectoryPlace,
  aRun,
  aSchedule,
  FakeApi,
  theUnknownPlace,
} from "../../test/fake-api";
import { PlacePage } from "./PlacePage";

let api: FakeApi;

beforeEach(() => {
  api = new FakeApi();
  api.install();
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
  // The hub knows the worktree — and, through it, the repository above — because
  // an operator registered it. Without that, a place nothing has ever run in is
  // published by nothing and the page would rightly say it knows no such place.
  api.registered = [{ locationId: "tree", registeredAt: "2026-08-06T12:00:00Z" }];
});

afterEach(() => {
  cleanup();
  api.restore();
  vi.useRealTimers();
});

/** Renders the page of one place and waits for the first read to arrive. */
async function showPlace(placeId: string): Promise<void> {
  render(<PlacePage placeId={placeId} />);
  await waitFor(() =>
    expect(screen.queryByText("Loading this place…")).not.toBeTruthy(),
  );
}

describe("the page of one place", () => {
  it("shows what the place is and what runs there", async () => {
    api.runs = [
      aRun({ id: "run-1", label: "Fix the flaky test", locationId: "repo" }),
      aRun({ id: "run-2", label: "In the worktree", locationId: "tree" }),
      aRun({ id: "run-3", label: "Somewhere else", locationId: "unknown" }),
    ];

    await showPlace("repo");

    expect(screen.getByRole("heading", { name: "checkout" })).toBeTruthy();
    expect(screen.getByText("/srv/checkout")).toBeTruthy();
    // The work of the place and of every place under it, and nothing else.
    expect(screen.getByRole("button", { name: /Fix the flaky test/ })).toBeTruthy();
    expect(screen.getByRole("button", { name: /In the worktree/ })).toBeTruthy();
    expect(screen.queryByRole("button", { name: /Somewhere else/ })).toBeNull();
  });

  it("groups the work by state", async () => {
    api.runs = [aRun({ id: "run-1", status: "done", locationId: "repo" })];
    api.schedules = [
      aSchedule({ id: "schedule-1", status: "waiting", locationId: "repo" }),
    ];

    await showPlace("repo");

    const work = screen.getByText("Work here").closest("section") as HTMLElement;
    expect(within(work).getByText("Waiting")).toBeTruthy();
    expect(within(work).getByText("Done")).toBeTruthy();
  });

  it("names the places above and below it", async () => {
    await showPlace("tree");

    const above = screen.getByRole("navigation", { name: "Places above this one" });
    expect(within(above).getByRole("link", { name: "checkout" })).toBeTruthy();

    await cleanup();
    await showPlace("repo");
    const here = screen.getByText("Places here").closest("section") as HTMLElement;
    expect(within(here).getByRole("link", { name: "feature" })).toBeTruthy();
  });

  it("says a place with no work is idle, not broken", async () => {
    await showPlace("tree");

    expect(screen.getByText("Nothing runs here at the moment.")).toBeTruthy();
  });

  it("says plainly that it knows no such place", async () => {
    await showPlace("gone");

    expect(screen.getByRole("heading", { name: "No such place" })).toBeTruthy();
  });

  it("reports that the API cannot be reached", async () => {
    api.down = true;

    render(<PlacePage placeId="repo" />);

    await waitFor(() =>
      expect(screen.getByRole("status").textContent).toContain(
        "Could not reach the Agent Hub API",
      ),
    );
  });

  it("shows the details of the work the operator picks", async () => {
    api.runs = [
      aRun({ id: "run-1", label: "Fix the flaky test", locationId: "repo" }),
    ];
    await showPlace("repo");

    fireEvent.click(screen.getByRole("button", { name: /Fix the flaky test/ }));

    const detail = screen.getByRole("complementary");
    expect(within(detail).getByText("Fix the flaky test")).toBeTruthy();
    expect(within(detail).getByText("Done")).toBeTruthy();
  });

  it("leads back to the overview", async () => {
    await showPlace("repo");

    expect(
      screen
        .getByRole("link", { name: "← Back to the overview" })
        .getAttribute("href"),
    ).toBe("#/");
  });

  it("shows the work that appeared since the last refresh", async () => {
    vi.useFakeTimers();
    render(<PlacePage placeId="repo" />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });

    api.runs = [aRun({ id: "run-9", label: "Upgrade the driver", locationId: "repo" })];
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5_000);
    });

    expect(screen.getByRole("button", { name: /Upgrade the driver/ })).toBeTruthy();
  });

  it("stops polling once the page goes away", async () => {
    vi.useFakeTimers();
    render(<PlacePage placeId="repo" />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    const polling = vi.getTimerCount();

    cleanup();

    expect(polling).toBeGreaterThan(0);
    expect(vi.getTimerCount()).toBe(0);
  });
});
