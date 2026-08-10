import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type MouseEvent as ReactMouseEvent,
  type PointerEvent as ReactPointerEvent,
  type ReactNode,
  type WheelEvent as ReactWheelEvent,
} from "react";
import {
  itemKey,
  sameItem,
  STATUS_LABEL,
  type WorkItem,
  type WorkItemId,
  type WorkItemStatus,
} from "../../domain/work-item";
import type { Place, PlaceRegistry } from "../../domain/place";
import { Icon } from "../../components/Icon";
import { onEachFrame, prefersReducedMotion } from "../../platform/motion";
import { advanced, positionAt } from "./orbit-motion";
import { CENTRE_RADIUS, layoutScene, type PlaceBody } from "./scene";
import {
  IDENTITY,
  panned,
  visibleDepthFor,
  wheelZoomFactor,
  zoomedAround,
  ZOOM_STEP,
  type View,
} from "./view";
import { Starfield } from "./Starfield";

const STATUS_VAR: Record<WorkItemStatus, string> = {
  todo: "var(--status-todo)",
  "in-progress": "var(--status-in-progress)",
  paused: "var(--status-paused)",
  "waiting-input": "var(--status-waiting-input)",
  waiting: "var(--status-waiting)",
  done: "var(--status-done)",
  failed: "var(--status-failed)",
};

/**
 * How a place announces itself: what it is called, how much work it holds, and
 * how many places it absorbed. A fold is never silent.
 */
function placeName(body: PlaceBody): string {
  const work = `${body.slots.length} ${body.slots.length === 1 ? "item" : "items"}`;
  if (body.foldedCount === 0) return `${body.place.label}, place, ${work}`;
  const folded = `${body.foldedCount} ${
    body.foldedCount === 1 ? "place" : "places"
  } folded in`;
  const why = body.crowded ? " to stay legible" : "";
  return `${body.place.label}, place, ${work}, ${folded}${why}`;
}

interface Props {
  items: WorkItem[];
  places: PlaceRegistry;
  selected: WorkItemId | null;
  selectedPlaceId: string | null;
  onSelect: (item: WorkItem) => void;
  onSelectPlace: (place: Place) => void;
  onClear: () => void;
}

