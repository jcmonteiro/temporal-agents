import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, within } from "storybook/test";
import { aDirectoryPlace, aPrompt, type FakeApi, theUnknownPlace } from "../../test/fake-api";
import { installStoryApi } from "../../stories/story-api";
import { SettingsPage } from "./SettingsPage";

type Scenario = "configured" | "empty" | "loading" | "read-error" | "validation-error";

const meta = {
  title: "Pages/Settings/Settings page",
  component: SettingsPage,
  tags: ["autodocs"],
  beforeEach: ({ parameters }) =>
    installStoryApi((api) => {
      configureApi(api, (parameters.settingsScenario ?? "configured") as Scenario);
    }),
  decorators: [
    (Story, context) => (
      <div
        style={
          context.parameters.settingsViewport === "narrow"
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
        <Story />
      </div>
    ),
  ],
  parameters: {
    layout: "fullscreen",
    a11y: { test: "error" },
    settingsScenario: "configured",
  },
} satisfies Meta<typeof SettingsPage>;

export default meta;
type Story = StoryObj<typeof meta>;

function configureApi(api: FakeApi, scenario: Scenario): void {
  if (scenario === "read-error") {
    api.down = true;
    return;
  }
  if (scenario === "empty") return;

  const pricing = aDirectoryPlace({
    id: "pricing",
    label: "pricing-services",
    directory: "/srv/repos/pricing-services",
  });
  const fulfilment = aDirectoryPlace({
    id: "fulfilment",
    label: "fulfilment-routing-and-capacity-planning",
    directory:
      "/srv/repos/commerce-platform/fulfilment/routing-and-capacity-planning",
  });
  api.locations = [theUnknownPlace(), pricing, fulfilment];
  api.registered = [
    { locationId: "pricing", registeredAt: "2026-08-10T09:00:00Z" },
    { locationId: "fulfilment", registeredAt: "2026-08-10T09:05:00Z" },
  ];
  api.promptCatalogues.global = [
    aPrompt({
      key: "review.perform",
      purpose: "How the agent reviews the current branch and reports actionable findings.",
      effective:
        "Perform a thorough review of the current branch. Prioritize correctness, operational risk, and clear evidence for every finding.",
      inherited:
        "Perform a thorough review of the current branch and report actionable findings.",
      source: "global",
      inheritedFrom: "factory",
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
    aPrompt({
      key: "pilot.address-one-actionable-review-comment",
      purpose: "How the agent addresses one selected review comment.",
      effective: "Read the referenced code, preserve its contracts, and fix the review comment.",
      inherited: "Read the referenced code and fix the review comment.",
      advanced: true,
      systemBlock:
        "Repository tools and the selected review comment are supplied by the hub and cannot be edited here.",
    }),
    aPrompt({
      key: "steering.question",
      purpose: "How the clarification agent answers operator questions.",
      effective: "Answer only the operator's question about the review material.",
      inherited: "Answer only the operator's question about the review material.",
    }),
  ];
  api.latencyMs = scenario === "loading" ? 450 : 90;
  if (scenario === "validation-error") {
    api.promptRefusal = "The instruction must keep the required repository context.";
  }
}

async function showsConfiguredInteraction(canvasElement: HTMLElement): Promise<void> {
  const canvas = within(canvasElement);
  await expect(canvas.findByRole("heading", { name: "Settings" })).resolves.toBeTruthy();
  await expect(
    canvas.getByRole("link", { name: "Instructions" }),
  ).toHaveAttribute("aria-current", "page");
  await expect(canvas.queryByRole("heading", { name: "Places" })).not.toBeInTheDocument();
  const instruction = await canvas.findByLabelText("Instruction text");
  const save = canvas.getByRole("button", { name: "Save override" });
  await expect(save).toBeDisabled();
  await expect(
    canvas.getByRole("button", { name: "Return to shipped default" }),
  ).toBeVisible();

  const scope = canvas.getByLabelText("Configuration scope");
  scope.focus();
  await userEvent.tab();
  await expect(
    canvas.getByRole("button", { name: /review\.perform overridden here/i }),
  ).toHaveFocus();

  await userEvent.clear(instruction);
  await userEvent.type(instruction, "Review correctness, failure handling, and tests.");
  await userEvent.click(save);
  await expect(canvas.getByRole("button", { name: "Saving…" })).toBeDisabled();
  await expect(canvas.findByText(/override saved for review\.perform/i)).resolves.toBeVisible();
}

const configuredStory = {
  play: async ({ canvasElement }) => showsConfiguredInteraction(canvasElement),
} satisfies Pick<Story, "play">;

export const WideLight: Story = {
  ...configuredStory,
  globals: { theme: "light" },
};

export const WideDark: Story = {
  ...configuredStory,
  globals: { theme: "dark" },
};

export const PlacesWide: Story = {
  args: { category: "places" },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.findByRole("heading", { name: "Settings" })).resolves.toBeTruthy();
    await expect(canvas.getByRole("link", { name: "Places" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    await expect(canvas.findByRole("link", { name: "pricing-services" })).resolves.toBeVisible();
    await expect(
      canvas.queryByRole("heading", { name: "Instructions" }),
    ).not.toBeInTheDocument();
  },
};

export const NarrowLight: Story = {
  ...configuredStory,
  globals: { theme: "light" },
  parameters: { settingsViewport: "narrow" },
  play: async (context) => {
    await showsConfiguredInteraction(context.canvasElement);
    const page = context.canvasElement.querySelector<HTMLElement>(".settings-page");
    if (page === null) throw new Error("Settings page missing");
    await expect(page.scrollWidth).toBeLessThanOrEqual(page.clientWidth);
  },
};

export const NarrowDark: Story = {
  ...NarrowLight,
  globals: { theme: "dark" },
};

export const Empty: Story = {
  parameters: { settingsScenario: "empty" },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.findByText("No configurable instructions")).resolves.toBeVisible();
    await expect(canvas.queryByText("No place is known yet")).not.toBeInTheDocument();
  },
};

export const EmptyPlaces: Story = {
  args: { category: "places" },
  parameters: { settingsScenario: "empty" },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.findByText("No place is known yet")).resolves.toBeVisible();
    await expect(canvas.getByRole("button", { name: "Register" })).toBeDisabled();
  },
};

