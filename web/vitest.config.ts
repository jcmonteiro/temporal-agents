import { defineConfig } from "vitest/config";
import { fileURLToPath } from "node:url";

// A dedicated config: vite.config.ts sets `root` to src for the app build,
// which is the wrong root for test discovery.
export default defineConfig({
  test: {
    root: fileURLToPath(new URL(".", import.meta.url)),
    include: ["src/**/*.test.ts", "src/**/*.test.tsx"],
    environment: "node",
    // Fills the browser API gaps of jsdom for the DOM test files.
    setupFiles: ["src/test/setup.ts"],
  },
});
