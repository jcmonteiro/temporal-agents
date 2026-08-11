import { describe, expect, it } from "vitest";

import { waitingFor } from "./waiting-time";

describe("waiting duration", () => {
  it("uses whole minutes for a wait shorter than one hour", () => {
    expect(waitingFor("2026-08-11T10:30:45Z", new Date("2026-08-11T10:35:59Z")))
      .toBe("waiting for 5 minutes");
  });

  it("uses whole hours for a wait shorter than one day", () => {
    expect(waitingFor("2026-08-11T05:05:00Z", new Date("2026-08-11T10:35:00Z")))
      .toBe("waiting for 5 hours");
  });

  it("uses whole days for a wait shorter than one week", () => {
    expect(waitingFor("2026-08-06T06:35:00Z", new Date("2026-08-11T10:35:00Z")))
      .toBe("waiting for 5 days");
  });

  it("uses whole weeks for a wait of at least one week", () => {
    expect(waitingFor("2026-07-28T06:35:00Z", new Date("2026-08-11T10:35:00Z")))
      .toBe("waiting for 2 weeks");
  });

  it("uses an unknown duration when the wait start is absent", () => {
    expect(waitingFor(null, new Date("2026-08-11T10:35:00Z")))
      .toBe("waiting for an unknown time");
  });
});
