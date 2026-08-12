import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import type { PromptDTO } from "../../clients/api";
import { ApiError } from "../../clients/http";
import { loadRegisteredPlaces, type RegisteredPlace } from "../../clients/places";
import { loadPrompts, resetPrompt, savePrompt } from "../../clients/prompts";
import "./settings.css";

interface FixedLocation {
  id: string;
  label: string;
}

interface Props {
  fixedLocation?: FixedLocation;
}

export function PromptConfiguration({ fixedLocation }: Props): ReactNode {
  const [locationId, setLocationId] = useState(fixedLocation?.id ?? "");
  const [places, setPlaces] = useState<RegisteredPlace[]>([]);
  const [catalogue, setCatalogue] = useState<PromptDTO[] | null>(null);
  const [readError, setReadError] = useState<string | null>(null);
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [confirmation, setConfirmation] = useState<string | null>(null);

  useEffect(() => {
    if (fixedLocation !== undefined) return;
    let cancelled = false;
    void loadRegisteredPlaces().then((result) => {
      if (!cancelled && result.ok) setPlaces(result.value);
    });
    return () => {
      cancelled = true;
    };
  }, [fixedLocation]);

  const refresh = useCallback(async (): Promise<void> => {
    const result = await loadPrompts(locationId);
    if (!result.ok) {
      setReadError(messageOf(result.error));
      return;
    }
    setCatalogue(result.value);
    setReadError(null);
    setSelectedKey((current) =>
      result.value.some((item) => item.key === current)
        ? current
        : (result.value[0]?.key ?? null),
    );
  }, [locationId]);

  useEffect(() => {
    setCatalogue(null);
    setConfirmation(null);
    void refresh();
  }, [refresh]);

  const selected = catalogue?.find((item) => item.key === selectedKey) ?? null;
  const scopeLabel =
    fixedLocation?.label ??
    (locationId === ""
      ? "Global"
      : (places.find((place) => place.place.id === locationId)?.place.label ?? "Place"));

  return (
    <section
      className="settings-section ui-surface prompt-configuration"
      aria-labelledby={`prompt-configuration-${fixedLocation?.id ?? "global"}`}
    >
      <header className="settings-section__header prompt-configuration__header">
        <div>
          <p className="ui-kicker">Agent behavior</p>
          <h2 id={`prompt-configuration-${fixedLocation?.id ?? "global"}`}>
            Instructions
          </h2>
          <p>
            Tune governed instructions without changing protected system
            material.
          </p>
        </div>
        {fixedLocation === undefined ? (
          <label className="ui-field prompt-configuration__scope">
            Configuration scope
            <select
              aria-label="Configuration scope"
              value={locationId}
              onChange={(event) => {
                setConfirmation(null);
                setLocationId(event.target.value);
              }}
            >
              <option value="">Global</option>
              {places.map(({ place }) => (
                <option key={place.id} value={place.id}>
                  {place.label}
                </option>
              ))}
            </select>
            <small>Editing {scopeLabel} configuration</small>
          </label>
        ) : (
          <span className="settings-state">Scope · {scopeLabel}</span>
        )}
      </header>

      <div className="settings-section__body prompt-configuration__body">
        {confirmation !== null && (
          <div className="ui-feedback ui-feedback--success" role="status">
            <strong>Configuration updated</strong>
            <span>{confirmation}</span>
          </div>
        )}
        {readError !== null && (
          <div className="ui-feedback ui-feedback--error" role="status">
            <strong>Instructions unavailable</strong>
            <span>The instructions could not be read: {readError}</span>
          </div>
        )}
        {catalogue === null && readError === null && (
          <p className="settings-loading" role="status">
            <span aria-hidden="true" />
            Reading the instructions…
          </p>
        )}
        {catalogue !== null && catalogue.length === 0 && (
          <div className="settings-empty" role="status">
            <strong>No configurable instructions</strong>
            <span>This build publishes no configurable instructions.</span>
          </div>
        )}
        {catalogue !== null && catalogue.length > 0 && (
          <div className="prompt-workspace">
            <PromptList
              prompts={catalogue}
              selectedKey={selectedKey}
              onSelect={(key) => {
                setConfirmation(null);
                setSelectedKey(key);
              }}
            />
            {selected !== null && (
              <PromptEditor
                key={`${locationId}:${selected.key}:${selected.version}:${selected.overridden}`}
                prompt={selected}
                locationId={locationId}
                onEditing={() => setConfirmation(null)}
                onChanged={async (message) => {
                  await refresh();
                  setConfirmation(message);
                }}
              />
            )}
          </div>
        )}
      </div>
    </section>
  );
}

function PromptList({
  prompts,
  selectedKey,
  onSelect,
}: {
  prompts: PromptDTO[];
  selectedKey: string | null;
  onSelect: (key: string) => void;
}): ReactNode {
  return (
    <nav className="prompt-navigation" aria-label="Instruction selection">
      <p className="prompt-navigation__label">Available instructions</p>
      <ul aria-label="Configurable instructions">
        {prompts.map((prompt) => {
          const state = prompt.overridden
            ? `Overridden here · ${prompt.source}`
            : `Inherited · ${prompt.source}`;
          return (
            <li key={prompt.key}>
              <button
                className="prompt-option"
                type="button"
                aria-label={`${prompt.key} ${state}`}
                aria-pressed={prompt.key === selectedKey}
                onClick={() => onSelect(prompt.key)}
              >
                <span className="prompt-option__heading">
                  <strong>{prompt.key}</strong>
                  <span
                    className={`settings-state ${prompt.overridden ? "settings-state--override" : ""}`}
                  >
                    {prompt.overridden ? "Override" : "Inherited"}
                  </span>
                </span>
                <span className="prompt-option__source">{state}</span>
                <span className="prompt-option__preview">{prompt.effective}</span>
              </button>
            </li>
          );
        })}
      </ul>
    </nav>
  );
}

