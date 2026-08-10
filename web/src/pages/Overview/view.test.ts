import { describe, expect, it } from "vitest";
import {
  clampZoom,
  IDENTITY,
  MAX_ZOOM,
  MIN_ZOOM,
  panned,
  wheelZoomFactor,
  zoomedAround,
  type View,
} from "./view";

/**
 * Where a content point lands on screen under a view. This is the contract of
 * a View — the same mapping the canvas renders as translate() scale().
 */
function toScreen(view: View, contentX: number, contentY: number): [number, number] {
  return [view.x + contentX * view.k, view.y + contentY * view.k];
}

/** Which content point a screen point currently sits on. */
function toContent(view: View, screenX: number, screenY: number): [number, number] {
  return [(screenX - view.x) / view.k, (screenY - view.y) / view.k];
}

describe("zooming around a focal point", () => {
  it("keeps the content under the cursor in place", () => {
    const view: View = { x: 40, y: -15, k: 1.3 };
    const [contentX, contentY] = toContent(view, 700, 300);

    const zoomed = zoomedAround(view, 2, 700, 300);

    const [screenX, screenY] = toScreen(zoomed, contentX, contentY);
    expect(screenX).toBeCloseTo(700, 6);
    expect(screenY).toBeCloseTo(300, 6);
  });

  it("keeps the content in place when the zoom hits its limit", () => {
    const atMaximum: View = { x: 40, y: -15, k: MAX_ZOOM };
    const [contentX, contentY] = toContent(atMaximum, 120, 480);

    const zoomed = zoomedAround(atMaximum, 4, 120, 480);

    const [screenX, screenY] = toScreen(zoomed, contentX, contentY);
    expect(screenX).toBeCloseTo(120, 6);
    expect(screenY).toBeCloseTo(480, 6);
  });

  it("magnifies the content", () => {
    expect(zoomedAround(IDENTITY, 2, 0, 0).k).toBe(2);
  });

  it("never magnifies past the maximum", () => {
    expect(zoomedAround(IDENTITY, 100, 300, 300).k).toBe(MAX_ZOOM);
  });

  it("never shrinks past the minimum", () => {
    expect(zoomedAround(IDENTITY, 0.01, 300, 300).k).toBe(MIN_ZOOM);
  });
});

describe("the zoom limits", () => {
  it("leaves a scale inside the limits alone", () => {
    expect(clampZoom(1.5)).toBe(1.5);
  });

  it("holds a scale at each limit", () => {
    expect(clampZoom(0.01)).toBe(MIN_ZOOM);
    expect(clampZoom(50)).toBe(MAX_ZOOM);
  });
});

describe("the wheel", () => {
  it("magnifies when the wheel turns up and shrinks when it turns down", () => {
    expect(wheelZoomFactor(-10)).toBeGreaterThan(1);
    expect(wheelZoomFactor(10)).toBeLessThan(1);
  });

  it("does nothing without movement", () => {
    expect(wheelZoomFactor(0)).toBe(1);
  });

  it("keeps one notch gentle", () => {
    // A single notch must stay well below a doubling, or the canvas jumps.
    expect(wheelZoomFactor(-100)).toBeLessThan(1.2);
  });

  it("treats a fast flick like the largest ordinary turn", () => {
    // A line-mode or page-mode wheel reports huge deltas; they must not
    // overshoot.
    expect(wheelZoomFactor(-5000)).toBe(wheelZoomFactor(-40));
    expect(wheelZoomFactor(5000)).toBe(wheelZoomFactor(40));
  });
});

describe("panning", () => {
  it("moves the content by the distance the pointer travelled", () => {
    const origin: View = { x: 10, y: 20, k: 1.5 };

    expect(panned(origin, 60, -30)).toEqual({ x: 70, y: -10, k: 1.5 });
  });

  it("leaves the scale alone", () => {
    expect(panned({ x: 0, y: 0, k: 2.5 }, 100, 100).k).toBe(2.5);
  });
});

describe("the starting view", () => {
  it("shows the content unmoved and unscaled", () => {
    expect(toScreen(IDENTITY, 450, 320)).toEqual([450, 320]);
  });
});
