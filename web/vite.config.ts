import { readdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { defineConfig, type Plugin } from "vite";

const sourceDirectory = fileURLToPath(new URL("./src", import.meta.url));
const outputDirectory = fileURLToPath(new URL("./dist", import.meta.url));

// Vite normally merges module scripts declared in index.html into the application
// entry. This keeps the bootstrap separate, so it can set the theme before the
// application bundle downloads, while its filename remains content-hashed.
function themeBootstrapEntry(): Plugin {
  return {
    name: "theme-bootstrap-entry",
    apply: "build",
    async closeBundle() {
      const assetsDirectory = path.join(outputDirectory, "assets");
      const names = await readdir(assetsDirectory);
      const bootstrap = names.filter((name) => /^theme-bootstrap-.+\.js$/.test(name));
      if (bootstrap.length !== 1) {
        throw new Error("the build must emit one theme bootstrap module");
      }

      const indexPath = path.join(outputDirectory, "index.html");
      const index = await readFile(indexPath, "utf8");
      const updated = index.replace(
        /<meta charset="UTF-8"\s*\/?>(?:\n)?/,
        `<meta charset="UTF-8" />\n    <script type="module" src="/assets/${bootstrap[0]}"></script>\n`,
      );
      if (updated === index) {
        throw new Error("the application entry document is missing its character encoding declaration");
      }
      await writeFile(indexPath, updated);
    },
  };
}

function developmentThemeBootstrapEntry(): Plugin {
  return {
    name: "development-theme-bootstrap-entry",
    apply: "serve",
    transformIndexHtml(index) {
      return index.replace(
        "<meta charset=\"UTF-8\" />",
        '<meta charset="UTF-8" />\n    <script type="module" src="/theme-bootstrap.ts"></script>',
      );
    },
  };
}

export default defineConfig({
  plugins: [react(), developmentThemeBootstrapEntry(), themeBootstrapEntry()],
  root: sourceDirectory,
  build: {
    outDir: outputDirectory,
    emptyOutDir: true,
    rollupOptions: {
      input: {
        index: path.join(sourceDirectory, "index.html"),
        "theme-bootstrap": path.join(sourceDirectory, "theme-bootstrap.ts"),
      },
    },
  },
  server: {
    host: "127.0.0.1",
    port: 3001,
    proxy: {
      "/api/v1": "http://127.0.0.1:3000",
    },
  },
});
