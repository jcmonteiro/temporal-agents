import type { ReactNode } from "react";
import {
  STATUS_LABEL,
  STATUS_ORDER,
  type WorkItem,
  type WorkItemStatus,
} from "../domain/work-item";
import type { Place } from "../domain/place";
import { upNextKey, type UpNextEntry } from "../domain/up-next";
import { temporalUrlFor } from "../config/temporal";
import { Icon } from "./Icon";
import { StatusDot } from "./StatusDot";

/**
 * What the rail details: one work item, or one place with the work it holds.
 * A place counts the work of every place under it, so a repository answers
 * "how much is running here" for its worktrees too.
 */
export type Selected =
  | { type: "item"; item: WorkItem }
  | {
      type: "place";
      place: Place;
      counts: Record<WorkItemStatus, number>;
      children: Place[];
    };

interface Props {
  selected: Selected | null;
  upNext: UpNextEntry[];
  counts: Record<WorkItemStatus, number>;
  // The statuses currently shown. Empty means "show all".
  visibleStatuses: Set<WorkItemStatus>;
  onToggleStatus: (status: WorkItemStatus) => void;
  onClearFilter: () => void;
}

function Section({
  title,
  action,
  children,
}: {
  title: string;
  action?: ReactNode;
  children: ReactNode;
}): ReactNode {
  return (
    <section
      style={{
        border: "1px solid var(--color-border)",
        borderRadius: "var(--radius-md)",
        background: "var(--color-surface)",
        padding: "var(--space-4)",
        display: "flex",
        flexDirection: "column",
        gap: "var(--space-3)",
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: 8,
        }}
      >
        <div
          style={{
            fontSize: "var(--font-size-xs)",
            letterSpacing: "0.08em",
            color: "var(--color-text-subtle)",
            textTransform: "uppercase",
          }}
        >
          {title}
        </div>
        {action}
      </div>
      {children}
    </section>
  );
}

function StatusLegend({
  counts,
  visibleStatuses,
  onToggleStatus,
}: {
  counts: Record<WorkItemStatus, number>;
  visibleStatuses: Set<WorkItemStatus>;
  onToggleStatus: (status: WorkItemStatus) => void;
}): ReactNode {
  // Empty filter = show all, so every row reads as active.
  const filtering = visibleStatuses.size > 0;
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
      {STATUS_ORDER.map((s: WorkItemStatus) => {
        const on = !filtering || visibleStatuses.has(s);
        const count = counts[s];
        return (
          <button
            key={s}
            type="button"
            aria-pressed={filtering ? visibleStatuses.has(s) : undefined}
            onClick={() => onToggleStatus(s)}
            title={`Filter by ${STATUS_LABEL[s]}`}
            style={{
              display: "flex",
              alignItems: "center",
              gap: 8,
              padding: "4px 6px",
              borderRadius: "var(--radius-sm)",
              textAlign: "left",
              opacity: on ? 1 : 0.4,
            }}
          >
            <StatusDot status={s} filled={filtering && visibleStatuses.has(s)} />
            <span
              style={{
                flex: 1,
                fontSize: "var(--font-size-sm)",
                color: "var(--color-text-muted)",
              }}
            >
              {STATUS_LABEL[s]}
            </span>
            <span
              style={{
                fontSize: "var(--font-size-xs)",
                color: "var(--color-text-subtle)",
                fontVariantNumeric: "tabular-nums",
              }}
            >
              {count}
            </span>
          </button>
        );
      })}
    </div>
  );
}

