import { useCallback, useEffect, useState, type ReactNode } from "react";
import {
  loadKnownPlaces,
  pickPlaceDirectory,
  registerPlace,
  type KnownPlace,
} from "../../clients/places";
import { ApiError } from "../../clients/http";
import {
  addressOf,
  SETTINGS,
  SETTINGS_PLACES,
  type SettingsCategory,
} from "../../platform/route";
import { PromptConfiguration } from "./PromptConfiguration";
import "./settings.css";

/** The hub configuration available to an operator, one category at a time. */
export function SettingsPage({
  category = "instructions",
}: {
  category?: SettingsCategory;
}): ReactNode {
  return (
    <main className="settings-page">
      <div className="settings-page__content">
        <header className="settings-page__header">
          <p className="ui-eyebrow">Hub configuration</p>
          <h1>Settings</h1>
          <p>
            Control where this hub can work and how its agents receive governed
            instructions.
          </p>
        </header>

        <nav className="settings-category-nav" aria-label="Settings categories">
          <a
            href={addressOf(SETTINGS)}
            aria-current={category === "instructions" ? "page" : undefined}
          >
            Instructions
          </a>
          <a
            href={addressOf(SETTINGS_PLACES)}
            aria-current={category === "places" ? "page" : undefined}
          >
            Places
          </a>
        </nav>

        {category === "instructions" ? <PromptConfiguration /> : <Places />}
      </div>
    </main>
  );
}

// The list and the failure of the last read stay separate. A failed refresh
// reports itself without emptying a list the operator is reading.
interface State {
  places: KnownPlace[] | null;
  error: string | null;
}

