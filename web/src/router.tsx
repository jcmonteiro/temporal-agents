import { lazy, Suspense, type ReactNode } from "react";
import { addressOf, useRoute, type Route } from "./platform/route";
import { RouteErrorBoundary } from "./platform/route-error-boundary";

/**
 * Which page the address names.
 *
 * Every page is loaded on demand. The hub opens on the overview, and an
 * operator who never opens a fleet never pays for the fleet page; more
 * importantly, a page added by a later feature cannot make the first paint of
 * the overview slower.
 *
 * The routed page is wrapped in a boundary that resets when the address
 * changes, so one failing page is a failed page and not a failed hub.
 */
const OverviewPage = lazy(async () => ({
  default: (await import("./pages/Overview/OverviewPage")).OverviewPage,
}));
const PlacePage = lazy(async () => ({
  default: (await import("./pages/Place/PlacePage")).PlacePage,
}));
const RunPage = lazy(async () => ({
  default: (await import("./pages/Run/RunPage")).RunPage,
}));
const FleetPage = lazy(async () => ({
  default: (await import("./pages/Fleet/FleetPage")).FleetPage,
}));
const SettingsPage = lazy(async () => ({
  default: (await import("./pages/Settings/SettingsPage")).SettingsPage,
}));

/** The page of the current route, contained and loaded on demand. */
export function Router(): ReactNode {
  const route = useRoute();
  return (
    <RouteErrorBoundary resetKey={addressOf(route)}>
      <Suspense fallback={<Loading />}>{pageOf(route)}</Suspense>
    </RouteErrorBoundary>
  );
}

function pageOf(route: Route): ReactNode {
  switch (route.name) {
    case "place":
      return <PlacePage placeId={route.placeId} />;
    case "run":
      return <RunPage runId={route.runId} />;
    case "fleet":
      return <FleetPage fleetId={route.fleetId} />;
    case "settings":
      return <SettingsPage />;
    default:
      return <OverviewPage />;
  }
}

/** Shown only for as long as a page's module is on its way. */
function Loading(): ReactNode {
  return (
    <main role="status" style={{ flex: 1, display: "grid", placeItems: "center" }}>
      <span style={{ color: "var(--color-text-muted)" }}>Opening…</span>
    </main>
  );
}
