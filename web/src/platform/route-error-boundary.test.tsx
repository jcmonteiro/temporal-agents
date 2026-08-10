// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { fireEvent } from "@testing-library/dom";
import { afterEach, beforeEach, expect, it, vi, type MockInstance } from "vitest";
import { RouteErrorBoundary } from "./route-error-boundary";

// What the boundary is for: a page that throws must not take the hub with it.

let logged: MockInstance;

beforeEach(() => {
  // React reports the caught error itself; the test asserts the operator's
  // side of it, so the report is kept out of the run's output.
  logged = vi.spyOn(console, "error").mockImplementation(() => {});
});

afterEach(() => {
  cleanup();
  logged.mockRestore();
});

/** A page that cannot be drawn. */
function Broken(): never {
  throw new Error("the run could not be read");
}

it("says the page failed instead of leaving nothing", () => {
  render(
    <RouteErrorBoundary resetKey="#/runs/run-1">
      <Broken />
    </RouteErrorBoundary>,
  );

  const alert = screen.getByRole("alert");
  expect(alert.textContent).toContain("This page could not be shown");
  expect(alert.textContent).toContain("the run could not be read");
});

it("lets the operator ask for the page again", () => {
  const { rerender } = render(
    <RouteErrorBoundary resetKey="#/runs/run-1">
      <Broken />
    </RouteErrorBoundary>,
  );

  // Whatever made the page fail is over — a store that was restarting, say —
  // so asking again draws it.
  rerender(
    <RouteErrorBoundary resetKey="#/runs/run-1">
      <p>The run</p>
    </RouteErrorBoundary>,
  );
  fireEvent.click(screen.getByRole("button", { name: /try again/i }));

  expect(screen.getByText("The run")).toBeTruthy();
  expect(screen.queryByRole("alert")).toBeNull();
});

it("forgets a failure that belonged to the page the operator left", () => {
  const { rerender } = render(
    <RouteErrorBoundary resetKey="#/runs/run-1">
      <Broken />
    </RouteErrorBoundary>,
  );
  expect(screen.getByRole("alert")).toBeTruthy();

  rerender(
    <RouteErrorBoundary resetKey="#/settings">
      <p>Settings</p>
    </RouteErrorBoundary>,
  );

  expect(screen.getByText("Settings")).toBeTruthy();
  expect(screen.queryByRole("alert")).toBeNull();
});
