import { useEffect, useRef, useState, type ReactNode } from "react";
import { useSession } from "../platform/session";
import type { NotificationCollectionDTO, NotificationDTO } from "../clients/api";
import { loadNotifications, markAllNotificationsRead, markNotificationRead } from "../clients/notifications";
import { Icon } from "./Icon";

export function TopBar(): ReactNode {
  return (
    <header
      className="top-bar"
      style={{
        minWidth: 0,
        height: 56,
        display: "flex",
        alignItems: "center",
        gap: "var(--space-4)",
        padding: "0 var(--space-5)",
        borderBottom: "1px solid var(--color-border)",
        background: "var(--color-surface)",
      }}
    >
      <div
        className="top-bar__brand"
        style={{
          display: "flex",
          alignItems: "center",
          gap: "var(--space-2)",
          width: 220,
        }}
      >
        <div
          aria-hidden="true"
          style={{
            width: 26,
            height: 26,
            borderRadius: "50%",
            border: "1.5px solid var(--color-text)",
            position: "relative",
          }}
        >
          <span
            style={{
              position: "absolute",
              inset: -3,
              border: "1px dashed var(--color-text-subtle)",
              borderRadius: "50%",
              transform: "rotate(-20deg)",
            }}
          />
        </div>
        <strong style={{ fontSize: "var(--font-size-lg)" }}>Agent Hub</strong>
      </div>

      <div className="top-bar__search" style={{ flex: 1, display: "flex", justifyContent: "center" }}>
        <div
          style={{
            width: "min(560px, 60%)",
            height: 34,
            display: "flex",
            alignItems: "center",
            gap: "var(--space-2)",
            padding: "0 var(--space-3)",
            border: "1px solid var(--color-border)",
            borderRadius: 999,
            background: "var(--color-surface-2)",
            color: "var(--color-text-subtle)",
          }}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            <circle cx="11" cy="11" r="6.5" />
            <path d="M20 20l-4-4" />
          </svg>
          <span style={{ fontSize: "var(--font-size-sm)" }}>Search anything…</span>
        </div>
      </div>

      <div className="top-bar__actions" style={{ display: "flex", alignItems: "center", gap: "var(--space-3)" }}>
        <Inbox />
        <SignedIn />
      </div>
    </header>
  );
}

