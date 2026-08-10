import { useEffect, useMemo, useState, type ReactNode } from "react";
import { loadOverview, type OverviewData } from "../../clients/work-items";
import {
  sameItem,
  STATUS_ORDER,
  type WorkItem,
  type WorkItemId,
  type WorkItemStatus,
} from "../../domain/work-item";
import { registryOf, workIn, type PlaceRegistry } from "../../domain/place";
import { RightRail, type Selected } from "../../components/RightRail";
import { Orbit } from "./Orbit";

type StatusCounts = Record<WorkItemStatus, number>;

function countByStatus(items: WorkItem[]): StatusCounts {
  const counts = Object.fromEntries(
    STATUS_ORDER.map((s) => [s, 0]),
  ) as StatusCounts;
  for (const item of items) counts[item.status] += 1;
  return counts;
}

// The places of the first render, before any response arrived.
const NO_PLACES: PlaceRegistry = registryOf([]);

// The overview is live data: poll the API on this cadence so running work and
// schedules stay current without a page reload.
const REFRESH_INTERVAL_MS = 5_000;

// `data` holds the last successful snapshot; `error` the last failure. Both can
// be set at once, so a failed refresh reports the problem without discarding
// the constellation the operator is looking at.
interface State {
  data: OverviewData | null;
  error: string | null;
}

export function OverviewPage(): ReactNode {
  const [state, setState] = useState<State>({ data: null, error: null });
  // Identity, not a bare id: a fleet and a run may share an id.
  const [selectedId, setSelectedId] = useState<WorkItemId | null>(null);
  // The place the operator picked, by its server-issued id. An item and a place
  // are never selected at once: picking one drops the other.
  const [selectedPlaceId, setSelectedPlaceId] = useState<string | null>(null);
  // Statuses currently shown. Empty set means "show all" — the natural
  // starting point, and what clearing the filter returns to.
  const [visibleStatuses, setVisibleStatuses] = useState<Set<WorkItemStatus>>(
    new Set(),
  );

  useEffect(() => {
    let cancelled = false;
    const refresh = async (): Promise<void> => {
      const result = await loadOverview();
      if (cancelled) return;
      setState((prev) =>
        result.ok
          ? { data: result.value, error: null }
          : { data: prev.data, error: result.error.message },
      );
    };
    void refresh();
    const timer = setInterval(() => void refresh(), REFRESH_INTERVAL_MS);
    // Stop polling and ignore an in-flight response once unmounted.
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, []);

  const items = state.data?.items ?? [];
  const upNext = state.data?.upNext ?? [];
  const places = state.data?.places ?? NO_PLACES;

  const counts = useMemo(() => countByStatus(items), [items]);
  const filtered = useMemo(
    () =>
      visibleStatuses.size === 0
        ? items
        : items.filter((i) => visibleStatuses.has(i.status)),
    [items, visibleStatuses],
  );

  // A selected item that is filtered out is no longer selectable.
  const selectedItem = filtered.find((i) => sameItem(i, selectedId)) ?? null;
  const selectedPlace =
    selectedPlaceId === null ? undefined : places.byId(selectedPlaceId);

  // The rail details whichever of the two the operator picked last. A place
  // reports the work of every place under it, so a repository answers for its
  // worktrees too.
  let selected: Selected | null = null;
  if (selectedPlace) {
    selected = {
      type: "place",
      place: selectedPlace,
      counts: countByStatus(workIn(filtered, places, selectedPlace.id)),
      children: places.childrenOf(selectedPlace.id),
    };
  } else if (selectedItem) {
    selected = { type: "item", item: selectedItem };
  }

  const selectItem = (item: WorkItem): void => {
    setSelectedPlaceId(null);
    setSelectedId({ kind: item.kind, id: item.id });
  };
  const selectPlace = (placeId: string): void => {
    setSelectedId(null);
    setSelectedPlaceId(placeId);
  };
  const clearSelection = (): void => {
    setSelectedId(null);
    setSelectedPlaceId(null);
  };

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
            places={places}
            selected={selectedId}
            selectedPlaceId={selectedPlaceId}
            onSelect={selectItem}
            onSelectPlace={(place) => selectPlace(place.id)}
            onClear={clearSelection}
          />
          <StatusOverlay state={state} />
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

// Shows the first load and any refresh failure. Nothing is rendered once data
// is present and the latest refresh succeeded.
function StatusOverlay({ state }: { state: State }): ReactNode {
  const isError = state.error !== null;
  if (!isError && state.data !== null) return null;
  const message = isError
    ? `Could not reach the Agent Hub API: ${state.error}`
    : "Loading orbit…";
  return (
    <div
      role="status"
      style={{
        position: "absolute",
        inset: 0,
        display: "grid",
        placeItems: "center",
        pointerEvents: "none",
        color: isError ? "var(--status-failed)" : "var(--color-text-muted)",
        fontSize: "var(--font-size-sm)",
      }}
    >
      <div
        style={{
          padding: "10px 16px",
          borderRadius: "var(--radius-md)",
          background: "var(--color-surface-overlay)",
          border: "1px solid var(--color-border)",
        }}
      >
        {message}
      </div>
    </div>
  );
}
