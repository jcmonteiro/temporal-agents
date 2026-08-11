// Versioned Agent Hub API endpoint. A relative default keeps the built-in and Vite-
// proxied deployments same-origin. A separately hosted bundle can name the API's
// exact origin at build time with VITE_AGENT_HUB_API_URL.
const API_BASE_URL =
  (import.meta.env.VITE_AGENT_HUB_API_URL as string | undefined)?.replace(/\/+$/, "") ||
  "/api/v1";

/** Returns one API address for fetches and browser navigations. */
export function apiAddress(path: string): string {
  return `${API_BASE_URL}${path}`;
}
