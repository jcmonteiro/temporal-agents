import type { Preview } from "@storybook/react-vite";
import "../src/styles/tokens.css";
import "../src/styles/global.css";

const preview: Preview = {
  decorators: [
    (Story, context) => {
      document.documentElement.setAttribute(
        "data-theme",
        context.globals.theme === "dark" ? "dark" : "light",
      );
      return <Story />;
    },
  ],
  globalTypes: {
    theme: {
      description: "Interface theme",
      defaultValue: "light",
      toolbar: {
        icon: "paintbrush",
        items: ["light", "dark"],
      },
    },
  },
  parameters: {
    layout: "fullscreen",
    controls: {
      matchers: {
        color: /(background|color)$/i,
        date: /Date$/i,
      },
    },
    a11y: {
      test: "todo",
    },
    docs: {
      story: {
        inline: false,
      },
    },
    options: {
      storySort: {
        order: ["Pages", "Work", "Overlays", "Navigation"],
      },
    },
  },
};

export default preview;
