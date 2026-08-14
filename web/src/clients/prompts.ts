import type { Collection, PromptDTO } from "./api";
import { fetchJSON, putJSON, send } from "./http";
import type { Result } from "../utils/result";

function scopeQuery(locationId: string): string {
  return locationId === "" ? "" : `?locationId=${encodeURIComponent(locationId)}`;
}

export function loadPrompts(
  locationId = "",
): Promise<Result<PromptDTO[], Error>> {
  return fetchJSON<Collection<PromptDTO>>(`/prompts${scopeQuery(locationId)}`).then(
    (result) => (result.ok ? { ok: true, value: result.value.items } : result),
  );
}

export function savePrompt(
  key: string,
  text: string,
  locationId = "",
): Promise<Result<void, Error>> {
  return putJSON(
    `/prompts/${encodeURIComponent(key)}${scopeQuery(locationId)}`,
    { text },
  );
}

export function resetPrompt(
  key: string,
  locationId = "",
): Promise<Result<void, Error>> {
  return send(
    `/prompts/${encodeURIComponent(key)}${scopeQuery(locationId)}`,
    "DELETE",
  );
}

export function savePromptModel(
  key: string,
  model: string,
  locationId = "",
): Promise<Result<void, Error>> {
  return putJSON(
    `/prompts/${encodeURIComponent(key)}/model${scopeQuery(locationId)}`,
    { model },
  );
}

export function resetPromptModel(
  key: string,
  locationId = "",
): Promise<Result<void, Error>> {
  return send(
    `/prompts/${encodeURIComponent(key)}/model${scopeQuery(locationId)}`,
    "DELETE",
  );
}
