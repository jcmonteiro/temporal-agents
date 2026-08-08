import { useEffect, useMemo, useState, type ReactNode } from "react";
import { loadOverview } from "../../clients/work-items";
import {
  STATUS_ORDER,
  type WorkItem,
  type WorkItemStatus,
} from "../../domain/work-item";
import { RightRail } from "../../components/RightRail";
import { Orbit } from "./Orbit";

type StatusCounts = Record<WorkItemStatus, number>;

function countByStatus(items: WorkItem[]): StatusCounts {
  const counts = Object.fromEntries(
    STATUS_ORDER.map((s) => [s, 0]),
  ) as StatusCounts;
  for (const item of items) counts[item.status] += 1;
  return counts;
}

type State =
  | { kind: "loading" }
  | { kind: "error"; message: string }
  | { kind: "ready"; items: WorkItem[]; upNext: WorkItem[] };

export function OverviewPage(): ReactNode {
  const [state, setState] = useState<State>({ kind: "loading" });
  const [selectedId, setSelectedId] = useState<string | null>(null);
  // Statuses currently shown. Empty set means "show all" — the natural
  // starting point, and what clearing the filter returns to.
  const [visibleStatuses, setVisibleStatuses] = useState<Set<WorkItemStatus>>(
    new Set(),
  );

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const result = await loadOverview();
      if (cancelled) return;
      if (result.ok) {
        setState({ kind: "ready", items: result.value.items, upNext: result.value.upNext });
      } else {
        setState({ kind: "error", message: result.error.message });
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const items = state.kind === "ready" ? state.items : [];
  const upNext = state.kind === "ready" ? state.upNext : [];

  const counts = useMemo(() => countByStatus(items), [items]);
  const filtered = useMemo(
    () =>
      visibleStatuses.size === 0
        ? items
        : items.filter((i) => visibleStatuses.has(i.status)),
    [items, visibleStatuses],
  );

  // A selected item that is filtered out is no longer selectable.
  const selected = filtered.find((i) => i.id === selectedId) ?? null;

  const toggleStatus = (status: WorkItemStatus): void => {
    setVisibleStatuses((prev) => {
      const next = new Set(prev);
      if (next.has(status)) next.delete(status);
      else next.add(status);
      return next;
    });
  };
  const clearFilter = (): void => setVisibleStatuses(new Set());

  return (
    <div style={{ display: "flex", flex: 1, minHeight: 0 }}>
      <main
        style={{
          flex: 1,
          position: "relative",
          minWidth: 0,
          display: "flex",
          flexDirection: "column",
        }}
      >
        <div style={{ padding: "var(--space-5) var(--space-5) 0" }}>
          <h1
            style={{
              margin: 0,
              fontSize: "var(--font-size-xl)",
              fontWeight: 600,
            }}
          >
            Overview
          </h1>
          <p
            style={{
              margin: "4px 0 0",
              color: "var(--color-text-muted)",
              fontSize: "var(--font-size-sm)",
            }}
          >
            Here's what's orbiting your work today.
          </p>
        </div>
        <div style={{ flex: 1, minHeight: 0, position: "relative" }}>
          <Orbit
            items={filtered}
            selectedId={selectedId}
            onSelect={(it) => setSelectedId(it.id)}
            onClear={() => setSelectedId(null)}
          />
          {state.kind !== "ready" && <StatusOverlay state={state} />}
        </div>
      </main>
      <RightRail
        selected={selected}
        upNext={upNext}
        counts={counts}
        visibleStatuses={visibleStatuses}
        onToggleStatus={toggleStatus}
        onClearFilter={clearFilter}
      />
    </div>
  );
}

function StatusOverlay({ state }: { state: Exclude<State, { kind: "ready" }> }): ReactNode {
  const message =
    state.kind === "loading"
      ? "Loading orbit…"
      : `Could not reach the Agent Hub API: ${state.message}`;
  return (
    <div
      role="status"
      style={{
        position: "absolute",
        inset: 0,
        display: "grid",
        placeItems: "center",
        pointerEvents: "none",
        color: state.kind === "error" ? "var(--status-failed)" : "var(--color-text-muted)",
        fontSize: "var(--font-size-sm)",
      }}
    >
      <div
        style={{
          padding: "10px 16px",
          borderRadius: "var(--radius-md)",
          background: "rgba(11,13,18,0.7)",
          border: "1px solid var(--color-border)",
        }}
      >
        {message}
      </div>
    </div>
  );
}
