// @vitest-environment jsdom
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { RunPage } from "./RunPage";
import { aDirectoryPlace, aRun, FakeApi, theUnknownPlace } from "../../test/fake-api";

// The run page is where an operator lands the moment they start work, so what it
// says while the hub has not caught up matters as much as what it says afterwards.

let api: FakeApi;

beforeEach(() => {
  api = new FakeApi();
  api.install();
  api.locations = [
    theUnknownPlace(),
    aDirectoryPlace({ id: "repo", label: "checkout", directory: "/srv/checkout" }),
  ];
});

afterEach(() => {
  cleanup();
  api.restore();
  vi.useRealTimers();
});

/** Opens the page of one run and waits for the first read. */
async function showRun(runId: string): Promise<void> {
  render(<RunPage runId={runId} />);
  await waitFor(() => expect(screen.queryByText("Loading this run…")).toBeNull());
}

it("reports what the run is, where it runs and how it stands", async () => {
  api.runs = [
    aRun({
      id: "develop-1",
      label: "Fix the flaky test",
      status: "in-progress",
      locationId: "repo",
      iterations: 2,
      startedAt: "2026-08-06T12:00:00Z",
      endedAt: null,
    }),
  ];

  await showRun("develop-1");

  expect(screen.getByRole("heading", { name: "Fix the flaky test" })).toBeTruthy();
  expect(screen.getByText("In Progress")).toBeTruthy();
  expect(screen.getByRole("link", { name: "checkout" }).getAttribute("href")).toBe(
    "#/places/repo",
  );
  expect(screen.getByText("2026-08-06T12:00:00Z")).toBeTruthy();
  expect(screen.getByText("Still running")).toBeTruthy();
  expect(screen.getByText("2")).toBeTruthy();
});

it("says a run that has only just been started is starting, not missing", async () => {
  // The operator has just landed here from the launcher. The orchestrator has
  // accepted the work and the read path does not list it yet.
  await showRun("develop-1");

  const said = screen.getByRole("status").textContent ?? "";
  expect(said).toContain("Starting");
  expect(said).not.toMatch(/no such run/i);
});

it("shows the run as soon as the hub reports it", async () => {
  vi.useFakeTimers();
  render(<RunPage runId="develop-1" />);
  await act(async () => {
    await vi.advanceTimersByTimeAsync(0);
  });
  expect(screen.getByRole("status").textContent).toContain("Starting");

  api.runs = [aRun({ id: "develop-1", label: "Fix the flaky test", locationId: "repo" })];
  await act(async () => {
    await vi.advanceTimersByTimeAsync(5_000);
  });

  expect(screen.getByRole("heading", { name: "Fix the flaky test" })).toBeTruthy();
});

it("stops calling a run that never appears starting", async () => {
  vi.useFakeTimers();
  render(<RunPage runId="develop-1" />);

  // A minute of asking is long past a start's delay: whatever this address names,
  // the hub has not got it.
  await act(async () => {
    await vi.advanceTimersByTimeAsync(65_000);
  });

  expect(screen.getByRole("heading", { name: "No such run" })).toBeTruthy();
});

it("reports that the API cannot be reached", async () => {
  api.down = true;

  render(<RunPage runId="develop-1" />);

  await waitFor(() =>
    expect(screen.getByRole("status").textContent).toContain(
      "could not be reached",
    ),
  );
});