export function Orbit({
  items,
  places,
  selected,
  selectedPlaceId,
  onSelect,
  onSelectPlace,
  onClear,
}: Props): ReactNode {
  const hostRef = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ w: 900, h: 640 });
  const [view, setView] = useState<View>(IDENTITY);
  // Whether every place is folded into its base ancestor. It is view state, not
  // data: the scene derives the folding from it.
  const [collapseAll, setCollapseAll] = useState(false);
  // Whether the orbital animation is running. Starts paused when the operator
  // prefers reduced motion; otherwise plays.
  const [playing, setPlaying] = useState(() => !prefersReducedMotion());

  // Whether a pan is in progress. This drives the cursor, so it is state: the
  // ref below alone would leave the cursor stuck on "grabbing" after pointer-up,
  // because releasing it schedules no render.
  const [dragging, setDragging] = useState(false);

  // Drag geometry kept in a ref so pointer moves don't re-render until we setView.
  const drag = useRef<{
    pointerId: number;
    startX: number;
    startY: number;
    origin: View;
    moved: boolean;
  } | null>(null);

  useLayoutEffect(() => {
    if (!hostRef.current) return;
    const el = hostRef.current;
    const observer = new ResizeObserver(() => {
      setSize({ w: el.clientWidth, h: el.clientHeight });
    });
    observer.observe(el);
    setSize({ w: el.clientWidth, h: el.clientHeight });
    return () => observer.disconnect();
  }, []);

  // Clearing selection must also drop DOM focus from the body it was on,
  // otherwise its keyboard focus ring lingers after there is nothing selected.
  const clearSelection = useCallback(() => {
    const active = document.activeElement;
    if (active instanceof HTMLElement || active instanceof SVGElement) {
      if (active.closest?.(".satellite, .place")) active.blur();
    }
    onClear();
  }, [onClear]);

  // Escape clears the current selection.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") clearSelection();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [clearSelection]);

  // How deep the places are drawn follows from the view: zooming out folds the
  // deeper ones into their parents, and collapse-all forces the base ancestor.
  const visibleDepth = visibleDepthFor(view, collapseAll);

  const scene = useMemo(
    () =>
      layoutScene(items, places, {
        width: size.w,
        height: size.h,
        visibleDepth,
      }),
    [items, places, size.w, size.h, visibleDepth],
  );

  // How far the constellation has turned, in radians. One angle drives every
  // satellite: a satellite only travels along its ring, so its icon and label
  // can never tilt, however long the canvas turns.
  const rotation = useRef(0);

  // The satellite groups, by item key. The motion writes their positions
  // straight to the DOM, so turning costs no React render per frame.
  const satellites = useRef(new Map<string, SVGGElement>());

  const placeSatellites = useCallback(() => {
    for (const body of scene.bodies) {
      for (const slot of body.slots) {
        const el = satellites.current.get(itemKey(slot.item));
        if (!el) continue;
        // A satellite turns about the body it belongs to, not about the canvas.
        const at = positionAt(slot, body.centre, rotation.current);
        el.setAttribute("transform", `translate(${at.x}, ${at.y})`);
      }
    }
  }, [scene]);

  // The orbital motion. Stopping simply drops the frame loop, which holds the
  // constellation at its current angle.
  useEffect(() => {
    if (!playing) return;
    let previous: number | null = null;
    return onEachFrame((now) => {
      if (previous !== null) {
        rotation.current = advanced(rotation.current, now - previous);
        placeSatellites();
      }
      previous = now;
    });
  }, [playing, placeSatellites]);

  // Zoom around a focal point (screen coords) so content under the cursor
  // stays put — the standard pan/zoom feel.
  const zoomAround = useCallback((factor: number, fx: number, fy: number) => {
    setView((v) => zoomedAround(v, factor, fx, fy));
  }, []);

  const zoomAtCenter = useCallback(
    (factor: number) => zoomAround(factor, size.w / 2, size.h / 2),
    [zoomAround, size.w, size.h],
  );

  const onWheel = useCallback(
    (e: ReactWheelEvent<HTMLDivElement>) => {
      e.preventDefault();
      const rect = hostRef.current?.getBoundingClientRect();
      const fx = rect ? e.clientX - rect.left : size.w / 2;
      const fy = rect ? e.clientY - rect.top : size.h / 2;
      zoomAround(wheelZoomFactor(e.deltaY), fx, fy);
    },
    [zoomAround, size.w, size.h],
  );

  const onPointerDown = useCallback(
    (e: ReactPointerEvent<HTMLDivElement>) => {
      // Only start tracking on the primary button / touch. We do NOT capture the
      // pointer here: capturing on down would route the click to the host div and
      // starve satellites of it. Capture happens only once a real drag begins.
      if (e.button !== 0) return;
      drag.current = {
        pointerId: e.pointerId,
        startX: e.clientX,
        startY: e.clientY,
        origin: view,
        moved: false,
      };
    },
    [view],
  );

  const onPointerMove = useCallback((e: ReactPointerEvent<HTMLDivElement>) => {
    const d = drag.current;
    if (!d || d.pointerId !== e.pointerId) return;
    const dx = e.clientX - d.startX;
    const dy = e.clientY - d.startY;
    if (!d.moved && Math.hypot(dx, dy) > 3) {
      // A genuine drag has started: now capture the pointer so panning keeps
      // tracking even if the cursor leaves the canvas.
      d.moved = true;
      setDragging(true);
      hostRef.current?.setPointerCapture(e.pointerId);
    }
    if (!d.moved) return;
    setView(panned(d.origin, dx, dy));
  }, []);

  // Set true when a pan ends, so the click event that immediately follows a
  // drag is ignored. Read and reset by the satellite's onClick.
  const suppressClick = useRef(false);

  const endDrag = useCallback((e: ReactPointerEvent<HTMLDivElement>) => {
    const d = drag.current;
    if (d?.moved) {
      suppressClick.current = true;
      if (hostRef.current?.hasPointerCapture(e.pointerId)) {
        hostRef.current.releasePointerCapture(e.pointerId);
      }
    }
    drag.current = null;
    setDragging(false);
  }, []);

  // A click on empty canvas clears the selection. Clicks that land on a
  // satellite or a control button are ignored here (the satellite selects; the
  // button acts), as is the click that terminates a pan gesture.
  const onBackgroundClick = useCallback(
    (e: ReactMouseEvent<HTMLDivElement>) => {
      if (suppressClick.current) {
        suppressClick.current = false;
        return;
      }
      const target = e.target as Element;
      if (target.closest(".satellite") || target.closest(".place")) return;
      if (target.closest("button")) return;
      clearSelection();
    },
    [clearSelection],
  );

  const percent = Math.round(view.k * 100);

  return (
    <div
      ref={hostRef}
      onWheel={onWheel}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={endDrag}
      onPointerCancel={endDrag}
      onClick={onBackgroundClick}
      style={{
        position: "relative",
        width: "100%",
        height: "100%",
        overflow: "hidden",
        touchAction: "none",
        cursor: dragging ? "grabbing" : "grab",
        background:
          "radial-gradient(1200px 700px at 50% 50%, var(--canvas-glow) 0%, var(--color-bg) 70%)",
      }}
    >
      {/* role="group", not "img": an image's descendants are presentational,
          which would hide the focusable satellites from assistive technology. */}
      <svg
        width={size.w}
        height={size.h}
        role="group"
        aria-label="Orbit overview of active agent work"
      >
        {/* Starfield stays fixed to the viewport (doesn't pan/zoom). */}
        <Starfield width={size.w} height={size.h} />

        {/* Everything else lives under the view transform. */}
        <g transform={`translate(${view.x}, ${view.y}) scale(${view.k})`}>
          <defs>
            <radialGradient id="planet-swirl" cx="35%" cy="30%" r="80%">
              <stop offset="0%" stopColor="var(--planet-glow)" stopOpacity="0.9" />
              <stop offset="100%" stopColor="var(--color-bg)" stopOpacity="0" />
            </radialGradient>
          </defs>

          {/* The centre is a neutral mark: no work ever sits on it. */}
          <circle
            cx={scene.centre.x}
            cy={scene.centre.y}
            r={CENTRE_RADIUS}
            fill="none"
            stroke="var(--color-border)"
            strokeDasharray="2 8"
            strokeWidth={1.5}
            aria-hidden="true"
          />

          {/* One body per place, with that place's work around it. Each body
              turns its own satellites: a satellite is only carried along its
              ring, so icons and labels stay upright. */}
          {scene.bodies.map((body) => {
            const isSelectedPlace = body.place.id === selectedPlaceId;
            return (
              <g key={body.place.id}>
                {body.orbits.map((r, i) => (
                  <circle
                    key={i}
                    cx={body.centre.x}
                    cy={body.centre.y}
                    r={r}
                    fill="none"
                    stroke="var(--orbit-ring)"
                    strokeDasharray="4 7"
                    strokeWidth={1.5}
                    opacity={0.9}
                  />
                ))}

                <g
                  className="place"
                  data-selected={isSelectedPlace || undefined}
                  transform={`translate(${body.centre.x}, ${body.centre.y})`}
                  style={{ cursor: "pointer" }}
                  tabIndex={0}
                  role="button"
                  aria-label={placeName(body)}
                  onClick={() => {
                    if (suppressClick.current) {
                      suppressClick.current = false;
                      return;
                    }
                    onSelectPlace(body.place);
                  }}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      onSelectPlace(body.place);
                    }
                  }}
                >
                  <circle
                    className="place-focus"
                    r={body.radius + 8}
                    fill="none"
                    stroke="var(--color-accent)"
                    strokeWidth={2}
                  />
                  <circle
                    r={body.radius}
                    fill="var(--color-surface-2)"
                    stroke={
                      isSelectedPlace
                        ? "var(--color-accent)"
                        : "var(--color-border-strong)"
                    }
                    strokeWidth={isSelectedPlace ? 2.5 : 1.5}
                  />
                  <circle
                    r={body.radius}
                    fill="url(#planet-swirl)"
                    opacity={0.35}
                    style={{ pointerEvents: "none" }}
                  />
                  {body.foldedCount > 0 && (
                    <g
                      transform={`translate(${body.radius * 0.75}, ${-body.radius * 0.75})`}
                      style={{ pointerEvents: "none" }}
                      aria-hidden="true"
                    >
                      <circle
                        r={14}
                        fill="var(--color-surface)"
                        stroke="var(--color-border-strong)"
                        strokeWidth={1.25}
                      />
                      <text
                        y={4}
                        textAnchor="middle"
                        fill="var(--color-text-muted)"
                        style={{ fontFamily: "var(--font-sans)", fontSize: 11 }}
                      >
                        {`+${body.foldedCount}`}
                      </text>
                    </g>
                  )}
                  <text
                    y={body.radius + 20}
                    textAnchor="middle"
                    fill="var(--color-text)"
                    style={{
                      fontFamily: "var(--font-sans)",
                      fontSize: 13,
                      pointerEvents: "none",
                    }}
                  >
                    {body.place.label}
                  </text>
                </g>

                {body.slots.map((slot) => {
                  const { item } = slot;
                  const isSelected = sameItem(item, selected);
                  const at = positionAt(slot, body.centre, rotation.current);
                  return (
                    <g
                      key={itemKey(item)}
                      ref={(el) => {
                        const key = itemKey(item);
                        if (el) satellites.current.set(key, el);
                        else satellites.current.delete(key);
                      }}
                      className="satellite"
                      data-selected={isSelected || undefined}
                      transform={`translate(${at.x}, ${at.y})`}
                      style={{ cursor: "pointer" }}
                      onClick={() => {
                        // Suppress the click that ends a pan gesture.
                        if (suppressClick.current) {
                          suppressClick.current = false;
                          return;
                        }
                        onSelect(item);
                      }}
                      tabIndex={0}
                      role="button"
                      aria-label={`${item.label}, ${STATUS_LABEL[item.status]}`}
                      onKeyDown={(e) => {
                        if (e.key === "Enter" || e.key === " ") {
                          e.preventDefault();
                          onSelect(item);
                        }
                      }}
                    >
                      {/* Circular focus ring, shown only for keyboard focus. */}
                      <circle
                        className="satellite-focus"
                        r={35}
                        fill="none"
                        stroke="var(--color-accent)"
                        strokeWidth={2}
                      />
                      <circle
                        r={30}
                        fill="var(--color-surface)"
                        stroke={
                          isSelected
                            ? "var(--color-accent)"
                            : "var(--color-border-strong)"
                        }
                        strokeWidth={isSelected ? 2 : 1.25}
                      />
                      <g
                        transform="translate(-12, -12)"
                        color="var(--color-text)"
                        style={{ pointerEvents: "none" }}
                      >
                        <Icon name={item.icon} size={24} />
                      </g>
                      <g transform="translate(0, 52)" style={{ pointerEvents: "none" }}>
                        <circle
                          cx={-6}
                          cy={-4}
                          r={4}
                          fill="none"
                          stroke={STATUS_VAR[item.status]}
                          strokeWidth={1.5}
                        />
                        <text
                          x={2}
                          y={0}
                          fill="var(--color-text-muted)"
                          style={{
                            fontFamily: "var(--font-sans)",
                            fontSize: 11,
                          }}
                        >
                          {STATUS_LABEL[item.status]}
                        </text>
                      </g>
                    </g>
                  );
                })}
              </g>
            );
          })}
        </g>
      </svg>

      {/* Bottom-left canvas controls */}
      <div
        style={{
          position: "absolute",
          bottom: "var(--space-4)",
          left: "var(--space-4)",
          display: "flex",
          alignItems: "center",
          gap: "var(--space-2)",
        }}
      >
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 6,
            padding: "4px 10px",
            border: "1px solid var(--color-border)",
            borderRadius: 999,
            background: "var(--color-surface)",
            color: "var(--color-text-muted)",
            fontSize: "var(--font-size-sm)",
          }}
        >
          <button
            aria-label="Zoom out"
            style={{ color: "inherit" }}
            onClick={() => zoomAtCenter(1 / ZOOM_STEP)}
          >
            −
          </button>
          <span style={{ minWidth: 40, textAlign: "center" }}>{percent}%</span>
          <button
            aria-label="Zoom in"
            style={{ color: "inherit" }}
            onClick={() => zoomAtCenter(ZOOM_STEP)}
          >
            +
          </button>
        </div>
        <button
          aria-label={
            collapseAll ? "Show the places again" : "Collapse every place"
          }
          aria-pressed={collapseAll}
          title={
            collapseAll
              ? "Show the places again"
              : "Fold every place into the place that holds it"
          }
          onClick={() => setCollapseAll((on) => !on)}
          style={{
            height: 32,
            padding: "0 12px",
            borderRadius: 999,
            border: "1px solid var(--color-border)",
            background: collapseAll
              ? "var(--color-surface-2)"
              : "var(--color-surface)",
            color: "var(--color-text-muted)",
            fontSize: "var(--font-size-sm)",
          }}
        >
          {collapseAll ? "Places folded" : "Collapse all"}
        </button>
        <button
          aria-label="Recenter"
          onClick={() => setView(IDENTITY)}
          style={{
            width: 32,
            height: 32,
            display: "grid",
            placeItems: "center",
            borderRadius: "50%",
            border: "1px solid var(--color-border)",
            background: "var(--color-surface)",
            color: "var(--color-text-muted)",
          }}
        >
          ⊕
        </button>
        <button
          aria-label={playing ? "Pause orbit animation" : "Play orbit animation"}
          aria-pressed={playing}
          title={playing ? "Pause motion" : "Play motion"}
          onClick={() => setPlaying((p) => !p)}
          style={{
            width: 32,
            height: 32,
            display: "grid",
            placeItems: "center",
            borderRadius: "50%",
            border: "1px solid var(--color-border)",
            background: "var(--color-surface)",
            color: "var(--color-text-muted)",
          }}
        >
          {playing ? (
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
              <rect x="6" y="5" width="4" height="14" rx="1" />
              <rect x="14" y="5" width="4" height="14" rx="1" />
            </svg>
          ) : (
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
              <path d="M8 5.5v13l11-6.5z" />
            </svg>
          )}
        </button>
      </div>
    </div>
  );
}
