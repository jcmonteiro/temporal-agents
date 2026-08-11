import type {
  FleetDTO,
  InstructionUseDTO,
  StartedWorkDTO,
  FleetNode,
  LocatedCollection,
  LocationResource,
  NotificationDTO,
  PlaceDTO,
  PromptDTO,
  RunDTO,
  ScheduleDTO,
  SteeringSessionDTO,
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
  /**
   * The places an operator registered. They are separate from `locations`: a
   * registered place is published by the places resource, and by the work
   * collections only once work has run there.
   */
  registered: PlaceDTO[] = [];
  /**
   * The directories this machine holds, and what the probe answers about each.
   * A directory that is not here does not exist; one mapped to null exists but
   * no repository holds it. This is how a test drives the two refusals.
   */
  directories: Record<string, LocationResource | null> = {};
  /**
   * What was started here, by request identity. It stands in for the hub's own
   * memory of what it started, which is what makes a repeated request one run.
   */
  launches: Record<string, StartedWorkDTO> = {};
  /** Start requests received at the HTTP edge, for launcher contract assertions. */
  startRequests: Array<{
    requestId?: string;
    kind?: string;
    placeId?: string;
    prompt?: string;
    worktree?: boolean;
  }> = [];
  /**
   * The place work is already running in, if any. A start there is refused, as
   * the server refuses one: two loops in one working tree commit over each
   * other.
   */
  busy: { locationId: string; runId: string } | null = null;
  /** Who started a run from the hub, by run id. */
  startedBy: Record<string, string> = {};
  /** Which stored instruction a run ran under, by run id. */
  instructionsUsed: Record<string, InstructionUseDTO[]> = {};
  /** Steering sessions, by their stable identity. */
  steeringSessions: Record<string, SteeringSessionDTO> = {};
  /** How many decision writes arrived, for burst-idempotency tests. */
  steeringDecisions = 0;
  notifications: NotificationDTO[] = [];
  /** Prompt catalogues exactly as the server resolved them, keyed by location id. */
  promptCatalogues: Record<string, PromptDTO[]> = { global: [] };
  /** Optional validation refusal for the next prompt save. */
  promptRefusal: string | null = null;
  promptResets: Array<{ locationId: string; key: string }> = [];

  private original: typeof globalThis.fetch | undefined;

  install(): void {
    this.original = globalThis.fetch;
    globalThis.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), "http://test.local");
      const path = url.pathname;
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
      if (path === "/api/v1/notifications" && method === "GET") {
        return Promise.resolve(this.json({ items: this.notifications, count: this.notifications.length, limit: 100, unread: this.notifications.filter((item) => !item.read).length }));
      }
      if (path.startsWith("/api/v1/notifications/") && path.endsWith("/read") && method === "POST") {
        const id = decodeURIComponent(path.slice("/api/v1/notifications/".length, -"/read".length));
        this.notifications = this.notifications.map((item) => item.id === id ? { ...item, read: true } : item);
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      if (path === "/api/v1/notifications/read" && method === "DELETE") {
        this.notifications = this.notifications.map((item) => ({ ...item, read: false }));
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      if (path === "/api/v1/steering/sessions") {
        return Promise.resolve(this.json({
          items: Object.values(this.steeringSessions).filter((session) => session.state === "waiting"),
          count: Object.values(this.steeringSessions).filter((session) => session.state === "waiting").length,
          limit: 100,
          locations: this.locations,
        }));
      }
      if (path.startsWith("/api/v1/steering/sessions/")) {
        return Promise.resolve(this.steering(path, method, init?.body));
      }
      if (path.startsWith("/api/v1/runs/")) {
        return Promise.resolve(this.run(decodeURIComponent(path.slice("/api/v1/runs/".length))));
      }
      if (path === "/api/v1/runs" && method === "POST") {
        return Promise.resolve(this.start(init?.body));
      }
      if (path === "/api/v1/places") {
        return Promise.resolve(this.places(method, init?.body));
      }
      if (path === "/api/v1/prompts" && method === "GET") {
        return Promise.resolve(this.prompts(url.searchParams.get("locationId") ?? ""));
      }
      if (path.startsWith("/api/v1/prompts/")) {
        const key = decodeURIComponent(path.slice("/api/v1/prompts/".length));
        return Promise.resolve(this.changePrompt(
          method,
          key,
          url.searchParams.get("locationId") ?? "",
          init?.body,
        ));
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

  /**
   * One run, with the registry its place resolves against. A run the hub has not
   * got is a 404, which is also what a run that has only just been started reads
   * as until the orchestrator reports it.
   */
  private run(id: string): Response {
    const run = this.runs.find((candidate) => candidate.id === id);
    if (!run) return this.problem(404, "not-found", "no such resource");
    const place = this.locations.find((location) => location.id === run.locationId);
    return this.json({
      ...run,
      // Provenance is published on a run's own resource only, exactly as the
      // server publishes it.
      startedBy: this.startedBy[run.id],
      instructions: this.instructionsUsed[run.id] ?? [],
      locations: place ? [theUnknownPlace(), place] : [theUnknownPlace()],
    });
  }

  private steering(path: string, method: string, body: BodyInit | null | undefined): Response {
    const rest = path.slice("/api/v1/steering/sessions/".length);
    const [encodedId, action] = rest.split("/");
    const id = decodeURIComponent(encodedId);
    const session = this.steeringSessions[id];
    if (!session) return this.problem(404, "not-found", "no such steering session");
    if (method === "GET" && !action) return this.json(session);
    const request = JSON.parse(String(body ?? "{}")) as {
      text?: string;
      finish?: boolean;
      decision?: "guide" | "skip" | "stop" | "continue" | "accept";
      guidance?: string;
    };
    if (method === "POST" && action === "question") {
      const messages = [...(session.messages ?? [])];
      messages.push({ sequence: messages.length + 1, role: "operator", author: this.principal?.id, text: request.text ?? "", at: "2026-08-06T12:00:00Z" });
      const agentText = request.finish ? "Keep the retry and preserve the cause." : "Which callers need the cause?";
      messages.push({ sequence: messages.length + 1, role: "agent", text: agentText, tokens: 40, at: "2026-08-06T12:00:01Z" });
      const updated = { ...session, messages, tokens: (session.tokens ?? 0) + 40, guidance: request.finish ? agentText : session.guidance };
      this.steeringSessions[id] = updated;
      return this.json(updated);
    }
    if (method === "POST" && action === "decision") {
      this.steeringDecisions += 1;
      if (session.state === "waiting") {
        this.steeringSessions[id] = { ...session, state: "decided", decision: request.decision, guidance: request.guidance ?? session.guidance };
      }
      return this.json(this.steeringSessions[id]);
    }
    return this.problem(404, "not-found", "no such steering resource");
  }

  /**
   * Starts work: the rules the server applies, in the order it applies them.
   * The request names a place, one request identity is one run, and a place
   * something is already running in is refused by name.
   */
  private start(body: BodyInit | null | undefined): Response {
    const asked = JSON.parse(String(body ?? "{}")) as {
      requestId?: string;
      kind?: string;
      placeId?: string;
      prompt?: string;
      worktree?: boolean;
    };
    this.startRequests.push(asked);
    const requestId = String(asked.requestId ?? "");
    const existing = this.launches[requestId];
    if (existing) return this.json(existing, 201);
    if (asked.kind === "develop" && !asked.prompt) {
      return this.problem(400, "invalid-request",
        "a develop pass needs a prompt saying what to do");
    }
    const place = this.locations.find((location) => location.id === asked.placeId);
    if (!place) {
      return this.problem(400, "invalid-request",
        `this hub knows no place "${asked.placeId}" to work in`);
    }
    if (this.busy?.locationId === place.id) {
      return this.problem(409, "place-is-busy",
        `${this.busy.runId} is already running in ${place.label}`, this.busy.runId);
    }
    const started: StartedWorkDTO = {
      // Minted from the request identity, as the server mints it: the same request
      // always names the same execution.
      id: `${asked.kind}-${requestId}`,
      kind: "run",
      type: String(asked.kind),
      label: String(asked.prompt ?? ""),
      locationId: place.id,
      startedAt: "2026-08-06T12:00:00Z",
      startedBy: this.principal?.id,
      locations: [theUnknownPlace(), place],
    };
    this.launches[requestId] = started;
    return this.json(started, 201);
  }

  /**
   * Answers the places resource: where the hub may work, and registering one
   * more. The refusals are the server's own, in the same order it applies them:
   * the request's own rules first, then what this machine holds.
   */
  private places(method: string, body: BodyInit | null | undefined): Response {
    if (method === "GET") {
      return this.json({
        items: this.registered,
        count: this.registered.length,
        limit: this.registered.length,
        locations: this.registeredLocations(),
      } satisfies LocatedCollection<PlaceDTO>);
    }
    const directory = String(JSON.parse(String(body ?? "{}")).directory ?? "");
    if (!directory.startsWith("/")) {
      return this.problem(400, "invalid-request",
        `the directory ${directory} must be an absolute path`);
    }
    const location = this.directories[directory];
    if (location === undefined) {
      return this.problem(422, "not-a-place", `no such directory: ${directory}`);
    }
    if (location === null) {
      return this.problem(422, "not-a-place", `not a repository: ${directory}`);
    }
    const existing = this.registered.find((place) => place.locationId === location.id);
    if (existing) {
      return this.json({ ...existing, locations: [theUnknownPlace(), location] }, 201);
    }
    const place: PlaceDTO = {
      locationId: location.id,
      registeredAt: "2026-08-06T12:00:00Z",
      registeredBy: this.principal?.id,
    };
    this.registered.push(place);
    if (!this.locations.some((known) => known.id === location.id)) {
      this.locations = [...this.locations, location];
    }
    return this.json({ ...place, locations: [theUnknownPlace(), location] }, 201);
  }

  private prompts(locationId: string): Response {
    const items = this.promptCatalogues[locationId || "global"] ?? [];
    return this.json({ items, count: items.length, limit: items.length });
  }

  private changePrompt(
    method: string,
    key: string,
    locationId: string,
    body: BodyInit | null | undefined,
  ): Response {
    const catalogueKey = locationId || "global";
    const items = this.promptCatalogues[catalogueKey] ?? [];
    const index = items.findIndex((item) => item.key === key);
    if (index < 0) return this.problem(404, "not-found", "no such prompt");
    if (method === "PUT") {
      if (this.promptRefusal !== null) {
        return this.problem(422, "invalid-prompt", this.promptRefusal);
      }
      const text = String((JSON.parse(String(body ?? "{}")) as { text?: unknown }).text ?? "");
      items[index] = {
        ...items[index],
        effective: text,
        source: locationId === "" ? "global" : "directory",
        overridden: true,
      };
      this.promptCatalogues[catalogueKey] = [...items];
      return new Response(null, { status: 204 });
    }
    if (method === "DELETE") {
      this.promptResets.push({ locationId, key });
      items[index] = {
        ...items[index],
        effective: items[index].inherited,
        source: items[index].inheritedFrom,
        version: items[index].inheritedVersion,
        overridden: false,
      };
      this.promptCatalogues[catalogueKey] = [...items];
      return new Response(null, { status: 204 });
    }
    return this.problem(405, "method-not-allowed", "method not allowed");
  }

  /** The registry the places resource publishes for what it holds. */
  private registeredLocations(): LocationResource[] {
    const referenced = new Set(this.registered.map((place) => place.locationId));
    for (const location of [...this.locations].reverse()) {
      if (referenced.has(location.id) && location.parentId) referenced.add(location.parentId);
    }
    return this.locations.filter(
      (location) => location.id === "unknown" || referenced.has(location.id),
    );
  }

  /** A JSON body, as the API sends one. */
  private json(body: unknown, status = 200): Response {
    return new Response(JSON.stringify(body), {
      status,
      headers: { "content-type": "application/json" },
    });
  }

  /** A problem document, as the API refuses with one. */
  private problem(status: number, code: string, detail: string, conflictingRunId?: string): Response {
    return new Response(
      JSON.stringify({
        type: `/api/v1/problems/${code}`,
        title: code,
        status,
        detail,
        conflictingRunId,
      }),
      { status, headers: { "content-type": "application/problem+json" } },
    );
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

  /**
   * One work collection, with the registry of the places its items refer to.
   *
   * The registry is closed over the referenced places and their ancestors, and
   * carries nothing else, exactly as the server's is: a place nothing runs in
   * appears in no work collection at all, which is the whole reason the places
   * resource exists.
   */
  private collection<T>(items: T[]): LocatedCollection<T> {
    const referenced = new Set(
      items.map((item) => (item as { locationId?: string }).locationId ?? "unknown"),
    );
    for (const location of [...this.locations].reverse()) {
      if (referenced.has(location.id) && location.parentId) referenced.add(location.parentId);
    }
    return {
      items,
      count: items.length,
      limit: 100,
      locations: this.locations.filter(
        (location) => location.id === "unknown" || referenced.has(location.id),
      ),
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

export function aPrompt(overrides: Partial<PromptDTO> = {}): PromptDTO {
  return {
    key: "review.perform",
    purpose: "How the agent reviews the current branch.",
    effective: "Review the branch",
    inherited: "Review the shipped branch",
    source: "global",
    inheritedFrom: "factory",
    version: 2,
    inheritedVersion: 1,
    overridden: false,
    systemBlock: "",
    requiredInserts: [],
    advanced: false,
    maxLength: 16_384,
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

export function aSteeringSession(
  overrides: Partial<SteeringSessionDTO> = {},
): SteeringSessionDTO {
  return {
    id: "steering-review-1",
    itemId: "review-1",
    round: "local-review",
    state: "waiting",
    waitingSince: "2026-08-06T11:00:00Z",
    locationId: "unknown",
    material: "The retry hides the original error.",
    guidance: "",
    tokens: 0,
    contributors: [],
    messages: [],
    locations: [theUnknownPlace()],
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
