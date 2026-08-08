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
  // Fleet-only fields; absent for runs and schedules.
  progress?: { done: number; total: number; fraction: number };
  // Run-only.
  runType?: string;
  iterations?: number;
  // Schedule-only.
  spec?: string;
  paused?: boolean;
  dismissible?: boolean;
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