/** The places the hub may work in, and the way to add one. */
export function Places(): ReactNode {
  const [state, setState] = useState<State>({ places: null, error: null });

  const refresh = useCallback(async (): Promise<void> => {
    const result = await loadKnownPlaces();
    setState((previous) =>
      result.ok
        ? { places: result.value, error: null }
        : { places: previous.places, error: messageOf(result.error) },
    );
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const { places, error } = state;
  return (
    <section className="settings-section ui-surface" aria-labelledby="places-heading">
      <header className="settings-section__header">
        <div>
          <p className="ui-kicker">Workspace registry</p>
          <h2 id="places-heading">Places</h2>
          <p>
            Register repositories before their first run so the hub knows where
            work is allowed.
          </p>
        </div>
        {places !== null && (
          <span className="settings-section__count">
            {places.length} {places.length === 1 ? "place" : "places"}
          </span>
        )}
      </header>

      <div className="settings-section__body settings-places__body">
        {error !== null && (
          <div className="ui-feedback ui-feedback--error" role="status">
            <strong>Places unavailable</strong>
            <span>The places could not be read: {error}</span>
          </div>
        )}
        {places === null && error === null && (
          <p className="settings-loading" role="status">
            <span aria-hidden="true" />
            Reading the places…
          </p>
        )}
        {places !== null &&
          (places.length === 0 ? (
            <div className="settings-empty" role="status">
              <strong>No place is known yet</strong>
              <span>Register the repository where the next run should start.</span>
            </div>
          ) : (
            <ul className="settings-place-list">
              {places.map((registered) => (
                <PlaceRow key={registered.place.id} registered={registered} />
              ))}
            </ul>
          ))}

        <RegisterAPlace onRegistered={refresh} />
      </div>
    </section>
  );
}

/** One known place: what it is, where it is, and the way to its page. */
function PlaceRow({ registered }: { registered: KnownPlace }): ReactNode {
  const { place } = registered;
  return (
    <li className="settings-place">
      <div className="settings-place__identity">
        <span className="settings-place__mark" aria-hidden="true" />
        <div>
          <a href={addressOf({ name: "place", placeId: place.id })}>{place.label}</a>
          <span>{place.directory ?? place.ref ?? "Repository location unavailable"}</span>
        </div>
      </div>
      <span
        className={`settings-state ${
          registered.registeredAt === null
            ? "settings-state--observed"
            : "settings-state--registered"
        }`}
      >
        {registered.registeredAt === null ? "Observed" : "Registered"}
      </span>
    </li>
  );
}

/** Registers one repository and keeps server refusals beside the input. */
function RegisterAPlace({ onRegistered }: { onRegistered: () => Promise<void> }): ReactNode {
  const [directory, setDirectory] = useState("");
  const [refusal, setRefusal] = useState<string | null>(null);
  const [selectionFailure, setSelectionFailure] = useState<string | null>(null);
  const [confirmation, setConfirmation] = useState<string | null>(null);
  const [registering, setRegistering] = useState(false);
  const [picking, setPicking] = useState(false);

  const chooseDirectory = async (): Promise<void> => {
    if (picking || registering) return;
    setPicking(true);
    setRefusal(null);
    setSelectionFailure(null);
    setConfirmation(null);
    const result = await pickPlaceDirectory();
    if (result.ok) {
      if (result.value !== null) setDirectory(result.value);
    } else {
      setSelectionFailure(messageOf(result.error));
    }
    setPicking(false);
  };

  const submit = async (): Promise<void> => {
    if (registering || picking) return;
    const requestedDirectory = directory.trim();
    setRegistering(true);
    setRefusal(null);
    setConfirmation(null);
    const result = await registerPlace(requestedDirectory);
    if (result.ok) {
      setDirectory("");
      await onRegistered();
      setConfirmation(`Repository registered: ${requestedDirectory}.`);
    } else {
      setRefusal(messageOf(result.error));
    }
    setRegistering(false);
  };

  return (
    <form
      className="settings-register"
      aria-busy={registering || picking}
      onSubmit={(event) => {
        event.preventDefault();
        void submit();
      }}
    >
      <div className="settings-register__heading">
        <h3>Register a repository</h3>
        <p>Use an absolute directory path on this hub.</p>
      </div>
      <div className="settings-register__controls">
        <div className="ui-field">
          <label htmlFor="place-directory">Directory</label>
          <div className="settings-register__directory">
            <input
              id="place-directory"
              name="directory"
              value={directory}
              onChange={(event) => {
                setDirectory(event.target.value);
                setRefusal(null);
                setSelectionFailure(null);
                setConfirmation(null);
              }}
              placeholder="/srv/repos/pricing"
              aria-invalid={refusal !== null || selectionFailure !== null}
              aria-describedby={
                refusal !== null
                  ? "place-refusal"
                  : selectionFailure !== null
                    ? "place-selection-failure"
                    : undefined
              }
            />
            <button
              className="ui-button ui-button--secondary settings-register__picker"
              type="button"
              disabled={picking || registering}
              onClick={() => void chooseDirectory()}
            >
              <svg viewBox="0 0 24 24" aria-hidden="true">
                <path d="M3.5 6.5h6l2 2h9v9.5H3.5z" />
              </svg>
              {picking ? "Choosing…" : "Choose folder"}
            </button>
          </div>
        </div>
        <button
          className="ui-button ui-button--primary settings-register__button"
          type="submit"
          disabled={registering || picking || directory.trim() === ""}
        >
          {registering ? "Registering…" : "Register"}
        </button>
      </div>
      {confirmation !== null && (
        <div className="ui-feedback ui-feedback--success" role="status">
          <strong>Repository registered</strong>
          <span>{confirmation}</span>
        </div>
      )}
      {selectionFailure !== null && (
        <div
          id="place-selection-failure"
          className="ui-feedback ui-feedback--error"
          role="alert"
        >
          <strong>Folder not selected</strong>
          <span>{selectionFailure} Enter the path manually.</span>
        </div>
      )}
      {refusal !== null && (
        <div id="place-refusal" className="ui-feedback ui-feedback--error" role="alert">
          <strong>Repository not registered</strong>
          <span>{refusal}</span>
        </div>
      )}
    </form>
  );
}

/** Uses the server detail when present and the transport message otherwise. */
function messageOf(error: Error): string {
  if (error instanceof ApiError && error.detail !== "") return error.detail;
  return error.message;
}
