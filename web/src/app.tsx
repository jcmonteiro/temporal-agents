import type { ReactNode } from "react";
import { TopBar } from "./components/TopBar";
import { LeftNav } from "./components/LeftNav";
import { OverviewPage } from "./pages/Overview/OverviewPage";
import { PlacePage } from "./pages/Place/PlacePage";
import { SessionUnavailablePage, SignInPage } from "./pages/SignIn/SignInPage";
import { useRoute } from "./platform/route";
import { SessionProvider, useSession } from "./platform/session";

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
      style={{
        display: "flex",
        flexDirection: "column",
        height: "100vh",
        background: "var(--color-bg)",
        color: "var(--color-text)",
      }}
    >
      <TopBar />
      <div style={{ display: "flex", flex: 1, minHeight: 0 }}>
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
  const route = useRoute();
  return (
    <>
      <LeftNav active="overview" />
      {route.name === "place" ? (
        <PlacePage placeId={route.placeId} />
      ) : (
        <OverviewPage />
      )}
    </>
  );
}
