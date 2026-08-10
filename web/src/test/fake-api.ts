import type {
  FleetDTO,
  FleetNode,
  LocatedCollection,
  LocationResource,
  RunDTO,
  ScheduleDTO,
} from "../clients/api";
import type { PrincipalDTO } from "../clients/session";

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
  /**
   * The registry every response publishes. The API always carries at least the
   * unknown place, so the stub does too.
   */
  locations: LocationResource[] = [theUnknownPlace()];
  /** While true, every request answers 503. */
  down = false;
  /**
   * Who the API says the request is made by, or null for a browser whose
   * credential the API refuses. A refused credential closes the whole API, not
   * only the session endpoint, exactly as the server does.
   */
  principal: PrincipalDTO | null = theOperator();
  /**
   * Whether this deployment can sign anybody in. When false the sign-in routes
   * do not exist at all, which is how a hub with no identity provider answers.
   */
  signInConfigured = true;
  /** How many times the session endpoint was asked, so a loop is visible. */
  sessionReads = 0;

  private original: typeof globalThis.fetch | undefined;

  install(): void {
    this.original = globalThis.fetch;
    globalThis.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
      const path = new URL(String(input), "http://test.local").pathname;
      const method = init?.method ?? "GET";
      if (this.down) {
        return Promise.resolve(
          new Response("service unavailable", { status: 503 }),
        );
      }
      if (path === "/api/v1/auth/session") {
        return Promise.resolve(this.session(method));
      }
      if (this.signInConfigured && this.principal === null) {
        return Promise.resolve(this.unauthenticated());
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

  /** Answers the session endpoint: who am I, and signing out. */
  private session(method: string): Response {
    if (!this.signInConfigured) {
      return new Response("not found", { status: 404 });
    }
    if (method === "DELETE") {
      this.principal = null;
      return new Response(null, { status: 204 });
    }
    this.sessionReads += 1;
    if (this.principal === null) return this.unauthenticated();
    return new Response(JSON.stringify({ principal: this.principal }), {
      status: 200,
      headers: { "content-type": "application/json" },
    });
  }

  /** The problem document the API answers a refused credential with. */
  private unauthenticated(): Response {
    return new Response(
      JSON.stringify({
        type: "/api/v1/problems/authentication-required",
        title: "Authentication is required",
        status: 401,
      }),
      { status: 401, headers: { "content-type": "application/problem+json" } },
    );
  }

  restore(): void {
    if (this.original) globalThis.fetch = this.original;
  }

  private bodyFor(path: string): unknown {
    switch (path) {
      case "/api/v1/fleets":
        return this.collection(this.fleets);
      case "/api/v1/runs":
        return this.collection(this.runs);
      case "/api/v1/schedules":
        return this.collection(this.schedules);
      default:
        return null;
    }
  }

  private collection<T>(items: T[]): LocatedCollection<T> {
    return {
      items,
      count: items.length,
      limit: 100,
      locations: this.locations,
    };
  }
}

/** The operator the fake API knows, as the session endpoint publishes them. */
export function theOperator(
  overrides: Partial<PrincipalDTO> = {},
): PrincipalDTO {
  return {
    id: "https://issuer.test|operator-1",
    issuer: "https://issuer.test",
    subject: "operator-1",
    name: "The Operator",
    email: "operator@example.test",
    ...overrides,
  };
}

/** The place work runs in when nothing was recorded about where. */
export function theUnknownPlace(): LocationResource {
  return { id: "unknown", kind: "unknown", label: "Unknown", parentId: null };
}

/** A directory place, optionally inside another one. */
export function aDirectoryPlace(
  overrides: Partial<LocationResource> = {},
): LocationResource {
  return {
    id: "dir-1",
    kind: "directory",
    label: "checkout",
    parentId: null,
    directory: "/srv/checkout",
    ...overrides,
  };
}

export function aFleet(overrides: Partial<FleetDTO> = {}): FleetDTO {
  return {
    id: "fleet-1",
    kind: "fleet",
    label: "Checkout revamp",
    status: "in-progress",
    locationId: "unknown",
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
    locationId: "unknown",
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
    locationId: "unknown",
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
