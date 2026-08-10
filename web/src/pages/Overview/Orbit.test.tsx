// @vitest-environment jsdom
import { act, cleanup, render, screen } from "@testing-library/react";
import { fireEvent } from "@testing-library/dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { WorkItem } from "../../domain/work-item";
import { registryOf, type Place } from "../../domain/place";
import { Orbit } from "./Orbit";
import { ORBIT_PERIOD_MS } from "./orbit-motion";

const FLEET: WorkItem = {
  id: "fleet-1",
  kind: "fleet",
  label: "Checkout revamp",
  status: "in-progress",
  icon: "rocket",
  placeId: "unknown",
};

const UNKNOWN_PLACE: Place = {
  id: "unknown",
  kind: "unknown",
  label: "Unknown",
  parentId: null,
};

function aDirectory(id: string, parentId: string | null = null): Place {
  return { id, kind: "directory", label: id, parentId, directory: `/srv/${id}` };
}

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
): {
  onSelect: ReturnType<typeof vi.fn>;
  onSelectPlace: ReturnType<typeof vi.fn>;
  onClear: ReturnType<typeof vi.fn>;
} {
  const onSelect = vi.fn();
  const onSelectPlace = vi.fn();
  const onClear = vi.fn();
  render(
    <Orbit
      items={[FLEET]}
      places={registryOf([UNKNOWN_PLACE])}
      selected={null}
      selectedPlaceId={null}
      onSelect={onSelect}
      onSelectPlace={onSelectPlace}
      onClear={onClear}
      {...overrides}
    />,
  );
  return { onSelect, onSelectPlace, onClear };
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

// A repository with two worktrees, and work in each of them.
const REPOSITORY_WITH_WORKTREES = registryOf([
  UNKNOWN_PLACE,
  aDirectory("repo"),
  aDirectory("tree-a", "repo"),
  aDirectory("tree-b", "repo"),
]);

function runIn(placeId: string, id: string): WorkItem {
  return {
    id,
    kind: "run",
    label: id,
    status: "in-progress",
    icon: "document",
    placeId,
  };
}

/** The places the canvas draws, as the operator hears them named. */
function placeNames(): string[] {
  return Array.from(document.querySelectorAll(".place")).map(
    (place) => place.getAttribute("aria-label") ?? "",
  );
}

describe("the places on the canvas", () => {
  it("draws one body per place, named and focusable", () => {
    showOrbit({
      items: [runIn("tree-a", "run-1")],
      places: REPOSITORY_WITH_WORKTREES,
    });

    expect(placeNames()).toEqual([
      "Unknown, place, 0 items",
      "repo, place, 0 items",
      "tree-a, place, 1 item",
      "tree-b, place, 0 items",
    ]);
    document.querySelectorAll(".place").forEach((place) => {
      expect(place.getAttribute("tabindex")).toBe("0");
    });
  });

  it("reports the place the operator picks", () => {
    const { onSelectPlace } = showOrbit({
      items: [runIn("tree-a", "run-1")],
      places: REPOSITORY_WITH_WORKTREES,
    });

    fireEvent.click(screen.getByRole("button", { name: "tree-a, place, 1 item" }));

    expect(onSelectPlace).toHaveBeenCalledWith(
      expect.objectContaining({ id: "tree-a" }),
    );
  });

  it("reports the place the operator confirms with the keyboard", () => {
    const { onSelectPlace } = showOrbit({
      items: [runIn("tree-a", "run-1")],
      places: REPOSITORY_WITH_WORKTREES,
    });

    fireEvent.keyDown(
      screen.getByRole("button", { name: "tree-a, place, 1 item" }),
      { key: "Enter" },
    );

    expect(onSelectPlace).toHaveBeenCalledWith(
      expect.objectContaining({ id: "tree-a" }),
    );
  });

  it("folds every place into the place that holds it on request", () => {
    showOrbit({
      items: [runIn("tree-a", "run-1"), runIn("tree-b", "run-2")],
      places: REPOSITORY_WITH_WORKTREES,
    });

    fireEvent.click(screen.getByRole("button", { name: "Collapse every place" }));

    // One body per repository, and the fold announces how many places it took.
    expect(placeNames()).toEqual([
      "Unknown, place, 0 items",
      "repo, place, 2 items, 2 places folded in",
    ]);
    expect(
      screen.getByRole("button", { name: "Show the places again" }).getAttribute(
        "aria-pressed",
      ),
    ).toBe("true");
  });

  it("shows the places again once the operator unfolds them", () => {
    showOrbit({
      items: [runIn("tree-a", "run-1")],
      places: REPOSITORY_WITH_WORKTREES,
    });
    fireEvent.click(screen.getByRole("button", { name: "Collapse every place" }));

    fireEvent.click(screen.getByRole("button", { name: "Show the places again" }));

    expect(placeNames()).toContain("tree-a, place, 1 item");
  });

  it("keeps the work of a folded place on the canvas", () => {
    showOrbit({
      items: [runIn("tree-a", "run-1")],
      places: REPOSITORY_WITH_WORKTREES,
    });

    fireEvent.click(screen.getByRole("button", { name: "Collapse every place" }));

    expect(screen.getByRole("button", { name: "run-1, In Progress" })).toBeTruthy();
  });

  it("turns the satellites of a place about that place", () => {
    vi.useFakeTimers();
    showOrbit({
      items: [runIn("tree-a", "run-1")],
      places: REPOSITORY_WITH_WORKTREES,
    });
    const target = satellite("run-1, In Progress");
    const place = screen.getByRole("button", { name: "tree-a, place, 1 item" });
    const home = placeOf(place as unknown as SVGGElement);
    const start = placeOf(target);

    letItTurn(ORBIT_PERIOD_MS / 2);

    const halfway = placeOf(target);
    // The satellite travelled, its place did not, and the satellite stayed the
    // same distance from it: it turns about its own place, not about the canvas.
    expect(placeOf(place as unknown as SVGGElement)).toEqual(home);
    expect(Math.hypot(halfway.x - start.x, halfway.y - start.y)).toBeGreaterThan(100);
    expect(Math.hypot(halfway.x - home.x, halfway.y - home.y)).toBeCloseTo(
      Math.hypot(start.x - home.x, start.y - home.y),
      6,
    );
  });
});
