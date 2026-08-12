// @vitest-environment jsdom
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { fireEvent } from "@testing-library/dom";
import { afterEach, beforeEach, expect, it } from "vitest";
import { App } from "./app";
import { aDirectoryPlace, aRun, FakeApi, theUnknownPlace } from "./test/fake-api";

// Routing is asserted through the whole hub, because that is where it matters:
// the address decides the page, the page arrives on demand, and the shell
// around it stays put. Rendering a page directly would prove none of that.

let api: FakeApi;

beforeEach(() => {
  api = new FakeApi();
  api.install();
  api.locations = [theUnknownPlace(), aDirectoryPlace()];
  api.runs = [aRun({ label: "Fix the flaky test", locationId: "dir-1" })];
  window.location.hash = "";
});

afterEach(() => {
  cleanup();
  api.restore();
  window.location.hash = "";
});

/** Opens the hub at an address, as a bookmark or a shared link would. */
async function openAt(address: string): Promise<void> {
  window.location.hash = address;
  render(<App />);
  await settled();
}

/** Waits until the session is answered and the page's module has arrived. */
async function settled(): Promise<void> {
  await waitFor(() => {
    expect(screen.queryByText("Signing in…")).toBeNull();
    expect(screen.queryByText("Opening…")).toBeNull();
  });
}

it("opens the overview when the address names nothing", async () => {
  await openAt("");

  expect(await screen.findByRole("heading", { name: "Overview" })).toBeTruthy();
});

it("opens the place a deep link names", async () => {
  await openAt("#/places/dir-1");

  expect(await screen.findByRole("heading", { name: "checkout" })).toBeTruthy();
});

it("opens the run a deep link names", async () => {
  await openAt("#/runs/run-1");

  expect(await screen.findByRole("heading", { name: "Fix the flaky test" })).toBeTruthy();
});

it("opens the fleet a deep link names", async () => {
  await openAt("#/fleets/fleet-1");

  expect(screen.getByRole("heading", { name: "Fleet" })).toBeTruthy();
  expect(screen.getByText("fleet-1")).toBeTruthy();
});

it("opens the settings a deep link names", async () => {
  await openAt("#/settings");

  expect(screen.getByRole("heading", { name: "Settings" })).toBeTruthy();
});

it("opens the overview when the address names a page the hub has not got", async () => {
  await openAt("#/somewhere/else");

  expect(await screen.findByRole("heading", { name: "Overview" })).toBeTruthy();
});

it("keeps the shell while the page changes", async () => {
  await openAt("#/settings");
  const sections = screen.getByRole("navigation", { name: "Sections" });

  expect(within(sections).getByRole("link", { name: "Overview" })).toBeTruthy();
  expect(within(sections).queryByText("Insights")).toBeNull();
  expect(screen.getByText("Agent Hub")).toBeTruthy();
});

it("navigates between sections, and says which one is open", async () => {
  await openAt("");
  const sections = screen.getByRole("navigation", { name: "Sections" });

  // Every destination is a link: reachable by keyboard, openable in a new tab,
  // and readable before it is followed.
  const settings = within(sections).getByRole("link", { name: "Settings" });
  expect(settings.getAttribute("href")).toBe("#/settings");

  await follow(settings);

  expect(await screen.findByRole("heading", { name: "Settings" })).toBeTruthy();
  expect(settings.getAttribute("aria-current")).toBe("page");
  expect(
    within(sections).getByRole("link", { name: "Overview" }).getAttribute("aria-current"),
  ).toBeNull();
});

it("offers no destination for a section that has none", async () => {
  await openAt("");
  const sections = screen.getByRole("navigation", { name: "Sections" });

  const workflows = within(sections).getByRole("button", { name: "Workflows" });
  expect(workflows.hasAttribute("disabled")).toBe(true);
  expect(within(sections).queryByRole("link", { name: "Workflows" })).toBeNull();
});

it("leads from a piece of work to its own page", async () => {
  await openAt("#/places/dir-1");

  fireEvent.click(await screen.findByRole("button", { name: /Fix the flaky test/ }));
  const details = screen.getByRole("link", { name: /view details/i });
  expect(details.getAttribute("href")).toBe("#/runs/run-1");

  await follow(details);

  expect(await screen.findByRole("heading", { name: "Fix the flaky test" })).toBeTruthy();
});

/**
 * Follows a link the way an operator does.
 *
 * jsdom stops at the click: it never follows a link, so the browser's part of
 * the act — taking the address from the link and announcing the change — is
 * played here. The address still comes from the link, so a link that leads
 * nowhere still fails the test.
 */
async function follow(link: HTMLElement): Promise<void> {
  await act(async () => {
    window.location.hash = link.getAttribute("href") ?? "";
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  });
  await settled();
}
