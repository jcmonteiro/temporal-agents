import type { ReactNode } from "react";

export function TopBar(): ReactNode {
  return (
    <header
      style={{
        height: 56,
        display: "flex",
        alignItems: "center",
        gap: "var(--space-4)",
        padding: "0 var(--space-5)",
        borderBottom: "1px solid var(--color-border)",
        background: "var(--color-surface)",
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: "var(--space-2)",
          width: 220,
        }}
      >
        <div
          aria-hidden="true"
          style={{
            width: 26,
            height: 26,
            borderRadius: "50%",
            border: "1.5px solid var(--color-text)",
            position: "relative",
          }}
        >
          <span
            style={{
              position: "absolute",
              inset: -3,
              border: "1px dashed var(--color-text-subtle)",
              borderRadius: "50%",
              transform: "rotate(-20deg)",
            }}
          />
        </div>
        <strong style={{ fontSize: "var(--font-size-lg)" }}>Agent Hub</strong>
      </div>

      <div style={{ flex: 1, display: "flex", justifyContent: "center" }}>
        <div
          style={{
            width: "min(560px, 60%)",
            height: 34,
            display: "flex",
            alignItems: "center",
            gap: "var(--space-2)",
            padding: "0 var(--space-3)",
            border: "1px solid var(--color-border)",
            borderRadius: 999,
            background: "var(--color-surface-2)",
            color: "var(--color-text-subtle)",
          }}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <circle cx="11" cy="11" r="6.5" />
            <path d="M20 20l-4-4" />
          </svg>
          <span style={{ fontSize: "var(--font-size-sm)" }}>Search anything…</span>
        </div>
      </div>

      <div style={{ display: "flex", alignItems: "center", gap: "var(--space-3)" }}>
        <button
          aria-label="Notifications"
          style={{
            width: 32,
            height: 32,
            display: "grid",
            placeItems: "center",
            borderRadius: 8,
            color: "var(--color-text-muted)",
          }}
        >
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <path d="M6 16V11a6 6 0 1 1 12 0v5l1.5 2H4.5z" />
            <path d="M10 20a2 2 0 0 0 4 0" />
          </svg>
        </button>
        <div
          aria-hidden="true"
          style={{
            width: 32,
            height: 32,
            borderRadius: "50%",
            border: "1px solid var(--color-border-strong)",
            display: "grid",
            placeItems: "center",
            fontSize: "var(--font-size-sm)",
            color: "var(--color-text-muted)",
          }}
        >
          A
        </div>
      </div>
    </header>
  );
}
