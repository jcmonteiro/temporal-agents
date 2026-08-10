// @vitest-environment jsdom
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { fireEvent } from "@testing-library/dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { App } from "./app";
import { aRun, FakeApi, theOperator } from "./test/fake-api";

// The hub is gated in one place, so these tests drive the whole application:
// what an operator sees while signed out, what they see after signing in, and
// what happens when a session ends underneath them. Anything asserted at a
// smaller scope would pass while the shell rendered the hub anyway.

let api: FakeApi;

beforeEach(() => {
  api = new FakeApi();
  api.install();
  api.runs = [aRun({ label: "Fix the flaky test" })];
  window.location.hash = "";
});

afterEach(() => {
  cleanup();
  api.restore();
  vi.useRealTimers();
  window.location.hash = "";
});

/** Renders the hub and waits for the session answer to arrive. */
async function openTheHub(): Promise<void> {
  render(<App />);
  await waitFor(() => expect(screen.queryByText("Signing in…")).not.toBeTruthy());
}

it("shows a way in, and nothing else, while signed out", async () => {
  api.principal = null;

  await openTheHub();

  expect(screen.getByRole("heading", { name: /sign in to agent hub/i })).toBeTruthy();
  expect(screen.queryByText("Overview")).toBeNull();
  expect(screen.queryByText("Fix the flaky test")).toBeNull();
});

it("keeps where the operator was going across the sign-in", async () => {
  api.principal = null;
  window.location.hash = "#/places/dir-1";

  await openTheHub();

  const signIn = screen.getByRole("link", { name: /sign in/i });
  const href = signIn.getAttribute("href") ?? "";
  expect(href.startsWith("/api/v1/auth/sign-in?return=")).toBe(true);
  expect(decodeURIComponent(href.split("return=")[1] ?? "")).toContain("#/places/dir-1");
});

it("shows who is signed in, and lets them sign out", async () => {
  api.principal = theOperator({ name: "Ada Lovelace" });

  await openTheHub();
  expect(screen.getByText("Ada Lovelace")).toBeTruthy();

  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: /sign out/i }));
  });

  expect(screen.getByRole("heading", { name: /sign in to agent hub/i })).toBeTruthy();
  expect(screen.queryByText("Ada Lovelace")).toBeNull();
  expect(api.principal).toBeNull();
});

it("lands on the sign-in page when a session ends mid-use, and stays there", async () => {
  vi.useFakeTimers();
  render(<App />);
  await settle();
  expect(screen.queryByText(/sign in to agent hub/i)).toBeNull();

  // The session ends elsewhere — an operator signing out in another tab, or the
  // session simply expiring — so the next poll is refused.
  api.principal = null;
  await settle(5_000);

  expect(screen.getByRole("heading", { name: /sign in to agent hub/i })).toBeTruthy();
  const readsAfterFirstRefusal = api.sessionReads;
  await settle(30_000);
  expect(api.sessionReads).toBe(readsAfterFirstRefusal);
});

it("says the hub is unreachable rather than signing the operator out", async () => {
  api.down = true;

  await openTheHub();

  expect(screen.getByRole("heading", { name: /could not be reached/i })).toBeTruthy();
  expect(screen.queryByText(/sign in to agent hub/i)).toBeNull();

  api.down = false;
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: /try again/i }));
  });
  expect(screen.queryByText(/could not be reached/i)).toBeNull();
});

it("offers no sign-in where the deployment configures none", async () => {
  api.signInConfigured = false;

  await openTheHub();

  expect(screen.queryByText(/sign in to agent hub/i)).toBeNull();
  expect(screen.queryByRole("button", { name: /sign out/i })).toBeNull();
  // The hub itself is there: a deployment with no provider is the open local
  // one, and gating it behind a sign-in that leads nowhere would lock its
  // operator out of their own machine.
  expect(screen.getAllByText("Overview").length).toBeGreaterThan(0);
});

it("keeps no credential where a script could read one", async () => {
  await openTheHub();

  // The session is a cookie the server set HttpOnly. Nothing the frontend does
  // may put a credential somewhere a script — including an injected one — can
  // read it back.
  expect(Object.keys(window.localStorage ?? {})).toHaveLength(0);
  expect(Object.keys(window.sessionStorage ?? {})).toHaveLength(0);
  expect(document.cookie).toBe("");
});

/** Lets the pending requests settle and React apply the result. */
async function settle(ms = 0): Promise<void> {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}
