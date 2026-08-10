import type { IconName, WorkItemStatus } from "./work-item";

/**
 * A node inside a fleet that has not started yet (the API's `upNext` list).
 *
 * This is deliberately NOT a WorkItem: a node is not a fleet, a run or a
 * schedule, it has no Temporal execution of its own, and it cannot be selected
 * in the orbit. Its id is only unique inside its fleet, so both ids are kept.
 */
export interface UpNextEntry {
  fleetId: string;
  nodeId: string;
  label: string;
  status: WorkItemStatus;
  icon: IconName;
}

/** Stable, collision-free string form of an entry, for use as a React key. */
export function upNextKey(entry: UpNextEntry): string {
  return `${entry.fleetId}/${entry.nodeId}`;
}
