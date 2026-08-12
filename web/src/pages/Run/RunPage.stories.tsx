import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, within } from "storybook/test";
import {
  aDirectoryPlace,
  aRun,
  aSteeringSession,
  type FakeApi,
  theUnknownPlace,
} from "../../test/fake-api";
import { SteeringProvider } from "../../platform/steering";
import { installStoryApi } from "../../stories/story-api";
import { RunPage } from "./RunPage";

const runId = "review-4fe4171d-485d-5f31-914e-d7d3d8938304";
type Scenario =
  | "active"
  | "waiting"
  | "completed"
  | "failed"
  | "sparse"
  | "loading"
  | "starting"
  | "unavailable";

const meta = {
  title: "Pages/Run/Run page",
  component: RunPage,
  tags: ["autodocs"],
  args: { runId },
  beforeEach: ({ parameters }) =>
    installStoryApi((api) => {
      configureApi(api, (parameters.runScenario ?? "active") as Scenario);
    }),
  decorators: [
    (Story, context) => (
      <div
        style={
          context.parameters.runViewport === "narrow"
            ? {
                display: "flex",
                width: "min(390px, 100vw)",
                minHeight: "100vh",
                marginInline: "auto",
                overflowX: "clip",
              }
            : { display: "flex", minHeight: "100vh" }
        }
      >
        <SteeringProvider>
          <Story />
        </SteeringProvider>
      </div>
    ),
  ],
  parameters: {
    layout: "fullscreen",
    a11y: { test: "error" },
    runScenario: "active",
  },
} satisfies Meta<typeof RunPage>;

export default meta;
type Story = StoryObj<typeof meta>;

function configureApi(api: FakeApi, scenario: Scenario): void {
  if (scenario === "unavailable") {
    api.down = true;
    return;
  }
  if (scenario === "starting") return;

  const place = aDirectoryPlace({
    id: "overcaffeinated-gecko",
    label: "overcaffeinated-gecko-2026-aug-11",
    directory:
      "/srv/worktrees/commerce-platform/overcaffeinated-gecko-2026-aug-11",
  });
  api.locations = [theUnknownPlace(), place];

  if (scenario === "loading") api.latencyMs = 700;

  const common = {
    id: runId,
    type: "review",
    locationId: place.id,
    startedAt: "2026-08-11T16:08:27Z",
  } as const;

  if (scenario === "active" || scenario === "loading") {
    api.runs = [
      aRun({
        ...common,
        label:
          "Review checkout resilience across retries, cancellation, partial responses, and delayed provider callbacks",
        status: "in-progress",
        endedAt: null,
        iterations: 7,
        tokens: 48_392,
      }),
    ];
    api.startedBy[runId] =
      "https://identity.agent-hub.example.test/operators/platform-reliability/on-call-operator-1";
    api.instructionsUsed[runId] = [
      { key: "review.perform", scope: "directory:/srv/worktrees/commerce-platform/overcaffeinated-gecko-2026-aug-11", version: 14 },
      { key: "review.implement", scope: "global", version: 8 },
      { key: "pilot.address-one-actionable-review-comment", scope: "factory", version: 3 },
    ];
    return;
  }

  if (scenario === "waiting") {
    api.runs = [
      aRun({
        ...common,
        label: "Preserve the original provider error while retaining bounded retries",
        status: "waiting-input",
        endedAt: null,
        iterations: 3,
        tokens: 12_840,
      }),
    ];
    api.startedBy[runId] = "https://issuer.test|operator-1";
    api.instructionsUsed[runId] = [
      { key: "review.perform", scope: `directory:${place.directory ?? ""}`, version: 14 },
    ];
    const session = aSteeringSession({
      id: "steering-review-4",
      itemId: runId,
      locationId: place.id,
      waitingSince: "2026-08-11T16:09:10Z",
      material:
        "The retry path reports the last transport failure, but the acceptance check expects the first provider refusal to remain available as the cause.",
      locations: [theUnknownPlace(), place],
    });
    api.steeringSessions[session.id] = session;
    return;
  }

  if (scenario === "completed") {
    api.runs = [
      aRun({
        ...common,
        label: "Review retry and cancellation handling",
        status: "done",
        endedAt: "2026-08-11T16:42:51Z",
        iterations: 5,
        tokens: 23_104,
      }),
    ];
    api.startedBy[runId] = "https://issuer.test|operator-1";
    api.instructionsUsed[runId] = [
      { key: "review.perform", scope: "global", version: 8 },
    ];
    return;
  }

  if (scenario === "failed") {
    api.runs = [
      aRun({
        ...common,
        label: "Review checkout provider integration",
        status: "failed",
        endedAt: "2026-08-11T16:08:52Z",
        iterations: 1,
        tokens: 1_942,
      }),
    ];
    api.startedBy[runId] = "http://localhost:15556/dex|operator-1";
    return;
  }

  api.runs = [
    aRun({
      ...common,
      type: "prompt",
      label: "One-off repository question",
      status: "done",
      locationId: "unknown",
      startedAt: null,
      endedAt: null,
      iterations: 0,
    }),
  ];
}