function PromptEditor({
  prompt,
  locationId,
  onEditing,
  onChanged,
}: {
  prompt: PromptDTO;
  locationId: string;
  onEditing: () => void;
  onChanged: (message: string) => Promise<void>;
}): ReactNode {
  const [draft, setDraft] = useState(prompt.effective);
  const [operation, setOperation] = useState<"save" | "reset" | null>(null);
  const [refusal, setRefusal] = useState<string | null>(null);
  const changed = draft !== prompt.effective;
  const diff = useMemo(
    () => (draft === prompt.inherited ? null : { before: prompt.inherited, after: draft }),
    [draft, prompt.inherited],
  );

  const save = async (): Promise<void> => {
    if (!changed || operation !== null) return;
    setOperation("save");
    setRefusal(null);
    const result = await savePrompt(prompt.key, draft, locationId);
    if (result.ok) await onChanged(`Override saved for ${prompt.key}.`);
    else setRefusal(messageOf(result.error));
    setOperation(null);
  };

  const reset = async (): Promise<void> => {
    const destination = locationId === "" ? "the shipped default" : "the inherited value";
    if (!window.confirm(`Return ${prompt.key} to ${destination}?`)) return;
    setOperation("reset");
    setRefusal(null);
    const result = await resetPrompt(prompt.key, locationId);
    if (result.ok) {
      await onChanged(`${prompt.key} returned to ${destination}.`);
    } else {
      setRefusal(messageOf(result.error));
    }
    setOperation(null);
  };

  return (
    <form
      className="prompt-editor"
      aria-busy={operation !== null}
      onSubmit={(event) => {
        event.preventDefault();
        void save();
      }}
    >
      <header className="prompt-editor__header">
        <div>
          <p className="ui-kicker">Selected instruction</p>
          <h3>{prompt.key}</h3>
          <p>{prompt.purpose}</p>
        </div>
        <span
          className={`settings-state ${prompt.overridden ? "settings-state--override" : ""}`}
        >
          {prompt.overridden ? `Overridden · ${prompt.source}` : `Inherited · ${prompt.source}`}
        </span>
      </header>

      {prompt.advanced && (
        <div className="ui-feedback ui-feedback--warning">
          <strong>Advanced instruction</strong>
          <span>Protected machine material is appended by the system.</span>
        </div>
      )}

      <div className="ui-field prompt-editor__field">
        <label htmlFor={`prompt-${prompt.key}`}>Instruction text</label>
        <textarea
          id={`prompt-${prompt.key}`}
          value={draft}
          onChange={(event) => {
            setDraft(event.target.value);
            setRefusal(null);
            onEditing();
          }}
          rows={10}
          maxLength={prompt.maxLength}
          aria-invalid={refusal !== null}
          aria-describedby={
            refusal === null ? `prompt-count-${prompt.key}` : `prompt-refusal-${prompt.key}`
          }
        />
        <small id={`prompt-count-${prompt.key}`} className="prompt-editor__count">
          {draft.length.toLocaleString()} / {prompt.maxLength.toLocaleString()} characters
        </small>
      </div>

      {refusal !== null && (
        <div
          id={`prompt-refusal-${prompt.key}`}
          className="ui-feedback ui-feedback--error"
          role="alert"
        >
          <strong>Override not saved</strong>
          <span>{refusal}</span>
        </div>
      )}

      <div className="prompt-editor__references">
        <ReadOnlyBlock title={`Inherited from ${prompt.inheritedFrom}`} text={prompt.inherited} />
        <ReadOnlyBlock title="Read-only system block" text={prompt.systemBlock || "None"} />
      </div>

      <div className="prompt-inserts">
        <strong>Required inserts</strong>
        {prompt.requiredInserts.length === 0 ? (
          <p>None</p>
        ) : (
          <ul>
            {prompt.requiredInserts.map((insert) => (
              <li key={insert.name}>
                <code>{insert.action}</code>
                <span>{insert.purpose}</span>
              </li>
            ))}
          </ul>
        )}
      </div>

      {diff !== null && (
        <section
          className="prompt-diff"
          role="region"
          aria-label="Changes against inherited value"
        >
          <strong>Changes against inherited value</strong>
          <pre>
            <span className="prompt-diff__removed">- {diff.before}</span>{"\n"}
            <span className="prompt-diff__added">+ {diff.after}</span>
          </pre>
        </section>
      )}

      <footer className="prompt-editor__footer">
        <p>
          {locationId === ""
            ? "This override applies to every place that inherits global configuration."
            : "This override applies to this place and everything inheriting from it."}
        </p>
        <div className="prompt-editor__actions">
          <button
            className="ui-button ui-button--primary"
            type="submit"
            disabled={!changed || operation !== null}
          >
            {operation === "save" ? "Saving…" : "Save override"}
          </button>
          {prompt.overridden && (
            <button
              className="ui-button ui-button--secondary"
              type="button"
              disabled={operation !== null}
              onClick={() => void reset()}
            >
              {operation === "reset"
                ? "Resetting…"
                : locationId === ""
                  ? "Return to shipped default"
                  : "Return to inherited"}
            </button>
          )}
        </div>
      </footer>
    </form>
  );
}

function ReadOnlyBlock({ title, text }: { title: string; text: string }): ReactNode {
  return (
    <section className="prompt-reference">
      <strong>{title}</strong>
      <pre>{text}</pre>
    </section>
  );
}

function messageOf(error: Error): string {
  if (error instanceof ApiError && error.detail !== "") return error.detail;
  return error.message;
}
