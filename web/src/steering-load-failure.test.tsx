// @vitest-environment jsdom
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { fireEvent } from "@testing-library/dom";
import { afterEach, beforeEach, expect, it, vi, type MockInstance } from "vitest";
import { App } from "./app";
import { aRun, aSteeringSession, FakeApi } from "./test/fake-api";

const modalLoad = vi.hoisted(() => {
  let reject: (error: Error) => void = () => undefined;
  const result = new Promise<never>((_resolve, rejectResult) => {
    reject = rejectResult;
  });
  return { reject, result };
});

// A chunk request is an external boundary. Keep the application real and hold
// only that request open, then reject it as a stale deployment chunk can reject.
vi.mock("./platform/steering-modal", () => modalLoad.result);

let api: FakeApi;
let logged: MockInstance;

beforeEach(() => {
  api = new FakeApi();
  api.runs = [aRun()];
  api.steeringSessions["steering-review-1"] = aSteeringSession({ itemId: "run-1" });
  api.install();
  logged = vi.spyOn(console, "error").mockImplementation(() => {});
  window.location.hash = "#/runs/run-1";
});

afterEach(() => {
  cleanup();
  api.restore();
  logged.mockRestore();
  window.location.hash = "";
});

it("contains a rejected steering chunk and keeps the application available", async () => {
  render(<App />);

  const open = await screen.findByRole("button", { name: /needs guidance/i });
  fireEvent.click(open);

  const loading = screen.getByText("Opening steering…").closest('[role="status"]');
  expect(loading).toBeTruthy();
  expect(screen.getByRole("button", { name: "Close steering" })).toBeTruthy();

  await act(async () => {
    modalLoad.reject(new Error("the steering chunk could not be loaded"));
  });

  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toContain("Steering could not be opened");
  expect(screen.getByRole("button", { name: "Reload application" })).toBeTruthy();

  // The chunk failure belongs to the modal. The application shell and route
  // remain mounted, and closing the failed modal returns to that route.
  expect(screen.getByRole("navigation", { name: "Sections" })).toBeTruthy();
  expect(screen.getByRole("heading", { name: "Fix the flaky test" })).toBeTruthy();
  fireEvent.click(within(alert).getByRole("button", { name: "Close steering" }));
  await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
  expect(screen.getByRole("heading", { name: "Fix the flaky test" })).toBeTruthy();
});
