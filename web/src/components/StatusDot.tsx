import type { ReactNode } from "react";
import type { WorkItemStatus } from "../domain/work-item";

const STATUS_VAR: Record<WorkItemStatus, string> = {
  todo: "var(--status-todo)",
  "in-progress": "var(--status-in-progress)",
  paused: "var(--status-paused)",
  "waiting-input": "var(--status-waiting-input)",
  waiting: "var(--status-waiting)",
  done: "var(--status-done)",
  failed: "var(--status-failed)",
};

interface Props {
  status: WorkItemStatus;
  size?: number;
  filled?: boolean;
}

export function StatusDot({ status, size = 10, filled = false }: Props): ReactNode {
  const color = STATUS_VAR[status];
  return (
    <span
      aria-hidden="true"
      style={{
        display: "inline-block",
        width: size,
        height: size,
        borderRadius: "50%",
        border: `1.5px solid ${color}`,
        background: filled ? color : "transparent",
        flexShrink: 0,
      }}
    />
  );
}
