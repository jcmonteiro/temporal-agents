import { describe, expect, it } from "vitest";
import { registryOf, type Place, type PlaceRegistry } from "../../domain/place";
import type { WorkItem, WorkItemStatus } from "../../domain/work-item";
import {
  boundsOf,
  CENTRE_RADIUS,
  fittedView,
  layoutScene,
  type PlaceBody,
  type Scene,
} from "./scene";
import { MIN_ZOOM, visibleDepthFor } from "./view";

const CANVAS = { width: 1200, height: 800 };

function aPlace(id: string, parentId: string | null = null): Place {
  return { id, kind: "directory", label: id, parentId, directory: `/srv/${id}` };
}

const UNKNOWN: Place = {
  id: "unknown",
  kind: "unknown",
  label: "Unknown",
  parentId: null,
};

function work(
  placeId: string,
  count: number,
  status: WorkItemStatus = "in-progress",
): WorkItem[] {
  return Array.from({ length: count }, (_, i) => ({
    id: `${placeId}-${i}`,
    kind: "run" as const,
    label: `${placeId} ${i}`,
    status,
    icon: "document" as const,
    placeId,
  }));
}

function scene(
  items: WorkItem[],
  places: PlaceRegistry,
  visibleDepth: number,
): Scene {
  return layoutScene(items, places, { ...CANVAS, visibleDepth });
}

function bodyOf(drawn: Scene, placeId: string): PlaceBody {
  const body = drawn.bodies.find((b) => b.place.id === placeId);
  if (!body) throw new Error(`no body for ${placeId}`);
  return body;
}

function itemsOf(body: PlaceBody): string[] {
  return body.slots.map((s) => s.item.id).sort();
}

/** The closest two bodies come to overlapping. Negative means they overlap. */
function tightestClearance(drawn: Scene): number {
  let tightest = Infinity;
  drawn.bodies.forEach((a, i) => {
    drawn.bodies.slice(i + 1).forEach((b) => {
      const between = Math.hypot(a.centre.x - b.centre.x, a.centre.y - b.centre.y);
      tightest = Math.min(tightest, between - a.reach - b.reach);
    });
  });
  return tightest;
}

// A repository with two worktrees, plus the place nothing is known about.
const REPOSITORY_WITH_WORKTREES = registryOf([
  UNKNOWN,
  aPlace("repo"),
  aPlace("tree-a", "repo"),
  aPlace("tree-b", "repo"),
]);

describe("grouping work into places", () => {
  it("draws one body per place, holding that place's work", () => {
    const drawn = scene(
      [...work("repo", 2), ...work("tree-a", 1), ...work("unknown", 1)],
      REPOSITORY_WITH_WORKTREES,
      1,
    );

    expect(drawn.bodies.map((b) => b.place.id)).toEqual([
      "unknown",
      "repo",
      "tree-a",
      "tree-b",
    ]);
    expect(itemsOf(bodyOf(drawn, "repo"))).toEqual(["repo-0", "repo-1"]);
    expect(itemsOf(bodyOf(drawn, "tree-a"))).toEqual(["tree-a-0"]);
    expect(itemsOf(bodyOf(drawn, "tree-b"))).toEqual([]);
  });

  it("keeps the centre a neutral mark that holds no work", () => {
    const drawn = scene(work("repo", 3), REPOSITORY_WITH_WORKTREES, 1);

    expect(drawn.centre).toEqual({ x: 600, y: 400 });
    expect(drawn.bodies.every((b) => b.centre.x !== 600 || b.centre.y !== 400)).toBe(
      true,
    );
  });

  it("draws the places apart from each other", () => {
    const drawn = scene(
      [...work("repo", 4), ...work("tree-a", 3), ...work("tree-b", 3)],
      REPOSITORY_WITH_WORKTREES,
      1,
    );

    expect(tightestClearance(drawn)).toBeGreaterThan(0);
  });

  it("draws the place work runs in when nothing is known about it", () => {
    const drawn = scene(work("unknown", 2), REPOSITORY_WITH_WORKTREES, 1);

    expect(itemsOf(bodyOf(drawn, "unknown"))).toEqual(["unknown-0", "unknown-1"]);
  });

  it("gives work a home even when its place was not published", () => {
    const drawn = scene(work("never-published", 1), registryOf([UNKNOWN]), 1);

    expect(itemsOf(bodyOf(drawn, "unknown"))).toEqual(["never-published-0"]);
  });
});

