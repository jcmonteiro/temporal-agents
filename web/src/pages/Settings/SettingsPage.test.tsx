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

/** Opens the settings and waits for the first read to land. */
async function openTheSettings(): Promise<void> {
  render(<SettingsPage />);
  await waitFor(() => expect(screen.queryByText("Reading the places…")).toBeNull());
}

/** Types a directory and submits the registration. */
async function register(directory: string): Promise<void> {
  fireEvent.change(screen.getByLabelText("Directory"), { target: { value: directory } });
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "Register" }));
  });
}

it("says no place is registered, and offers to register one", async () => {
  await openTheSettings();

  expect(screen.getByText(/no place is registered yet/i)).toBeTruthy();
  expect(screen.getByLabelText("Directory")).toBeTruthy();
});

it("registers a repository that has never run anything", async () => {
  api.directories["/srv/repos/pricing"] = aDirectoryPlace({
    id: "dir-pricing",
    label: "pricing",
    directory: "/srv/repos/pricing",
  });
  await openTheSettings();

  await register("/srv/repos/pricing");

  const places = screen.getByRole("list");
  expect(within(places).getByRole("link", { name: "pricing" })).toBeTruthy();
  expect(within(places).getByText("/srv/repos/pricing")).toBeTruthy();
  expect(screen.queryByText(/no place is registered yet/i)).toBeNull();
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
    expect(screen.getByText(/no place is registered yet/i)).toBeTruthy();
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

  expect(screen.getByRole("status").textContent).toMatch(/could not be read/i);
  expect(screen.queryByText(/no place is registered yet/i)).toBeNull();
});
