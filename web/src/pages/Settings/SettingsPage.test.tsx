// @vitest-environment jsdom
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { fireEvent } from "@testing-library/dom";
import { afterEach, beforeEach, expect, it } from "vitest";
import { SettingsPage } from "./SettingsPage";
import { aDirectoryPlace, FakeApi } from "../../test/fake-api";

// Registering a place is the operator's way of saying "you may work here" before
// anything has run there. The page is driven end to end against the stubbed HTTP
// edge, so the request, the refusal and the list that follows all take part.

let api: FakeApi;

beforeEach(() => {
  api = new FakeApi();
  api.install();
});

afterEach(() => {
  cleanup();
  api.restore();
});

/** Opens the places category and waits for the first read to land. */
async function openTheSettings(): Promise<void> {
  render(<SettingsPage category="places" />);
  await waitFor(() => expect(screen.queryByText("Reading the places…")).toBeNull());
}

/** Types a directory and submits the registration. */
async function register(directory: string): Promise<void> {
  fireEvent.change(screen.getByLabelText("Directory"), { target: { value: directory } });
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "Register" }));
  });
}

it("shows one selected settings category at a time", () => {
  render(<SettingsPage category="instructions" />);

  const categories = screen.getByRole("navigation", { name: "Settings categories" });
  expect(
    within(categories).getByRole("link", { name: "Instructions" }).getAttribute("href"),
  ).toBe("#/settings");
  expect(
    within(categories).getByRole("link", { name: "Instructions" }).getAttribute(
      "aria-current",
    ),
  ).toBe("page");
  expect(within(categories).getByRole("link", { name: "Places" }).getAttribute("href")).toBe(
    "#/settings/places",
  );
  expect(screen.getByRole("heading", { name: "Instructions" })).toBeTruthy();
  expect(screen.queryByRole("heading", { name: "Places" })).toBeNull();
  expect(screen.queryByText("Reading the places…")).toBeNull();
});

it("says no place is known, and offers to register one", async () => {
  await openTheSettings();

  expect(screen.getByText(/no place is known yet/i)).toBeTruthy();
  expect(screen.getByLabelText("Directory")).toBeTruthy();
  expect(screen.queryByRole("heading", { name: "Instructions" })).toBeNull();
});

it("shows a repository already known from recorded work", async () => {
  const observed = aDirectoryPlace({
    id: "dir-pricing",
    label: "pricing",
    directory: "/srv/repos/pricing",
  });
  api.locations = [...api.locations, observed];
  api.runs = [{
    id: "develop-observed",
    kind: "run",
    type: "develop",
    label: "Earlier work",
    status: "done",
    locationId: observed.id,
    startedAt: "2026-08-06T12:00:00Z",
    endedAt: "2026-08-06T12:05:00Z",
    iterations: 1,
    tokens: 100,
    dismissible: true,
  }];

  await openTheSettings();

  expect(screen.getByRole("link", { name: "pricing" })).toBeTruthy();
  expect(screen.getByText("/srv/repos/pricing")).toBeTruthy();
  expect(screen.getByText("Observed")).toBeTruthy();
  expect(screen.queryByText(/no place is known yet/i)).toBeNull();
});

it("fills the editable directory field from the native folder picker", async () => {
  api.pickedDirectory = "/srv/repos/pricing";
  await openTheSettings();

  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "Choose folder" }));
  });

  const directory = screen.getByLabelText("Directory") as HTMLInputElement;
  expect(directory.value).toBe("/srv/repos/pricing");
  fireEvent.change(directory, { target: { value: "/srv/repos/checkout" } });
  expect(directory.value).toBe("/srv/repos/checkout");
  expect(screen.queryByText(/for example/i)).toBeNull();
});

it("registers a repository that has never run anything", async () => {
  api.directories["/srv/repos/pricing"] = aDirectoryPlace({
    id: "dir-pricing",
    label: "pricing",
    directory: "/srv/repos/pricing",
  });
  await openTheSettings();

  await register("/srv/repos/pricing");

  const placesSection = screen
    .getByRole("heading", { name: "Places" })
    .closest("section") as HTMLElement;
  const places = within(placesSection).getByRole("list");
  expect(within(places).getByRole("link", { name: "pricing" })).toBeTruthy();
  expect(within(places).getByText("/srv/repos/pricing")).toBeTruthy();
  expect(screen.queryByText(/no place is known yet/i)).toBeNull();
  expect(within(placesSection).getByRole("status").textContent).toMatch(
    /repository registered/i,
  );
  // The typed directory is gone, so the next registration starts from nothing.
  expect((screen.getByLabelText("Directory") as HTMLInputElement).value).toBe("");
});

it("leads from a registered place to its page", async () => {
  api.directories["/srv/repos/pricing"] = aDirectoryPlace({
    id: "dir-pricing",
    label: "pricing",
    directory: "/srv/repos/pricing",
  });
  await openTheSettings();

  await register("/srv/repos/pricing");

  expect(screen.getByRole("link", { name: "pricing" }).getAttribute("href")).toBe(
    "#/places/dir-pricing",
  );
});

it("shows the server's own reason where the directory was typed", async () => {
  api.directories["/srv/notes"] = null;
  await openTheSettings();

  const cases = [
    { directory: "/srv/gone", says: /no such directory/i },
    { directory: "/srv/notes", says: /not a repository/i },
    { directory: "srv/repos/pricing", says: /absolute/i },
  ];
  for (const { directory, says } of cases) {
    await register(directory);

    expect(screen.getByRole("alert").textContent).toMatch(says);
    expect(screen.getByText(/no place is known yet/i)).toBeTruthy();
    // The typed directory stays, so the operator corrects it instead of retyping.
    expect((screen.getByLabelText("Directory") as HTMLInputElement).value).toBe(directory);
  }
});

it("registers a place once, however often it is asked for", async () => {
  api.directories["/srv/repos/pricing"] = aDirectoryPlace({
    id: "dir-pricing",
    label: "pricing",
    directory: "/srv/repos/pricing",
  });
  await openTheSettings();

  await register("/srv/repos/pricing");
  await register("/srv/repos/pricing");

  expect(within(screen.getByRole("list")).getAllByRole("link")).toHaveLength(1);
});

it("says the places could not be read rather than that there are none", async () => {
  api.down = true;

  await openTheSettings();

  const places = screen.getByRole("heading", { name: "Places" }).closest("section") as HTMLElement;
  expect(within(places).getByRole("status").textContent).toMatch(/could not be read/i);
  expect(within(places).queryByText(/no place is known yet/i)).toBeNull();
});
