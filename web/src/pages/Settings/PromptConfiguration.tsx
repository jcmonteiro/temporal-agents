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
      aria-labelledby={`prompt-configuration-${fixedLocation?.id ?? "global"}`}
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
      <div style={{ display: "flex", justifyContent: "space-between", gap: 12 }}>
        <div>
          <h2
            id={`prompt-configuration-${fixedLocation?.id ?? "global"}`}
            style={{ margin: 0, fontSize: "var(--font-size-lg)", fontWeight: 600 }}
          >
            Instructions
          </h2>
          <p
            style={{
              margin: "4px 0 0",
              color: "var(--color-text-muted)",
              fontSize: "var(--font-size-sm)",
            }}
          >
            Tune the governed instructions without changing their protected system blocks.
          </p>
        </div>
        {fixedLocation === undefined && (
          <label style={{ display: "flex", flexDirection: "column", gap: 4 }}>
            <span style={{ color: "var(--color-text-muted)", fontSize: "var(--font-size-xs)" }}>
              Configuration scope
            </span>
            <select
              aria-label="Configuration scope"
              value={locationId}
              onChange={(event) => {
                setConfirmation(null);
                setLocationId(event.target.value);
              }}
              style={fieldStyle}
            >
              <option value="">Global</option>
              {places.map(({ place }) => (
                <option key={place.id} value={place.id}>
                  {place.label}
                </option>
              ))}
            </select>
          </label>
        )}
      </div>

      <div style={{ color: "var(--color-text-subtle)", fontSize: "var(--font-size-xs)" }}>
        Scope: {scopeLabel}
      </div>
      {confirmation !== null && (
        <p role="status" style={{ margin: 0, color: "var(--color-success-text)" }}>
          {confirmation}
        </p>
      )}
      {readError !== null && (
        <p role="status" style={{ margin: 0, color: "var(--status-failed)" }}>
          The instructions could not be read: {readError}
        </p>
      )}
      {catalogue === null && readError === null && (
        <p role="status" style={{ margin: 0, color: "var(--color-text-muted)" }}>
          Reading the instructions…
        </p>
      )}
      {catalogue !== null && catalogue.length === 0 && (
        <p role="status" style={{ margin: 0, color: "var(--color-text-subtle)" }}>
          This build publishes no configurable instructions.
        </p>
      )}
      {catalogue !== null && catalogue.length > 0 && (
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "minmax(210px, 0.8fr) minmax(320px, 2fr)",
            gap: "var(--space-4)",
            alignItems: "start",
          }}
        >
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
              onChanged={async (message) => {
                await refresh();
                setConfirmation(message);
              }}
            />
          )}
        </div>
      )}
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
    <ul
      aria-label="Configurable instructions"
      style={{ display: "grid", gap: 6, margin: 0, padding: 0, listStyle: "none" }}
    >
      {prompts.map((prompt) => (
        <li key={prompt.key}>
          <button
            type="button"
            aria-label={`${prompt.key} ${prompt.overridden ? `Overridden here · ${prompt.source}` : `Inherited · ${prompt.source}`}`}
            aria-pressed={prompt.key === selectedKey}
            onClick={() => onSelect(prompt.key)}
            style={{
              ...buttonStyle,
              width: "100%",
              display: "flex",
              flexDirection: "column",
              alignItems: "flex-start",
              gap: 3,
              borderColor:
                prompt.key === selectedKey ? "var(--color-accent)" : "var(--color-border)",
            }}
          >
            <strong style={{ fontWeight: 600 }}>{prompt.key}</strong>
            <span style={{ color: "var(--color-text-muted)", fontSize: "var(--font-size-xs)" }}>
              {prompt.overridden
                ? `Overridden here · ${prompt.source}`
                : `Inherited · ${prompt.source}`}
            </span>
            <span
              style={{
                width: "100%",
                overflow: "hidden",
                textOverflow: "ellipsis",
                whiteSpace: "nowrap",
                color: "var(--color-text-subtle)",
                fontSize: "var(--font-size-xs)",
              }}
            >
              {prompt.effective}
            </span>
          </button>
        </li>
      ))}
    </ul>
  );
}

