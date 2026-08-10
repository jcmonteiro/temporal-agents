/**
 * The orbit canvas view: pure pan/zoom arithmetic, no DOM.
 *
 * A view maps a content point to a screen point as `screen = offset + k *
 * content`, which is exactly the SVG `translate(x, y) scale(k)` the canvas
 * renders.
 */
export interface View {
  // Screen offset of the content origin, and the scale factor.
  x: number;
  y: number;
  k: number;
}

export const IDENTITY: View = { x: 0, y: 0, k: 1 };

export const MIN_ZOOM = 0.4;
export const MAX_ZOOM = 3;
export const ZOOM_STEP = 1.2; // per button press

// Zoom levels at which one more level of places earns a body of its own. The
// visible depth is derived from the view, never stored: zooming out folds the
// deeper places into their parents, and zooming back in unfolds them.
const DEPTH_ZOOM_THRESHOLDS = [0.75, 1.5];

/** The deepest a place may sit and still draw as its own body. */
export const MAX_VISIBLE_DEPTH = DEPTH_ZOOM_THRESHOLDS.length;

/**
 * How deep the scene draws places, given the view and the collapse-all option.
 * Collapse-all forces every place into its base ancestor, which is depth 0.
 */
export function visibleDepthFor(view: View, collapseAll: boolean): number {
  if (collapseAll) return 0;
  return DEPTH_ZOOM_THRESHOLDS.filter((threshold) => view.k >= threshold).length;
}

// Wheel zoom sensitivity: multiplier per unit of wheel delta. Small = gentle.
const WHEEL_ZOOM_SENSITIVITY = 0.0015;

// Bound on one wheel event, so a fast flick (or a line/page-mode wheel) cannot
// overshoot.
const MAX_WHEEL_DELTA = 40;

export function clampZoom(k: number): number {
  return Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, k));
}

/**
 * Scales the view around a focal point in screen coordinates, so the content
 * under that point stays where it is — the standard pan/zoom feel.
 */
export function zoomedAround(
  view: View,
  factor: number,
  focusX: number,
  focusY: number,
): View {
  const k = clampZoom(view.k * factor);
  const actual = k / view.k; // clamped ratio
  return {
    k,
    x: focusX - (focusX - view.x) * actual,
    y: focusY - (focusY - view.y) * actual,
  };
}

/** Moves the content by a screen distance, at an unchanged scale. */
export function panned(origin: View, dx: number, dy: number): View {
  return { k: origin.k, x: origin.x + dx, y: origin.y + dy };
}

/** Gentle exponential zoom factor for one wheel event. */
export function wheelZoomFactor(deltaY: number): number {
  const delta = Math.max(-MAX_WHEEL_DELTA, Math.min(MAX_WHEEL_DELTA, deltaY));
  return Math.exp(-delta * WHEEL_ZOOM_SENSITIVITY);
}
