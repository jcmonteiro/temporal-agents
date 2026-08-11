import { announceUnauthenticated } from "./http";

const BASE = "/api/v1";

export interface EventSourceLike {
  addEventListener(type: string, listener: (event: MessageEvent<string>) => void): void;
  close(): void;
}

interface StreamOptions {
  open?: (url: string) => EventSourceLike;
  storage?: Pick<Storage, "getItem" | "setItem">;
}

export interface ConversationMessageEvent {
  sequence: number;
  role: "operator" | "agent";
  author?: string;
  text: string;
  tokens?: number;
  at: string | null;
}

export interface StreamConnection {
  close(): void;
}

function nativeSource(url: string): EventSourceLike {
  return new EventSource(url) as unknown as EventSourceLike;
}

function sourceURL(path: string, position: string | null): string {
  if (!position) return BASE + path;
  const separator = path.includes("?") ? "&" : "?";
  return `${BASE}${path}${separator}after=${encodeURIComponent(position)}`;
}

function positionStorage(options: StreamOptions): Pick<Storage, "getItem" | "setItem"> | undefined {
  if (options.storage) return options.storage;
  return typeof sessionStorage === "undefined" ? undefined : sessionStorage;
}

function connect(
  path: string,
  eventType: string,
  positionKey: string,
  onEvent: (event: MessageEvent<string>) => void,
  options: StreamOptions,
): StreamConnection {
  const storage = positionStorage(options);
  const source = (options.open ?? nativeSource)(sourceURL(path, storage?.getItem(positionKey) ?? null));
  source.addEventListener(eventType, (event) => {
    if (event.lastEventId) storage?.setItem(positionKey, event.lastEventId);
    onEvent(event);
  });
  source.addEventListener("auth-expired", () => {
    source.close();
    announceUnauthenticated();
  });
  return { close: () => source.close() };
}

/**
 * Connects to small hub notifications. The event body is deliberately ignored:
 * the callback refetches the normal list resource, which remains the only source
 * of current state.
 */
export function connectHubEvents(
  refetch: () => void,
  options: StreamOptions = {},
): StreamConnection {
  return connect("/events", "hub", "hub-events", () => refetch(), options);
}

/** Connects to one append-only conversation and resumes after its last sequence. */
export function connectConversation(
  sessionId: string,
  onMessage: (message: ConversationMessageEvent) => void,
  options: StreamOptions = {},
): StreamConnection {
  return connect(
    `/steering/sessions/${encodeURIComponent(sessionId)}/events`,
    "message",
    `steering-conversation:${sessionId}`,
    (event) => onMessage(JSON.parse(event.data) as ConversationMessageEvent),
    options,
  );
}
