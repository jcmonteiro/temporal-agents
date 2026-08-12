import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, within } from "storybook/test";
import {
  aDirectoryPlace,
  aFleet,
  aPrompt,
  aRun,
  aSchedule,
  aSteeringSession,
  type FakeApi,
  theUnknownPlace,
} from "../../test/fake-api";
import { SteeringProvider } from "../../platform/steering";
import { installStoryApi } from "../../stories/story-api";
import { PlacePage } from "./PlacePage";

const placeId = "checkout-reliability";
type Scenario = "active" | "idle" | "loading" | "missing" | "unavailable" | "waiting";

const meta = {
  title: "Pages/Place/Place page",
  component: PlacePage,
  tags: ["autodocs"],
  args: { placeId },
  beforeEach: ({ parameters }) =>
    installStoryApi((api) => {
      configureApi(api, (parameters.placeScenario ?? "active") as Scenario);
    }),
  decorators: [
    (Story, context) => (
      <div
        style={
          context.parameters.placeViewport === "narrow"
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
    placeScenario: "active",
  },
} satisfies Meta<typeof PlacePage>;

export default meta;
type Story = StoryObj<typeof meta>;

function configureApi(api: FakeApi, scenario: Scenario): void {
  if (scenario === "unavailable") {
    api.down = true;
    return;
  }

  const repository = aDirectoryPlace({
    id: "commerce-platform",
    label: "commerce-platform",
    directory: "/srv/repos/commerce-platform",
  });
  const place = aDirectoryPlace({
    id: placeId,
    label: "checkout-reliability",
    parentId: repository.id,
    directory: "/srv/worktrees/commerce-platform/checkout-reliability",
  });
  const child = aDirectoryPlace({
    id: "provider-timeouts",
    label: "provider-timeouts",
    parentId: place.id,
    directory: "/srv/worktrees/commerce-platform/provider-timeouts",
  });
  api.locations = [theUnknownPlace(), repository, place, child];
  api.registered = [
    { locationId: place.id, registeredAt: "2026-08-11T08:45:00Z" },
    { locationId: child.id, registeredAt: "2026-08-11T09:00:00Z" },
  ];
  api.promptCatalogues[place.id] = [
    aPrompt({
      key: "review.perform",
      purpose: "How the agent reviews checkout reliability changes.",
      effective:
        "Review retry boundaries, cancellation, and preservation of the original provider error.",
      inherited: "Review the branch and report actionable findings.",
      source: "directory",
      inheritedFrom: "global",
      overridden: true,
      version: 6,
    }),
    aPrompt({
      key: "review.implement",
      purpose: "How the agent implements accepted review findings.",
      effective: "Implement the accepted findings and verify each changed behavior.",
      inherited: "Implement the accepted findings.",
      requiredInserts: [
        {
          name: "Review",
          action: "{{.Review}}",
          purpose: "The accepted review findings.",
        },
      ],
    }),
  ];

  if (scenario === "missing") {
    api.registered = [];
    api.locations = [theUnknownPlace()];
    return;
  }
  if (scenario === "loading") api.latencyMs = 700;
  if (scenario === "idle") return;

  api.fleets = [
    aFleet({
      id: "fleet-checkout",
      label: "Harden checkout provider integration",
      status: "in-progress",
      locationId: place.id,
      progress: { done: 3, total: 7, fraction: 3 / 7 },
    }),
  ];
  api.runs = [
    aRun({
      id: "run-retry-cause",
      label: "Preserve the original provider failure across bounded retries",
      status: scenario === "waiting" ? "waiting-input" : "in-progress",
      locationId: place.id,
      iterations: 4,
    }),
    aRun({
      id: "run-timeout-tests",
      label: "Add delayed callback and cancellation coverage",
      status: "done",
      locationId: child.id,
      iterations: 2,
    }),
  ];
  api.schedules = [
    aSchedule({
      id: "schedule-nightly",
      label: "Nightly checkout reliability review",
      status: "waiting",
      locationId: place.id,
    }),
  ];

  if (scenario === "waiting") {
    const session = aSteeringSession({
      id: "steering-checkout",
      itemId: "run-retry-cause",
      locationId: place.id,
      waitingSince: "2026-08-11T16:09:10Z",
      locations: [theUnknownPlace(), repository, place],
    });
    api.steeringSessions[session.id] = session;
  }
}

async function showsActivePlace(canvasElement: HTMLElement): Promise<void> {
  const canvas = within(canvasElement);
  await expect(
    canvas.findByRole("heading", { name: "checkout-reliability", level: 1 }),
  ).resolves.toBeVisible();
  await expect(canvas.findByRole("heading", { name: "Start work here" })).resolves.toBeVisible();
  await expect(canvas.findByRole("heading", { name: "Instructions" })).resolves.toBeVisible();

  const work = await canvas.findByRole("button", {
    name: /Preserve the original provider failure/,
  });
  await userEvent.click(work);
  const detail = canvas.getByRole("complementary");
  await expect(within(detail).getByText("Preserve the original provider failure across bounded retries")).toBeVisible();
  await expect(within(detail).getByRole("link", { name: "View details" })).toBeVisible();
}

const activeStory = {
  play: async ({ canvasElement }) => showsActivePlace(canvasElement),
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
  parameters: { placeViewport: "narrow" },
};

export const ActiveNarrowDark: Story = {
  ...ActiveNarrowLight,
  globals: { theme: "dark" },
};

export const WaitingForGuidance: Story = {
  globals: { theme: "dark" },
  parameters: { placeScenario: "waiting" },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.findByRole("button", { name: /needs guidance/i })).resolves.toBeEnabled();
    await expect(canvas.findByText("Waiting Input")).resolves.toBeVisible();
  },
};

export const Idle: Story = {
  parameters: { placeScenario: "idle" },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.findByText("Nothing runs here at the moment.")).resolves.toBeVisible();
    await expect(canvas.findByRole("link", { name: "provider-timeouts" })).resolves.toBeVisible();
  },
};

export const Loading: Story = {
  globals: { theme: "dark" },
  parameters: { placeScenario: "loading" },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText("Loading this place…")).toBeVisible();
    await expect(
      canvas.findByRole("heading", { name: "checkout-reliability", level: 1 }),
    ).resolves.toBeVisible();
  },
};

export const Missing: Story = {
  args: { placeId: "retired-worktree" },
  parameters: { placeScenario: "missing", placeViewport: "narrow" },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.findByRole("heading", { name: "No such place" })).resolves.toBeVisible();
    await expect(canvas.findByText(/retired-worktree/)).resolves.toBeVisible();
  },
};

export const Unavailable: Story = {
  globals: { theme: "dark" },
  parameters: { placeScenario: "unavailable" },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.findByText(/Could not reach the Agent Hub API/)).resolves.toBeVisible();
  },
};
