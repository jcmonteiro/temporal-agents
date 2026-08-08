import type { ReactNode } from "react";

const ITEMS = [
  { key: "overview", label: "Overview" },
  { key: "fleets", label: "Fleets" },
  { key: "workflows", label: "Workflows" },
  { key: "templates", label: "Templates" },
  { key: "insights", label: "Insights" },
  { key: "settings", label: "Settings" },
];

export function LeftNav({ active = "overview" }: { active?: string }): ReactNode {
  return (
    <nav
      style={{
        width: 220,
        borderRight: "1px solid var(--color-border)",
        background: "var(--color-surface)",
        padding: "var(--space-4)",
        display: "flex",
        flexDirection: "column",
        gap: "var(--space-1)",
      }}
    >
      {ITEMS.map((it) => {
        const isActive = it.key === active;
        return (
          <button
            key={it.key}
            style={{
              display: "flex",
              alignItems: "center",
              padding: "8px 12px",
              borderRadius: "var(--radius-sm)",
              textAlign: "left",
              fontSize: "var(--font-size-md)",
              color: isActive ? "var(--color-text)" : "var(--color-text-muted)",
              background: isActive ? "var(--color-surface-2)" : "transparent",
              border: isActive
                ? "1px solid var(--color-border-strong)"
                : "1px solid transparent",
            }}
          >
            {it.label}
          </button>
        );
      })}
      <div style={{ flex: 1 }} />
      <button
        style={{
          padding: "8px 12px",
          textAlign: "left",
          color: "var(--color-text-subtle)",
          fontSize: "var(--font-size-sm)",
        }}
      >
        ? Help
      </button>
    </nav>
  );
}
