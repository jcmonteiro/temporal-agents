import type { ReactNode } from "react";
import { NotBuiltYet } from "../NotBuiltYet";

/**
 * What the hub is configured to do.
 *
 * The destination exists because the navigation offers it, and an offer that
 * leads nowhere is worse than one that says what it will hold. The settings
 * themselves — the instructions and the per-place values that resolve through
 * the place chain — are a feature of their own.
 */
export function SettingsPage(): ReactNode {
  return (
    <NotBuiltYet
      title="Settings"
      says="Settings are not built yet. Instructions and per-place values are configured on the command line for now."
    />
  );
}
