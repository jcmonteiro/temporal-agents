// Wire types matching the Go API's DTOs (internal/httpapi/dto.go).
// Only the fields the Overview needs are declared; extra fields are ignored.
//
// These declarations are a hand-written copy, so nothing here detects a rename
// on the Go side. The names below are pinned by
// TestOverviewResourcesKeepTheFieldNamesTheWebClientReads in
// internal/httpapi/httpapi_test.go; add a field to that list when the Overview
// starts reading it.

import type { PlaceKind } from "../domain/place";
import type { WorkItemStatus } from "../domain/work-item";

export interface Collection<T> {
  items: T[];
  count: number;
  limit: number;
}

// One place in a response's location registry. The union is discriminated by
// `kind`: a directory carries a path and no ref, a remote carries a ref and no
// path, the unknown place carries neither and no parent.
export interface LocationResource {
  id: string;
  kind: PlaceKind;
  label: string;
  parentId: string | null;
  directory?: string;
  ref?: string;
}

// A collection of work: the page, plus the registry every item's `locationId`
// resolves against. The registry is closed under ancestry and ordered
// parents-first, so a consumer builds the tree in one pass.
export interface LocatedCollection<T> extends Collection<T> {
  locations?: LocationResource[];
}

export interface FleetProgress {
  done: number;
  total: number;
  fraction: number;
}

export interface FleetNode {
  id: string;
  label: string;
  prompt?: string;
  dependsOn?: string[];
  status: WorkItemStatus;
  locationId?: string;
  execution: null | {
    workflowId: string;
    runId?: string;
    startedAt: string | null;
    endedAt: string | null;
    tokens?: number;
  };
}

export interface FleetDTO {
  id: string;
  kind: "fleet";
  label: string;
  status: WorkItemStatus;
  locationId?: string;
  progress: FleetProgress;
  planId?: string;
  startedAt: string | null;
  endedAt: string | null;
  dismissible: boolean;
  upNext?: FleetNode[];
  nodes?: FleetNode[];
}

// One instruction a run ran under. The text is deliberately absent: the version
// is named, not copied.
export interface InstructionUseDTO {
  key: string;
  scope: string;
  version: number;
  hash?: string;
}

export interface RunDTO {
  id: string;
  kind: "run";
  type: string;
  label: string;
  status: WorkItemStatus;
  locationId?: string;
  startedAt: string | null;
  endedAt: string | null;
  iterations: number;
  tokens?: number;
  dismissible: boolean;
  // Provenance, published on a run's own resource only.
  startedBy?: string;
  instructions?: InstructionUseDTO[];
}

export interface DismissalDTO {
  id: string;
  kind: "fleet" | "run";
  itemId: string;
  dismissedAt: string | null;
}

export interface ScheduleDTO {
  id: string;
  kind: "schedule";
  label: string;
  spec?: string;
  status: WorkItemStatus;
  locationId?: string;
  paused: boolean;
  runningActions: number;
  lastRunAt: string | null;
  nextRunAt: string | null;
  dismissible: boolean;
}

// One place the hub knows: which place it is, and registration provenance when
// present. The place itself is published once, in the response's
// registry, and referenced here by id.
export interface PlaceDTO {
  locationId: string;
  registeredAt: string | null;
  registeredBy?: string;
  // Present when the place is served on its own, because there is then no
  // envelope to carry the registry.
  locations?: LocationResource[];
}

export type PromptSource = "directory" | "global" | "factory";

export interface PromptInsertDTO {
  name: string;
  action: string;
  purpose: string;
}

export interface PromptDTO {
  key: string;
  purpose: string;
  effective: string;
  inherited: string;
  source: PromptSource;
  inheritedFrom: PromptSource;
  version: number;
  inheritedVersion: number;
  overridden: boolean;
  systemBlock: string;
  requiredInserts: PromptInsertDTO[];
  advanced: boolean;
  maxLength: number;
}

// Work that has just been started. It is not a run: a start returns as soon as
// the orchestrator accepts the submission, so there is no status, no iteration
// count and no token usage yet, and the API publishes none.
export interface NotificationDTO {
  id: string;
  kind: string;
  title: string;
  body: string;
  url?: string;
  sessionId?: string;
  createdAt: string | null;
  read: boolean;
}

export interface NotificationCollectionDTO extends Collection<NotificationDTO> {
  unread: number;
}

export interface SteeringMessageDTO {
  sequence: number;
  role: "operator" | "agent";
  author?: string;
  text: string;
  tokens?: number;
  at: string | null;
}

export interface SteeringSessionDTO {
  id: string;
  itemId: string;
  round: "local-review" | "remote-comments" | "pass-limit";
  state: "waiting" | "decided" | "abandoned";
  waitingSince: string | null;
  locationId: string;
  material?: string;
  guidance?: string;
  decision?: "guide" | "skip" | "stop" | "continue" | "accept";
  decidedAt?: string | null;
  decidedBy?: string;
  tokens?: number;
  contributors?: string[];
  messages?: SteeringMessageDTO[];
  locations?: LocationResource[];
}

export interface StartedWorkDTO {
  id: string;
  kind: "run";
  type: string;
  label: string;
  locationId: string;
  startedAt: string | null;
  startedBy?: string;
  locations?: LocationResource[];
}
