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
  | { name: "place"; placeId: string };

export const OVERVIEW: Route = { name: "overview" };

/** The address of a route, fragment included. */
export function addressOf(route: Route): string {
  if (route.name === "place") return `#/places/${encodeURIComponent(route.placeId)}`;
  return "#/";
}

/**
 * The route an address names. Anything unrecognised reads as the overview: a
 * stale bookmark lands somewhere real instead of on a blank page.
 */
export function routeOf(address: string): Route {
  const fragment = address.startsWith("#") ? address.slice(1) : address;
  const path = fragment.replace(/^\/+/, "").replace(/\/+$/, "");
  const [section, ...rest] = path.split("/");
  if (section === "places" && rest.length === 1 && rest[0] !== "") {
    return { name: "place", placeId: decodeURIComponent(rest[0]) };
  }
  return OVERVIEW;
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