function PromptEditor({
  prompt,
  locationId,
  onChanged,
}: {
  prompt: PromptDTO;
  locationId: string;
  onChanged: (message: string) => Promise<void>;
}): ReactNode {
  const [draft, setDraft] = useState(prompt.effective);
  const [saving, setSaving] = useState(false);
  const [refusal, setRefusal] = useState<string | null>(null);
  const changed = draft !== prompt.effective;
  const diff = useMemo(
    () => (draft === prompt.inherited ? null : { before: prompt.inherited, after: draft }),
    [draft, prompt.inherited],
  );

  const save = async (): Promise<void> => {
    if (!changed || saving) return;
    setSaving(true);
    setRefusal(null);
    const result = await savePrompt(prompt.key, draft, locationId);
    if (result.ok) await onChanged(`Override saved for ${prompt.key}.`);
    else setRefusal(messageOf(result.error));
    setSaving(false);
  };

  const reset = async (): Promise<void> => {
    const destination = locationId === "" ? "the shipped default" : "the inherited value";
    if (!window.confirm(`Return ${prompt.key} to ${destination}?`)) return;
    setSaving(true);
    setRefusal(null);
    const result = await resetPrompt(prompt.key, locationId);
    if (result.ok) {
      await onChanged(
        `${prompt.key} returned to ${locationId === "" ? "the shipped default" : "the inherited value"}.`,
      );
    } else setRefusal(messageOf(result.error));
    setSaving(false);
  };

  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        void save();
      }}
      style={{ display: "flex", flexDirection: "column", gap: "var(--space-3)" }}
    >
      <div>
        <h3 style={{ margin: 0, fontSize: "var(--font-size-md)" }}>{prompt.key}</h3>
        <p style={{ margin: "4px 0 0", color: "var(--color-text-muted)" }}>
          {prompt.purpose}
        </p>
      </div>
      {prompt.advanced && (
        <p style={{ margin: 0, color: "var(--status-waiting-input)" }}>
          Advanced instruction: protected machine material is appended by the system.
        </p>
      )}
      <label htmlFor={`prompt-${prompt.key}`} style={{ fontSize: "var(--font-size-sm)" }}>
        Instruction text
      </label>
      <textarea
        id={`prompt-${prompt.key}`}
        value={draft}
        onChange={(event) => {
          setDraft(event.target.value);
          setRefusal(null);
        }}
        rows={10}
        maxLength={prompt.maxLength}
        aria-invalid={refusal !== null}
        aria-describedby={refusal === null ? undefined : `prompt-refusal-${prompt.key}`}
        style={{ ...fieldStyle, width: "100%", resize: "vertical", lineHeight: 1.5 }}
      />
      <div style={{ color: "var(--color-text-subtle)", fontSize: "var(--font-size-xs)" }}>
        {draft.length} / {prompt.maxLength} characters
      </div>
      {refusal !== null && (
        <p
          id={`prompt-refusal-${prompt.key}`}
          role="alert"
          style={{ margin: 0, color: "var(--status-failed)" }}
        >
          {refusal}
        </p>
      )}

      <ReadOnlyBlock title={`Inherited from ${prompt.inheritedFrom}`} text={prompt.inherited} />
      <ReadOnlyBlock title="Read-only system block" text={prompt.systemBlock || "None"} />

      <div>
        <strong style={{ fontSize: "var(--font-size-sm)" }}>Required inserts</strong>
        {prompt.requiredInserts.length === 0 ? (
          <p style={{ margin: "4px 0 0", color: "var(--color-text-subtle)" }}>None</p>
        ) : (
          <ul style={{ margin: "4px 0 0", paddingLeft: 20 }}>
            {prompt.requiredInserts.map((insert) => (
              <li key={insert.name}>
                <code>{insert.action}</code> — {insert.purpose}
              </li>
            ))}
          </ul>
        )}
      </div>

      {diff !== null && (
        <section
          role="region"
          aria-label="Changes against inherited value"
          style={{
            border: "1px solid var(--color-border)",
            borderRadius: "var(--radius-sm)",
            overflow: "hidden",
          }}
        >
          <div style={{ padding: "6px 8px", color: "var(--color-text-muted)" }}>
            Changes against inherited value
          </div>
          <pre style={{ margin: 0, padding: 8, whiteSpace: "pre-wrap", background: "var(--color-surface-2)" }}>
            <span style={{ color: "var(--status-failed)" }}>- {diff.before}</span>{"\n"}
            <span style={{ color: "var(--status-done)" }}>+ {diff.after}</span>
          </pre>
        </section>
      )}

      <p style={{ margin: 0, color: "var(--color-text-muted)" }}>
        {locationId === ""
          ? "This override applies to every place that inherits global configuration."
          : "This override applies to this place and everything inheriting from it."}
      </p>
      <div style={{ display: "flex", gap: 8 }}>
        <button type="submit" disabled={!changed || saving} style={buttonStyle}>
          Save override
        </button>
        {prompt.overridden && (
          <button type="button" disabled={saving} onClick={() => void reset()} style={buttonStyle}>
            {locationId === "" ? "Return to shipped default" : "Return to inherited"}
          </button>
        )}
      </div>
    </form>
  );
}

function ReadOnlyBlock({ title, text }: { title: string; text: string }): ReactNode {
  return (
    <div>
      <strong style={{ fontSize: "var(--font-size-sm)" }}>{title}</strong>
      <pre
        style={{
          margin: "4px 0 0",
          padding: 8,
          whiteSpace: "pre-wrap",
          borderRadius: "var(--radius-sm)",
          background: "var(--color-surface-2)",
          color: "var(--color-text-muted)",
        }}
      >
        {text}
      </pre>
    </div>
  );
}

const fieldStyle = {
  padding: "8px 10px",
  borderRadius: "var(--radius-sm)",
  border: "1px solid var(--color-border)",
  background: "var(--color-surface-2)",
  color: "var(--color-text)",
  font: "inherit",
} as const;

const buttonStyle = {
  padding: "8px 10px",
  borderRadius: "var(--radius-sm)",
  border: "1px solid var(--color-border-strong)",
  background: "var(--color-surface-2)",
  color: "var(--color-text)",
  textAlign: "left",
} as const;

function messageOf(error: Error): string {
  if (error instanceof ApiError && error.detail !== "") return error.detail;
  return error.message;
}
