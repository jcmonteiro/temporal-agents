import type { ReactNode } from "react";

/**
 * A destination that is routed but not built.
 *
 * It says so plainly. A page that draws an empty frame, a fake chart or a
 * spinner that never ends teaches the operator to distrust the hub; one that
 * states what it will hold, and what to do meanwhile, does not.
 */
export function NotBuiltYet({
  title,
  says,
  detail,
}: {
  title: string;
  says: string;
  detail?: ReactNode;
}): ReactNode {
  return (
    <main
      style={{
        flex: 1,
        minWidth: 0,
        overflowY: "auto",
        padding: "var(--space-5)",
        display: "flex",
        flexDirection: "column",
        gap: "var(--space-3)",
        alignItems: "flex-start",
      }}
    >
      <h1 style={{ margin: 0, fontSize: "var(--font-size-xl)", fontWeight: 600 }}>
        {title}
      </h1>
      <p role="status" style={{ margin: 0, color: "var(--color-text-muted)" }}>
        {says}
      </p>
      {detail}
    </main>
  );
}
