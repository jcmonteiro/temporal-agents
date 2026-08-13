import { useEffect, useMemo, useState, type ReactNode } from "react";
import { dismissWorkItem } from "../../clients/dismissals";
import { loadOverview, type OverviewData } from "../../clients/work-items";
import {
  itemKey,
  sameItem,
  STATUS_ORDER,
  type WorkItem,
  type WorkItemId,
  type WorkItemStatus,
} from "../../domain/work-item";
import { registryOf, workIn, type PlaceRegistry } from "../../domain/place";
import { RightRail, type Selected } from "../../components/RightRail";
import { Orbit } from "./Orbit";
import { useSteering } from "../../platform/steering";
import { waitingFor } from "../../platform/waiting-time";

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
  dismissalError: string | null;
}

export function OverviewPage(): ReactNode {
  const steering = useSteering();
  const [state, setState] = useState<State>({
    data: null,
    error: null,
    dismissalError: null,
  });
  const [dismissing, setDismissing] = useState<Set<string>>(new Set());
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
          ? { ...prev, data: result.value, error: null }
          : { ...prev, error: result.error.message },
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

  const dismissItem = async (item: WorkItem): Promise<void> => {
    const key = itemKey(item);
    setDismissing((current) => new Set(current).add(key));
    setState((current) => ({ ...current, dismissalError: null }));
    const result = await dismissWorkItem(item);
    setDismissing((current) => {
      const next = new Set(current);
      next.delete(key);
      return next;
    });
    if (!result.ok) {
      setState((current) => ({
        ...current,
        dismissalError: `Could not dismiss ${item.label}: ${result.error.message}`,
      }));
      return;
    }
    setState((current) => ({
      ...current,
      data: current.data === null
        ? null
        : {
            ...current.data,
            items: current.data.items.filter((candidate) => !sameItem(candidate, item)),
          },
    }));
    if (sameItem(selectedId, item)) setSelectedId(null);
  };

  return (
    <div className="overview-page" style={{ display: "flex", flex: 1, minWidth: 0, minHeight: 0 }}>
      <main
        className="overview-page__canvas"
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
          {state.dismissalError !== null && (
            <div className="ui-feedback ui-feedback--error" role="alert" style={{ marginTop: 12 }}>
              {state.dismissalError}
            </div>
          )}
          {steering.sessions.length > 0 && (
            <section aria-label="Work waiting for guidance" style={{ marginTop: 12, padding: 12, border: "1px solid var(--status-waiting)", borderRadius: "var(--radius-sm)" }}>
              <strong>{steering.sessions.length} review round{steering.sessions.length === 1 ? "" : "s"} waiting for guidance</strong>
              {steering.sessions.map((session) => (
                <button key={session.id} type="button" onClick={() => steering.open(session.id)} style={{ display: "block", marginTop: 8 }}>
                  Guide {session.itemId} · {waitingFor(session.waitingSince)}
                </button>
              ))}
            </section>
          )}
        </div>
        <div style={{ flex: 1, minHeight: 0, position: "relative" }}>
          <Orbit
            items={filtered}
            places={places}
            selected={selectedId}
            selectedPlaceId={selectedPlaceId}
            onSelect={selectItem}
            onSelectPlace={(place) => selectPlace(place.id)}
            onDismiss={(item) => void dismissItem(item)}
            dismissing={dismissing}
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