export const Loading: Story = {
  globals: { theme: "dark" },
  parameters: { settingsScenario: "loading" },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText("Reading the instructions…")).toBeVisible();
    await expect(canvas.queryByText("Reading the places…")).not.toBeInTheDocument();
    await expect(canvas.findByLabelText("Instruction text")).resolves.toBeVisible();
  },
};

export const LoadingPlaces: Story = {
  args: { category: "places" },
  globals: { theme: "dark" },
  parameters: { settingsScenario: "loading" },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText("Reading the places…")).toBeVisible();
    await expect(canvas.findByRole("link", { name: "pricing-services" })).resolves.toBeVisible();
  },
};

export const ReadFailure: Story = {
  parameters: { settingsScenario: "read-error" },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.findByText("Instructions unavailable")).resolves.toBeVisible();
    await expect(canvas.queryByText("Places unavailable")).not.toBeInTheDocument();
  },
};

export const ReadFailurePlaces: Story = {
  args: { category: "places" },
  parameters: { settingsScenario: "read-error" },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.findByText("Places unavailable")).resolves.toBeVisible();
    await expect(canvas.queryByText("No place is known yet")).not.toBeInTheDocument();
  },
};

export const ValidationFailure: Story = {
  globals: { theme: "dark" },
  parameters: { settingsScenario: "validation-error" },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const instruction = await canvas.findByLabelText("Instruction text");
    await userEvent.clear(instruction);
    await userEvent.type(instruction, "Review it.");
    await userEvent.click(canvas.getByRole("button", { name: "Save override" }));
    await expect(canvas.findByRole("alert")).resolves.toHaveTextContent(
      "The instruction must keep the required repository context.",
    );
    await expect(instruction).toHaveAttribute("aria-invalid", "true");
  },
};

export const RegistrationFailure: Story = {
  args: { category: "places" },
  parameters: { settingsScenario: "configured", settingsViewport: "narrow" },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const directory = await canvas.findByLabelText("Directory");
    await userEvent.type(directory, "/srv/repos/missing");
    await userEvent.click(canvas.getByRole("button", { name: "Register" }));
    await expect(canvas.findByRole("alert")).resolves.toHaveTextContent(
      "no such directory: /srv/repos/missing",
    );
    await expect(directory).toHaveAttribute("aria-invalid", "true");
  },
};
