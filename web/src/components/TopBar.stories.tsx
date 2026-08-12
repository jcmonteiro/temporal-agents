import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, within } from "storybook/test";
import { SessionProvider } from "../platform/session";
import { installStoryApi } from "../stories/story-api";
import { TopBar } from "./TopBar";

const meta = {
  title: "References/Notification menu",
  component: TopBar,
  tags: ["autodocs"],
  beforeEach: () => installStoryApi((api) => {
    api.notifications = [
      {
        id: "waiting-review",
        kind: "steering",
        title: "Guidance waiting",
        body: "review-4fe4171d needs a decision before implementation can continue.",
        url: "/#/runs/review-4fe4171d",
        sessionId: "steering-review-1",
        createdAt: "2026-08-11T16:05:00Z",
        read: false,
      },
      {
        id: "finished-run",
        kind: "completed",
        title: "Review finished",
        body: "The checkout review completed with no actionable findings.",
        url: "/#/runs/review-finished",
        createdAt: "2026-08-11T15:10:00Z",
        read: false,
      },
      {
        id: "older-run",
        kind: "completed",
        title: "Development finished",
        body: "The retry now preserves the payment provider cause.",
        url: "/#/runs/develop-finished",
        createdAt: "2026-08-10T12:00:00Z",
        read: true,
      },
    ];
  }),
  decorators: [
    (Story) => (
      <SessionProvider>
        <div style={{ minHeight: "100vh" }}>
          <Story />
        </div>
      </SessionProvider>
    ),
  ],
  parameters: {
    a11y: {
      test: "error",
    },
  },
} satisfies Meta<typeof TopBar>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Open: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const trigger = await canvas.findByRole("button", { name: "Notifications, 2 unread" });
    await userEvent.click(trigger);

    const inbox = await canvas.findByRole("region", { name: "Notification inbox" });
    await expect(within(inbox).findByText("Guidance waiting")).resolves.toBeTruthy();
    await expect(within(inbox).findByText("Review finished")).resolves.toBeTruthy();
  },
};

export const OpenDark: Story = {
  ...Open,
  globals: { theme: "dark" },
};

export const Actions: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(
      await canvas.findByRole("button", { name: "Notifications, 2 unread" }),
    );
    await userEvent.click(
      await canvas.findByRole("button", { name: "Notification actions" }),
    );

    await expect(
      canvas.findByRole("menuitem", { name: "Mark all as read" }),
    ).resolves.toBeEnabled();
    await expect(
      canvas.findByRole("menuitem", { name: "Enable native notifications" }),
    ).resolves.toBeTruthy();
  },
};
