import { FakeApi } from "../test/fake-api";

type ConfigureApi = (api: FakeApi) => void;

class SilentEventSource {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;

  readonly CONNECTING = SilentEventSource.CONNECTING;
  readonly OPEN = SilentEventSource.OPEN;
  readonly CLOSED = SilentEventSource.CLOSED;
  readonly readyState = SilentEventSource.OPEN;
  readonly withCredentials = false;

  onerror: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onopen: ((event: Event) => void) | null = null;

  constructor(readonly url: string | URL) {}

  addEventListener(): void {}
  dispatchEvent(): boolean { return true; }
  removeEventListener(): void {}
  close(): void {}
}

/** Installs one isolated HTTP-edge fixture for a story and returns its cleanup. */
export function installStoryApi(
  configure: ConfigureApi,
  now = "2026-08-11T16:10:00Z",
): () => void {
  const api = new FakeApi();
  configure(api);
  api.install();

  const originalEventSource = globalThis.EventSource;
  const originalNow = Date.now;
  Object.defineProperty(globalThis, "EventSource", {
    configurable: true,
    value: SilentEventSource,
    writable: true,
  });
  Date.now = () => Date.parse(now);

  return () => {
    api.restore();
    Date.now = originalNow;
    if (originalEventSource === undefined) {
      Reflect.deleteProperty(globalThis, "EventSource");
    } else {
      Object.defineProperty(globalThis, "EventSource", {
        configurable: true,
        value: originalEventSource,
        writable: true,
      });
    }
  };
}