describe("folding by depth", () => {
  it("folds a deeper place into its nearest visible ancestor, and says how many", () => {
    const drawn = scene(
      [...work("repo", 1), ...work("tree-a", 2), ...work("tree-b", 1)],
      REPOSITORY_WITH_WORKTREES,
      0,
    );

    expect(drawn.bodies.map((b) => b.place.id)).toEqual(["unknown", "repo"]);
    expect(itemsOf(bodyOf(drawn, "repo"))).toEqual([
      "repo-0",
      "tree-a-0",
      "tree-a-1",
      "tree-b-0",
    ]);
    expect(bodyOf(drawn, "repo").foldedCount).toBe(2);
  });

  it("unfolds the deeper places as the visible depth grows", () => {
    const items = [...work("repo", 1), ...work("tree-a", 1)];

    const folded = scene(items, REPOSITORY_WITH_WORKTREES, 0);
    const unfolded = scene(items, REPOSITORY_WITH_WORKTREES, 1);

    expect(bodyOf(folded, "repo").foldedCount).toBe(2);
    expect(bodyOf(unfolded, "repo").foldedCount).toBe(0);
    expect(unfolded.bodies).toHaveLength(4);
  });

  it("never folds the place nothing is known about", () => {
    const drawn = scene(work("unknown", 1), REPOSITORY_WITH_WORKTREES, 0);

    const unknown = bodyOf(drawn, "unknown");
    expect(unknown.foldedCount).toBe(0);
    expect(itemsOf(unknown)).toEqual(["unknown-0"]);
  });

  it("folds every place into its base ancestor when the depth is zero", () => {
    const deep = registryOf([
      UNKNOWN,
      aPlace("repo"),
      aPlace("tree", "repo"),
      aPlace("nested", "tree"),
    ]);

    const drawn = scene(work("nested", 2), deep, 0);

    expect(drawn.bodies.map((b) => b.place.id)).toEqual(["unknown", "repo"]);
    expect(bodyOf(drawn, "repo").foldedCount).toBe(2);
    expect(itemsOf(bodyOf(drawn, "repo"))).toEqual(["nested-0", "nested-1"]);
  });
});

describe("legibility", () => {
  it("folds a crowded place's children rather than overlapping them", () => {
    const many = registryOf([
      UNKNOWN,
      aPlace("repo"),
      ...Array.from({ length: 14 }, (_, i) => aPlace(`tree-${i}`, "repo")),
    ]);
    const items = Array.from({ length: 14 }, (_, i) => work(`tree-${i}`, 6)).flat();

    const drawn = scene(items, many, 1);

    const repo = bodyOf(drawn, "repo");
    expect(repo.crowded).toBe(true);
    expect(repo.foldedCount).toBe(14);
    expect(drawn.bodies.map((b) => b.place.id)).toEqual(["unknown", "repo"]);
    expect(repo.slots).toHaveLength(14 * 6);
  });

  it("leaves an uncrowded place's children beside it", () => {
    const drawn = scene(
      [...work("tree-a", 2), ...work("tree-b", 2)],
      REPOSITORY_WITH_WORKTREES,
      1,
    );

    expect(bodyOf(drawn, "repo").crowded).toBe(false);
    expect(drawn.bodies.map((b) => b.place.id)).toContain("tree-a");
  });

  it("keeps the satellites of a place apart, however much work it holds", () => {
    const drawn = scene(work("repo", 40), REPOSITORY_WITH_WORKTREES, 0);

    const slots = bodyOf(drawn, "repo").slots;
    let closest = Infinity;
    slots.forEach((a, i) => {
      slots.slice(i + 1).forEach((b) => {
        closest = Math.min(closest, Math.hypot(a.x - b.x, a.y - b.y));
      });
    });
    expect(closest).toBeGreaterThanOrEqual(90);
  });
});

describe("a place that holds one place and no work of its own", () => {
  it("draws once, as the place that holds the work", () => {
    const registry = registryOf([UNKNOWN, aPlace("repo"), aPlace("tree", "repo")]);

    const drawn = scene(work("tree", 2), registry, 1);

    expect(drawn.bodies.map((b) => b.place.id)).toEqual(["unknown", "tree"]);
    expect(bodyOf(drawn, "tree").standingInFor.map((p) => p.id)).toEqual(["repo"]);
  });

  it("draws both once the holder has work of its own", () => {
    const registry = registryOf([UNKNOWN, aPlace("repo"), aPlace("tree", "repo")]);

    const drawn = scene([...work("repo", 1), ...work("tree", 1)], registry, 1);

    expect(drawn.bodies.map((b) => b.place.id)).toEqual(["unknown", "repo", "tree"]);
    expect(bodyOf(drawn, "tree").standingInFor).toEqual([]);
  });

  it("draws both once the holder has a second place under it", () => {
    const drawn = scene(work("tree-a", 1), REPOSITORY_WITH_WORKTREES, 1);

    expect(drawn.bodies.map((b) => b.place.id)).toEqual([
      "unknown",
      "repo",
      "tree-a",
      "tree-b",
    ]);
  });
});

