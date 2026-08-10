import type { ReactNode } from "react";
import { TopBar } from "./components/TopBar";
import { LeftNav } from "./components/LeftNav";
import { OverviewPage } from "./pages/Overview/OverviewPage";

export function App(): ReactNode {
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
        <OverviewPage />
      </div>
    </div>
  );
}
