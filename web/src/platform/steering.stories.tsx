import { useEffect, type ReactNode } from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, within } from "storybook/test";
import { aSteeringSession, type FakeApi } from "../test/fake-api";
import { installStoryApi } from "../stories/story-api";
import { SteeringProvider, useSteering } from "./steering";

type Scenario =
  | "initial"
  | "active"
  | "loading"
  | "pending"
  | "error"
  | "long"
  | "pass-limit";

function OpenWaitingRound(): ReactNode {
  const steering = useSteering();
  const first = steering.sessions[0];

  useEffect(() => {
    if (first !== undefined) steering.open(first.id);
  }, [first, steering]);

  return (
    <main className="steering-story-context">
      <p className="ui-eyebrow">Review run</p>
      <h1>Preserve provider errors across bounded retries</h1>
      <p>
        This run is waiting for operator input. Steering opens without losing the
        operational context behind it.
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
  beforeEach: ({ parameters }) => installStoryApi((api) => {
    configureApi(api, (parameters.steeringScenario ?? "active") as Scenario);
  }),
  decorators: [
    (Story, context) => (
      <div className={context.parameters.steeringViewport === "narrow"
        ? "steering-story-frame steering-story-frame--narrow"
        : "steering-story-frame"}
      >
        <Story />
      </div>
    ),
  ],
  parameters: {
    layout: "fullscreen",
    a11y: { test: "error" },
    steeringScenario: "active",
  },
} satisfies Meta<typeof SteeringExample>;

export default meta;
type Story = StoryObj<typeof meta>;

function configureApi(api: FakeApi, scenario: Scenario): void {
  const initial = scenario === "initial";
  const passLimit = scenario === "pass-limit";
  const long = scenario === "long";
  const session = aSteeringSession({
    itemId: "review-4fe4171d-485d-5f31-914e-d7d3d8938304",
    round: passLimit ? "pass-limit" : "local-review",
    waitingSince: "2026-08-11T11:10:00Z",
    material: passLimit
      ? "The review loop used its pass budget. Accumulated token cost: 12,480."
      : long
        ? "The retry path hides the first provider refusal when several transports fail. The decision must preserve the original cause for the checkout API, audit log, delayed callback reconciler, and support diagnostics while retaining the bounded retry policy."
        : "The retry hides the original error and drops the cause returned by the payment provider.",
    guidance: initial || passLimit ? "" : "Keep the retry, but preserve the original cause.",
    tokens: passLimit ? 12_480 : long ? 4_892 : initial ? 0 : 640,
    contributors: initial || passLimit
      ? []
      : ["http://localhost:15556/dex|CiQwOGE4Njg0Yi1kYjg4LTRiNzMtOTBhOS0zY2QxNjYxZjU0NjYSBWxvY2Fs"],
    messages: initial || passLimit
      ? []
      : long
        ? Array.from({ length: 12 }, (_, index) => ({
            sequence: index + 1,
            role: index % 2 === 0 ? "operator" as const : "agent" as const,
            author: index % 2 === 0 ? "operator@example.test" : undefined,
            text: index % 2 === 0
              ? `Question ${index / 2 + 1}: Which downstream caller still needs the original refusal after another transport attempt?`
              : "The checkout API, audit log, delayed callback reconciler, and support diagnostics inspect the original cause. The retry metadata must remain available separately.",
            tokens: index % 2 === 0 ? undefined : 380,
            at: `2026-08-11T15:00:${String(index).padStart(2, "0")}Z`,
          }))
        : [
            {
              sequence: 1,
              role: "operator",
              author: "http://localhost:15556/dex|CiQwOGE4Njg0Yi1kYjg4LTRiNzMtOTBhOS0zY2QxNjYxZjU0NjYSBWxvY2Fs",
              text: "Which callers need the original cause?",
              at: "2026-08-11T15:00:00Z",
            },
            {
              sequence: 2,
              role: "agent",
              text: "**Affected callers:**\n\n- Checkout API\n- Audit log",
              tokens: 640,
              at: "2026-08-11T15:00:02Z",
            },
          ],
  });
  api.steeringSessions[session.id] = session;

  if (scenario === "loading") api.steeringDetailLatencyMs = 10_000;
  if (scenario === "pending") api.pendingSteeringCommands = true;
  if (scenario === "error") api.steeringDetailDown = true;
}

async function findDialog(canvasElement: HTMLElement, name = "Guide this review round") {
  return within(canvasElement).findByRole("dialog", { name }, { timeout: 5_000 });
}

async function showsLocalChoices(dialog: HTMLElement): Promise<void> {
  const modal = within(dialog);
  await expect(modal.findByRole("button", { name: "Build with guidance" })).resolves.toBeEnabled();
  await expect(modal.getByRole("button", { name: "Proceed without guidance" })).toBeEnabled();
  await expect(modal.getByRole("button", { name: "Stop the loop" })).toBeEnabled();
}

export const InitialWideLight: Story = {
  globals: { theme: "light" },
  parameters: { steeringScenario: "initial" },
  play: async ({ canvasElement }) => {
    const dialog = await findDialog(canvasElement);
    const modal = within(dialog);
    await expect(modal.findByRole("button", { name: "Build with guidance" })).resolves.toBeEnabled();
    await expect(modal.getByRole("button", { name: "Maximize review outcome" })).toBeEnabled();
    await expect(modal.queryByText("No questions asked yet.")).not.toBeInTheDocument();
  },
};

export const MaximizedReviewOutcome: Story = {
  globals: { theme: "dark" },
  parameters: { steeringScenario: "long" },
  play: async ({ canvasElement }) => {
    const dialog = await findDialog(canvasElement);
    const modal = within(dialog);
    await userEvent.click(await modal.findByRole("button", { name: "Maximize review outcome" }));
    await expect(modal.getByRole("button", { name: "Restore review outcome" })).toBeVisible();
    await expect(modal.queryByRole("heading", { name: "Choose what happens next" })).not.toBeInTheDocument();
  },
};

export const ActiveWideDark: Story = {
  globals: { theme: "dark" },
  play: async ({ canvasElement }) => {
    const dialog = await findDialog(canvasElement);
    const modal = within(dialog);
    await expect(modal.findByText(/retry hides the original error/i)).resolves.toBeVisible();
    await userEvent.click(modal.getByRole("button", { name: "Build with guidance" }));
    const conversation = modal.getByRole("region", { name: "Clarification conversation" });
    await expect(within(conversation).getByText("Affected callers:").tagName).toBe("STRONG");
    await expect(conversation).not.toHaveTextContent("http://localhost:15556/dex");
    await userEvent.click(modal.getByRole("button", { name: "Continue to guidance" }));
    await expect(
      modal.getByLabelText("Guidance for the implementing agent"),
    ).toHaveValue("Keep the retry, but preserve the original cause.");
  },
};

export const DecisionReadyNarrowDark: Story = {
  globals: { theme: "dark" },
  parameters: { steeringViewport: "narrow" },
  play: async ({ canvasElement }) => {
    const dialog = await findDialog(canvasElement);
    await showsLocalChoices(dialog);
    await expect(dialog.scrollWidth).toBeLessThanOrEqual(dialog.clientWidth);
  },
};

export const LongConversationNarrowLight: Story = {
  globals: { theme: "light" },
  parameters: { steeringScenario: "long", steeringViewport: "narrow" },
  play: async ({ canvasElement }) => {
    const dialog = await findDialog(canvasElement);
    const modal = within(dialog);
    await userEvent.click(await modal.findByRole("button", { name: "Build with guidance" }));
    const conversation = await modal.findByRole("region", { name: "Clarification conversation" });
    await expect(within(conversation).getAllByRole("listitem")).toHaveLength(12);
    await expect(dialog.scrollWidth).toBeLessThanOrEqual(dialog.clientWidth);
  },
};

export const LoadingRound: Story = {
  globals: { theme: "dark" },
  parameters: { steeringScenario: "loading" },
  play: async ({ canvasElement }) => {
    const dialog = await findDialog(canvasElement);
    await expect(within(dialog).getByText("Loading the waiting round…")).toBeVisible();
  },
};

export const QuestionPending: Story = {
  parameters: { steeringScenario: "pending" },
  play: async ({ canvasElement }) => {
    const dialog = await findDialog(canvasElement);
    const modal = within(dialog);
    await userEvent.click(await modal.findByRole("button", { name: "Build with guidance" }));
    await userEvent.type(await modal.findByLabelText("Question for the clarification agent"), "Which callers inspect it?");
    await userEvent.click(modal.getByRole("button", { name: "Ask question" }));
    await expect(modal.getByRole("button", { name: "Ask question" })).toBeDisabled();
    await expect(dialog).toHaveAttribute("aria-busy", "true");
    await expect(modal.getByText("Steering update in progress…")).toBeVisible();
  },
};

export const LoadError: Story = {
  globals: { theme: "dark" },
  parameters: { steeringScenario: "error" },
  play: async ({ canvasElement }) => {
    const dialog = await findDialog(canvasElement);
    await expect(within(dialog).findByRole("alert")).resolves.toBeVisible();
  },
};

export const PassLimitReached: Story = {
  parameters: { steeringScenario: "pass-limit" },
  play: async ({ canvasElement }) => {
    const dialog = await findDialog(canvasElement, "Review pass limit reached");
    const modal = within(dialog);
    await expect(modal.getByRole("button", { name: "Continue with a fresh pass budget" })).toBeEnabled();
    await expect(modal.getByRole("button", { name: "Accept the work as finished" })).toBeEnabled();
    await expect(modal.getByRole("button", { name: "Stop the loop" })).toBeEnabled();
  },
};
