import type { ReactNode } from "react";
import { addressOf, navigationKeyOf, OVERVIEW, SETTINGS, useRoute } from "../platform/route";

/**
 * The way between the destinations the hub has.
 *
 * An entry with a destination is a link, so it is reachable by keyboard, can be
 * opened in a new tab, and shows where it leads before it is followed. An entry
 * whose destination does not exist yet is a disabled control that says so: it
 * keeps the shape of the hub visible without promising a page that is not
 * there.
 */
interface Entry {
  key: string;
  label: string;
  href?: string;
}

const ENTRIES: Entry[] = [
  { key: "overview", label: "Overview", href: addressOf(OVERVIEW) },
  { key: "fleets", label: "Fleets" },
  { key: "workflows", label: "Workflows" },
  { key: "templates", label: "Templates" },
  { key: "settings", label: "Settings", href: addressOf(SETTINGS) },
];

export function LeftNav(): ReactNode {
  const active = navigationKeyOf(useRoute());
  return (
    <nav
      className="left-nav"
      aria-label="Sections"
      style={{
        minWidth: 0,
        width: 220,
        borderRight: "1px solid var(--color-border)",
        background: "var(--color-surface)",
        padding: "var(--space-4)",
        display: "flex",
        flexDirection: "column",
        gap: "var(--space-1)",
      }}
    >
      {ENTRIES.map((entry) => {
        const isActive = entry.key === active;
        const style = {
          display: "flex",
          alignItems: "center",
          padding: "8px 12px",
          borderRadius: "var(--radius-sm)",
          textAlign: "left" as const,
          textDecoration: "none",
          fontSize: "var(--font-size-md)",
          color: isActive ? "var(--color-text)" : "var(--color-text-muted)",
          background: isActive ? "var(--color-surface-2)" : "transparent",
          border: isActive
            ? "1px solid var(--color-border-strong)"
            : "1px solid transparent",
        };
        return entry.href === undefined ? (
          <button
            key={entry.key}
            type="button"
            disabled
            title={`${entry.label} are not built yet`}
            style={{ ...style, opacity: 0.5, cursor: "not-allowed" }}
          >
            {entry.label}
          </button>
        ) : (
          <a
            key={entry.key}
            href={entry.href}
            aria-current={isActive ? "page" : undefined}
            style={style}
          >
            {entry.label}
          </a>
        );
      })}
      <div className="left-nav__spacer" style={{ flex: 1 }} />
      <button
        className="left-nav__help"
        type="button"
        disabled
        title="Help is not built yet"
        style={{
          padding: "8px 12px",
          textAlign: "left",
          color: "var(--color-text-subtle)",
          fontSize: "var(--font-size-sm)",
          opacity: 0.5,
          cursor: "not-allowed",
        }}
      >
        ? Help
      </button>
    </nav>
  );
}
