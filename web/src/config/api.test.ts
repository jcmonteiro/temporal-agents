import { afterEach, expect, it, vi } from "vitest";

afterEach(() => {
  vi.unstubAllEnvs();
  vi.resetModules();
});

it("uses the configured versioned API endpoint without a trailing slash", async () => {
  vi.stubEnv("VITE_AGENT_HUB_API_URL", "http://127.0.0.1:3000/api/v1/");

  const { apiAddress } = await import("./api");
  const { signInAddress } = await import("../clients/session");

  expect(apiAddress("/runs")).toBe("http://127.0.0.1:3000/api/v1/runs");
  expect(signInAddress("/#/runs", "http://127.0.0.1:3001")).toBe(
    "http://127.0.0.1:3000/api/v1/auth/sign-in?return=" +
      "http%3A%2F%2F127.0.0.1%3A3001%2F%23%2Fruns",
  );
});

it("keeps a relative return path when the API is same-origin", async () => {
  const { signInAddress } = await import("../clients/session");

  expect(signInAddress("/#/runs", "http://127.0.0.1:3001")).toBe(
    "/api/v1/auth/sign-in?return=%2F%23%2Fruns",
  );
});
