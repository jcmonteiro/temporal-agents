import type { LocatedCollection, SteeringSessionDTO } from "./api";
import { fetchJSON, postJSON } from "./http";
import type { Result } from "../utils/result";

export function loadWaitingSessions(): Promise<Result<SteeringSessionDTO[], Error>> {
  return fetchJSON<LocatedCollection<SteeringSessionDTO>>("/steering/sessions")
    .then((result) => result.ok ? { ...result, value: result.value.items } : result);
}

export function loadSteeringSession(
  id: string,
): Promise<Result<SteeringSessionDTO, Error>> {
  return fetchJSON<SteeringSessionDTO>(`/steering/sessions/${encodeURIComponent(id)}`);
}

export function questionSteeringSession(
  id: string,
  text: string,
  finish = false,
): Promise<Result<SteeringSessionDTO, Error>> {
  return postJSON<SteeringSessionDTO>(
    `/steering/sessions/${encodeURIComponent(id)}/question`,
    { text, finish },
  );
}

export function decideSteeringSession(
  id: string,
  decision: "guide" | "skip" | "stop",
  guidance?: string,
): Promise<Result<SteeringSessionDTO, Error>> {
  return postJSON<SteeringSessionDTO>(
    `/steering/sessions/${encodeURIComponent(id)}/decision`,
    { decision, guidance },
  );
}
