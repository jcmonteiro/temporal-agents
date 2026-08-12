import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";
import { aDirectoryPlace, aRun, theUnknownPlace } from "../../test/fake-api";
import { installStoryApi } from "../../stories/story-api";
import { RunPage } from "./RunPage";

const runId = "review-4fe4171d-485d-5f31-914e-d7d3d8938304";

const meta = {
  title: "Pages/Run/Content",
  component: RunPage,
  tags: ["autodocs"],
  args: { runId },
  beforeEach: () => installStoryApi((api) => {
    api.locations = [
      theUnknownPlace(),
      aDirectoryPlace({
        id: "overcaffeinated-gecko",
        label: "overcaffeinated-gecko-2026-aug-11",
        directory: "/srv/worktrees/overcaffeinated-gecko-2026-aug-11",
      }),
    ];
    api.runs = [aRun({
      id: runId,
      type: "review",
      label: runId,
      status: "failed",
      locationId: "overcaffeinated-gecko",
      startedAt: "2026-08-11T16:08:27Z",
      endedAt: "2026-08-11T16:08:52Z",
      iterations: 1,
    })];
    api.startedBy[runId] = "http://localhost:15556/dex|operator-1";
  }),
  decorators: [
    (Story) => (
      <div style={{ display: "flex", minHeight: "100vh" }}>
        <Story />
      </div>
    ),
  ],
} satisfies Meta<typeof RunPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const FailedReview: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.findByRole("heading", { name: runId })).resolves.toBeTruthy();
    await expect(canvas.findByText("Failed")).resolves.toBeTruthy();
    await expect(
      canvas.findByRole("link", { name: "overcaffeinated-gecko-2026-aug-11" }),
    ).resolves.toBeTruthy();
    await expect(canvas.findByRole("button", { name: "Run this again" })).resolves.toBeEnabled();
  },
};
