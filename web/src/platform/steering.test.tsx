// @vitest-environment jsdom
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import { fireEvent } from "@testing-library/dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import { aSteeringSession, FakeApi } from "../test/fake-api";
import { SteeringProvider, useSteering } from "./steering";

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  readonly listeners = new Map<string, Array<(event: MessageEvent<string>) => void>>();
  constructor(readonly url: string) { FakeEventSource.instances.push(this); }
  addEventListener(type: string, listener: (event: MessageEvent<string>) => void): void {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener]);
  }
  close(): void {}
  emit(type: string): void {
    for (const listener of this.listeners.get(type) ?? []) listener({ data: "{}", lastEventId: "1" } as MessageEvent<string>);
  }
}

let api: FakeApi;

beforeEach(() => {
  api = new FakeApi();
  api.steeringSessions["steering-review-1"] = aSteeringSession();
  api.install();
});

afterEach(() => {
  cleanup();
  api.restore();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  FakeEventSource.instances = [];
});

function Surface() {
  const steering = useSteering();
  const session = steering.sessions[0];
  return session
    ? <button type="button" onClick={() => steering.open(session.id)}>Open guidance</button>
    : <span>Nothing waiting</span>;
}

async function openModal(): Promise<HTMLElement> {
  render(<SteeringProvider><Surface /></SteeringProvider>);
  const open = await screen.findByRole("button", { name: "Open guidance" });
  open.focus();
  fireEvent.click(open);
  const dialog = await screen.findByRole("dialog", { name: "Guide this review round" });
  await waitFor(() => expect(within(dialog).queryByText("Loading the waiting round…")).toBeNull());
  return dialog;
}

it("describes the elapsed wait without exposing its timestamp", async () => {
  const waitingSince = new Date(Date.now() - (5 * 24 + 1) * 60 * 60 * 1_000).toISOString();
  api.steeringSessions["steering-review-1"] = aSteeringSession({ waitingSince });

  const dialog = await openModal();

  expect(within(dialog).getByText("waiting for 5 days")).toBeTruthy();
  expect(dialog.textContent).not.toContain(waitingSince);
});

it("cannot build with empty guidance and manages keyboard focus", async () => {
  const dialog = await openModal();
  const guidance = within(dialog).getByLabelText("Guidance for the implementing agent");
  const build = within(dialog).getByRole("button", { name: "Build with guidance" });

  expect(build.hasAttribute("disabled")).toBe(true);
  await waitFor(() => expect(document.activeElement).toBe(guidance));

  fireEvent.keyDown(window, { key: "Escape" });
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("returns keyboard focus to the action that opened steering", async () => {
  const dialog = await openModal();
  const opener = screen.getByRole("button", { name: "Open guidance" });
  await waitFor(() => expect(document.activeElement).toBe(
    within(dialog).getByLabelText("Guidance for the implementing agent"),
  ));

  fireEvent.keyDown(window, { key: "Escape" });

  expect(document.activeElement).toBe(opener);
});

it("keeps keyboard focus inside the steering dialog", async () => {
  const dialog = await openModal();
  const close = within(dialog).getByRole("button", { name: "Close steering" });
  const stop = within(dialog).getByRole("button", { name: "Stop the loop" });
  stop.focus();

  fireEvent.keyDown(dialog, { key: "Tab" });

  expect(document.activeElement).toBe(close);
  fireEvent.keyDown(dialog, { key: "Tab", shiftKey: true });
  expect(document.activeElement).toBe(stop);
});

it("sends one decision for a burst of clicks", async () => {
  const dialog = await openModal();
  fireEvent.change(within(dialog).getByLabelText("Guidance for the implementing agent"), {
    target: { value: "Keep the retry." },
  });
  const build = within(dialog).getByRole("button", { name: "Build with guidance" });

  await act(async () => {
    fireEvent.click(build);
    fireEvent.click(build);
  });

  expect(api.steeringDecisions).toBe(1);
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("closes gracefully when another operator decides the round", async () => {
  vi.stubGlobal("EventSource", FakeEventSource);
  await openModal();
  api.steeringSessions["steering-review-1"] = {
    ...api.steeringSessions["steering-review-1"],
    state: "decided",
    decision: "skip",
  };

  await act(async () => {
    FakeEventSource.instances.find((source) => source.listeners.has("hub"))?.emit("hub");
  });

  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(screen.getByRole("status").textContent).toContain("decided elsewhere");
});

it("offers continue, accept, and stop at the pass limit", async () => {
  api.steeringSessions["steering-review-1"] = aSteeringSession({
    round: "pass-limit",
    material: "Budget exhausted. Accumulated token cost: 12000.",
  });
  const dialog = await openModal();

  expect(within(dialog).getByText(/12000/)).toBeTruthy();
  expect(within(dialog).getByRole("button", { name: "Continue with a fresh pass budget" })).toBeTruthy();
  expect(within(dialog).getByRole("button", { name: "Accept the work as finished" })).toBeTruthy();
  expect(within(dialog).getByRole("button", { name: "Stop the loop" })).toBeTruthy();
  expect(within(dialog).queryByRole("button", { name: "Build with guidance" })).toBeNull();
});

it("renders questioning turns in durable sequence and finishes into guidance", async () => {
  const dialog = await openModal();
  const answer = within(dialog).getByLabelText("Answer the questioning agent");
  fireEvent.change(answer, { target: { value: "Question me" } });
  await act(async () => {
    fireEvent.click(within(dialog).getByRole("button", { name: "Ask one question" }));
  });

  const conversation = within(dialog).getByRole("region", { name: "Questioning conversation" });
  expect(conversation.textContent).toContain("Question me");
  expect(conversation.textContent).toContain("Which callers need the cause?");

  fireEvent.change(answer, { target: { value: "That is enough" } });
  await act(async () => {
    fireEvent.click(within(dialog).getByRole("button", { name: "Finish into guidance" }));
  });
  expect((within(dialog).getByLabelText("Guidance for the implementing agent") as HTMLTextAreaElement).value)
    .toBe("Keep the retry and preserve the cause.");
});
