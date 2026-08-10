import type { ReactNode } from "react";
import { useSession } from "../../platform/session";

/**
 * What a signed-out browser sees: a way in, and nothing else.
 *
 * The page holds no form and no credential. Signing in is a full-page
 * navigation to the API's own sign-in route, which sends the browser to the
 * identity provider — the frontend never sees a password, a code or a token,
 * and has no identity library to be wrong about them with.
 */
export function SignInPage(): ReactNode {
  const { signInHref } = useSession();
  return (
    <main
      style={{
        flex: 1,
        display: "grid",
        placeItems: "center",
        padding: "var(--space-6)",
      }}
    >
      <div style={{ maxWidth: 420, textAlign: "center", display: "grid", gap: "var(--space-4)" }}>
        <h1 style={{ fontSize: "var(--font-size-lg)", margin: 0 }}>Sign in to Agent Hub</h1>
        <p style={{ color: "var(--color-text-muted)", margin: 0 }}>
          The hub starts and steers agent work, so it is only usable by somebody it
          knows. Signing in happens at your identity provider.
        </p>
        <a
          href={signInHref()}
          style={{
            justifySelf: "center",
            padding: "var(--space-2) var(--space-5)",
            borderRadius: 8,
            border: "1px solid var(--color-border-strong)",
            background: "var(--color-surface-2)",
            color: "var(--color-text)",
            textDecoration: "none",
          }}
        >
          Sign in
        </a>
      </div>
    </main>
  );
}

/** What a browser sees when the hub itself could not say who they are. */
export function SessionUnavailablePage({ message }: { message: string }): ReactNode {
  const { refresh } = useSession();
  return (
    <main
      style={{
        flex: 1,
        display: "grid",
        placeItems: "center",
        padding: "var(--space-6)",
      }}
    >
      <div style={{ maxWidth: 420, textAlign: "center", display: "grid", gap: "var(--space-4)" }}>
        <h1 style={{ fontSize: "var(--font-size-lg)", margin: 0 }}>The hub could not be reached</h1>
        <p style={{ color: "var(--color-text-muted)", margin: 0 }}>
          Your session is untouched — the hub simply could not answer just now.
          <br />
          <span style={{ color: "var(--color-text-subtle)" }}>{message}</span>
        </p>
        <button
          onClick={() => void refresh()}
          style={{
            justifySelf: "center",
            padding: "var(--space-2) var(--space-5)",
            borderRadius: 8,
            border: "1px solid var(--color-border-strong)",
            background: "var(--color-surface-2)",
            color: "var(--color-text)",
          }}
        >
          Try again
        </button>
      </div>
    </main>
  );
}