function SelectedItem({ item }: { item: WorkItem }): ReactNode {
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
      {/* The fleet detail view does not exist yet, so this control stays
          disabled: an enabled button that does nothing is worse than one that
          says so. Enable it (and add the handler) with the detail view. */}
      <button
        style={{
          marginTop: 4,
          padding: "8px 12px",
          borderRadius: "var(--radius-sm)",
          border: "1px solid var(--color-border-strong)",
          color: "var(--color-text)",
          background: "var(--color-surface-2)",
          fontSize: "var(--font-size-sm)",
          cursor: "not-allowed",
          opacity: 0.5,
        }}
        disabled
        title="Fleet details are not available yet"
      >
        View Details
      </button>
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

function SelectedEmpty(): ReactNode {
  return (
    <div style={{ fontSize: "var(--font-size-sm)", color: "var(--color-text-subtle)" }}>
      Select a satellite or a place to see its details.
    </div>
  );
}

/** What is known about a place: what it is, what runs there, and what is under it. */
function SelectedPlace({
  place,
  counts,
  placesHere,
}: {
  place: Place;
  counts: Record<WorkItemStatus, number>;
  placesHere: Place[];
}): ReactNode {
  const total = STATUS_ORDER.reduce((sum, status) => sum + counts[status], 0);
  const held = STATUS_ORDER.filter((status) => counts[status] > 0);
  return (
    <>
      <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
        <div style={{ fontSize: "var(--font-size-md)", fontWeight: 600 }}>
          {place.label}
        </div>
        <div
          style={{
            fontSize: "var(--font-size-sm)",
            color: "var(--color-text-muted)",
          }}
        >
          {place.directory ?? place.ref ?? "Where this work ran was not recorded"}
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
        <span>Work:</span>
        <span style={{ color: "var(--color-text)" }}>{total}</span>
        {held.map((status) => (
          <span key={status} style={{ display: "contents" }}>
            <span style={{ display: "flex", alignItems: "center", gap: 6 }}>
              <StatusDot status={status} size={8} />
              {STATUS_LABEL[status]}:
            </span>
            <span style={{ color: "var(--color-text)" }}>{counts[status]}</span>
          </span>
        ))}
      </div>
      <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
        <div
          style={{
            fontSize: "var(--font-size-xs)",
            letterSpacing: "0.08em",
            color: "var(--color-text-subtle)",
            textTransform: "uppercase",
          }}
        >
          Places here
        </div>
        {placesHere.length === 0 ? (
          <span
            style={{
              fontSize: "var(--font-size-sm)",
              color: "var(--color-text-subtle)",
            }}
          >
            None
          </span>
        ) : (
          placesHere.map((child) => (
            <span key={child.id} style={{ fontSize: "var(--font-size-sm)" }}>
              {child.label}
            </span>
          ))
        )}
      </div>
    </>
  );
}

function UpNext({ items }: { items: UpNextEntry[] }): ReactNode {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--space-3)" }}>
      {items.map((it) => (
        <div key={upNextKey(it)} style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <div
            style={{
              width: 30,
              height: 30,
              borderRadius: "50%",
              border: "1px solid var(--color-border-strong)",
              display: "grid",
              placeItems: "center",
              color: "var(--color-text-muted)",
            }}
          >
            <Icon name={it.icon} size={14} />
          </div>
          <div style={{ display: "flex", flexDirection: "column" }}>
            <span style={{ fontSize: "var(--font-size-sm)" }}>{it.label}</span>
            <span
              style={{
                fontSize: "var(--font-size-xs)",
                color: "var(--color-text-subtle)",
                display: "flex",
                alignItems: "center",
                gap: 6,
              }}
            >
              <StatusDot status={it.status} size={8} />
              {STATUS_LABEL[it.status]}
            </span>
          </div>
        </div>
      ))}
    </div>
  );
}

export function RightRail({
  selected,
  upNext,
  counts,
  visibleStatuses,
  onToggleStatus,
  onClearFilter,
}: Props): ReactNode {
  const filtering = visibleStatuses.size > 0;
  return (
    <aside
      style={{
        width: 280,
        padding: "var(--space-4)",
        borderLeft: "1px solid var(--color-border)",
        display: "flex",
        flexDirection: "column",
        gap: "var(--space-4)",
        overflowY: "auto",
      }}
    >
      <Section
        title="Filter by state"
        action={
          filtering ? (
            <button
              type="button"
              onClick={onClearFilter}
              style={{
                fontSize: "var(--font-size-xs)",
                color: "var(--color-accent)",
              }}
            >
              Clear
            </button>
          ) : undefined
        }
      >
        <StatusLegend
          counts={counts}
          visibleStatuses={visibleStatuses}
          onToggleStatus={onToggleStatus}
        />
      </Section>
      <Section title="Selected">
        {selected === null && <SelectedEmpty />}
        {selected?.type === "item" && <SelectedItem item={selected.item} />}
        {selected?.type === "place" && (
          <SelectedPlace
            place={selected.place}
            counts={selected.counts}
            placesHere={selected.children}
          />
        )}
      </Section>
      <Section title="Up Next">
        <UpNext items={upNext} />
      </Section>
    </aside>
  );
}
