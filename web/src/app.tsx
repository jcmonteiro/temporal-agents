import type { ReactNode } from "react";
import { TopBar } from "./components/TopBar";
import { LeftNav } from "./components/LeftNav";
import { SessionUnavailablePage, SignInPage } from "./pages/SignIn/SignInPage";
import { Router } from "./router";
import { SessionProvider, useSession } from "./platform/session";
import { SteeringProvider } from "./platform/steering";

export function App(): ReactNode {
  return (
    <SessionProvider>
      <Shell />
    </SessionProvider>
  );
}

/**
 * The shell decides what the operator may see at all.
 *
 * Gating here, once, is what keeps every page free of the question: a page is
 * rendered only inside a usable session, so no component has to check, and no
 * component can forget to.
 */
function Shell(): ReactNode {
  const { state } = useSession();
  return (
    <div
      className="app-shell"
      style={{
        display: "flex",
        flexDirection: "column",
        width: "100%",
        height: "100vh",
        minWidth: 0,
        background: "var(--color-bg)",
        color: "var(--color-text)",
      }}
    >
      <TopBar />
      <div className="app-shell__body" style={{ display: "flex", flex: 1, minHeight: 0 }}>
        {state.status === "signed-out" ? (
          <SignInPage />
        ) : state.status === "unavailable" ? (
          <SessionUnavailablePage message={state.message} />
        ) : state.status === "unknown" ? (
          <main style={{ flex: 1, display: "grid", placeItems: "center" }}>
            <span style={{ color: "var(--color-text-muted)" }}>Signing in…</span>
          </main>
        ) : (
          <Workspace />
        )}
      </div>
    </div>
  );
}

/** The hub itself, which only a usable session reaches. */
function Workspace(): ReactNode {
  return (
    <SteeringProvider>
      <LeftNav />
      <Router />
    </SteeringProvider>
  );
}
