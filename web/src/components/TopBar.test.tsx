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

  fireEvent.click(screen.getByRole("button", { name: "Enable native notifications" }));
  await waitFor(() => expect(requestPermission).toHaveBeenCalledTimes(1));
});
