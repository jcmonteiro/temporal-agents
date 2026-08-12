import type { WorkItem } from "../domain/work-item";
import { err, ok, type Result } from "../utils/result";
import type { DismissalDTO } from "./api";
import { postJSON } from "./http";

/**
 * Acknowledges the exact terminal state currently published for one item. The
 * server owns both eligibility and the state revision; the browser only names
 * the item the operator acted on.
 */
export async function dismissWorkItem(
  item: WorkItem,
): Promise<Result<void, Error>> {
  const result = await postJSON<DismissalDTO>("/dismissals", {
    kind: item.kind,
    itemId: item.id,
  });
  return result.ok ? ok(undefined) : err(result.error);
}
