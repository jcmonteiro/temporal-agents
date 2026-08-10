/**
 * Browser motion preferences. Kept behind one function so components state
 * *what* they need ("does the operator want less motion?") instead of reaching
 * for `window.matchMedia` in the middle of a render.
 */
export function prefersReducedMotion(): boolean {
  if (typeof window === "undefined" || !window.matchMedia) return false;
  return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}
