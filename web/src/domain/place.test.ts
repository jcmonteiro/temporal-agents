import { describe, expect, it } from "vitest";
import { registryOf, workIn, type Place } from "./place";

function aPlace(overrides: Partial<Place> = {}): Place {
  return {
    id: "dir-1",
    kind: "directory",
    label: "checkout",
    parentId: null,
    ...overrides,
  };
}

describe("the place registry", () => {
  it("keeps the places in the order the server published them", () => {
    const registry = registryOf([
      aPlace({ id: "repo", label: "checkout" }),
      aPlace({ id: "tree", label: "feature", parentId: "repo" }),
    ]);

    expect(registry.places.map((p) => p.id)).toEqual(["repo", "tree"]);
  });

  it("answers which places hang under a place", () => {
    const registry = registryOf([
      aPlace({ id: "repo" }),
      aPlace({ id: "tree-a", parentId: "repo" }),
      aPlace({ id: "tree-b", parentId: "repo" }),
    ]);

    expect(registry.childrenOf("repo").map((p) => p.id)).toEqual([
      "tree-a",
      "tree-b",
    ]);
    expect(registry.childrenOf("tree-a")).toEqual([]);
  });

  it("reports the places that hang under nothing", () => {
    const registry = registryOf([
      aPlace({ id: "unknown", kind: "unknown", label: "Unknown" }),
      aPlace({ id: "repo" }),
      aPlace({ id: "tree", parentId: "repo" }),
    ]);

    expect(registry.roots.map((p) => p.id)).toEqual(["unknown", "repo"]);
  });

  it("measures how deep a place sits", () => {
    const registry = registryOf([
      aPlace({ id: "repo" }),
      aPlace({ id: "tree", parentId: "repo" }),
      aPlace({ id: "nested", parentId: "tree" }),
    ]);

    expect(registry.depthOf("repo")).toBe(0);
    expect(registry.depthOf("tree")).toBe(1);
    expect(registry.depthOf("nested")).toBe(2);
  });

  it("names the chain from the topmost ancestor down to a place", () => {
    const registry = registryOf([
      aPlace({ id: "repo" }),
      aPlace({ id: "tree", parentId: "repo" }),
      aPlace({ id: "nested", parentId: "tree" }),
    ]);

    expect(registry.ancestryOf("nested").map((p) => p.id)).toEqual([
      "repo",
      "tree",
      "nested",
    ]);
  });

  it("collapses the same place published by two responses onto one entry", () => {
    const registry = registryOf([
      aPlace({ id: "repo" }),
      aPlace({ id: "tree", parentId: "repo" }),
      aPlace({ id: "repo" }),
    ]);

    expect(registry.places.map((p) => p.id)).toEqual(["repo", "tree"]);
    expect(registry.childrenOf("repo")).toHaveLength(1);
  });

  it("treats a place whose parent was not published as a root", () => {
    const registry = registryOf([aPlace({ id: "tree", parentId: "gone" })]);

    expect(registry.roots.map((p) => p.id)).toEqual(["tree"]);
    expect(registry.depthOf("tree")).toBe(0);
  });

  it("stops an ancestry that closes on itself", () => {
    const registry = registryOf([
      aPlace({ id: "a", parentId: "b" }),
      aPlace({ id: "b", parentId: "a" }),
    ]);

    expect(registry.ancestryOf("a").map((p) => p.id)).toEqual(["b", "a"]);
  });
});

describe("the work of a place", () => {
  const registry = registryOf([
    aPlace({ id: "unknown", kind: "unknown", label: "Unknown" }),
    aPlace({ id: "repo" }),
    aPlace({ id: "tree", parentId: "repo" }),
  ]);
  const work = [
    { id: "a", placeId: "repo" },
    { id: "b", placeId: "tree" },
    { id: "c", placeId: "unknown" },
  ];

  it("counts what runs in the place itself and in every place under it", () => {
    expect(workIn(work, registry, "repo").map((w) => w.id)).toEqual(["a", "b"]);
  });

  it("holds no work of a place beside it", () => {
    expect(workIn(work, registry, "tree").map((w) => w.id)).toEqual(["b"]);
  });

  it("is empty for a place nothing runs in", () => {
    expect(workIn(work, registry, "gone")).toEqual([]);
  });
});
