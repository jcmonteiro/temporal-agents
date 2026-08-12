import { useEffect, useState, type ReactNode } from "react";
import { loadPlace, type PlaceView } from "../../clients/work-items";
import { Icon } from "../../components/Icon";
import { StatusDot } from "../../components/StatusDot";
import { WorkItemDetail } from "../../components/WorkItemDetail";
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
import { addressOf, OVERVIEW } from "../../platform/route";
import { useSteering } from "../../platform/steering";
import { waitingFor } from "../../platform/waiting-time";
import { PromptConfiguration } from "../Settings/PromptConfiguration";
import { Launcher } from "./Launcher";
import "./place.css";

// The page shows live work, so it polls on the same cadence as the overview.
const REFRESH_INTERVAL_MS = 5_000;

// The last successful view remains visible if a later refresh fails.
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
      setState((previous) =>
        result.ok
          ? { view: result.value, error: null }
          : { view: previous.view, error: result.error.message },
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
  const waitingSessions = steering.sessions.filter(
    (session) => session.locationId === placeId,
  );

  return (
    <main className="place-page">
      <div className="place-page__frame">
        <a className="place-page__back" href={addressOf(OVERVIEW)}>
          <span>←</span> Back to the overview
        </a>

        <div className="place-page__primary">
          {error !== null && view === null && (
            <div className="ui-feedback ui-feedback--error place-page__feedback" role="status">
              <strong>Place information unavailable</strong>
              <span>Could not reach the Agent Hub API: {error}</span>
            </div>
          )}
          {error !== null && view !== null && (
            <div className="ui-feedback ui-feedback--error place-page__feedback" role="status">
              <strong>Place refresh unavailable</strong>
              <span>The last refresh failed: {error}</span>
            </div>
          )}
          {error === null && view === null && <LoadingState />}
          {view?.found === false && <NotFound placeId={placeId} />}
          {waitingSessions.length > 0 && (
            <section className="place-guidance" aria-label="Work needing guidance">
              {waitingSessions.map((session) => (
                <button
                  className="place-guidance__button"
                  key={session.id}
                  type="button"
                  onClick={() => steering.open(session.id)}
                >
                  <span className="place-guidance__signal" aria-hidden="true" />
                  <span>
                    <strong>{session.itemId} needs guidance</strong>
                    <small>Waiting {waitingFor(session.waitingSince)}</small>
                  </span>
                  <span aria-hidden="true">→</span>
                </button>
              ))}
            </section>
          )}
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
        </div>

        <aside
          className="place-selection ui-surface"
          aria-labelledby="place-selection-heading"
        >
          <header className="place-selection__header">
            <div>
              <p className="ui-kicker">Inspection rail</p>
              <h2 id="place-selection-heading">Selected</h2>
            </div>
            {selected !== null && <span className="place-selection__active">Active</span>}
          </header>
          <div className="place-selection__body">
            {selected ? (
              <WorkItemDetail item={selected} />
            ) : (
              <div className="place-selection__empty">
                <span className="place-selection__orbit" aria-hidden="true" />
                <strong>No work selected</strong>
                <span>Select a piece of work to see its details.</span>
              </div>
            )}
          </div>
        </aside>
      </div>
    </main>
  );
}

function LoadingState(): ReactNode {
  return (
    <section className="place-state ui-surface" role="status">
      <span className="place-state__spinner" aria-hidden="true" />
      <div>
        <p className="ui-kicker">Place workspace</p>
        <h1>Loading this place…</h1>
        <p>Reading its hierarchy, live work, and governed instructions.</p>
      </div>
    </section>
  );
}

