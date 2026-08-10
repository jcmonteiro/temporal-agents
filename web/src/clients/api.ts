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

// One place an operator registered: which place it is, and the provenance of
// the registration. The place itself is published once, in the response's
// registry, and referenced here by id.
export interface PlaceDTO {
  locationId: string;
  registeredAt: string | null;
  registeredBy?: string;
  // Present when the place is served on its own, because there is then no
  // envelope to carry the registry.
  locations?: LocationResource[];
}
