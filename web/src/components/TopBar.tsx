import type { ReactNode } from "react";
import { useSession } from "../platform/session";

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
        <SignedIn />
      </div>
    </header>
  );
}

/**
 * Who is signed in, and the way out.
 *
 * It shows the name the provider disclosed, because "signed in as somebody"
 * with no name is not an answer an operator on a shared instance can act on.
 * When the deployment configures no sign-in there is nobody to be, so the
 * indicator says nothing rather than inventing an identity.
 */
function SignedIn(): ReactNode {
  const { state, signOut } = useSession();
  if (state.status !== "signed-in") return null;
  return (
    <div style={{ display: "flex", alignItems: "center", gap: "var(--space-2)" }}>
      <span
        style={{
          fontSize: "var(--font-size-sm)",
          color: "var(--color-text-muted)",
          maxWidth: 220,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
      >
        {state.principal.name}
      </span>
      <button
        onClick={() => void signOut()}
        style={{
          padding: "var(--space-1) var(--space-3)",
          borderRadius: 8,
          border: "1px solid var(--color-border)",
          background: "var(--color-surface-2)",
          color: "var(--color-text-muted)",
          fontSize: "var(--font-size-sm)",
        }}
      >
        Sign out
      </button>
    </div>
  );
}
