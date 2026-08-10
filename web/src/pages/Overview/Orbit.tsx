import {
  useCallback,
  useEffect,
  useLayoutEffect,
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
import { Icon } from "../../components/Icon";
import { layoutOrbit } from "./layout";
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

const MIN_ZOOM = 0.4;
const MAX_ZOOM = 3;
const ZOOM_STEP = 1.2; // per button press
// Wheel zoom sensitivity: multiplier per unit of wheel delta. Small = gentle.
const WHEEL_ZOOM_SENSITIVITY = 0.0015;

// Shared angular velocity for every satellite: one gentle revolution roughly
// every ~4 minutes. All rings turn at the same rate, so the constellation
// holds its shape while it drifts.
const ANGULAR_VELOCITY = (2 * Math.PI) / 240_000; // radians per millisecond

interface View {
  // Screen offset of the content origin, and the scale factor.
  x: number;
  y: number;
  k: number;
}

const IDENTITY: View = { x: 0, y: 0, k: 1 };

function clampZoom(k: number): number {
  return Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, k));
}

interface Props {
  items: WorkItem[];
  selected: WorkItemId | null;
  onSelect: (item: WorkItem) => void;
  onClear: () => void;
}

export function Orbit({ items, selected, onSelect, onClear }: Props): ReactNode {
  const hostRef = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ w: 900, h: 640 });
  const [view, setView] = useState<View>(IDENTITY);
  // Shared rotation offset (radians) added to every satellite's base angle.
  const [rotation, setRotation] = useState(0);
  // Whether the orbital animation is running. Starts paused when the user
  // prefers reduced motion; otherwise plays.
  const prefersReducedMotion =
    typeof window !== "undefined" &&
    (window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false);
  const [playing, setPlaying] = useState(!prefersReducedMotion);

  // Drag state kept in a ref so pointer moves don't re-render until we setView.
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

  // Gentle shared orbital motion, driven by a single rAF loop off the real
  // clock (so it stays correct regardless of frame rate). Rotation accumulates,
  // so pausing holds the current position and resuming continues from it.
  useEffect(() => {
    if (!playing) return;

    let raf = 0;
    let last: number | null = null;
    const tick = (now: number) => {
      if (last !== null) {
        const delta = (now - last) * ANGULAR_VELOCITY;
        setRotation((r) => (r + delta) % (2 * Math.PI));
      }
      last = now;
      raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [playing]);

  // Clearing selection must also drop DOM focus from the satellite, otherwise
  // its keyboard focus ring lingers after there is nothing selected.
  const clearSelection = useCallback(() => {
    const active = document.activeElement;
    if (active instanceof HTMLElement || active instanceof SVGElement) {
      if (active.closest?.(".satellite")) active.blur();
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

  const layout = layoutOrbit(items, { width: size.w, height: size.h });

  // Zoom around a focal point (screen coords) so content under the cursor
  // stays put — the standard pan/zoom feel.
  const zoomAround = useCallback((factor: number, fx: number, fy: number) => {
    setView((v) => {
      const k = clampZoom(v.k * factor);
      const actual = k / v.k; // clamped ratio
      return {
        k,
        x: fx - (fx - v.x) * actual,
        y: fy - (fy - v.y) * actual,
      };
    });
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
      // Gentle exponential zoom proportional to wheel delta. The low
      // sensitivity keeps a normal scroll notch from jumping too far; the
      // clamp stops a fast flick (or a line/page-mode wheel) from overshooting.
      const delta = Math.max(-40, Math.min(40, e.deltaY));
      const factor = Math.exp(-delta * WHEEL_ZOOM_SENSITIVITY);
      zoomAround(factor, fx, fy);
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
      hostRef.current?.setPointerCapture(e.pointerId);
    }
    if (!d.moved) return;
    setView({ k: d.origin.k, x: d.origin.x + dx, y: d.origin.y + dy });
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
      if (target.closest(".satellite") || target.closest("button")) return;
      clearSelection();
    },
    [clearSelection],
  );

  const isDragging = drag.current?.moved ?? false;
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
        cursor: isDragging ? "grabbing" : "grab",
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
          {/* Orbit rings */}
          {layout.orbits.map((r, i) => (
            <circle
              key={i}
              cx={layout.center.x}
              cy={layout.center.y}
              r={r}
              fill="none"
              stroke="var(--orbit-ring)"
              strokeDasharray="4 7"
              strokeWidth={1.5}
              opacity={0.9}
            />
          ))}

          {/* Central body (the "planet") */}
          <g>
            <circle
              cx={layout.center.x}
              cy={layout.center.y}
              r={80}
              fill="var(--color-surface-2)"
              stroke="var(--color-border-strong)"
              strokeWidth={1.5}
            />
            <circle
              cx={layout.center.x}
              cy={layout.center.y}
              r={80}
              fill="url(#planet-swirl)"
              opacity={0.35}
            />
            <defs>
              <radialGradient id="planet-swirl" cx="35%" cy="30%" r="80%">
                <stop offset="0%" stopColor="var(--planet-glow)" stopOpacity="0.9" />
                <stop offset="100%" stopColor="var(--color-bg)" stopOpacity="0" />
              </radialGradient>
            </defs>
          </g>

          {/* Satellites */}
          {layout.slots.map(({ item, angle, radius }) => {
            const isSelected = sameItem(item, selected);
            // Same shared rotation for every satellite, so the whole
            // constellation turns as one rigid body.
            const a = angle + rotation;
            const x = layout.center.x + Math.cos(a) * radius;
            const y = layout.center.y + Math.sin(a) * radius;
            return (
              <g
                key={itemKey(item)}
                className="satellite"
                data-selected={isSelected || undefined}
                transform={`translate(${x}, ${y})`}
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
