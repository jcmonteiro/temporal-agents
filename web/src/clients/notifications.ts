import type { NotificationCollectionDTO } from "./api";
import { fetchJSON, send } from "./http";

export function loadNotifications() {
  return fetchJSON<NotificationCollectionDTO>("/notifications");
}

export function markNotificationRead(id: string) {
  return send(`/notifications/${encodeURIComponent(id)}/read`, "POST");
}

export function markAllNotificationsRead() {
  return send("/notifications/read", "POST");
}
