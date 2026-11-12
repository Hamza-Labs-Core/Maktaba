// Epic 57 family settings: how long a rule or an approval lasts. "Today" is the
// home's day (it ends at the home's 04:00 day line), so the home computes its end:
// this device's clock and time zone say nothing about it.
import { describe, it, expect } from "vitest";
import { expiryOf } from "./subjects";

describe("expiryOf", () => {
  it("Today is the home's to end: no instant from this device, at any hour", () => {
    for (const at of ["2026-11-12T00:30:00", "2026-11-12T12:00:00", "2026-11-12T23:59:59"]) {
      expect(expiryOf("today", new Date(at))).toEqual({ until: "day_end" });
    }
  });

  it("This week is seven days on; Always does not end", () => {
    const now = new Date("2026-11-12T10:00:00Z");
    expect(expiryOf("week", now)).toEqual({ expiresAt: "2026-11-19T10:00:00.000Z" });
    expect(expiryOf("always", now)).toEqual({});
  });
});
