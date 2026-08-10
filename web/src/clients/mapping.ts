// Pure projection from the API's wire model (internal/httpapi/dto.go) into the
// frontend's domain model. No I/O, so every rule here is unit testable.

import type {
  IconName,
  WorkItem,
  WorkItemKind,
} from "../domain/work-item";
import type { FleetDTO, RunDTO, ScheduleDTO } from "./api";

// The API returns items already in a portable, DB-agnostic shape (dto.go).
// The frontend chooses an icon per kind/status — icons aren't part of the
// wire model.
const KIND_ICON: Record<WorkItemKind, IconName> = {
  fleet: "rocket",
  run: "document",
  schedule: "clock",
};

/** Icon for an item: the status wins where it carries a clear meaning. */
export function pickIcon(kind: WorkItemKind, status: string): IconName {
  if (status === "failed") return "alert";
  if (status === "done") return "check";
  if (status === "waiting-input") return "users";
  return KIND_ICON[kind];
}

export function fromFleet(f: FleetDTO): WorkItem {
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

export function fromRun(r: RunDTO): WorkItem {
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

export function fromSchedule(s: ScheduleDTO): WorkItem {
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

/**
 * "Up Next" is derived from the fleets' `upNext` node lists (the API's answer
 * to "what has not started yet"), projected into WorkItem shape for display.
 */
export function upNextOf(fleets: FleetDTO[]): WorkItem[] {
  const entries: WorkItem[] = [];
  for (const f of fleets) {
    for (const n of f.upNext ?? []) {
      entries.push({
        id: `${f.id}:${n.id}`,
        kind: "fleet",
        label: n.label || n.id,
        status: n.status,
        icon: pickIcon("fleet", n.status),
      });
    }
  }
  return entries;
}
