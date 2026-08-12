import type { ReactNode } from "react";
import "./place-mark.css";

/** The shared visual identity for a place. */
export function PlaceMark(): ReactNode {
  return (
    <span className="place-mark" aria-hidden="true">
      <span />
    </span>
  );
}
