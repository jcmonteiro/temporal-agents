import { useEffect, type ReactNode } from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";
import { aSteeringSession } from "../test/fake-api";
import { installStoryApi } from "../stories/story-api";
import { SteeringProvider, useSteering } from "./steering";

function OpenWaitingRound(): ReactNode {
  const steering = useSteering();
  const first = steering.sessions[0];

  useEffect(() => {
    if (first !== undefined) steering.open(first.id);
  }, [first, steering]);

  return (
    <main style={{ minHeight: "100vh", padding: 24 }}>
      <h1 style={{ margin: 0 }}>Run details</h1>
      <p style={{ color: "var(--color-text-muted)" }}>
        The waiting round opens over the page that led to it.
      </p>
    </main>
  );
}

function SteeringExample(): ReactNode {
  return (
    <SteeringProvider>
      <OpenWaitingRound />
    </SteeringProvider>
  );
}

const meta = {
  title: "Overlays/Steering modal",
  component: SteeringExample,
  tags: ["autodocs"],
  parameters: { steeringRound: "local-review" },
  beforeEach: ({ parameters }) => installStoryApi((api) => {
    const passLimit = parameters.steeringRound === "pass-limit";
    api.steeringSessions["steering-review-1"] = aSteeringSession({
      round: passLimit ? "pass-limit" : "local-review",
      waitingSince: "2026-08-11T11:10:00Z",
      material: passLimit
        ? "The review loop used its pass budget. Accumulated token cost: 12,480."
        : "The retry hides the original error and drops the cause returned by the payment provider.",
      guidance: passLimit ? "" : "Keep the retry, but preserve the original cause.",
      tokens: passLimit ? 12_480 : 640,
      contributors: passLimit ? [] : ["operator@example.test"],
      messages: passLimit ? [] : [
        {
          sequence: 1,
          role: "operator",
          author: "operator@example.test",
          text: "Which callers need the original cause?",
          at: "2026-08-11T15:00:00Z",
        },
        {
          sequence: 2,
          role: "agent",
          text: "The checkout API and audit log both inspect it.",
          tokens: 640,
          at: "2026-08-11T15:00:02Z",
        },
      ],
    });
  }),
} satisfies Meta<typeof SteeringExample>;

export default meta;
type Story = StoryObj<typeof meta>;

export const WaitingForGuidance: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const dialog = await canvas.findByRole("dialog", { name: "Guide this review round" });
    await expect(within(dialog).findByText(/retry hides the original error/i)).resolves.toBeTruthy();
    await expect(
      within(dialog).findByLabelText("Guidance for the implementing agent"),
    ).resolves.toHaveValue("Keep the retry, but preserve the original cause.");
  },
};

export const PassLimitReached: Story = {
  parameters: { steeringRound: "pass-limit" },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const dialog = await canvas.findByRole("dialog", { name: "Review pass limit reached" });
    await expect(
      within(dialog).findByRole("button", { name: "Continue with a fresh pass budget" }),
    ).resolves.toBeTruthy();
    await expect(
      within(dialog).findByRole("button", { name: "Accept the work as finished" }),
    ).resolves.toBeTruthy();
  },
};
