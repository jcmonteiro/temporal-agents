// @vitest-environment jsdom
import { act, cleanup, render, screen } from "@testing-library/react";
import { fireEvent } from "@testing-library/dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { WorkItem } from "../../domain/work-item";
import { Orbit } from "./Orbit";
import { ORBIT_PERIOD_MS } from "./orbit-motion";

const FLEET: WorkItem = {
  id: "fleet-1",
  kind: "fleet",
  label: "Checkout revamp",
  status: "in-progress",
  icon: "rocket",
};

/** Answers the reduced-motion query with the given preference. */
function preferReducedMotion(reduce: boolean): void {
  vi.spyOn(window, "matchMedia").mockImplementation(
    (query: string) =>
      ({
        matches: reduce && query.includes("prefers-reduced-motion"),
        media: query,
        addEventListener: () => {},
        removeEventListener: () => {},
      }) as unknown as MediaQueryList,
  );
}

function showOrbit(
  overrides: Partial<Parameters<typeof Orbit>[0]> = {},
): { onSelect: ReturnType<typeof vi.fn>; onClear: ReturnType<typeof vi.fn> } {
  const onSelect = vi.fn();
  const onClear = vi.fn();
  render(
    <Orbit
      items={[FLEET]}
      selected={null}
      onSelect={onSelect}
      onClear={onClear}
      {...overrides}
    />,
  );
  return { onSelect, onClear };
}

/** The satellite of the given work item, as the operator picks it out. */
function satellite(label: string): SVGGElement {
  return screen.getByRole("button", { name: label }) as unknown as SVGGElement;
}

/** The place a satellite occupies on the canvas. */
function placeOf(el: SVGGElement): { x: number; y: number } {
  const transform = el.getAttribute("transform") ?? "";
  const place = /^translate\(\s*(-?[\d.e+-]+)\s*,\s*(-?[\d.e+-]+)\s*\)$/.exec(transform);
  if (!place) throw new Error(`the satellite is not simply placed: "${transform}"`);
  return { x: Number(place[1]), y: Number(place[2]) };
}

/** Every transform on the satellite and on everything it carries. */
function transformsWithin(el: SVGGElement): string[] {
  return [el, ...Array.from(el.querySelectorAll("*"))]
    .map((node) => node.getAttribute("transform") ?? "")
    .filter((transform) => transform !== "");
}

/** Lets the given amount of orbital motion happen, frame by frame. */
function letItTurn(ms: number): void {
  act(() => {
    vi.advanceTimersByTime(ms);
  });
}

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("the orbit motion", () => {
  it("turns by default", () => {
    showOrbit();

    expect(screen.getByRole("button", { name: "Pause orbit animation" })).toBeTruthy();
  });

  it("starts still when the operator prefers reduced motion", () => {
    preferReducedMotion(true);

    showOrbit();

    expect(screen.getByRole("button", { name: "Play orbit animation" })).toBeTruthy();
  });

  it("stops and starts on request", () => {
    showOrbit();

    fireEvent.click(screen.getByRole("button", { name: "Pause orbit animation" }));

    expect(screen.getByRole("button", { name: "Play orbit animation" })).toBeTruthy();
  });

  it("carries a satellite once around the centre per period", () => {
    vi.useFakeTimers();
    showOrbit();
    const target = satellite("Checkout revamp, In Progress");
    const start = placeOf(target);

    letItTurn(ORBIT_PERIOD_MS / 4);
    const quarterWay = placeOf(target);
    letItTurn((ORBIT_PERIOD_MS * 3) / 4);
    const fullTurn = placeOf(target);

    // A quarter of a period takes the satellite well away from where it began,
    // and a whole period brings it back (bar the odd frame of travel).
    expect(Math.hypot(quarterWay.x - start.x, quarterWay.y - start.y)).toBeGreaterThan(100);
    expect(Math.hypot(fullTurn.x - start.x, fullTurn.y - start.y)).toBeLessThan(1);
  });

  it("holds the satellite still while the motion is stopped", () => {
    vi.useFakeTimers();
    showOrbit();
    const target = satellite("Checkout revamp, In Progress");
    fireEvent.click(screen.getByRole("button", { name: "Pause orbit animation" }));
    const stopped = placeOf(target);

    letItTurn(ORBIT_PERIOD_MS / 4);

    expect(placeOf(target)).toEqual(stopped);
  });

  it("never tilts a satellite, however many turns it makes", () => {
    vi.useFakeTimers();
    showOrbit();
    const target = satellite("Checkout revamp, In Progress");
    const upright = transformsWithin(target).slice(1);

    letItTurn(ORBIT_PERIOD_MS * 20);

    // The satellite is only ever placed, never turned, and nothing it carries
    // moves relative to it: its icon and label therefore stay upright.
    expect(placeOf(target)).toBeTruthy();
    expect(transformsWithin(target).slice(1)).toEqual(upright);
  });
});

describe("picking a satellite", () => {
  it("reports the item the operator clicks", () => {
    const { onSelect } = showOrbit();

    fireEvent.click(
      screen.getByRole("button", { name: "Checkout revamp, In Progress" }),
    );

    expect(onSelect).toHaveBeenCalledWith(FLEET);
  });

  it("reports the item the operator confirms with the keyboard", () => {
    const { onSelect } = showOrbit();

    fireEvent.keyDown(
      screen.getByRole("button", { name: "Checkout revamp, In Progress" }),
      { key: "Enter" },
    );

    expect(onSelect).toHaveBeenCalledWith(FLEET);
  });
});

describe("clearing the selection", () => {
  it("clears on the Escape key", () => {
    const { onClear } = showOrbit({ selected: { kind: "fleet", id: "fleet-1" } });

    fireEvent.keyDown(window, { key: "Escape" });

    expect(onClear).toHaveBeenCalled();
  });

  it("keeps the selection on any other key", () => {
    const { onClear } = showOrbit({ selected: { kind: "fleet", id: "fleet-1" } });

    fireEvent.keyDown(window, { key: "a" });

    expect(onClear).not.toHaveBeenCalled();
  });

  it("stops listening for Escape once the canvas goes away", () => {
    const { onClear } = showOrbit({ selected: { kind: "fleet", id: "fleet-1" } });

    cleanup();
    fireEvent.keyDown(window, { key: "Escape" });

    expect(onClear).not.toHaveBeenCalled();
  });
});
