// Pure projection from the API's wire model (internal/httpapi/dto.go) into the
// frontend's domain model. No I/O, so every rule here is unit testable.

import type { UpNextEntry } from "../domain/up-next";
import { UNKNOWN_PLACE_ID, type Place } from "../domain/place";
import type {
  IconName,
  WorkItem,
  WorkItemKind,
} from "../domain/work-item";
import type {
  FleetDTO,
  LocationResource,
  RunDTO,
  ScheduleDTO,
} from "./api";

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

/**
 * The place an item runs in. An item that carries no reference runs in the
 * unknown place — the API's own answer for work whose place was never recorded,
 * never a guess made here.
 */
function placeOf(locationId: string | undefined): string {
  return locationId ? locationId : UNKNOWN_PLACE_ID;
}

/** One registry entry, as the frontend reads it. */
export function fromLocation(location: LocationResource): Place {
  return {
    id: location.id,
    kind: location.kind,
    label: location.label,
    parentId: location.parentId ?? null,
    directory: location.directory,
    ref: location.ref,
  };
}

export function fromFleet(f: FleetDTO): WorkItem {
  return {
    id: f.id,
    kind: "fleet",
    label: f.label || f.id,
    status: f.status,
    icon: pickIcon("fleet", f.status),
    placeId: placeOf(f.locationId),
    progress: f.progress,
    dismissible: f.dismissible,
    stateRevision: f.stateRevision,
  };
}

export function fromRun(r: RunDTO): WorkItem {
  return {
    id: r.id,
    kind: "run",
    label: r.label || r.id,
    status: r.status,
    icon: pickIcon("run", r.status),
    placeId: placeOf(r.locationId),
    runType: r.type,
    iterations: r.iterations,
    dismissible: r.dismissible,
    stateRevision: r.stateRevision,
  };
}

export function fromSchedule(s: ScheduleDTO): WorkItem {
  return {
    id: s.id,
    kind: "schedule",
    label: s.label || s.id,
    status: s.status,
    icon: pickIcon("schedule", s.status),
    placeId: placeOf(s.locationId),
    spec: s.spec,
    paused: s.paused,
    dismissible: s.dismissible,
  };
}

/**
 * "Up Next" is derived from the fleets' `upNext` node lists (the API's answer
 * to "what has not started yet").
 */
export function upNextOf(fleets: FleetDTO[]): UpNextEntry[] {
  const entries: UpNextEntry[] = [];
  for (const f of fleets) {
    for (const n of f.upNext ?? []) {
      entries.push({
        fleetId: f.id,
        nodeId: n.id,
        label: n.label || n.id,
        status: n.status,
        // A node belongs to a fleet, so it borrows the fleet glyph.
        icon: pickIcon("fleet", n.status),
      });
    }
  }
  return entries;
}
