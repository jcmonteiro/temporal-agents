/**
 * Where the operator is, read from the address bar.
 *
 * The hub is served as one static document, so the route lives in the fragment:
 * a fragment is never sent to the server and needs no rewrite rule. Parsing and
 * formatting are pure, so both are unit tested; the hook is the thin adapter
 * that subscribes to the browser.
 */

import { useEffect, useState } from "react";

export type PlaceCategory = "overview" | "start" | "instructions";
export type SettingsCategory = "appearance" | "instructions" | "places";

export type Route =
  | { name: "overview" }
  | { name: "place"; placeId: string; category: PlaceCategory }
  | { name: "run"; runId: string }
  | { name: "fleet"; fleetId: string }
  | { name: "settings"; category: SettingsCategory };

export const OVERVIEW: Route = { name: "overview" };
export const SETTINGS: Route = { name: "settings", category: "instructions" };
export const SETTINGS_APPEARANCE: Route = { name: "settings", category: "appearance" };
export const SETTINGS_PLACES: Route = { name: "settings", category: "places" };

/** The address of a route, fragment included. */
export function addressOf(route: Route): string {
  switch (route.name) {
    case "place": {
      const place = `#/places/${encodeURIComponent(route.placeId)}`;
      return route.category === "overview" ? place : `${place}/${route.category}`;
    }
    case "run":
      return `#/runs/${encodeURIComponent(route.runId)}`;
    case "fleet":
      return `#/fleets/${encodeURIComponent(route.fleetId)}`;
    case "settings":
      if (route.category === "appearance") return "#/settings/appearance";
      return route.category === "places" ? "#/settings/places" : "#/settings";
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
  const placeCategory =
    rest.length === 1
      ? "overview"
      : rest.length === 2 && (rest[1] === "start" || rest[1] === "instructions")
        ? rest[1]
        : null;
  if (section === "places" && rest[0] !== "" && placeCategory !== null) {
    return {
      name: "place",
      placeId: decodeURIComponent(rest[0]),
      category: placeCategory,
    };
  }
  const id = rest.length === 1 && rest[0] !== "" ? decodeURIComponent(rest[0]) : null;
  if (section === "runs" && id !== null) return { name: "run", runId: id };
  if (section === "fleets" && id !== null) return { name: "fleet", fleetId: id };
  if (section === "settings" && rest.length === 0) return SETTINGS;
  if (section === "settings" && rest.length === 1 && rest[0] === "appearance") {
    return SETTINGS_APPEARANCE;
  }
  if (section === "settings" && rest.length === 1 && rest[0] === "places") {
    return SETTINGS_PLACES;
  }
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
