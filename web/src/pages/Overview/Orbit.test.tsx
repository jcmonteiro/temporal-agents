// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { fireEvent } from "@testing-library/dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { WorkItem } from "../../domain/work-item";
import { Orbit } from "./Orbit";

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

afterEach(() => {
  cleanup();
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
