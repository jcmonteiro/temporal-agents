import { useCallback, useEffect, useState, type ReactNode } from "react";
import {
  loadRegisteredPlaces,
  registerPlace,
  type RegisteredPlace,
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
  places: RegisteredPlace[] | null;
  error: string | null;
}

/** The places the hub may work in, and the way to add one. */
export function Places(): ReactNode {
  const [state, setState] = useState<State>({ places: null, error: null });

  const refresh = useCallback(async (): Promise<void> => {
    const result = await loadRegisteredPlaces();
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
              <strong>No place is registered yet</strong>
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

/** One registered place: what it is, where it is, and the way to its page. */
function PlaceRow({ registered }: { registered: RegisteredPlace }): ReactNode {
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
      <span className="settings-state settings-state--registered">Registered</span>
    </li>
  );
}

/** Registers one repository and keeps server refusals beside the input. */
function RegisterAPlace({ onRegistered }: { onRegistered: () => Promise<void> }): ReactNode {
  const [directory, setDirectory] = useState("");
  const [refusal, setRefusal] = useState<string | null>(null);
  const [confirmation, setConfirmation] = useState<string | null>(null);
  const [registering, setRegistering] = useState(false);

  const submit = async (): Promise<void> => {
    if (registering) return;
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
      aria-busy={registering}
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
          <input
            id="place-directory"
            name="directory"
            value={directory}
            onChange={(event) => {
              setDirectory(event.target.value);
              setRefusal(null);
              setConfirmation(null);
            }}
            placeholder="/srv/repos/pricing"
            aria-invalid={refusal !== null}
            aria-describedby={refusal === null ? "place-directory-hint" : "place-refusal"}
          />
          <small id="place-directory-hint">For example, /srv/repos/pricing</small>
        </div>
        <button
          className="ui-button ui-button--primary settings-register__button"
          type="submit"
          disabled={registering || directory.trim() === ""}
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
