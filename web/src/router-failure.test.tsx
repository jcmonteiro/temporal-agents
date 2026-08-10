// @vitest-environment jsdom
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi, type MockInstance } from "vitest";
import { App } from "./app";
import { FakeApi } from "./test/fake-api";

// A page that fails outright is the case nothing else can produce on purpose,
// so it is produced here: the settings page is made to throw, and the hub is
// asked to survive it. Everything else in the tree — the shell, the navigation,
// the router and the boundary — is the real thing.
vi.mock("./pages/Settings/SettingsPage", () => ({
  SettingsPage: () => {
    throw new Error("the settings could not be read");
  },
}));

let api: FakeApi;
let logged: MockInstance;

beforeEach(() => {
  api = new FakeApi();
  api.install();
  logged = vi.spyOn(console, "error").mockImplementation(() => {});
  window.location.hash = "#/settings";
});

afterEach(() => {
  cleanup();
  api.restore();
  logged.mockRestore();
  window.location.hash = "";
});

it("contains a page that fails, and keeps the way out of it", async () => {
  render(<App />);

  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toContain("This page could not be shown");

  // The shell is what makes the failure survivable: the operator leaves the
  // broken page instead of reloading the hub.
  const sections = screen.getByRole("navigation", { name: "Sections" });
  const overview = within(sections).getByRole("link", { name: "Overview" });
  await act(async () => {
    window.location.hash = overview.getAttribute("href") ?? "";
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  });

  expect(await screen.findByRole("heading", { name: "Overview" })).toBeTruthy();
  await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
});