describe("the stability of the picture", () => {
  it("draws the same snapshot the same way twice", () => {
    const items = [...work("repo", 3), ...work("tree-a", 2)];

    expect(scene(items, REPOSITORY_WITH_WORKTREES, 1)).toEqual(
      scene(items, REPOSITORY_WITH_WORKTREES, 1),
    );
  });

  it("keeps the places in the same order when the work changes", () => {
    const before = scene(work("tree-a", 1), REPOSITORY_WITH_WORKTREES, 1);
    const after = scene(
      [...work("tree-a", 5), ...work("tree-b", 3), ...work("unknown", 2)],
      REPOSITORY_WITH_WORKTREES,
      1,
    );

    expect(after.bodies.map((b) => b.place.id)).toEqual(
      before.bodies.map((b) => b.place.id),
    );
  });

  it("does not move the work of one place when another place's work changes", () => {
    const registry = registryOf([UNKNOWN, aPlace("repo")]);
    const before = scene([...work("repo", 3), ...work("unknown", 1)], registry, 1);
    const after = scene([...work("repo", 3), ...work("unknown", 4)], registry, 1);

    expect(itemsOf(bodyOf(after, "repo"))).toEqual(itemsOf(bodyOf(before, "repo")));
  });
});

describe("the view that shows the whole picture", () => {
  const size = { width: 1200, height: 800 };
  const busy = [
    ...work("repo", 4),
    ...work("tree-a", 3, "done"),
    ...work("tree-b", 5),
  ];

  /** Where a scene point lands on the canvas under a view. */
  function onScreen(
    view: { x: number; y: number; k: number },
    point: { x: number; y: number },
  ): { x: number; y: number } {
    return { x: view.x + point.x * view.k, y: view.y + point.y * view.k };
  }

  it("brings every place inside the canvas", () => {
    const view = fittedView(busy, REPOSITORY_WITH_WORKTREES, size, false);
    const drawn = layoutScene(busy, REPOSITORY_WITH_WORKTREES, {
      ...size,
      visibleDepth: visibleDepthFor(view, false),
    });

    for (const body of drawn.bodies) {
      const at = onScreen(view, body.centre);
      const reach = body.reach * view.k;
      expect(at.x - reach).toBeGreaterThanOrEqual(0);
      expect(at.y - reach).toBeGreaterThanOrEqual(0);
      expect(at.x + reach).toBeLessThanOrEqual(size.width);
      expect(at.y + reach).toBeLessThanOrEqual(size.height);
    }
  });

  it("centres the full picture when Unknown is the only place", () => {
    const items = work("unknown", 1);
    const places = registryOf([UNKNOWN]);
    const view = fittedView(items, places, size, false);
    const drawn = layoutScene(items, places, {
      ...size,
      visibleDepth: visibleDepthFor(view, false),
    });
    const unknown = bodyOf(drawn, "unknown");
    const left = Math.min(
      drawn.centre.x - CENTRE_RADIUS,
      unknown.centre.x - unknown.reach,
    );
    const right = Math.max(
      drawn.centre.x + CENTRE_RADIUS,
      unknown.centre.x + unknown.reach,
    );
    const top = Math.min(
      drawn.centre.y - CENTRE_RADIUS,
      unknown.centre.y - unknown.reach,
    );
    const bottom = Math.max(
      drawn.centre.y + CENTRE_RADIUS,
      unknown.centre.y + unknown.reach,
    );
    const topLeft = onScreen(view, { x: left, y: top });
    const bottomRight = onScreen(view, { x: right, y: bottom });

    expect((topLeft.x + bottomRight.x) / 2).toBeCloseTo(size.width / 2);
    expect((topLeft.y + bottomRight.y) / 2).toBeCloseTo(size.height / 2);
  });

  it("shows the folding the zoom it needs agrees with", () => {
    const view = fittedView(busy, REPOSITORY_WITH_WORKTREES, size, false);
    const depth = visibleDepthFor(view, false);

    const scene = layoutScene(busy, REPOSITORY_WITH_WORKTREES, {
      ...size,
      visibleDepth: depth,
    });
    const bounds = boundsOf(scene);
    expect((bounds.right - bounds.left) * view.k).toBeLessThanOrEqual(size.width);
    expect((bounds.bottom - bounds.top) * view.k).toBeLessThanOrEqual(size.height);
  });

  it("fits the folded picture when the operator collapsed everything", () => {
    const view = fittedView(busy, REPOSITORY_WITH_WORKTREES, size, true);

    const scene = layoutScene(busy, REPOSITORY_WITH_WORKTREES, {
      ...size,
      visibleDepth: 0,
    });
    const bounds = boundsOf(scene);
    expect((bounds.right - bounds.left) * view.k).toBeLessThanOrEqual(size.width);
    expect((bounds.bottom - bounds.top) * view.k).toBeLessThanOrEqual(size.height);
    expect(view.k).toBeGreaterThanOrEqual(MIN_ZOOM);
  });

  it("never magnifies a picture that already fits", () => {
    const view = fittedView(work("repo", 1), REPOSITORY_WITH_WORKTREES, size, false);

    expect(view.k).toBeLessThanOrEqual(1);
  });

  it("leaves the view alone while the canvas has no size", () => {
    const view = fittedView(busy, REPOSITORY_WITH_WORKTREES, {
      width: 0,
      height: 0,
    }, false);

    expect(view).toEqual({ x: 0, y: 0, k: 1 });
  });
});
