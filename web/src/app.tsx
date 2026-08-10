import type { ReactNode } from "react";
import { TopBar } from "./components/TopBar";
import { LeftNav } from "./components/LeftNav";
import { OverviewPage } from "./pages/Overview/OverviewPage";
import { PlacePage } from "./pages/Place/PlacePage";
import { useRoute } from "./platform/route";

export function App(): ReactNode {
  const route = useRoute();
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
        <LeftNav active="overview" />
        {route.name === "place" ? (
          <PlacePage placeId={route.placeId} />
        ) : (
          <OverviewPage />
        )}
      </div>
    </div>
  );
}
