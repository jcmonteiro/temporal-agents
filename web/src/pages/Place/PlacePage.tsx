import { useEffect, useState, type ReactNode } from "react";
import { loadPlace, type PlaceView } from "../../clients/work-items";
import type { Place } from "../../domain/place";
import {
  itemKey,
  sameItem,
  STATUS_LABEL,
  STATUS_ORDER,
  type WorkItem,
  type WorkItemId,
  type WorkItemStatus,
} from "../../domain/work-item";
import { StatusDot } from "../../components/StatusDot";
import { Icon } from "../../components/Icon";
import { WorkItemDetail } from "../../components/WorkItemDetail";
import { addressOf, OVERVIEW } from "../../platform/route";
import { Launcher } from "./Launcher";
import { useSteering } from "../../platform/steering";
import { PromptConfiguration } from "../Settings/PromptConfiguration";

// The page shows live work, so it polls on the same cadence as the overview.
const REFRESH_INTERVAL_MS = 5_000;

// `view` holds the last successful read; `error` the last failure. Both can be
// set at once, so a failed refresh reports the problem without emptying the
// page the operator is reading.
interface State {
  view: PlaceView | null;
  error: string | null;
}

export function PlacePage({ placeId }: { placeId: string }): ReactNode {
  const steering = useSteering();
  const [state, setState] = useState<State>({ view: null, error: null });
  const [selectedId, setSelectedId] = useState<WorkItemId | null>(null);

  useEffect(() => {
    let cancelled = false;
    const refresh = async (): Promise<void> => {
      const result = await loadPlace(placeId);
      if (cancelled) return;
      setState((prev) =>
        result.ok
          ? { view: result.value, error: null }
          : { view: prev.view, error: result.error.message },
      );
    };
    void refresh();
    const timer = setInterval(() => void refresh(), REFRESH_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [placeId]);

  const { view, error } = state;
  const items = view?.found ? view.items : [];
  const selected = items.find((item) => sameItem(item, selectedId)) ?? null;

  return (
    <div style={{ display: "flex", flex: 1, minHeight: 0 }}>
      <main
        style={{
          flex: 1,
          minWidth: 0,
          overflowY: "auto",
          padding: "var(--space-5)",
          display: "flex",
          flexDirection: "column",
          gap: "var(--space-4)",
        }}
      >
        <a
          href={addressOf(OVERVIEW)}
          style={{
            color: "var(--color-text-muted)",
            fontSize: "var(--font-size-sm)",
            textDecoration: "none",
          }}
        >
          ← Back to the overview
        </a>

        {error !== null && view === null && (
          <p role="status" style={{ color: "var(--status-failed)" }}>
            Could not reach the Agent Hub API: {error}
          </p>
        )}
        {error !== null && view !== null && (
          <p role="status" style={{ color: "var(--status-failed)" }}>
            The last refresh failed: {error}
          </p>
        )}
        {error === null && view === null && (
          <p role="status" style={{ color: "var(--color-text-muted)" }}>
            Loading this place…
          </p>
        )}
        {view?.found === false && <NotFound placeId={placeId} />}
        {steering.sessions.filter((session) => session.locationId === placeId).map((session) => (
          <button key={session.id} type="button" onClick={() => steering.open(session.id)}>
            {session.itemId} needs guidance · waiting since {session.waitingSince ?? "an unknown time"}
          </button>
        ))}
        {view?.found === true && (
          <PlaceReport
            place={view.place}
            ancestry={view.ancestry}
            placesHere={view.children}
            items={view.items}
            selected={selectedId}
            onSelect={(item) => setSelectedId({ kind: item.kind, id: item.id })}
          />
        )}
      </main>
      <aside
        style={{
          width: 280,
          padding: "var(--space-4)",
          borderLeft: "1px solid var(--color-border)",
          display: "flex",
          flexDirection: "column",
          gap: "var(--space-3)",
          overflowY: "auto",
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
          Selected
        </div>
        {selected ? (
          <WorkItemDetail item={selected} />
        ) : (
          <span
            style={{
              fontSize: "var(--font-size-sm)",
              color: "var(--color-text-subtle)",
            }}
          >
            Select a piece of work to see its details.
          </span>
        )}
      </aside>
    </div>
  );
}

/**
 * A place the hub does not know. It is said plainly: an address that no longer
 * resolves must read as stale, not as a place with nothing in it.
 */
function NotFound({ placeId }: { placeId: string }): ReactNode {
  return (
    <div role="status" style={{ display: "flex", flexDirection: "column", gap: 8 }}>
      <h1 style={{ margin: 0, fontSize: "var(--font-size-xl)", fontWeight: 600 }}>
        No such place
      </h1>
      <p style={{ margin: 0, color: "var(--color-text-muted)" }}>
        The hub knows no place with the id {placeId}. It may belong to work that
        is no longer listed.
      </p>
    </div>
  );
}

function PlaceReport({
  place,
  ancestry,
  placesHere,
  items,
  selected,
  onSelect,
}: {
  place: Place;
  ancestry: Place[];
  placesHere: Place[];
  items: WorkItem[];
  selected: WorkItemId | null;
  onSelect: (item: WorkItem) => void;
}): ReactNode {
  // The chain above this place, topmost first. The place itself closes the
  // chain, so it is dropped here.
  const above = ancestry.slice(0, -1);
  return (
    <>
      <header style={{ display: "flex", flexDirection: "column", gap: 6 }}>
        {above.length > 0 && (
          <nav
            aria-label="Places above this one"
            style={{
              display: "flex",
              gap: 6,
              fontSize: "var(--font-size-sm)",
              color: "var(--color-text-muted)",
            }}
          >
            {above.map((ancestor) => (
              <span key={ancestor.id} style={{ display: "flex", gap: 6 }}>
                <a
                  href={addressOf({ name: "place", placeId: ancestor.id })}
                  style={{ color: "inherit" }}
                >
                  {ancestor.label}
                </a>
                <span aria-hidden="true">›</span>
              </span>
            ))}
          </nav>
        )}
        <h1 style={{ margin: 0, fontSize: "var(--font-size-xl)", fontWeight: 600 }}>
          {place.label}
        </h1>
        <p style={{ margin: 0, color: "var(--color-text-muted)" }}>
          {place.directory ?? place.ref ?? "Where this work ran was not recorded"}
        </p>
      </header>

      <section style={{ display: "flex", flexDirection: "column", gap: 6 }}>
        <SectionTitle>Places here</SectionTitle>
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
            <a
              key={child.id}
              href={addressOf({ name: "place", placeId: child.id })}
              style={{ fontSize: "var(--font-size-sm)", color: "var(--color-text)" }}
            >
              {child.label}
            </a>
          ))
        )}
      </section>

      <section style={{ display: "flex", flexDirection: "column", gap: 12 }}>
        <SectionTitle>Work here</SectionTitle>
        {items.length === 0 ? (
          <span
            style={{
              fontSize: "var(--font-size-sm)",
              color: "var(--color-text-subtle)",
            }}
          >
            Nothing runs here at the moment.
          </span>
        ) : (
          STATUS_ORDER.filter((status) =>
            items.some((item) => item.status === status),
          ).map((status) => (
            <WorkOfStatus
              key={status}
              status={status}
              items={items.filter((item) => item.status === status)}
              selected={selected}
              onSelect={onSelect}
            />
          ))
        )}
      </section>

      <Launcher place={place} />

      <PromptConfiguration fixedLocation={{ id: place.id, label: place.label }} />
    </>
  );
}

