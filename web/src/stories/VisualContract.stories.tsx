import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, within } from "storybook/test";
import { VisualContract } from "./VisualContract";

const meta = {
  title: "Foundations/Visual contract",
  component: VisualContract,
  tags: ["autodocs"],
  args: {
    viewport: "wide",
  },
  decorators: [
    (Story, context) => context.args.viewport === "narrow" ? (
      <div className="visual-contract-frame--narrow"><Story /></div>
    ) : <Story />,
  ],
  parameters: {
    layout: "fullscreen",
    a11y: {
      test: "error",
    },
  },
} satisfies Meta<typeof VisualContract>;

export default meta;
type Story = StoryObj<typeof meta>;

const exercisesContract = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const runName = canvas.getByLabelText("Run name");
    const reviewDepth = canvas.getByLabelText("Review depth");
    const primary = canvas.getByRole("button", { name: "Start review" });
    const density = canvas.getByRole("button", { name: "Compact density" });

    runName.focus();
    await userEvent.tab();
    await expect(reviewDepth).toHaveFocus();
    await userEvent.tab();
    await expect(primary).toHaveFocus();

    await userEvent.click(density);
    await expect(density).toHaveAttribute("aria-pressed", "true");
    await expect(canvas.getByRole("status", { name: "Success" })).toBeVisible();
  },
} satisfies Pick<Story, "play">;

export const WideLight: Story = {
  ...exercisesContract,
  globals: { theme: "light" },
};

export const WideDark: Story = {
  ...exercisesContract,
  globals: { theme: "dark" },
};

export const NarrowLight: Story = {
  ...exercisesContract,
  args: { viewport: "narrow" },
  globals: { theme: "light" },
  play: async (context) => {
    await exercisesContract.play(context);
    const contract = context.canvasElement.querySelector<HTMLElement>(".visual-contract");
    if (contract === null) throw new Error("Visual contract missing");
    await expect(contract.scrollWidth).toBeLessThanOrEqual(contract.clientWidth);
  },
};

export const NarrowDark: Story = {
  ...NarrowLight,
  globals: { theme: "dark" },
};
