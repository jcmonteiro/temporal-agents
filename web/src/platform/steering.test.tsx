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

it("reviews guidance before the build decision is submitted", async () => {
  const dialog = await openModal();

  fireEvent.click(within(dialog).getByRole("button", { name: "Build with guidance" }));
  expect(within(dialog).getByRole("heading", { name: "Clarify the direction" })).toBeTruthy();
  expect(api.steeringDecisions).toBe(0);

  fireEvent.click(within(dialog).getByRole("button", { name: "Skip clarification" }));
  fireEvent.change(within(dialog).getByLabelText("Guidance for the implementing agent"), {
    target: { value: "Keep the retry." },
  });
  fireEvent.click(within(dialog).getByRole("button", { name: "Continue to review" }));

  expect(within(dialog).getByRole("heading", { name: "Review the decision" })).toBeTruthy();
  expect(within(dialog).getByText("Keep the retry.")).toBeTruthy();
  expect(api.steeringDecisions).toBe(0);

  await act(async () => {
    fireEvent.click(within(dialog).getByRole("button", { name: "Confirm and build" }));
  });

  expect(api.steeringDecisions).toBe(1);
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("renders review material as safe Markdown", async () => {
  api.steeringSessions["steering-review-1"] = aSteeringSession({
    material: [
      "**Affected callers:**",
      "",
      "- Checkout API",
      "- Audit log",
      "",
      '<img src="https://attacker.example/pixel" alt="tracking pixel">',
    ].join("\n"),
  });
  const dialog = await openModal();
  const material = dialog.querySelector("#steering-review-outcome");

  expect(within(material as HTMLElement).getByText("Affected callers:").tagName).toBe("STRONG");
  expect(within(material as HTMLElement).getByText("Checkout API").closest("ul")?.children).toHaveLength(2);
  expect(material?.querySelector("img")).toBeNull();
});

it("maximizes and restores the review outcome inside the steering surface", async () => {
  const dialog = await openModal();

  fireEvent.click(within(dialog).getByRole("button", { name: "Maximize review outcome" }));

  expect(within(dialog).getByRole("button", { name: "Restore review outcome" })).toBeTruthy();
  expect(within(dialog).queryByRole("heading", { name: "Choose what happens next" })).toBeNull();

  fireEvent.click(within(dialog).getByRole("button", { name: "Restore review outcome" }));

  expect(within(dialog).getByRole("heading", { name: "Choose what happens next" })).toBeTruthy();
});

it("reviews a no-guidance outcome without showing irrelevant steps", async () => {
  const dialog = await openModal();

  fireEvent.click(within(dialog).getByRole("button", { name: "Proceed without guidance" }));

  expect(within(dialog).getByRole("heading", { name: "Review the decision" })).toBeTruthy();
  expect(within(dialog).queryByRole("heading", { name: "Clarify the direction" })).toBeNull();
  expect(within(dialog).queryByLabelText("Guidance for the implementing agent")).toBeNull();
  expect(api.steeringDecisions).toBe(0);

  await act(async () => {
    fireEvent.click(within(dialog).getByRole("button", { name: "Confirm and proceed" }));
  });

  expect(api.steeringDecisions).toBe(1);
});

it("describes the elapsed wait without exposing its timestamp", async () => {
  const waitingSince = new Date(Date.now() - (5 * 24 + 1) * 60 * 60 * 1_000).toISOString();
  api.steeringSessions["steering-review-1"] = aSteeringSession({ waitingSince });

  const dialog = await openModal();

  expect(within(dialog).getByText("waiting for 5 days")).toBeTruthy();
  expect(dialog.textContent).not.toContain(waitingSince);
});

it("requires guidance before review and manages keyboard focus", async () => {
  const dialog = await openModal();
  fireEvent.click(within(dialog).getByRole("button", { name: "Build with guidance" }));
  fireEvent.click(within(dialog).getByRole("button", { name: "Skip clarification" }));
  const guidance = within(dialog).getByLabelText("Guidance for the implementing agent");
  const review = within(dialog).getByRole("button", { name: "Continue to review" });

  expect(review.hasAttribute("disabled")).toBe(true);
  await waitFor(() => expect(document.activeElement).toBe(guidance));

  fireEvent.keyDown(window, { key: "Escape" });
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("returns keyboard focus to the action that opened steering", async () => {
  const dialog = await openModal();
  const opener = screen.getByRole("button", { name: "Open guidance" });
  await waitFor(() => expect(document.activeElement).toBe(
    within(dialog).getByRole("button", { name: "Build with guidance" }),
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

it("sends one decision for a burst of confirmation clicks", async () => {
  const dialog = await openModal();
  fireEvent.click(within(dialog).getByRole("button", { name: "Build with guidance" }));
  fireEvent.click(within(dialog).getByRole("button", { name: "Skip clarification" }));
  fireEvent.change(within(dialog).getByLabelText("Guidance for the implementing agent"), {
    target: { value: "Keep the retry." },
  });
  fireEvent.click(within(dialog).getByRole("button", { name: "Continue to review" }));
  const confirm = within(dialog).getByRole("button", { name: "Confirm and build" });

  await act(async () => {
    fireEvent.click(confirm);
    fireEvent.click(confirm);
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

it("reviews pass-limit outcomes before submitting them", async () => {
  api.steeringSessions["steering-review-1"] = aSteeringSession({
    round: "pass-limit",
    material: "Budget exhausted. Accumulated token cost: 12000.",
  });
  const dialog = await openModal();

  expect(within(dialog).getByText(/12000/)).toBeTruthy();
  expect(within(dialog).getByRole("button", { name: "Accept the work as finished" })).toBeTruthy();
  expect(within(dialog).getByRole("button", { name: "Stop the loop" })).toBeTruthy();
  expect(within(dialog).queryByRole("button", { name: "Build with guidance" })).toBeNull();

  fireEvent.click(within(dialog).getByRole("button", { name: "Continue with a fresh pass budget" }));
  expect(within(dialog).getByRole("heading", { name: "Review the decision" })).toBeTruthy();
  expect(api.steeringDecisions).toBe(0);

  await act(async () => {
    fireEvent.click(within(dialog).getByRole("button", { name: "Confirm fresh pass budget" }));
  });
  expect(api.steeringDecisions).toBe(1);
});

it("records clarification turns and can use an answer as draft guidance", async () => {
  const dialog = await openModal();
  fireEvent.click(within(dialog).getByRole("button", { name: "Build with guidance" }));
  const question = within(dialog).getByLabelText("Question for the clarification agent");
  fireEvent.change(question, { target: { value: "Which callers need the cause?" } });
  await act(async () => {
    fireEvent.click(within(dialog).getByRole("button", { name: "Ask question" }));
  });

  const conversation = within(dialog).getByRole("region", { name: "Clarification conversation" });
  expect(conversation.textContent).toContain("Which callers need the cause?");
  expect(conversation.textContent).toContain("The Checkout API and audit log need the cause.");
  fireEvent.click(within(conversation).getByRole("button", { name: "Use this answer as draft guidance" }));

  expect((within(dialog).getByLabelText("Guidance for the implementing agent") as HTMLTextAreaElement).value)
    .toBe("The Checkout API and audit log need the cause.");
  expect(api.steeringDecisions).toBe(0);
});

it("sends a clarification question when Enter is pressed", async () => {
  const dialog = await openModal();
  fireEvent.click(within(dialog).getByRole("button", { name: "Build with guidance" }));
  const question = within(dialog).getByLabelText("Question for the clarification agent");
  fireEvent.change(question, { target: { value: "Which callers need the cause?" } });

  await act(async () => {
    fireEvent.keyDown(question, { key: "Enter", code: "Enter" });
  });

  const conversation = within(dialog).getByRole("region", { name: "Clarification conversation" });
  expect(conversation.textContent).toContain("Which callers need the cause?");
  expect((question as HTMLInputElement).value).toBe("");
});

it("labels operator turns without exposing the principal identifier", async () => {
  api.steeringSessions["steering-review-1"] = aSteeringSession({
    contributors: [
      "http://localhost:15556/dex|CiQwOGE4Njg0Yi1kYjg4LTRiNzMtOTBhOS0zY2QxNjYxZjU0NjYSBWxvY2Fs",
    ],
    messages: [{
      sequence: 1,
      role: "operator",
      author: "http://localhost:15556/dex|CiQwOGE4Njg0Yi1kYjg4LTRiNzMtOTBhOS0zY2QxNjYxZjU0NjYSBWxvY2Fs",
      text: "Is point 4 really necessary?",
      at: "2026-08-06T12:00:00Z",
    }],
  });
  const dialog = await openModal();

  fireEvent.click(within(dialog).getByRole("button", { name: "Build with guidance" }));

  const conversation = within(dialog).getByRole("region", { name: "Clarification conversation" });
  expect(conversation.textContent).toContain("Operator");
  expect(conversation.textContent).toContain("Is point 4 really necessary?");
  expect(conversation.textContent).not.toContain("http://localhost:15556/dex");
});

it("renders agent clarification responses with only safe Markdown HTML", async () => {
  api.steeringSessions["steering-review-1"] = aSteeringSession({
    messages: [{
      sequence: 1,
      role: "agent",
      text: [
        "**Affected callers:**",
        "",
        "- Checkout API",
        "- Audit log",
        "",
        "<a href=\"javascript:alert('unsafe')\">Unsafe link</a>",
        "<a href=\"https://attacker.example\" aria-label=\"Trusted internal documentation\">Sign in</a>",
        "<p aria-hidden=\"true\" data-state=\"spoofed\">Important warning</p>",
        "![tracking pixel](https://attacker.example/pixel)",
        "<form action=\"https://attacker.example\"><input name=\"secret\"><button>Send</button></form>",
        "<p style=\"position:fixed;inset:0;z-index:999\">Sign in again</p>",
        "<svg><a href=\"https://attacker.example\">Deceptive vector</a></svg>",
      ].join("\n"),
      at: "2026-08-06T12:00:01Z",
    }],
  });
  const dialog = await openModal();

  fireEvent.click(within(dialog).getByRole("button", { name: "Build with guidance" }));

  const conversation = within(dialog).getByRole("region", { name: "Clarification conversation" });
  expect(within(conversation).getByText("Affected callers:").tagName).toBe("STRONG");
  expect(within(conversation).getByText("Checkout API").closest("ul")?.children).toHaveLength(2);
  expect(within(conversation).getByText("Unsafe link").getAttribute("href")).toBeNull();
  expect(within(conversation).getByText("Sign in").getAttribute("aria-label")).toBeNull();
  const warning = within(conversation).getByText("Important warning");
  expect(warning.getAttribute("aria-hidden")).toBeNull();
  expect(warning.getAttribute("data-state")).toBeNull();
  const markdown = conversation.querySelector(".steering-message__markdown");
  expect(markdown?.querySelector("img, form, input, button, svg")).toBeNull();
  expect(within(conversation).getByText("Sign in again").getAttribute("style")).toBeNull();
});

it("validates the guidance limit in UTF-8 bytes", async () => {
  const dialog = await openModal();
  fireEvent.click(within(dialog).getByRole("button", { name: "Build with guidance" }));
  fireEvent.click(within(dialog).getByRole("button", { name: "Skip clarification" }));
  const guidance = within(dialog).getByLabelText("Guidance for the implementing agent");
  const review = within(dialog).getByRole("button", { name: "Continue to review" });

  fireEvent.change(guidance, { target: { value: "é".repeat(4_097) } });

  expect(within(dialog).getByText("8194 / 8192 bytes")).toBeTruthy();
  expect(review.hasAttribute("disabled")).toBe(true);
});
