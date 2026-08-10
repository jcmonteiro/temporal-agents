import type {
  Collection,
  FleetDTO,
  FleetNode,
  RunDTO,
  ScheduleDTO,
} from "../clients/api";

/**
 * Hand-written stub of the Agent Hub HTTP API, installed at the transport edge
 * (`fetch`). Tests stub the edge, never the client, so the request paths, the
 * status handling and the DTO projection all take part in the test.
 *
 * The data is mutable, so a test can change what the API answers between two
 * polls of the Overview.
 */
export class FakeApi {
  fleets: FleetDTO[] = [];
  runs: RunDTO[] = [];
  schedules: ScheduleDTO[] = [];
  /** While true, every request answers 503. */
  down = false;

  private original: typeof globalThis.fetch | undefined;

  install(): void {
    this.original = globalThis.fetch;
    globalThis.fetch = ((input: RequestInfo | URL) => {
      const path = new URL(String(input), "http://test.local").pathname;
      if (this.down) {
        return Promise.resolve(
          new Response("service unavailable", { status: 503 }),
        );
      }
      const body = this.bodyFor(path);
      if (!body) {
        return Promise.resolve(new Response("not found", { status: 404 }));
      }
      return Promise.resolve(
        new Response(JSON.stringify(body), {
          status: 200,
          headers: { "content-type": "application/json" },
        }),
      );
    }) as typeof globalThis.fetch;
  }

  restore(): void {
    if (this.original) globalThis.fetch = this.original;
  }

  private bodyFor(path: string): unknown {
    switch (path) {
      case "/api/v1/fleets":
        return collection(this.fleets);
      case "/api/v1/runs":
        return collection(this.runs);
      case "/api/v1/schedules":
        return collection(this.schedules);
      default:
        return null;
    }
  }
}

function collection<T>(items: T[]): Collection<T> {
  return { items, count: items.length, limit: 100 };
}

export function aFleet(overrides: Partial<FleetDTO> = {}): FleetDTO {
  return {
    id: "fleet-1",
    kind: "fleet",
    label: "Checkout revamp",
    status: "in-progress",
    progress: { done: 1, total: 4, fraction: 0.25 },
    startedAt: null,
    endedAt: null,
    dismissible: false,
    ...overrides,
  };
}

export function aRun(overrides: Partial<RunDTO> = {}): RunDTO {
  return {
    id: "run-1",
    kind: "run",
    type: "coder",
    label: "Fix the flaky test",
    status: "done",
    startedAt: null,
    endedAt: null,
    iterations: 3,
    dismissible: false,
    ...overrides,
  };
}

export function aSchedule(overrides: Partial<ScheduleDTO> = {}): ScheduleDTO {
  return {
    id: "schedule-1",
    kind: "schedule",
    label: "Nightly triage",
    spec: "0 2 * * *",
    status: "waiting",
    paused: false,
    runningActions: 0,
    lastRunAt: null,
    nextRunAt: null,
    dismissible: false,
    ...overrides,
  };
}

export function aNode(overrides: Partial<FleetNode> = {}): FleetNode {
  return {
    id: "node-1",
    label: "Write the migration",
    status: "todo",
    execution: null,
    ...overrides,
  };
}
