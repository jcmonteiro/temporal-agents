package httpapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestBuiltApplicationAppliesThePersistedThemeBeforeRendering exercises the built
// application through the HTTP server that publishes it. It protects the contract
// between Vite's generated module entry points and the server's strict CSP.
func TestBuiltApplicationAppliesThePersistedThemeBeforeRendering(t *testing.T) {
	webDir := webProjectDirectory(t)
	runWebCommand(t, webDir, "pnpm", "build")

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for browser test server: %v", err)
	}
	origin := "http://" + listener.Addr().String()
	server := httptest.NewUnstartedServer(newTestServer(t, &viewStub{}, func(options *Options) {
		options.AllowedOrigins = []string{origin}
		options.WebDir = filepath.Join(webDir, "dist")
	}))
	server.Listener = listener
	server.Start()
	defer server.Close()

	var report themeBootstrapReport
	output := runWebCommand(t, webDir, "node", "--input-type=module", "--eval", themeBootstrapBrowserScript, server.URL)
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("decode browser report: %v\n%s", err, output)
	}
	if len(report.CSPViolations) != 0 {
		t.Errorf("CSP violations = %q, want none", report.CSPViolations)
	}
	if !report.Rendered {
		t.Fatalf("application did not render: root=%q diagnostics=%q", report.Root, report.Diagnostics)
	}
	if report.Theme != "dark" {
		t.Errorf("theme before application rendering = %q, want dark", report.Theme)
	}
}

type themeBootstrapReport struct {
	CSPViolations []string `json:"cspViolations"`
	Diagnostics   []string `json:"diagnostics"`
	Rendered      bool     `json:"rendered"`
	Root          string   `json:"root"`
	Theme         string   `json:"theme"`
}

func webProjectDirectory(t *testing.T) string {
	t.Helper()
	directory, err := filepath.Abs(filepath.Join("..", "..", "web"))
	if err != nil {
		t.Fatalf("resolve web project directory: %v", err)
	}
	return directory
}

func runWebCommand(t *testing.T, directory, name string, arguments ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	command := exec.CommandContext(ctx, name, arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("%s timed out: %v\n%s", name, ctx.Err(), output)
	}
	if err != nil {
		t.Fatalf("run %s: %v\n%s", name, err, output)
	}
	return output
}

const themeBootstrapBrowserScript = `
import { chromium } from "playwright";

const cspViolations = [];
const diagnostics = [];
const browser = await chromium.launch({ headless: true });
try {
  const page = await browser.newPage();
  page.on("console", (message) => {
    if (message.type() !== "error") return;
    diagnostics.push(message.text());
    if (/content security policy|violates the following content security policy/i.test(message.text())) {
      cspViolations.push(message.text());
    }
  });
  page.on("pageerror", (error) => diagnostics.push(error.message));
  await page.addInitScript(() => {
    localStorage.setItem("agent-hub.theme-preference", "dark");
    const observer = new MutationObserver(() => {
      const root = document.getElementById("root");
      if (root?.childNodes.length && !("__themeAtFirstApplicationRender" in window)) {
        window.__themeAtFirstApplicationRender = document.documentElement.dataset.theme;
        observer.disconnect();
      }
    });
    observer.observe(document, { childList: true, subtree: true });
  });
  await page.goto(process.argv[1], { waitUntil: "domcontentloaded" });
  const rendered = await page
    .waitForFunction(
      () => "__themeAtFirstApplicationRender" in window,
      undefined,
      { timeout: 10_000 },
    )
    .then(() => true)
    .catch(() => false);
  process.stdout.write(
    JSON.stringify({
      cspViolations,
      diagnostics,
      rendered,
      root: await page.evaluate(() => document.getElementById("root")?.innerHTML ?? ""),
      theme: await page.evaluate(() => window.__themeAtFirstApplicationRender ?? ""),
    }),
  );
} finally {
  await browser.close();
}
`
