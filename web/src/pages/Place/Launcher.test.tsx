// @vitest-environment jsdom
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { fireEvent } from "@testing-library/dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { PlacePage } from "./PlacePage";
import { aDirectoryPlace, aRun, FakeApi, theUnknownPlace } from "../../test/fake-api";

// Starting work from the hub, driven through the place page against the stubbed
// HTTP edge: what the operator can ask for, what happens when they ask twice, and
// what a refusal reads like.

let api: FakeApi;

beforeEach(() => {
  api = new FakeApi();
  api.install();
  api.locations = [
    theUnknownPlace(),
    aDirectoryPlace({ id: "repo", label: "checkout", directory: "/srv/checkout" }),
  ];
  api.registered = [{ locationId: "repo", registeredAt: "2026-08-06T12:00:00Z" }];
  window.location.hash = "";
});

afterEach(() => {
  cleanup();
  api.restore();
  vi.useRealTimers();
  window.location.hash = "";
});

/** Opens the place page and waits for the first read. */
async function showPlace(): Promise<void> {
  render(<PlacePage placeId="repo" />);
  await waitFor(() =>
    expect(screen.queryByText("Loading this place…")).not.toBeTruthy(),
  );
}

/** Fills in what to do and submits, as an operator does. */
async function startDevelop(what: string): Promise<void> {
  fireEvent.change(screen.getByLabelText("What to do"), { target: { value: what } });
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "Start" }));
  });
}

it("starts a develop pass here and lands on the run", async () => {
  await showPlace();

  await startDevelop("make the flaky test pass");

  expect(window.location.hash).toBe("#/runs/develop-1");
  const started = Object.values(api.launches);
  expect(started).toHaveLength(1);
  expect(started[0].label).toBe("make the flaky test pass");
  expect(started[0].locationId).toBe("repo");
});

it("shows where the work will run, and offers no way to type it", async () => {
  await showPlace();

  const launcher = screen.getByRole("region", { name: "Start work here" });
  expect(launcher.textContent).toContain("/srv/checkout");
  // Every field the launcher offers is about the work, never about the place:
  // the request names the place, and the server resolves the directory.
  const fields = [
    ...launcher.querySelectorAll("input"),
    ...launcher.querySelectorAll("textarea"),
  ];
  for (const field of fields) {
    expect(field.getAttribute("type") === "radio" || field.id === "launch-prompt").toBe(true);
  }
});

it("offers a review, which is told nothing", async () => {
  await showPlace();

  fireEvent.click(screen.getByLabelText("Review"));

  expect(screen.queryByLabelText("What to do")).toBeNull();
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "Start" }));
  });
  expect(Object.values(api.launches)[0].type).toBe("review");
});

it("starts one run however impatiently the operator clicks", async () => {
  await showPlace();
  fireEvent.change(screen.getByLabelText("What to do"), {
    target: { value: "make the flaky test pass" },
  });
  const start = screen.getByRole("button", { name: "Start" });

  await act(async () => {
    fireEvent.click(start);
    fireEvent.click(start);
    fireEvent.click(start);
  });

  expect(Object.values(api.launches)).toHaveLength(1);
});

it("says why it would not start, and leads to the work in the way", async () => {
  api.busy = { locationId: "repo", runId: "develop-9" };
  await showPlace();

  await startDevelop("make the flaky test pass");

  const refusal = screen.getByRole("alert");
  expect(refusal.textContent).toContain("develop-9");
  expect(
    screen.getByRole("link", { name: /run in the way/i }).getAttribute("href"),
  ).toBe("#/runs/develop-9");
  // The operator is still here, with what they typed, so they can try again.
  expect(window.location.hash).toBe("");
  expect((screen.getByLabelText("What to do") as HTMLTextAreaElement).value).toBe(
    "make the flaky test pass",
  );
});

it("retrying the same intent starts the run it already started", async () => {
  api.busy = { locationId: "repo", runId: "develop-9" };
  await showPlace();
  await startDevelop("make the flaky test pass");

  // The work in the way settles, and the operator tries the same thing again.
  api.busy = null;
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "Start" }));
  });
  const identities = Object.keys(api.launches);

  expect(identities).toHaveLength(1);
  expect(window.location.hash).toBe("#/runs/develop-1");
});

it("asks for something else under an identity of its own", async () => {
  // The first attempt is refused, and the operator changes their mind about what
  // the run should do. That is different work, so it must not be answered with
  // the run the first intent would have started.
  api.busy = { locationId: "repo", runId: "develop-9" };
  await showPlace();
  await startDevelop("make the flaky test pass");
  api.busy = null;

  await startDevelop("and now the other one");

  const started = Object.values(api.launches);
  expect(started).toHaveLength(1);
  expect(started[0].label).toBe("and now the other one");
  expect(window.location.hash).toBe("#/runs/develop-1");
});

it("keeps the launcher out of the way of the work the place already holds", async () => {
  api.runs = [aRun({ id: "run-1", label: "Fix the flaky test", locationId: "repo" })];

  await showPlace();

  expect(screen.getByRole("button", { name: /Fix the flaky test/ })).toBeTruthy();
  expect(screen.getByRole("region", { name: "Start work here" })).toBeTruthy();
});
