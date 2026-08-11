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

// Room left between the content and the edge of the canvas when the view is
// fitted, so a body's label is never cut off.
const FIT_MARGIN = 48;

/** The rectangular boundary of the content in canvas coordinates. */
export interface Bounds {
  left: number;
  top: number;
  right: number;
  bottom: number;
}

/**
 * The view that brings the full content boundary into the canvas. Content that
 * already fits is never magnified. Both axes are centred independently, so an
 * asymmetric picture, such as one place beside the neutral mark, is centred as
 * a whole.
 *
 * A canvas that has not been measured yet cannot be fitted, so the view is left
 * as it is.
 */
export function fittedTo(bounds: Bounds, width: number, height: number): View {
  const contentWidth = bounds.right - bounds.left;
  const contentHeight = bounds.bottom - bounds.top;
  if (width <= 0 || height <= 0 || contentWidth <= 0 || contentHeight <= 0) {
    return IDENTITY;
  }
  const roomWidth = width - 2 * FIT_MARGIN;
  const roomHeight = height - 2 * FIT_MARGIN;
  if (roomWidth <= 0 || roomHeight <= 0) return IDENTITY;
  return centredOn(
    bounds,
    Math.min(1, roomWidth / contentWidth, roomHeight / contentHeight),
    width,
    height,
  );
}

/** The view at this zoom, with the given content boundary in the middle. */
export function centredOn(
  bounds: Bounds,
  zoom: number,
  width: number,
  height: number,
): View {
  const k = clampZoom(zoom);
  const contentX = (bounds.left + bounds.right) / 2;
  const contentY = (bounds.top + bounds.bottom) / 2;
  return { k, x: width / 2 - contentX * k, y: height / 2 - contentY * k };
}

/** The zoom at which one more level of places appears. */
export function zoomThatShowsDepth(depth: number): number {
  if (depth <= 0) return MIN_ZOOM;
  return DEPTH_ZOOM_THRESHOLDS[Math.min(depth, MAX_VISIBLE_DEPTH) - 1];
}

/** The least zoom that still counts as below a threshold. */
export const ZOOM_HAIR = 0.01;
