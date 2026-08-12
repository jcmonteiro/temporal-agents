import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, within } from "storybook/test";
import type { Place } from "../../domain/place";
import { Launcher } from "./Launcher";

const checkout: Place = {
  id: "checkout",
  kind: "directory",
  label: "checkout",
  parentId: null,
  directory: "/srv/repos/checkout",
};

const meta = {
  title: "Work/Launcher",
  component: Launcher,
  tags: ["autodocs"],
  args: { place: checkout },
  decorators: [
    (Story) => (
      <div style={{ maxWidth: 880, padding: 24 }}>
        <Story />
      </div>
    ),
  ],
} satisfies Meta<typeof Launcher>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Develop: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const start = canvas.getByRole("button", { name: "Start" });
    await expect(start).toBeDisabled();

    await userEvent.type(
      canvas.getByLabelText("What to do"),
      "Preserve the original payment failure cause",
    );
    await expect(start).toBeEnabled();
    await expect(canvas.getByLabelText("Use a fresh worktree")).toBeChecked();
  },
};

export const Review: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByLabelText("Review"));
    await expect(canvas.queryByLabelText("What to do")).not.toBeInTheDocument();
    await expect(canvas.getByRole("button", { name: "Start" })).toBeEnabled();
  },
};
