import type { IconName, WorkItem, WorkItemKind } from "../domain/work-item";
import { err, ok, type Result } from "../utils/result";
import type {
  Collection,
  FleetDTO,
  RunDTO,
  ScheduleDTO,
} from "./api";

// Base path is proxied by Vite in dev (see vite.config.ts) and served by the
// same origin in production. See internal/httpapi/httpapi.go.
const BASE = "/api/v1";

// One request bound; the Go server also enforces its own.
const DEFAULT_TIMEOUT_MS = 15_000;

async function fetchJSON<T>(path: string): Promise<Result<T, Error>> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), DEFAULT_TIMEOUT_MS);
  try {
    const res = await fetch(BASE + path, {
      headers: { Accept: "application/json" },
      signal: controller.signal,
    });
    if (!res.ok) {
      return err(new Error(`GET ${path} → ${res.status}`));
    }
    const body = (await res.json()) as T;
    return ok(body);
  } catch (e) {
    return err(e instanceof Error ? e : new Error(String(e)));
  } finally {
    clearTimeout(timer);
  }
}

// The API returns items already in a portable, DB-agnostic shape (dto.go).
// The frontend chooses an icon per kind/status — icons aren't part of the
// wire model.
const KIND_ICON: Record<WorkItemKind, IconName> = {
  fleet: "rocket",
  run: "document",
  schedule: "clock",
};

function pickIcon(kind: WorkItemKind, status: string): IconName {
  if (status === "failed") return "alert";
  if (status === "done") return "check";
  if (status === "waiting-input") return "users";
  return KIND_ICON[kind];
}

function fromFleet(f: FleetDTO): WorkItem {
  return {
    id: f.id,
    kind: "fleet",
    label: f.label || f.id,
    status: f.status,
    icon: pickIcon("fleet", f.status),
    progress: f.progress,
    dismissible: f.dismissible,
  };
}

function fromRun(r: RunDTO): WorkItem {
  return {
    id: r.id,
    kind: "run",
    label: r.label || r.id,
    status: r.status,
    icon: pickIcon("run", r.status),
    runType: r.type,
    iterations: r.iterations,
    dismissible: r.dismissible,
  };
}

function fromSchedule(s: ScheduleDTO): WorkItem {
  return {
    id: s.id,
    kind: "schedule",
    label: s.label || s.id,
    status: s.status,
    icon: pickIcon("schedule", s.status),
    spec: s.spec,
    paused: s.paused,
    dismissible: s.dismissible,
  };
}

export interface OverviewData {
  items: WorkItem[];
  upNext: WorkItem[];
}

/**
 * Loads the Overview by calling the three resource endpoints in parallel and
 * merging the results into one list of satellites. This is the boundary
 * described in the implementation brief §3 — pages/components never call
 * `fetch` directly.
 *
 * "Up Next" is derived from the fleets' `upNext` node lists (the API's answer
 * to "what has not started yet"), projected into WorkItem shape for display.
 */
export async function loadOverview(): Promise<Result<OverviewData, Error>> {
  const [fleets, runs, schedules] = await Promise.all([
    fetchJSON<Collection<FleetDTO>>("/fleets"),
    fetchJSON<Collection<RunDTO>>("/runs"),
    fetchJSON<Collection<ScheduleDTO>>("/schedules"),
  ]);

  if (!fleets.ok) return err(fleets.error);
  if (!runs.ok) return err(runs.error);
  if (!schedules.ok) return err(schedules.error);

  const items: WorkItem[] = [
    ...fleets.value.items.map(fromFleet),
    ...runs.value.items.map(fromRun),
    ...schedules.value.items.map(fromSchedule),
  ];

  const upNext: WorkItem[] = [];
  for (const f of fleets.value.items) {
    for (const n of f.upNext ?? []) {
      upNext.push({
        id: `${f.id}:${n.id}`,
        kind: "fleet",
        label: n.label || n.id,
        status: n.status,
        icon: pickIcon("fleet", n.status),
      });
    }
  }

  return ok({ items, upNext });
}
