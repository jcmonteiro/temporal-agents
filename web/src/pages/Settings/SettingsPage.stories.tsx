import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";
import { aDirectoryPlace, aPrompt, theUnknownPlace } from "../../test/fake-api";
import { installStoryApi } from "../../stories/story-api";
import { SettingsPage } from "./SettingsPage";

const meta = {
  title: "Pages/Settings/Content",
  component: SettingsPage,
  tags: ["autodocs"],
  beforeEach: () => installStoryApi((api) => {
    api.locations = [
      theUnknownPlace(),
      aDirectoryPlace({
        id: "pricing",
        label: "pricing",
        directory: "/srv/repos/pricing",
      }),
    ];
    api.registered = [{ locationId: "pricing", registeredAt: "2026-08-10T09:00:00Z" }];
    api.promptCatalogues.global = [
      aPrompt({
        key: "review.perform",
        purpose: "How the agent reviews the current branch.",
        effective: "Perform a thorough code review of the current branch",
        inherited: "Perform a thorough code review of the current branch",
      }),
      aPrompt({
        key: "review.implement",
        purpose: "How the agent implements actionable review comments.",
        effective: "Implement the actionable changes called for by the code review.",
        inherited: "Implement the actionable changes called for by the code review.",
      }),
      aPrompt({
        key: "pilot.address",
        purpose: "How the agent addresses one review comment.",
        effective: "Read the referenced code, then fix the review comment.",
        inherited: "Read the referenced code, then fix the review comment.",
        advanced: true,
        systemBlock: "Repository tools and the referenced review comment.",
      }),
      aPrompt({
        key: "steering.question",
        purpose: "How the questioning agent turns operator context into guidance.",
        effective: "Ask concise questions, then produce implementation guidance.",
        inherited: "Ask concise questions, then produce implementation guidance.",
      }),
    ];
  }),
  decorators: [
    (Story) => (
      <div style={{ display: "flex", minHeight: "100vh" }}>
        <Story />
      </div>
    ),
  ],
} satisfies Meta<typeof SettingsPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Configured: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.findByRole("heading", { name: "Settings" })).resolves.toBeTruthy();
    await expect(canvas.findByLabelText("Instruction text")).resolves.toHaveValue(
      "Perform a thorough code review of the current branch",
    );
    await expect(canvas.findByRole("link", { name: "pricing" })).resolves.toBeTruthy();
  },
};