async function showsActiveRun(canvasElement: HTMLElement): Promise<void> {
  const canvas = within(canvasElement);
  await expect(canvas.findByRole("status", { name: "Run status: In Progress" })).resolves.toBeVisible();
  await expect(canvas.findByRole("region", { name: "Operational details" })).resolves.toBeVisible();
  await expect(canvas.findByRole("region", { name: "Instructions it ran under" })).resolves.toBeVisible();
  const repeat = await canvas.findByRole("button", { name: "Run this again" });
  await expect(repeat).toBeEnabled();

  const breadcrumb = canvas.getByRole("navigation", { name: "Breadcrumb" });
  within(breadcrumb).getByRole("link", { name: "Overview" }).focus();
  await userEvent.tab();
  await expect(
    within(breadcrumb).getByRole("link", { name: "overcaffeinated-gecko-2026-aug-11" }),
  ).toHaveFocus();
}

const activeStory = {
  play: async ({ canvasElement }) => showsActiveRun(canvasElement),
} satisfies Pick<Story, "play">;

export const ActiveWideLight: Story = {
  ...activeStory,
  globals: { theme: "light" },
};

export const ActiveWideDark: Story = {
  ...activeStory,
  globals: { theme: "dark" },
};

export const ActiveNarrowLight: Story = {
  ...activeStory,
  globals: { theme: "light" },
  parameters: { runViewport: "narrow" },
  play: async (context) => {
    await showsActiveRun(context.canvasElement);
    const page = context.canvasElement.querySelector<HTMLElement>(".run-page");
    if (page === null) throw new Error("Run page missing");
    await expect(page.scrollWidth).toBeLessThanOrEqual(page.clientWidth);
  },
};

export const ActiveNarrowDark: Story = {
  ...ActiveNarrowLight,
  globals: { theme: "dark" },
};

export const WaitingForGuidance: Story = {
  globals: { theme: "dark" },
  parameters: { runScenario: "waiting" },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.findByRole("status", { name: "Run status: Waiting Input" })).resolves.toBeVisible();
    await expect(canvas.findByRole("button", { name: /needs guidance/i })).resolves.toBeEnabled();
  },
};

export const Completed: Story = {
  parameters: { runScenario: "completed" },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.findByRole("status", { name: "Run status: Done" })).resolves.toBeVisible();
    await expect(canvas.findByText("2026-08-11T16:42:51Z")).resolves.toBeVisible();
    await expect(canvas.getByRole("button", { name: "Run this again" })).toBeEnabled();
  },
};

export const Failed: Story = {
  globals: { theme: "dark" },
  parameters: { runScenario: "failed" },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.findByRole("status", { name: "Run status: Failed" })).resolves.toBeVisible();
    await expect(canvas.getByRole("region", { name: "Available actions" })).toBeVisible();
  },
};

export const SparseRecord: Story = {
  parameters: { runScenario: "sparse", runViewport: "narrow" },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.findByRole("heading", { name: "One-off repository question" })).resolves.toBeVisible();
    await expect(canvas.getByText("Not started from the hub")).toBeVisible();
    await expect(canvas.getByRole("button", { name: "Run this again" })).toBeDisabled();
  },
};

export const Loading: Story = {
  globals: { theme: "dark" },
  parameters: { runScenario: "loading" },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText("Loading this run…")).toBeVisible();
    await expect(canvas.findByRole("status", { name: "Run status: In Progress" })).resolves.toBeVisible();
  },
};

export const Starting: Story = {
  parameters: { runScenario: "starting" },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.findByRole("heading", { name: "Starting…" })).resolves.toBeVisible();
    await expect(canvas.queryByText(/no such run/i)).not.toBeInTheDocument();
  },
};

export const Unavailable: Story = {
  globals: { theme: "dark" },
  parameters: { runScenario: "unavailable" },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.findByText("Run information unavailable")).resolves.toBeVisible();
    await expect(canvas.findByText(/could not be reached/i)).resolves.toBeVisible();
  },
};
