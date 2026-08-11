import { useEffect, useRef, useState, type ReactNode } from "react";
import { useSession } from "../platform/session";
import type { NotificationDTO } from "../clients/api";
import { clearNotificationReadState, loadNotifications, markNotificationRead } from "../clients/notifications";

export function TopBar(): ReactNode {
  return (
    <header
      style={{
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

      <div style={{ flex: 1, display: "flex", justifyContent: "center" }}>
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

      <div style={{ display: "flex", alignItems: "center", gap: "var(--space-3)" }}>
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
  const [nativeEnabled, setNativeEnabled] = useState(false);
  const seen = useRef(new Set<string>());

  useEffect(() => {
    if (state.status !== "signed-in") return;
    let cancelled = false;
    const refresh = async (): Promise<void> => {
      const result = await loadNotifications();
      if (cancelled || !result.ok) return;
      setItems(result.value.items);
      setUnread(result.value.unread);
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

  const read = async (item: NotificationDTO): Promise<void> => {
    await markNotificationRead(item.id);
    setItems((current) => current.map((candidate) => candidate.id === item.id ? { ...candidate, read: true } : candidate));
    setUnread((count) => Math.max(0, count - (item.read ? 0 : 1)));
  };
  const enableNative = async (): Promise<void> => {
    if (typeof Notification === "undefined") return;
    const permission = await Notification.requestPermission();
    setNativeEnabled(permission === "granted");
  };

  return (
    <div style={{ position: "relative" }}>
      <button aria-label={`Notifications, ${unread} unread`} onClick={() => setOpen((value) => !value)} style={{ width: 32, height: 32, position: "relative" }}>
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="M6 16V11a6 6 0 1 1 12 0v5l1.5 2H4.5z" /><path d="M10 20a2 2 0 0 0 4 0" />
        </svg>
        {unread > 0 && <span aria-hidden="true" style={{ position: "absolute", top: -4, right: -4 }}>{unread}</span>}
      </button>
      {open && (
        <section aria-label="Notification inbox" style={{ position: "absolute", right: 0, top: 38, zIndex: 90, width: 360, maxHeight: 480, overflowY: "auto", background: "var(--color-surface)", border: "1px solid var(--color-border)", padding: 12 }}>
          <div style={{ display: "flex", justifyContent: "space-between" }}>
            <strong>Notifications</strong>
            <button type="button" onClick={() => void enableNative()}>Enable native notifications</button>
          </div>
          {items.length === 0 ? <p>No notifications.</p> : items.map((item) => (
            <article key={item.id} style={{ opacity: item.read ? .65 : 1, borderTop: "1px solid var(--color-border)", padding: "10px 0" }}>
              <a href={item.url} onClick={() => void read(item)}><strong>{item.title}</strong></a>
              <p style={{ margin: "4px 0" }}>{item.body}</p>
              <small>{item.createdAt ?? "Time unknown"}</small>
            </article>
          ))}
          <button type="button" onClick={() => void clearNotificationReadState().then(() => { setItems((current) => current.map((item) => ({ ...item, read: false }))); setUnread(items.length); })}>Clear read state</button>
        </section>
      )}
    </div>
  );
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
    <div style={{ display: "flex", alignItems: "center", gap: "var(--space-2)" }}>
      <span
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