/** A stale place address must not look like an idle workspace. */
function NotFound({ placeId }: { placeId: string }): ReactNode {
  return (
    <section className="place-state ui-surface" role="status">
      <span className="place-state__mark" aria-hidden="true">?</span>
      <div>
        <p className="ui-kicker">Place workspace</p>
        <h1>No such place</h1>
        <p>
          The hub knows no place with the id <code>{placeId}</code>. It may belong
          to work that is no longer listed.
        </p>
      </div>
    </section>
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
  // The place itself closes the ancestry chain and is not a link above itself.
  const above = ancestry.slice(0, -1);
  const location = place.directory ?? place.ref ?? "Where this work ran was not recorded";

  return (
    <article className="place-report">
      <header className="place-hero ui-surface">
        <div className="place-hero__identity">
          <span className="place-hero__orbit" aria-hidden="true"><span /></span>
          <div>
            <p className="ui-eyebrow">Place workspace</p>
            {above.length > 0 && (
              <nav className="place-hero__ancestry" aria-label="Places above this one">
                {above.map((ancestor) => (
                  <span key={ancestor.id}>
                    <a href={addressOf({ name: "place", placeId: ancestor.id })}>
                      {ancestor.label}
                    </a>
                    <span aria-hidden="true">›</span>
                  </span>
                ))}
              </nav>
            )}
            <h1>{place.label}</h1>
            <p className="place-hero__location">{location}</p>
          </div>
        </div>
        <dl className="place-hero__summary">
          <div>
            <dt>Live work</dt>
            <dd>{items.length}</dd>
          </div>
          <div>
            <dt>Nested places</dt>
            <dd>{placesHere.length}</dd>
          </div>
          <div>
            <dt>Place kind</dt>
            <dd>{place.kind}</dd>
          </div>
        </dl>
      </header>

      <div className="place-report__overview">
        <section
          className="place-section place-section--hierarchy ui-surface"
          aria-labelledby="places-here-heading"
        >
          <header className="place-section__header">
            <div>
              <p className="ui-kicker">Hierarchy</p>
              <h2 id="places-here-heading">Places here</h2>
            </div>
            <span className="place-section__count">{placesHere.length}</span>
          </header>
          <div className="place-section__body">
            {placesHere.length === 0 ? (
              <div className="place-empty">
                <strong>No nested places</strong>
                <span>This is the last known place in this branch.</span>
              </div>
            ) : (
              <ul className="place-children">
                {placesHere.map((child) => (
                  <li key={child.id}>
                    <a
                      href={addressOf({ name: "place", placeId: child.id })}
                      aria-label={child.label}
                    >
                      <span className="place-children__mark" aria-hidden="true" />
                      <span>
                        <strong>{child.label}</strong>
                        <small>{child.directory ?? child.ref ?? child.kind}</small>
                      </span>
                      <span aria-hidden="true">→</span>
                    </a>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </section>

        <section
          className="place-section place-section--work ui-surface"
          aria-labelledby="work-here-heading"
        >
          <header className="place-section__header">
            <div>
              <p className="ui-kicker">Current activity</p>
              <h2 id="work-here-heading">Work here</h2>
            </div>
            <span className="place-section__count">{items.length}</span>
          </header>
          <div className="place-section__body place-work">
            {items.length === 0 ? (
              <div className="place-empty">
                <strong>This place is idle</strong>
                <span>Nothing runs here at the moment.</span>
              </div>
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
          </div>
        </section>
      </div>

      <Launcher place={place} />
      <PromptConfiguration fixedLocation={{ id: place.id, label: place.label }} />
    </article>
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
    <section className="place-work__group" aria-label={`${STATUS_LABEL[status]} work`}>
      <header>
        <span className={`ui-status ui-status--${status}`}>
          <StatusDot status={status} />
          {STATUS_LABEL[status]}
        </span>
        <span>{items.length} {items.length === 1 ? "item" : "items"}</span>
      </header>
      <div className="place-work__items">
        {items.map((item) => (
          <button
            className="place-work__item"
            key={itemKey(item)}
            type="button"
            aria-pressed={sameItem(item, selected)}
            onClick={() => onSelect(item)}
          >
            <span className="place-work__icon" aria-hidden="true">
              <Icon name={item.icon} size={17} />
            </span>
            <span>
              <strong>{item.label}</strong>
              <small>{item.kind}</small>
            </span>
            <span className="place-work__arrow" aria-hidden="true">→</span>
          </button>
        ))}
      </div>
    </section>
  );
}
