/**
 * Where the operator is, read from the address bar.
 *
 * The hub is served as one static document, so the route lives in the fragment:
 * a fragment is never sent to the server and needs no rewrite rule. Parsing and
 * formatting are pure, so both are unit tested; the hook is the thin adapter
 * that subscribes to the browser.
 */

import { useEffect, useState } from "react";

export type Route =
  | { name: "overview" }
  | { name: "place"; placeId: string }
  | { name: "run"; runId: string }
  | { name: "fleet"; fleetId: string }
  | { name: "settings" };

export const OVERVIEW: Route = { name: "overview" };
export const SETTINGS: Route = { name: "settings" };

/** The address of a route, fragment included. */
export function addressOf(route: Route): string {
  switch (route.name) {
    case "place":
      return `#/places/${encodeURIComponent(route.placeId)}`;
    case "run":
      return `#/runs/${encodeURIComponent(route.runId)}`;
    case "fleet":
      return `#/fleets/${encodeURIComponent(route.fleetId)}`;
    case "settings":
      return "#/settings";
    default:
      return "#/";
  }
}

/**
 * The route an address names. Anything unrecognised reads as the overview: a
 * stale bookmark lands somewhere real instead of on a blank page.
 */
export function routeOf(address: string): Route {
  const fragment = address.startsWith("#") ? address.slice(1) : address;
  const path = fragment.replace(/^\/+/, "").replace(/\/+$/, "");
  const [section, ...rest] = path.split("/");
  const id = rest.length === 1 && rest[0] !== "" ? decodeURIComponent(rest[0]) : null;
  if (section === "places" && id !== null) return { name: "place", placeId: id };
  if (section === "runs" && id !== null) return { name: "run", runId: id };
  if (section === "fleets" && id !== null) return { name: "fleet", fleetId: id };
  if (section === "settings" && rest.length === 0) return SETTINGS;
  return OVERVIEW;
}

/**
 * The navigation entry a route belongs under.
 *
 * A place and the overview share an entry, because a place is the overview
 * looked at closely; a run and a fleet belong to no entry of their own, so the
 * navigation highlights nothing rather than highlighting something wrong.
 */
export function navigationKeyOf(route: Route): string | null {
  switch (route.name) {
    case "overview":
    case "place":
      return "overview";
    case "fleet":
      return "fleets";
    case "settings":
      return "settings";
    default:
      return null;
  }
}

/**
 * Goes to a route.
 *
 * Assigning the address is what a link does, so the browser records the step and
 * the operator can go back — which matters most exactly where this is used: an
 * operator who has just started work and wants to return to where they started
 * it from.
 */
export function goTo(route: Route): void {
  window.location.hash = addressOf(route);
}

/** The current route, kept current as the operator navigates. */
export function useRoute(): Route {
  const [route, setRoute] = useState<Route>(() => routeOf(window.location.hash));
  useEffect(() => {
    const onChange = (): void => setRoute(routeOf(window.location.hash));
    window.addEventListener("hashchange", onChange);
    onChange();
    return () => window.removeEventListener("hashchange", onChange);
  }, []);
  return route;
}
