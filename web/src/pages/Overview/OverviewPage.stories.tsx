import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, within } from "storybook/test";
import {
  aDirectoryPlace,
  aFleet,
  aNode,
  aRun,
  aSchedule,
  theUnknownPlace,
} from "../../test/fake-api";
import { installStoryApi } from "../../stories/story-api";
import { OverviewPage } from "./OverviewPage";

const meta = {
  title: "References/Locations canvas",
  component: OverviewPage,
  tags: ["autodocs"],
  beforeEach: () => installStoryApi((api) => {
    api.locations = [
      theUnknownPlace(),
      aDirectoryPlace({
        id: "checkout",
        label: "checkout",
        directory: "/srv/repos/checkout",
      }),
      aDirectoryPlace({
        id: "checkout-retry",
        label: "preserve-retry-cause",
        parentId: "checkout",
        directory: "/srv/worktrees/preserve-retry-cause",
      }),
    ];
    api.registered = [{ locationId: "checkout", registeredAt: "2026-08-01T09:00:00Z" }];
    api.fleets = [aFleet({
      id: "fleet-checkout",
      label: "Checkout reliability",
      locationId: "checkout",
      progress: { done: 2, total: 4, fraction: 0.5 },
      upNext: [aNode({ id: "node-audit", label: "Update the audit log" })],
    })];
    api.runs = [
      aRun({
        id: "develop-retry",
        label: "Preserve the payment failure cause",
        status: "in-progress",
        locationId: "checkout-retry",
      }),
      aRun({
        id: "review-tax",
        label: "Review tax rounding",
        status: "done",
        locationId: "checkout",
      }),
    ];
    api.schedules = [aSchedule({
      id: "schedule-nightly",
      label: "Nightly checkout review",
      locationId: "checkout",
    })];
  }),
  decorators: [
    (Story) => (
      <div style={{ display: "flex", height: "760px", minHeight: "100vh" }}>
        <Story />
      </div>
    ),
  ],
  parameters: {
    a11y: {
      test: "error",
    },
  },
} satisfies Meta<typeof OverviewPage>;

export default meta;
type Story = StoryObj<typeof meta>;

const activeWork = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const completed = await canvas.findByRole("button", {
      name: "Review tax rounding, Done",
    });
    await expect(
      canvas.findByRole("button", { name: "Preserve the payment failure cause, In Progress" }),
    ).resolves.toBeTruthy();

    await userEvent.click(completed);
    const selected = await canvas.findByText("Selected");
    const section = selected.closest("section");
    if (section === null) throw new Error("Selected section missing");
    await expect(within(section).findByText("Review tax rounding")).resolves.toBeTruthy();
  },
} satisfies Pick<Story, "play">;

export const ActiveWork: Story = {
  ...activeWork,
  globals: { theme: "light" },
};

export const ActiveWorkDark: Story = {
  ...activeWork,
  globals: { theme: "dark" },
};
