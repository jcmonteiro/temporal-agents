import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, within } from "storybook/test";
import { App } from "../app";
import {
  aDirectoryPlace,
  aFleet,
  aNode,
  aPrompt,
  aRun,
  aSchedule,
  aSteeringSession,
  type FakeApi,
  theUnknownPlace,
} from "../test/fake-api";
import { installStoryApi } from "./story-api";

const runId = "review-checkout-resilience";
const runLabel = "Preserve the original provider error across bounded retries";

const meta = {
  title: "Journeys/Cohesive operator experience",
  component: App,
  tags: ["autodocs"],
  beforeEach: () => {
    const previousAddress = window.location.href;
    window.history.replaceState(null, "", "#/");
    const restoreApi = installStoryApi(configureReviewApi);
    return () => {
      restoreApi();
      window.history.replaceState(null, "", previousAddress);
    };
  },
  decorators: [
    (Story, context) => (
      <div
        onClickCapture={(event) => {
          if (!(event.target instanceof Element)) return;
          const link = event.target.closest("a");
          const address = link?.getAttribute("href");
          if (address?.startsWith("#/") !== true) return;
          event.preventDefault();
          window.history.pushState(null, "", address);
          window.dispatchEvent(new HashChangeEvent("hashchange"));
        }}
        className={
          context.parameters.reviewViewport === "narrow"
            ? "cohesive-review-frame cohesive-review-frame--narrow"
            : "cohesive-review-frame"
        }
        style={
          context.parameters.reviewViewport === "narrow"
            ? { width: "min(390px, 100vw)", height: 760, marginInline: "auto" }
            : { width: "100%", height: 900 }
        }
      >
        <Story />
      </div>
    ),
  ],
  parameters: {
    layout: "fullscreen",
    a11y: { test: "error" },
    reviewViewport: "wide",
  },
} satisfies Meta<typeof App>;

export default meta;
type Story = StoryObj<typeof meta>;

function configureReviewApi(api: FakeApi): void {
  const repository = aDirectoryPlace({
    id: "checkout",
    label: "checkout",
    directory: "/srv/repos/checkout",
  });
  const worktree = aDirectoryPlace({
    id: "checkout-retry",
    label: "preserve-retry-cause",
    parentId: repository.id,
    directory: "/srv/worktrees/preserve-retry-cause",
  });
  api.locations = [theUnknownPlace(), repository, worktree];
  api.registered = [{ locationId: repository.id, registeredAt: "2026-08-10T09:00:00Z" }];
  api.fleets = [
    aFleet({
      id: "fleet-checkout",
      label: "Checkout reliability",
      locationId: repository.id,
      progress: { done: 2, total: 4, fraction: 0.5 },
      upNext: [aNode({ id: "node-audit", label: "Update the audit log" })],
    }),
  ];
  api.runs = [
    aRun({
      id: runId,
      type: "review",
      label: runLabel,
      status: "waiting-input",
      locationId: worktree.id,
      startedAt: "2026-08-11T16:08:27Z",
      endedAt: null,
      iterations: 3,
      tokens: 12_840,
    }),
    aRun({
      id: "review-tax-rounding",
      type: "review",
      label: "Review tax rounding",
      status: "done",
      locationId: repository.id,
      startedAt: "2026-08-11T14:10:00Z",
      endedAt: "2026-08-11T14:24:00Z",
    }),
  ];
  api.schedules = [
    aSchedule({
      id: "schedule-nightly",
      label: "Nightly checkout review",
      locationId: repository.id,
    }),
  ];
  api.startedBy[runId] = "https://issuer.test|operator-1";
  api.instructionsUsed[runId] = [
    { key: "review.perform", scope: `directory:${worktree.directory ?? ""}`, version: 14 },
    { key: "review.implement", scope: "global", version: 8 },
  ];
  const steering = aSteeringSession({
    id: "steering-checkout-review",
    itemId: runId,
    locationId: worktree.id,
    waitingSince: "2026-08-11T16:09:10Z",
    material:
      "The retry reports the last transport failure, but callers need the first provider refusal to remain available as the cause.",
    guidance: "Keep the bounded retry and preserve the original provider refusal as the cause.",
    tokens: 640,
    contributors: ["operator@example.test"],
    messages: [
      {
        sequence: 1,
        role: "operator",
        author: "operator@example.test",
        text: "Which callers inspect the original cause?",
        at: "2026-08-11T16:09:20Z",
      },
      {
        sequence: 2,
        role: "agent",
        text: "The checkout API and audit log both inspect it.",
        tokens: 640,
        at: "2026-08-11T16:09:22Z",
      },
    ],
    locations: [theUnknownPlace(), repository, worktree],
  });
  api.steeringSessions[steering.id] = steering;
  api.notifications = [
    {
      id: "waiting-review",
      kind: "steering",
      title: "Guidance waiting",
      body: `${runLabel} needs a decision before implementation can continue.`,
      url: `/#/runs/${runId}`,
      sessionId: steering.id,
      createdAt: "2026-08-11T16:09:10Z",
      read: false,
    },
    {
      id: "finished-run",
      kind: "completed",
      title: "Review finished",
      body: "The tax rounding review completed with no actionable findings.",
      url: "/#/runs/review-tax-rounding",
      createdAt: "2026-08-11T15:10:00Z",
      read: false,
    },
  ];
  api.promptCatalogues.global = [
    aPrompt({
      key: "review.perform",
      purpose: "How the agent reviews the current branch and reports actionable findings.",
      effective:
        "Review correctness, operational risk, and failure handling. Give evidence for every finding.",
      inherited: "Review the current branch and report actionable findings.",
      source: "global",
      overridden: true,
      version: 4,
    }),
    aPrompt({
      key: "review.implement",
      purpose: "How the agent implements the actionable review comments.",
      effective: "Implement the actionable changes in {{.Review}} and verify each result.",
      inherited: "Implement the actionable changes in {{.Review}}.",
      requiredInserts: [
        { name: "Review", action: "{{.Review}}", purpose: "The verified review output." },
      ],
    }),
  ];
}

