// Status vocabulary from the Go API (agenthub.WorkStatus).
export type WorkItemStatus =
  | "todo"
  | "in-progress"
  | "paused"
  | "waiting-input"
  | "waiting"
  | "done"
  | "failed";

// ItemKind from the Go API (agenthub.ItemKind).
export type WorkItemKind = "fleet" | "run" | "schedule";

// Icon glyph names. The kind → icon mapping is chosen here on the frontend
// (the API does not expose icons).
export type IconName =
  | "rocket"
  | "users"
  | "document"
  | "clock"
  | "check"
  | "alert";

// A satellite in the Overview orbit. Derived from the three resource
// collections (`/api/v1/fleets|runs|schedules`); fields the API does not
// expose (owner, estimate, free-text description) are absent by design.
export interface WorkItem {
  id: string;
  kind: WorkItemKind;
  label: string;
  status: WorkItemStatus;
  icon: IconName;
  // The place the item runs in, resolved against the response's registry.
  placeId: string;
  // Fleet-only fields; absent for runs and schedules.
  progress?: { done: number; total: number; fraction: number };
  // Run-only.
  runType?: string;
  iterations?: number;
  // Schedule-only.
  spec?: string;
  paused?: boolean;
  dismissible?: boolean;
  // Opaque optimistic precondition for an exact-state dismissal.
  stateRevision?: string;
}

// Identity of a work item. The API only guarantees that an id is unique within
// its own collection (`/fleets`, `/runs`, `/schedules`), so a fleet and a run
// can share an id: kind is part of the identity.
export interface WorkItemId {
  kind: WorkItemKind;
  id: string;
}

/** Stable, collision-free string form of an identity, for use as a React key. */
export function itemKey(item: WorkItemId): string {
  return `${item.kind}:${item.id}`;
}

/** True when both identities denote the same work item. */
export function sameItem(
  a: WorkItemId | null | undefined,
  b: WorkItemId | null | undefined,
): boolean {
  return a != null && b != null && a.kind === b.kind && a.id === b.id;
}

export const STATUS_LABEL: Record<WorkItemStatus, string> = {
  todo: "Todo",
  "in-progress": "In Progress",
  paused: "Paused",
  "waiting-input": "Waiting Input",
  waiting: "Waiting",
  done: "Done",
  failed: "Failed",
};

export const STATUS_ORDER: WorkItemStatus[] = [
  "todo",
  "in-progress",
  "paused",
  "waiting-input",
  "waiting",
  "done",
  "failed",
];
