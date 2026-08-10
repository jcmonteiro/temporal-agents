// Wire types matching the Go API's DTOs (internal/httpapi/dto.go).
// Only the fields the Overview needs are declared; extra fields are ignored.
//
// These declarations are a hand-written copy, so nothing here detects a rename
// on the Go side. The names below are pinned by
// TestOverviewResourcesKeepTheFieldNamesTheWebClientReads in
// internal/httpapi/httpapi_test.go; add a field to that list when the Overview
// starts reading it.

import type { WorkItemStatus } from "../domain/work-item";

export interface Collection<T> {
  items: T[];
  count: number;
  limit: number;
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
  paused: boolean;
  runningActions: number;
  lastRunAt: string | null;
  nextRunAt: string | null;
  dismissible: boolean;
}