async function reviewOperatorJourney(canvasElement: HTMLElement): Promise<void> {
  const canvas = within(canvasElement);
  await expect(
    canvas.findByRole("heading", { name: "Overview" }, { timeout: 5_000 }),
  ).resolves.toBeVisible();
  await expect(
    canvas.findByRole("button", { name: `${runLabel}, Waiting Input` }),
  ).resolves.toBeVisible();

  const notifications = await canvas.findByRole("button", {
    name: "Notifications, 2 unread",
  });
  await userEvent.click(notifications);
  const inbox = await canvas.findByRole("region", { name: "Notification inbox" });
  await expect(within(inbox).findByText("Guidance waiting")).resolves.toBeVisible();
  await expect(within(inbox).findByText("Review finished")).resolves.toBeVisible();
  const frame = canvasElement.querySelector<HTMLElement>(".cohesive-review-frame");
  if (frame === null) throw new Error("Cohesive review frame missing");
  const frameBounds = frame.getBoundingClientRect();
  const inboxBounds = inbox.getBoundingClientRect();
  await expect(inboxBounds.left).toBeGreaterThanOrEqual(frameBounds.left);
  await expect(inboxBounds.right).toBeLessThanOrEqual(frameBounds.right);
  await userEvent.click(notifications);

  await userEvent.click(canvas.getByRole("button", { name: `${runLabel}, Waiting Input` }));
  await userEvent.click(await canvas.findByRole("link", { name: "View details" }));
  await expect(canvas.findByRole("heading", { name: runLabel })).resolves.toBeVisible();
  await expect(
    canvas.findByRole("status", { name: "Run status: Waiting Input" }),
  ).resolves.toBeVisible();

  await userEvent.click(await canvas.findByRole("button", { name: /needs guidance/i }));
  const dialog = await canvas.findByRole("dialog", { name: "Guide this review round" });
  await expect(
    within(dialog).findByText(/callers need the first provider refusal/i),
  ).resolves.toBeVisible();
  await expect(
    within(dialog).getByRole("button", { name: "Build with guidance" }),
  ).toBeEnabled();
  await userEvent.click(within(dialog).getByRole("button", { name: "Close steering" }));
  await expect(canvas.queryByRole("dialog")).not.toBeInTheDocument();

  const sections = canvas.getByRole("navigation", { name: "Sections" });
  await userEvent.click(within(sections).getByRole("link", { name: "Settings" }));
  await expect(canvas.findByRole("heading", { name: "Settings" })).resolves.toBeVisible();
  await expect(canvas.findByLabelText("Instruction text")).resolves.toBeVisible();

  await userEvent.click(within(sections).getByRole("link", { name: "Overview" }));
  await expect(canvas.findByRole("heading", { name: "Overview" })).resolves.toBeVisible();
}

const operatorJourney = {
  play: async ({ canvasElement }) => reviewOperatorJourney(canvasElement),
} satisfies Pick<Story, "play">;

export const WideLight: Story = {
  ...operatorJourney,
  globals: { theme: "light" },
};

export const WideDark: Story = {
  ...operatorJourney,
  globals: { theme: "dark" },
};

export const NarrowLight: Story = {
  ...operatorJourney,
  globals: { theme: "light" },
  parameters: { reviewViewport: "narrow" },
  play: async (context) => {
    await reviewOperatorJourney(context.canvasElement);
    const frame = context.canvasElement.querySelector<HTMLElement>(".cohesive-review-frame");
    if (frame === null) throw new Error("Cohesive review frame missing");
    await expect(frame.scrollWidth).toBeLessThanOrEqual(frame.clientWidth);
  },
};

export const NarrowDark: Story = {
  ...NarrowLight,
  globals: { theme: "dark" },
};
