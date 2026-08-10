/**
 * Browser motion preferences. Kept behind one function so components state
 * *what* they need ("does the operator want less motion?") instead of reaching
 * for `window.matchMedia` in the middle of a render.
 */
export function prefersReducedMotion(): boolean {
  if (typeof window === "undefined" || !window.matchMedia) return false;
  return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

/**
 * Calls `frame` once per animation frame with that frame's timestamp, until the
 * returned stop function runs. The browser holds the frames back while the tab
 * is hidden, so motion never runs up a backlog off screen.
 */
export function onEachFrame(frame: (timestampMs: number) => void): () => void {
  let stopped = false;
  let handle = requestAnimationFrame(tick);

  function tick(timestampMs: number): void {
    frame(timestampMs);
    if (!stopped) handle = requestAnimationFrame(tick);
  }

  return () => {
    stopped = true;
    cancelAnimationFrame(handle);
  };
}
