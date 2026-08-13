// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { fireEvent } from "@testing-library/dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import { SessionProvider } from "../platform/session";
import { FakeApi } from "../test/fake-api";
import { TopBar } from "./TopBar";

let api: FakeApi;
const requestPermission = vi.fn(async () => "granted" as NotificationPermission);

beforeEach(() => {
  api = new FakeApi();
  api.notifications = [{ id: "waiting-1", kind: "steering", title: "Guidance waiting", body: "Review needs input", url: "/#/runs/review-1", createdAt: "2026-08-06T12:00:00Z", read: false }];
  api.install();
  vi.stubGlobal("Notification", class {
    static permission: NotificationPermission = "default";
    static requestPermission = requestPermission;
  });
});

afterEach(() => {
  cleanup();
  api.restore();
  requestPermission.mockClear();
  vi.unstubAllGlobals();
});

it("shows the unread count and requests native permission only after a gesture", async () => {
  render(<SessionProvider><TopBar /></SessionProvider>);

  const bell = await screen.findByRole("button", { name: "Notifications, 1 unread" });
  expect(requestPermission).not.toHaveBeenCalled();
  fireEvent.click(bell);
  expect(screen.getByRole("region", { name: "Notification inbox" }).textContent).toContain("Guidance waiting");

  fireEvent.click(screen.getByRole("button", { name: "Notification actions" }));
  fireEvent.click(screen.getByRole("menuitem", { name: "Enable native notifications" }));
  await waitFor(() => expect(requestPermission).toHaveBeenCalledTimes(1));
});

it("closes the notification inbox when clicking outside it", async () => {
  render(<SessionProvider><TopBar /></SessionProvider>);

  fireEvent.click(await screen.findByRole("button", { name: "Notifications, 1 unread" }));
  expect(screen.getByRole("region", { name: "Notification inbox" })).toBeTruthy();

  fireEvent.pointerDown(document.body);

  expect(screen.queryByRole("region", { name: "Notification inbox" })).toBeNull();
});

it("closes the notification inbox when Escape is pressed", async () => {
  render(<SessionProvider><TopBar /></SessionProvider>);

  fireEvent.click(await screen.findByRole("button", { name: "Notifications, 1 unread" }));
  expect(screen.getByRole("region", { name: "Notification inbox" })).toBeTruthy();

  fireEvent.keyDown(document, { key: "Escape" });

  expect(screen.queryByRole("region", { name: "Notification inbox" })).toBeNull();
});

it("marks every notification as read from the notification actions menu", async () => {
  render(<SessionProvider><TopBar /></SessionProvider>);

  fireEvent.click(await screen.findByRole("button", { name: "Notifications, 1 unread" }));
  fireEvent.click(screen.getByRole("button", { name: "Notification actions" }));
  fireEvent.click(screen.getByRole("menuitem", { name: "Mark all as read" }));

  await waitFor(() => expect(api.notifications.every((item) => item.read)).toBe(true));
  expect(screen.getByRole("button", { name: "Notifications, 0 unread" })).toBeTruthy();
});

it("shows only unread notifications on request with workflow state icons", async () => {
  api.notifications.push({ id: "read-1", kind: "steering", title: "Already seen", body: "Earlier guidance", url: "/#/runs/review-2", createdAt: "2026-08-05T12:00:00Z", read: true });
  render(<SessionProvider><TopBar /></SessionProvider>);

  fireEvent.click(await screen.findByRole("button", { name: "Notifications, 1 unread" }));

  expect(screen.getByText("Already seen")).toBeTruthy();
  expect(screen.getAllByLabelText("Workflow waiting for input")).toHaveLength(2);
  expect(screen.queryByText("Direct")).toBeNull();
  expect(screen.queryByText("Watching")).toBeNull();
  expect(screen.queryByText("Give feedback")).toBeNull();
  expect(screen.queryByText("Clear read state")).toBeNull();

  fireEvent.click(screen.getByRole("switch", { name: "Only show unread" }));

  expect(screen.queryByText("Already seen")).toBeNull();
  expect(screen.getByText("Guidance waiting")).toBeTruthy();
});
