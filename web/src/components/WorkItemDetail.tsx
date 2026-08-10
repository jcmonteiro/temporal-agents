import type { ReactNode } from "react";
import { STATUS_LABEL, type WorkItem } from "../domain/work-item";
import { temporalUrlFor } from "../config/temporal";
import { addressOf, type Route } from "../platform/route";
import { Icon } from "./Icon";
import { StatusDot } from "./StatusDot";

/**
 * What is known about one piece of work. The rail and the place page show the
 * same thing, so they show it through the same component.
 */
export function WorkItemDetail({ item }: { item: WorkItem }): ReactNode {
  return (
    <>
      <div
        style={{
          width: 56,
          height: 56,
          borderRadius: "50%",
          border: "1.5px solid var(--color-border-strong)",
          display: "grid",
          placeItems: "center",
          color: "var(--color-text)",
        }}
      >
        <Icon name={item.icon} size={26} />
      </div>
      <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
        <div style={{ fontSize: "var(--font-size-md)", fontWeight: 600 }}>{item.label}</div>
        <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
          <StatusDot status={item.status} />
          <span style={{ fontSize: "var(--font-size-sm)", color: "var(--color-text-muted)" }}>
            {STATUS_LABEL[item.status]}
          </span>
        </div>
      </div>
      <div
        style={{
          display: "grid",
          gridTemplateColumns: "auto 1fr",
          columnGap: 12,
          rowGap: 6,
          fontSize: "var(--font-size-sm)",
          color: "var(--color-text-muted)",
        }}
      >
        <span>Kind:</span>
        <span style={{ color: "var(--color-text)", textTransform: "capitalize" }}>
          {item.kind}
        </span>
        {item.progress && item.progress.total > 0 && (
          <>
            <span>Progress:</span>
            <span style={{ color: "var(--color-text)" }}>
              {item.progress.done}/{item.progress.total}
              {" · "}
              {Math.round(item.progress.fraction * 100)}%
            </span>
          </>
        )}
        {item.runType && (
          <>
            <span>Type:</span>
            <span style={{ color: "var(--color-text)" }}>{item.runType}</span>
          </>
        )}
        {typeof item.iterations === "number" && item.iterations > 1 && (
          <>
            <span>Iterations:</span>
            <span style={{ color: "var(--color-text)" }}>{item.iterations}</span>
          </>
        )}
        {item.spec && (
          <>
            <span>Schedule:</span>
            <span style={{ color: "var(--color-text)" }}>{item.spec}</span>
          </>
        )}
      </div>
      <ItsOwnPage item={item} />
      <a
        href={temporalUrlFor(item) ?? undefined}
        target="_blank"
        rel="noopener noreferrer"
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          gap: 6,
          padding: "8px 12px",
          borderRadius: "var(--radius-sm)",
          border: "1px solid var(--color-border)",
          color: "var(--color-text-muted)",
          background: "transparent",
          fontSize: "var(--font-size-sm)",
          textDecoration: "none",
        }}
      >
        View in Temporal
        <svg
          width="13"
          height="13"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
        >
          <path d="M14 4h6v6" />
          <path d="M20 4l-9 9" />
          <path d="M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5" />
        </svg>
      </a>
    </>
  );
}

/**
 * The way from a piece of work to the page about it.
 *
 * A run and a fleet each have a page; a schedule does not, so for a schedule
 * the control stays disabled and says why. An enabled control that leads
 * nowhere is worse than one that admits it.
 */
function ItsOwnPage({ item }: { item: WorkItem }): ReactNode {
  const style = {
    marginTop: 4,
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    padding: "8px 12px",
    borderRadius: "var(--radius-sm)",
    border: "1px solid var(--color-border-strong)",
    color: "var(--color-text)",
    background: "var(--color-surface-2)",
    fontSize: "var(--font-size-sm)",
    textDecoration: "none",
  };
  const route: Route | null =
    item.kind === "run"
      ? { name: "run", runId: item.id }
      : item.kind === "fleet"
        ? { name: "fleet", fleetId: item.id }
        : null;
  if (route === null) {
    return (
      <button
        type="button"
        disabled
        title="A schedule has no page of its own yet"
        style={{ ...style, cursor: "not-allowed", opacity: 0.5 }}
      >
        View details
      </button>
    );
  }
  return (
    <a href={addressOf(route)} style={style}>
      View details
    </a>
  );
}
