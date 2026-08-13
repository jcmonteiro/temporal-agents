import type { DismissibleWorkItem } from "../domain/work-item";
import { err, ok, type Result } from "../utils/result";
import type { DismissalDTO } from "./api";
import { postJSON } from "./http";

/**
 * Acknowledges the exact terminal state currently published for one item. The
 * server owns eligibility and validates the current state revision. The browser
 * echoes the opaque revision that was published with the item.
 */
export async function dismissWorkItem(
  item: DismissibleWorkItem,
): Promise<Result<void, Error>> {
  const result = await postJSON<DismissalDTO>("/dismissals", {
    kind: item.kind,
    itemId: item.id,
    stateRevision: item.stateRevision,
  });
  return result.ok ? ok(undefined) : err(result.error);
}
