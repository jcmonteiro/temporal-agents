import type { WorkItem } from "../domain/work-item";

// Base URL of the Temporal web UI. Defaults to the local dev server's UI port
// (docker-compose maps the container's 8233 to host 18233), and can be
// overridden at build time with VITE_TEMPORAL_UI_URL for other deployments.
const TEMPORAL_UI_URL =
  (import.meta.env.VITE_TEMPORAL_UI_URL as string | undefined)?.replace(/\/+$/, "") ??
  "http://localhost:18233";

// Namespace the CLI/worker use (client.DefaultNamespace).
const NAMESPACE = "default";

/**
 * URL of a work item in the Temporal web UI, or null when the item has no
 * Temporal execution to open. Fleets and runs are identified by their workflow
 * ID; schedules have their own schedule page.
 */
export function temporalUrlFor(item: WorkItem): string | null {
  const base = `${TEMPORAL_UI_URL}/namespaces/${NAMESPACE}`;
  switch (item.kind) {
    case "fleet":
    case "run":
      return `${base}/workflows/${encodeURIComponent(item.id)}`;
    case "schedule":
      return `${base}/schedules/${encodeURIComponent(item.id)}`;
    default:
      return null;
  }
}
