import { describe, expect, it } from "vitest";
import {
  addressOf,
  navigationKeyOf,
  OVERVIEW,
  routeOf,
  SETTINGS,
  SETTINGS_PLACES,
} from "./route";

describe("the address of a route", () => {
  it("names a place by its id", () => {
    expect(addressOf({ name: "place", placeId: "abc123" })).toBe("#/places/abc123");
  });

  it("escapes an id that would otherwise break the address", () => {
    expect(addressOf({ name: "place", placeId: "a/b c" })).toBe(
      "#/places/a%2Fb%20c",
    );
  });

  it("names the overview", () => {
    expect(addressOf(OVERVIEW)).toBe("#/");
  });

  it("names a run, a fleet and each settings category", () => {
    expect(addressOf({ name: "run", runId: "run-1" })).toBe("#/runs/run-1");
    expect(addressOf({ name: "fleet", fleetId: "fleet-1" })).toBe("#/fleets/fleet-1");
    expect(addressOf(SETTINGS)).toBe("#/settings");
    expect(addressOf(SETTINGS_PLACES)).toBe("#/settings/places");
  });
});

describe("the route an address names", () => {
  it("reads a place address", () => {
    expect(routeOf("#/places/abc123")).toEqual({
      name: "place",
      placeId: "abc123",
    });
  });

  it("reads an escaped id back", () => {
    expect(routeOf("#/places/a%2Fb%20c")).toEqual({
      name: "place",
      placeId: "a/b c",
    });
  });

  it("reads an empty address as the overview", () => {
    expect(routeOf("")).toEqual(OVERVIEW);
    expect(routeOf("#/")).toEqual(OVERVIEW);
  });

  it("reads an address it does not know as the overview", () => {
    expect(routeOf("#/somewhere/else/entirely")).toEqual(OVERVIEW);
    expect(routeOf("#/places")).toEqual(OVERVIEW);
    expect(routeOf("#/places/")).toEqual(OVERVIEW);
  });

  it("reads back every address it writes", () => {
    const route = { name: "place", placeId: "dir:/srv/checkout" } as const;

    expect(routeOf(addressOf(route))).toEqual(route);
  });

  it("reads a run, a fleet and each settings category", () => {
    expect(routeOf("#/runs/run-1")).toEqual({ name: "run", runId: "run-1" });
    expect(routeOf("#/fleets/fleet-1")).toEqual({ name: "fleet", fleetId: "fleet-1" });
    expect(routeOf("#/settings")).toEqual(SETTINGS);
    expect(routeOf("#/settings/places")).toEqual(SETTINGS_PLACES);
  });

  it("reads a destination that names no thing as the overview", () => {
    expect(routeOf("#/runs")).toEqual(OVERVIEW);
    expect(routeOf("#/fleets/")).toEqual(OVERVIEW);
    expect(routeOf("#/settings/anything")).toEqual(OVERVIEW);
  });
});

describe("the navigation entry a route belongs under", () => {
  it("puts a place under the overview, because it is the overview close up", () => {
    expect(navigationKeyOf(OVERVIEW)).toBe("overview");
    expect(navigationKeyOf({ name: "place", placeId: "dir-1" })).toBe("overview");
  });

  it("puts a fleet under the fleets, and every settings category under settings", () => {
    expect(navigationKeyOf({ name: "fleet", fleetId: "fleet-1" })).toBe("fleets");
    expect(navigationKeyOf(SETTINGS)).toBe("settings");
    expect(navigationKeyOf(SETTINGS_PLACES)).toBe("settings");
  });

  it("puts a run under no entry rather than under a wrong one", () => {
    expect(navigationKeyOf({ name: "run", runId: "run-1" })).toBeNull();
  });
});
