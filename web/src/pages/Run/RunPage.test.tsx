// @vitest-environment jsdom
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { fireEvent } from "@testing-library/dom";
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
  window.location.hash = "";
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
  const summary = screen.getByLabelText("Run summary");
  expect(within(summary).getByRole("link", { name: "checkout" }).getAttribute("href")).toBe(
    "#/places/repo",
  );
  expect(screen.getByText("2026-08-06T12:00:00Z")).toBeTruthy();
  expect(screen.getByText("Still running")).toBeTruthy();
  expect(screen.getByText("2")).toBeTruthy();
});

it("organizes run state, operational details, and available actions for scanning", async () => {
  api.runs = [
    aRun({
      id: "develop-1",
      type: "develop",
      label: "Fix the flaky test",
      status: "in-progress",
      locationId: "repo",
    }),
  ];

  await showRun("develop-1");

  expect(screen.getByRole("status", { name: "Run status: In Progress" })).toBeTruthy();
  expect(screen.getByRole("region", { name: "Operational details" })).toBeTruthy();
  const actions = screen.getByRole("region", { name: "Available actions" });
  expect(actions.textContent).toContain("Run this again");

  const breadcrumb = screen.getByRole("navigation", { name: "Breadcrumb" });
  expect(within(breadcrumb).getByRole("link", { name: "Overview" }).getAttribute("href")).toBe(
    "#/",
  );
  expect(within(breadcrumb).getByRole("link", { name: "checkout" }).getAttribute("href")).toBe(
    "#/places/repo",
  );
  expect(within(breadcrumb).getByText("Fix the flaky test").getAttribute("aria-current")).toBe(
    "page",
  );
  expect(screen.queryByText(/back to overview/i)).toBeNull();
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

it("says who started it and which instruction it ran under", async () => {
  api.runs = [aRun({ id: "develop-1", label: "Fix the flaky test", locationId: "repo" })];
  api.startedBy["develop-1"] = "https://issuer.test|operator-1";
  api.instructionsUsed["develop-1"] = [
    {
      key: "review.perform",
      scope: "directory:/srv/checkout",
      version: 3,
      modelScope: "global",
      modelVersion: 4,
    },
  ];

  await showRun("develop-1");

  expect(screen.getByText("https://issuer.test|operator-1")).toBeTruthy();
  const provenance = screen.getByRole("region", { name: /instructions it ran under/i });
  expect(provenance.textContent).toContain("review.perform");
  expect(provenance.textContent).toContain("directory:/srv/checkout");
  expect(provenance.textContent).toContain("version 3");
});

it("says plainly when the hub did not start the run", async () => {
  api.runs = [aRun({ id: "develop-1", label: "Fix the flaky test", locationId: "repo" })];

  await showRun("develop-1");

  expect(screen.getByText("Not started from the hub")).toBeTruthy();
});

it("repeats the run in one action, and lands on the new one", async () => {
  api.runs = [
    aRun({ id: "develop-1", type: "develop", label: "Fix the flaky test", locationId: "repo" }),
  ];
  await showRun("develop-1");

  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "Run this again" }));
  });

  const started = Object.values(api.launches);
  expect(started).toHaveLength(1);
  // Exactly what the record holds: the same pass, the same instruction, the same
  // place — and nothing invented.
  expect(started[0].type).toBe("develop");
  expect(started[0].label).toBe("Fix the flaky test");
  expect(started[0].locationId).toBe("repo");
  expect(window.location.hash).toBe(`#/runs/${started[0].id}`);
});

it("repeats once however impatiently the operator clicks", async () => {
  api.runs = [
    aRun({ id: "develop-1", type: "develop", label: "Fix the flaky test", locationId: "repo" }),
  ];
  await showRun("develop-1");
  const repeat = screen.getByRole("button", { name: "Run this again" });

  await act(async () => {
    fireEvent.click(repeat);
    fireEvent.click(repeat);
  });

  expect(Object.values(api.launches)).toHaveLength(1);
});

it("refuses to repeat a run whose place was never recorded", async () => {
  api.runs = [
    aRun({ id: "develop-1", type: "develop", label: "Fix the flaky test", locationId: "unknown" }),
  ];

  await showRun("develop-1");

  const repeat = screen.getByRole("button", { name: "Run this again" });
  expect(repeat.hasAttribute("disabled")).toBe(true);
  expect(screen.getByText(/never recorded/i)).toBeTruthy();
  expect(Object.values(api.launches)).toHaveLength(0);
});

it("refuses to repeat work the hub does not start", async () => {
  api.runs = [
    aRun({ id: "run-1", type: "prompt", label: "A one-off prompt", locationId: "repo" }),
  ];

  await showRun("run-1");

  expect(screen.getByRole("button", { name: "Run this again" }).hasAttribute("disabled")).toBe(true);
  expect(screen.getByText(/is not started from the hub/i)).toBeTruthy();
});

it("says why a repeat would collide, and leads to the work in the way", async () => {
  api.runs = [
    aRun({ id: "develop-1", type: "develop", label: "Fix the flaky test", locationId: "repo" }),
  ];
  api.busy = { locationId: "repo", runId: "develop-9" };
  await showRun("develop-1");

  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "Run this again" }));
  });

  expect(screen.getByRole("alert").textContent).toContain("develop-9");
  expect(
    screen.getByRole("link", { name: /run in the way/i }).getAttribute("href"),
  ).toBe("#/runs/develop-9");
});
