import type { ReactNode } from "react";
import { NotBuiltYet } from "../NotBuiltYet";

/**
 * One fleet.
 *
 * A fleet is drawn on the overview and detailed in the rail; a page of its own
 * (its plan, its nodes, and how far each got) is not built. The route exists so
 * that "view details" leads somewhere real instead of nowhere.
 */
export function FleetPage({ fleetId }: { fleetId: string }): ReactNode {
  return (
    <NotBuiltYet
      title="Fleet"
      says="This fleet has a page, but what it reports is not built yet."
      detail={
        <code style={{ color: "var(--color-text-subtle)" }}>{fleetId}</code>
      }
    />
  );
}
