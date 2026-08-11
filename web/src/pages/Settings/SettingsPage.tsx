import { useCallback, useEffect, useState, type ReactNode } from "react";
import {
  loadRegisteredPlaces,
  registerPlace,
  type RegisteredPlace,
} from "../../clients/places";
import { ApiError } from "../../clients/http";
import { addressOf } from "../../platform/route";
import { PromptConfiguration } from "./PromptConfiguration";

/**
 * What the hub is configured with.
 *
 * Today that is one thing: where it may work. A place with work in it is
 * observed, so the hub already knows it; a place with none exists only because
 * an operator said so here, and until somebody does, the first run in a
 * repository cannot be started from the hub at all.
 */
export function SettingsPage(): ReactNode {
  return (
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
      <header style={{ display: "flex", flexDirection: "column", gap: 6 }}>
        <h1 style={{ margin: 0, fontSize: "var(--font-size-xl)", fontWeight: 600 }}>
          Settings
        </h1>
        <p style={{ margin: 0, color: "var(--color-text-muted)" }}>
          What this hub is configured with.
        </p>
      </header>
      <PromptConfiguration />
      <Places />
    </main>
  );
}

// What the page holds while it reads and writes. The list and the failure of the
// last read are separate, so a failed refresh reports itself without emptying a
// list the operator is reading.
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
    <section
      aria-labelledby="places-heading"
      style={{
        display: "flex",
        flexDirection: "column",
        gap: "var(--space-3)",
        border: "1px solid var(--color-border)",
        borderRadius: "var(--radius-md)",
        background: "var(--color-surface)",
        padding: "var(--space-4)",
      }}
    >
      <h2
        id="places-heading"
        style={{ margin: 0, fontSize: "var(--font-size-lg)", fontWeight: 600 }}
      >
        Places
      </h2>
      <p
        style={{
          margin: 0,
          color: "var(--color-text-muted)",
          fontSize: "var(--font-size-sm)",
        }}
      >
        Where the hub may work. A repository that has never run anything is
        unknown to the hub until it is registered here.
      </p>

      {error !== null && (
        <p role="status" style={{ margin: 0, color: "var(--status-failed)" }}>
          The places could not be read: {error}
        </p>
      )}
      {places === null && error === null && (
        <p role="status" style={{ margin: 0, color: "var(--color-text-muted)" }}>
          Reading the places…
        </p>
      )}
      {places !== null &&
        (places.length === 0 ? (
          <p role="status" style={{ margin: 0, color: "var(--color-text-subtle)" }}>
            No place is registered yet. Register the repository you want to work
            in.
          </p>
        ) : (
          <ul
            style={{
              margin: 0,
              padding: 0,
              listStyle: "none",
              display: "flex",
              flexDirection: "column",
              gap: 8,
            }}
          >
            {places.map((registered) => (
              <PlaceRow key={registered.place.id} registered={registered} />
            ))}
          </ul>
        ))}

      <RegisterAPlace onRegistered={refresh} />
    </section>
  );
}

/** One registered place: what it is, where it is, and the way to its page. */
function PlaceRow({ registered }: { registered: RegisteredPlace }): ReactNode {
  const { place } = registered;
  return (
    <li style={{ display: "flex", flexDirection: "column", gap: 2 }}>
      <a
        href={addressOf({ name: "place", placeId: place.id })}
        style={{ color: "var(--color-text)", fontSize: "var(--font-size-md)" }}
      >
        {place.label}
      </a>
      <span
        style={{ color: "var(--color-text-subtle)", fontSize: "var(--font-size-sm)" }}
      >
        {place.directory ?? place.ref ?? ""}
      </span>
    </li>
  );
}

/**
 * The registration itself.
 *
 * The refusal is shown where the directory was typed, in the server's own
 * words: only the server knows whether the directory is missing, unversioned or
 * badly written, and only the sentence it sent says which.
 */
function RegisterAPlace({ onRegistered }: { onRegistered: () => Promise<void> }): ReactNode {
  const [directory, setDirectory] = useState("");
  const [refusal, setRefusal] = useState<string | null>(null);
  const [registering, setRegistering] = useState(false);

  const submit = async (): Promise<void> => {
    if (registering) return;
    setRegistering(true);
    setRefusal(null);
    const result = await registerPlace(directory.trim());
    if (result.ok) {
      setDirectory("");
      await onRegistered();
    } else {
      setRefusal(messageOf(result.error));
    }
    setRegistering(false);
  };

  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        void submit();
      }}
      style={{ display: "flex", flexDirection: "column", gap: 6 }}
    >
      <label
        htmlFor="place-directory"
        style={{ fontSize: "var(--font-size-sm)", color: "var(--color-text-muted)" }}
      >
        Directory
      </label>
      <div style={{ display: "flex", gap: 8 }}>
        <input
          id="place-directory"
          name="directory"
          value={directory}
          onChange={(event) => setDirectory(event.target.value)}
          placeholder="/srv/repos/pricing"
          aria-invalid={refusal !== null}
          aria-describedby={refusal === null ? undefined : "place-refusal"}
          style={{
            flex: 1,
            padding: "8px 10px",
            borderRadius: "var(--radius-sm)",
            border: "1px solid var(--color-border)",
            background: "var(--color-surface-2)",
            color: "var(--color-text)",
            fontSize: "var(--font-size-sm)",
          }}
        />
        <button
          type="submit"
          disabled={registering || directory.trim() === ""}
          style={{
            padding: "8px 12px",
            borderRadius: "var(--radius-sm)",
            border: "1px solid var(--color-border-strong)",
            background: "var(--color-surface-2)",
            color: "var(--color-text)",
            fontSize: "var(--font-size-sm)",
            opacity: registering || directory.trim() === "" ? 0.5 : 1,
          }}
        >
          Register
        </button>
      </div>
      {refusal !== null && (
        <p
          id="place-refusal"
          role="alert"
          style={{ margin: 0, color: "var(--status-failed)", fontSize: "var(--font-size-sm)" }}
        >
          {refusal}
        </p>
      )}
    </form>
  );
}

/**
 * What to put in front of the operator about a failure: the server's own
 * explanation where there is one, and the transport's message otherwise.
 */
function messageOf(error: Error): string {
  if (error instanceof ApiError && error.detail !== "") return error.detail;
  return error.message;
}
