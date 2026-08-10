import { describe, expect, it } from "vitest";
import { addressOf, OVERVIEW, routeOf } from "./route";

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
});
