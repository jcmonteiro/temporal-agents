const MINUTE_MS = 60_000;
const HOUR_MS = 60 * MINUTE_MS;
const DAY_MS = 24 * HOUR_MS;
const WEEK_MS = 7 * DAY_MS;

export function waitingFor(waitingSince: string | null, now = new Date()): string {
  if (waitingSince === null) return "waiting for an unknown time";

  const elapsed = now.getTime() - Date.parse(waitingSince);
  if (elapsed >= WEEK_MS) {
    const weeks = Math.floor(elapsed / WEEK_MS);
    return `waiting for ${weeks} ${weeks === 1 ? "week" : "weeks"}`;
  }
  if (elapsed >= DAY_MS) {
    const days = Math.floor(elapsed / DAY_MS);
    return `waiting for ${days} ${days === 1 ? "day" : "days"}`;
  }
  if (elapsed >= HOUR_MS) {
    const hours = Math.floor(elapsed / HOUR_MS);
    return `waiting for ${hours} ${hours === 1 ? "hour" : "hours"}`;
  }

  const minutes = Math.max(1, Math.floor(elapsed / MINUTE_MS));
  return `waiting for ${minutes} ${minutes === 1 ? "minute" : "minutes"}`;
}
