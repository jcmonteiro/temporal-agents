import { describe, expect, it, vi } from "vitest";

import { connectHubEvents, type EventSourceLike } from "./streams";

class FakeEventSource implements EventSourceLike {
  readonly listeners = new Map<string, Array<(event: MessageEvent<string>) => void>>();
  closed = false;

  addEventListener(type: string, listener: (event: MessageEvent<string>) => void): void {
    const listeners = this.listeners.get(type) ?? [];
    listeners.push(listener);
    this.listeners.set(type, listeners);
  }

  close(): void {
    this.closed = true;
  }

  emit(type: string, data: string, lastEventId = ""): void {
    for (const listener of this.listeners.get(type) ?? []) {
      listener({ data, lastEventId } as MessageEvent<string>);
    }
  }
}

describe("hub event stream", () => {
  it("uses an event only to trigger one refetch", () => {
    const source = new FakeEventSource();
    const refetch = vi.fn();
    const connection = connectHubEvents(refetch, {
      open: () => source,
    });

    source.emit("hub", JSON.stringify({
      type: "session-waiting",
      sessionId: "steering-review-1",
      itemId: "review-1",
    }), "7");

    expect(refetch).toHaveBeenCalledTimes(1);
    expect(refetch).toHaveBeenCalledWith();
    connection.close();
    expect(source.closed).toBe(true);
  });
});