function SectionTitle({ children }: { children: ReactNode }): ReactNode {
  return (
    <div
      style={{
        fontSize: "var(--font-size-xs)",
        letterSpacing: "0.08em",
        color: "var(--color-text-subtle)",
        textTransform: "uppercase",
      }}
    >
      {children}
    </div>
  );
}

function WorkOfStatus({
  status,
  items,
  selected,
  onSelect,
}: {
  status: WorkItemStatus;
  items: WorkItem[];
  selected: WorkItemId | null;
  onSelect: (item: WorkItem) => void;
}): ReactNode {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 8,
          fontSize: "var(--font-size-sm)",
          color: "var(--color-text-muted)",
        }}
      >
        <StatusDot status={status} />
        {STATUS_LABEL[status]}
        <span style={{ color: "var(--color-text-subtle)" }}>{items.length}</span>
      </div>
      {items.map((item) => (
        <button
          key={itemKey(item)}
          type="button"
          aria-pressed={sameItem(item, selected)}
          onClick={() => onSelect(item)}
          style={{
            display: "flex",
            alignItems: "center",
            gap: 10,
            padding: "8px 10px",
            borderRadius: "var(--radius-sm)",
            border: sameItem(item, selected)
              ? "1px solid var(--color-accent)"
              : "1px solid var(--color-border)",
            background: "var(--color-surface)",
            color: "var(--color-text)",
            textAlign: "left",
            fontSize: "var(--font-size-sm)",
          }}
        >
          <Icon name={item.icon} size={16} />
          {item.label}
        </button>
      ))}
    </div>
  );
}