function Inbox(): ReactNode {
  const { state } = useSession();
  const [items, setItems] = useState<NotificationDTO[]>([]);
  const [unread, setUnread] = useState(0);
  const [open, setOpen] = useState(false);
  const [actionsOpen, setActionsOpen] = useState(false);
  const [onlyUnread, setOnlyUnread] = useState(false);
  const [markingAll, setMarkingAll] = useState(false);
  const [nativeEnabled, setNativeEnabled] = useState(false);
  const inbox = useRef<HTMLDivElement>(null);
  const seen = useRef(new Set<string>());

  const accept = (notifications: NotificationCollectionDTO): void => {
    setItems(notifications.items);
    setUnread(notifications.unread);
  };

  useEffect(() => {
    if (state.status !== "signed-in") return;
    let cancelled = false;
    const refresh = async (): Promise<void> => {
      const result = await loadNotifications();
      if (cancelled || !result.ok) return;
      accept(result.value);
      if (nativeEnabled && typeof Notification !== "undefined" && Notification.permission === "granted") {
        for (const item of result.value.items) {
          if (!item.read && !seen.current.has(item.id)) new Notification(item.title, { body: item.body });
          seen.current.add(item.id);
        }
      }
    };
    void refresh();
    const timer = setInterval(() => void refresh(), 5_000);
    return () => { cancelled = true; clearInterval(timer); };
  }, [state.status, nativeEnabled]);

  useEffect(() => {
    if (!open) return;
    const closeOutside = (event: PointerEvent): void => {
      if (event.target instanceof Node && !inbox.current?.contains(event.target)) {
        setOpen(false);
        setActionsOpen(false);
      }
    };
    const closeOnEscape = (event: KeyboardEvent): void => {
      if (event.key === "Escape") {
        setOpen(false);
        setActionsOpen(false);
      }
    };
    document.addEventListener("pointerdown", closeOutside);
    document.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("pointerdown", closeOutside);
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, [open]);

  const read = async (item: NotificationDTO): Promise<void> => {
    const result = await markNotificationRead(item.id);
    if (!result.ok) return;
    setItems((current) => current.map((candidate) => candidate.id === item.id ? { ...candidate, read: true } : candidate));
    setUnread((count) => Math.max(0, count - (item.read ? 0 : 1)));
  };

  const markAllRead = async (): Promise<void> => {
    setMarkingAll(true);
    const result = await markAllNotificationsRead();
    if (result.ok) {
      const refreshed = await loadNotifications();
      if (refreshed.ok) {
        accept(refreshed.value);
      } else {
        setItems((current) => current.map((item) => ({ ...item, read: true })));
        setUnread(0);
      }
    }
    setMarkingAll(false);
    setActionsOpen(false);
  };

  const enableNative = async (): Promise<void> => {
    if (typeof Notification === "undefined") return;
    const permission = await Notification.requestPermission();
    setNativeEnabled(permission === "granted");
    setActionsOpen(false);
  };

  const visibleItems = onlyUnread ? items.filter((item) => !item.read) : items;

  return (
    <div ref={inbox} className="notification-inbox">
      <button
        type="button"
        aria-label={`Notifications, ${unread} unread`}
        aria-expanded={open}
        onClick={() => { setOpen((value) => !value); setActionsOpen(false); }}
        className="notification-trigger"
      >
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="M6 16V11a6 6 0 1 1 12 0v5l1.5 2H4.5z" /><path d="M10 20a2 2 0 0 0 4 0" />
        </svg>
        {unread > 0 && <span aria-hidden="true" className="notification-count">{unread}</span>}
      </button>
      {open && (
        <section aria-label="Notification inbox" className="notification-panel">
          <div className="notification-panel-header">
            <h2>Notifications</h2>
            <div className="notification-toolbar">
              <span id="only-unread-label">Only show unread</span>
              <button
                type="button"
                role="switch"
                aria-labelledby="only-unread-label"
                aria-checked={onlyUnread}
                onClick={() => setOnlyUnread((value) => !value)}
                className="notification-switch"
              >
                <span aria-hidden="true" />
              </button>
              <div className="notification-actions">
                <button
                  type="button"
                  aria-label="Notification actions"
                  aria-haspopup="menu"
                  aria-expanded={actionsOpen}
                  onClick={() => setActionsOpen((value) => !value)}
                  className="notification-actions-trigger"
                >
                  <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
                    <circle cx="12" cy="5" r="1.6" /><circle cx="12" cy="12" r="1.6" /><circle cx="12" cy="19" r="1.6" />
                  </svg>
                </button>
                {actionsOpen && (
                  <div role="menu" aria-label="Notification actions" className="notification-actions-menu">
                    <button type="button" role="menuitem" disabled={unread === 0 || markingAll} onClick={() => void markAllRead()}>
                      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" aria-hidden="true">
                        <circle cx="12" cy="12" r="8.5" /><path d="M8.5 12l2.5 2.5L15.5 10" />
                      </svg>
                      {markingAll ? "Marking as read…" : "Mark all as read"}
                    </button>
                    <button type="button" role="menuitem" disabled={nativeEnabled} onClick={() => void enableNative()}>
                      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" aria-hidden="true">
                        <path d="M6 16V11a6 6 0 1 1 12 0v5l1.5 2H4.5z" /><path d="M10 20a2 2 0 0 0 4 0" />
                      </svg>
                      {nativeEnabled ? "Native notifications enabled" : "Enable native notifications"}
                    </button>
                  </div>
                )}
              </div>
            </div>
          </div>
          <div className="notification-list">
            <h3>Latest</h3>
            {visibleItems.length === 0 ? (
              <p className="notification-empty">{onlyUnread ? "No unread notifications." : "No notifications."}</p>
            ) : visibleItems.map((item) => (
              <article key={item.id} className="notification-item" data-read={item.read || undefined}>
                <a href={item.url} onClick={() => void read(item)}>
                  <span className="notification-state-icon" aria-label={item.kind === "steering" ? "Workflow waiting for input" : "Workflow completed"}>
                    <Icon
                      name={item.kind === "steering" ? "users" : "check"}
                      size={22}
                      color={item.kind === "steering" ? "var(--status-waiting-input)" : "var(--status-done)"}
                    />
                  </span>
                  <span className="notification-copy">
                    <strong>{item.title}</strong>
                    <span>{item.body}</span>
                    <time dateTime={item.createdAt ?? undefined}>{ageOf(item.createdAt)}</time>
                  </span>
                  {!item.read && <span aria-label="Unread" className="notification-unread-dot" />}
                </a>
              </article>
            ))}
          </div>
        </section>
      )}
    </div>
  );
}

function ageOf(createdAt: string | null): string {
  if (createdAt === null) return "Time unknown";
  const elapsed = Math.max(0, Date.now() - Date.parse(createdAt));
  const minute = 60_000;
  const hour = 60 * minute;
  const day = 24 * hour;
  if (elapsed >= day) {
    const days = Math.floor(elapsed / day);
    return `${days} ${days === 1 ? "day" : "days"} ago`;
  }
  if (elapsed >= hour) {
    const hours = Math.floor(elapsed / hour);
    return `${hours} ${hours === 1 ? "hour" : "hours"} ago`;
  }
  if (elapsed >= minute) {
    const minutes = Math.floor(elapsed / minute);
    return `${minutes} ${minutes === 1 ? "minute" : "minutes"} ago`;
  }
  return "Just now";
}

/**
 * Who is signed in, and the way out.
 *
 * It shows the name the provider disclosed, because "signed in as somebody"
 * with no name is not an answer an operator on a shared instance can act on.
 * When the deployment configures no sign-in there is nobody to be, so the
 * indicator says nothing rather than inventing an identity.
 */
function SignedIn(): ReactNode {
  const { state, signOut } = useSession();
  if (state.status !== "signed-in") return null;
  return (
    <div className="top-bar__identity" style={{ display: "flex", alignItems: "center", gap: "var(--space-2)" }}>
      <span
        className="top-bar__identity-name"
        style={{
          fontSize: "var(--font-size-sm)",
          color: "var(--color-text-muted)",
          maxWidth: 220,
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
      >
        {state.principal.name}
      </span>
      <button
        onClick={() => void signOut()}
        style={{
          padding: "var(--space-1) var(--space-3)",
          borderRadius: 8,
          border: "1px solid var(--color-border)",
          background: "var(--color-surface-2)",
          color: "var(--color-text-muted)",
          fontSize: "var(--font-size-sm)",
        }}
      >
        Sign out
      </button>
    </div>
  );
}
